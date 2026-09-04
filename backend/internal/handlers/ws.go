package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

const (
	CloseCodeAuthFailure = 4401
	CloseCodeRateLimit   = 4429
)

const (
	DefaultReadBufferSize  = 1024
	DefaultWriteBufferSize = 1024
	DefaultPingInterval    = 30 * time.Second
	DefaultConnectionLimit = 10
)

var upgrader websocket.Upgrader

func InitUpgrader(corsOrigins string, authDisabled bool) {
	upgrader = websocket.Upgrader{
		ReadBufferSize:  DefaultReadBufferSize,
		WriteBufferSize: DefaultWriteBufferSize,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			// Non-browser clients send no Origin; allow them.
			if origin == "" {
				return true
			}

			if corsOrigins != "" {
				// Explicit allowlist (production behind a reverse proxy that sets
				// CORS_ORIGINS) is authoritative.
				for _, allowed := range strings.Split(corsOrigins, ",") {
					if strings.TrimSpace(allowed) == origin {
						return true
					}
				}
			} else if origin == "http://"+r.Host || origin == "https://"+r.Host {
				// No allowlist configured: only same-origin (frontend served by the
				// backend itself) is accepted.
				return true
			}

			// Dev convenience: AUTH_DISABLED is the documented trusted-network/dev
			// mode. The Vite dev server proxies /api to the backend with changeOrigin,
			// so r.Host (localhost:5001) no longer matches the browser Origin
			// (localhost:3001) and the same-origin check above fails. Accept loopback
			// origins in that mode so the stack-events/metrics WebSockets connect.
			if authDisabled && isLoopbackOrigin(origin) {
				return true
			}

			return false
		},
	}
}

// isLoopbackOrigin reports whether an Origin header points at localhost, 127.0.0.1,
// or [::1] (any port). Used only to relax the WS origin check when auth is disabled.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

type Connection struct {
	ID     string
	UserID string
	// SessionID is the "jti" AuthMiddleware validated for the HTTP request
	// that upgraded this connection (middleware/auth.go publishes it on the
	// gin context after checking the session row). Empty under AUTH_DISABLED,
	// which never mints a session — see CloseForSession/CloseForUser, which
	// both treat that as "nothing to match" rather than "match everything"
	// (agent-os-teop).
	SessionID  string
	Conn       *websocket.Conn
	CreatedAt  time.Time
	WriteMutex sync.Mutex
}

type ConnectionManager struct {
	connections map[string]*Connection
	userCounts  map[string]int
	// metered marks which connection IDs counted against userCounts when they
	// were registered (via Add). AddUnmetered registers a connection WITHOUT
	// an entry here, and Remove/closeMatching consult this map before
	// decrementing userCounts — both already unconditionally decrement it for
	// every connection they remove, so a connection that never incremented it
	// must be excluded there too, or removing it silently inflates every
	// other user's effective cap (agent-os-pu4y).
	metered    map[string]bool
	mu         sync.RWMutex
	maxPerUser int
}

func NewConnectionManager(maxPerUser int) *ConnectionManager {
	if maxPerUser <= 0 {
		maxPerUser = DefaultConnectionLimit
	}
	return &ConnectionManager{
		connections: make(map[string]*Connection),
		userCounts:  make(map[string]int),
		metered:     make(map[string]bool),
		maxPerUser:  maxPerUser,
	}
}

func (cm *ConnectionManager) Add(id string, conn *Connection) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	count := cm.userCounts[conn.UserID]
	if count >= cm.maxPerUser {
		return &models.AppError{
			Code:    models.ErrRateLimited,
			Message: "Connection limit per user exceeded",
			Status:  http.StatusTooManyRequests,
		}
	}

	cm.connections[id] = conn
	cm.userCounts[conn.UserID] = count + 1
	cm.metered[id] = true
	return nil
}

// AddUnmetered registers conn for revocation (CloseForSession/CloseForUser can
// reach it, exactly like a connection added via Add) WITHOUT consuming a
// per-user cap slot: it never refuses, and it never increments userCounts.
//
// This is for a handler that must not abandon an already-running operation's
// only viewer just because the caller is at the cap (wsAttach in backup.go),
// but still wants the connection reachable by session/user revocation. A
// dedicated lower-cap ConnectionManager does NOT give the same result:
// NewConnectionManager coerces maxPerUser <= 0 to DefaultConnectionLimit, so
// there is no way to construct an actually-uncapped manager — only a
// registration path that bypasses the cap on the existing one.
func (cm *ConnectionManager) AddUnmetered(id string, conn *Connection) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.connections[id] = conn
	// cm.metered[id] intentionally left unset — see the field comment.
}

func (cm *ConnectionManager) Remove(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if conn, exists := cm.connections[id]; exists {
		if cm.metered[id] {
			cm.userCounts[conn.UserID]--
			if cm.userCounts[conn.UserID] <= 0 {
				delete(cm.userCounts, conn.UserID)
			}
		}
		delete(cm.metered, id)
		delete(cm.connections, id)
	}
}

func (cm *ConnectionManager) Get(id string) (*Connection, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	conn, exists := cm.connections[id]
	return conn, exists
}

func (cm *ConnectionManager) Count() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.connections)
}

func (cm *ConnectionManager) CountByUser(userID string) int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.userCounts[userID]
}

func (cm *ConnectionManager) CloseAll() {
	// The 100ms grace is shutdown-only (see closeMatching's doc comment) —
	// not on a request path, so paying it once here is fine.
	cm.closeMatching(func(*Connection) bool { return true }, websocket.CloseNormalClosure, "Server shutting down", 100*time.Millisecond)
}

// CloseForSession closes every live connection whose SessionID matches
// sessionID — e.g. the session logout just deleted. Sends
// CloseCodeAuthFailure (4401) so the frontend's shouldReconnectAfter
// (frontend/src/lib/ws.ts) stops retrying instead of running its 5-attempt
// backoff against a session no request can ever satisfy again.
//
// A blank sessionID closes nothing. AuthMiddleware only publishes "jti" for a
// real (non-AUTH_DISABLED) request; treating "" as a wildcard here would let a
// dev-mode (AUTH_DISABLED) caller — which never carries a jti — close every
// anonymous connection on the host (agent-os-teop).
func (cm *ConnectionManager) CloseForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	// No grace period: measured to buy nothing (safeWriteCloseMessage's doc
	// comment), and this runs on the logout request path.
	cm.closeMatching(func(conn *Connection) bool {
		return conn.SessionID == sessionID
	}, CloseCodeAuthFailure, "Session revoked", 0)
}

// CloseForUser closes every live connection belonging to userID, except one
// whose SessionID equals exceptSessionID (pass "" to except none). Mirrors
// database.DeleteSessionsByUserExcluding, which the password-change
// revocation path (handlers/settings.go) uses to invalidate every OTHER
// session for the user without logging out the request that changed it.
//
// A blank or "anonymous" userID closes nothing. AUTH_DISABLED assigns every
// connection userID "anonymous" (upgradeConnection) and a real caller's
// userID is never blank past AuthMiddleware, so either value here means "no
// real user to scope the close to," not "close everything" (agent-os-teop).
func (cm *ConnectionManager) CloseForUser(userID, exceptSessionID string) {
	if userID == "" || userID == "anonymous" {
		return
	}
	// No grace period: same reasoning as CloseForSession — this runs on the
	// password-change request path.
	cm.closeMatching(func(conn *Connection) bool {
		return conn.UserID == userID && conn.SessionID != exceptSessionID
	}, CloseCodeAuthFailure, "Session revoked", 0)
}

// closeMatching collects every connection satisfying match under cm.mu,
// removes it from the manager, releases the lock, writes every close frame,
// waits grace ONCE (not per connection — see below), and only then closes
// the collected sockets.
//
// Holding cm.mu across the writes would block every concurrent Add/Remove on
// what is otherwise a fast request path (logout, password change) — the
// previous CloseAll held cm.mu across its own writeCloseMessage calls, which
// was fine at its shutdown-only call site but is not fine reused for a live
// revocation (agent-os-teop).
//
// grace is applied ONCE after every frame is written, not per connection:
// CloseForSession/CloseForUser pass 0 (measured to buy nothing — see
// safeWriteCloseMessage), so they pay no sleep at all; CloseAll passes a
// small grace for its shutdown-only path. A per-connection sleep here would
// make a revocation O(N) against the manager's per-user cap (up to 1.5s at
// cap 10+5) for zero benefit.
func (cm *ConnectionManager) closeMatching(match func(*Connection) bool, closeCode int, reason string, grace time.Duration) {
	cm.mu.Lock()
	var targets []*Connection
	for id, conn := range cm.connections {
		if !match(conn) {
			continue
		}
		targets = append(targets, conn)
		// Same meteredness check as Remove — a connection registered via
		// AddUnmetered never incremented userCounts, so revoking it here must
		// not decrement it either (agent-os-pu4y).
		if cm.metered[id] {
			cm.userCounts[conn.UserID]--
			if cm.userCounts[conn.UserID] <= 0 {
				delete(cm.userCounts, conn.UserID)
			}
		}
		delete(cm.metered, id)
		delete(cm.connections, id)
	}
	cm.mu.Unlock()

	for _, conn := range targets {
		if conn.Conn != nil {
			safeWriteCloseMessage(conn, closeCode, reason)
		}
	}

	if grace > 0 {
		time.Sleep(grace)
	}

	for _, conn := range targets {
		if conn.Conn != nil {
			conn.Conn.Close()
		}
	}
}

// ConnectionManagers is every ConnectionManager whose live connections must be
// closed together when a session or user is revoked. There are two today
// (the shared cap and the lower-cap terminal one, cmd/server/main.go) and
// handlers that revoke sessions (logout, password change) hold one of these
// rather than each ConnectionManager individually, so wiring in a third
// manager later is a one-line change in main.go instead of a signature change
// in every revoking handler — the failure mode this guards against is a fix
// that silently reaches only one manager and leaves another connection type
// open while appearing to work (agent-os-teop).
type ConnectionManagers []*ConnectionManager

// CloseForSession fans out to every manager. See ConnectionManager.CloseForSession.
func (cms ConnectionManagers) CloseForSession(sessionID string) {
	for _, cm := range cms {
		if cm != nil {
			cm.CloseForSession(sessionID)
		}
	}
}

// CloseForUser fans out to every manager. See ConnectionManager.CloseForUser.
func (cms ConnectionManagers) CloseForUser(userID, exceptSessionID string) {
	for _, cm := range cms {
		if cm != nil {
			cm.CloseForUser(userID, exceptSessionID)
		}
	}
}

func authenticateToken(token string, db *database.DB, jwtSecret string) (string, error) {
	claims, err := middleware.ValidateJWT(token, jwtSecret)
	if err != nil {
		// Every failure here is session loss (no usable token), matching the
		// AuthMiddleware contract documented in models/errors.go: SESSION_EXPIRED,
		// not UNAUTHORIZED. The message still distinguishes "expired" from
		// "otherwise invalid" for logging; the code does not.
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", &models.AppError{
				Code:    models.ErrSessionExpired,
				Message: "Token has expired",
				Status:  http.StatusUnauthorized,
			}
		}
		return "", &models.AppError{
			Code:    models.ErrSessionExpired,
			Message: "Invalid token",
			Status:  http.StatusUnauthorized,
		}
	}

	// A validly-signed token with no "jti" (or a non-string one) names no
	// session row, so it can never be found and revoked by logout. Before this
	// fix the type assertion had no else branch and simply skipped the
	// session/revocation lookup, admitting an unrevocable token — same shape
	// as the missing-"sub" gap below. Reject with SESSION_EXPIRED instead,
	// matching middleware.AuthMiddleware (agent-os-gm5).
	jti, ok := claims["jti"].(string)
	if !ok {
		return "", &models.AppError{
			Code:    models.ErrSessionExpired,
			Message: "Invalid token",
			Status:  http.StatusUnauthorized,
		}
	}

	session, err := db.GetSession(jti)
	if err != nil || session == nil {
		return "", &models.AppError{
			Code:    models.ErrSessionExpired,
			Message: "Session not found or expired",
			Status:  http.StatusUnauthorized,
		}
	}

	if time.Now().After(session.ExpiresAt) {
		return "", &models.AppError{
			Code:    models.ErrSessionExpired,
			Message: "Session has expired",
			Status:  http.StatusUnauthorized,
		}
	}

	userID, ok := claims["sub"].(string)
	if !ok || userID == "" {
		return "", &models.AppError{
			Code:    models.ErrSessionExpired,
			Message: "Invalid user ID in token",
			Status:  http.StatusUnauthorized,
		}
	}

	return userID, nil
}

func writeJSON(conn *websocket.Conn, v interface{}) error {
	// A failed deadline set surfaces immediately as a write error below,
	// which the caller already handles.
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(v)
}

func readJSON(conn *websocket.Conn, v interface{}) error {
	// A failed deadline set surfaces immediately as a read error below,
	// which the caller already handles.
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	return conn.ReadJSON(v)
}

func safeWriteJSON(c *Connection, v interface{}) error {
	c.WriteMutex.Lock()
	defer c.WriteMutex.Unlock()
	return writeJSON(c.Conn, v)
}

// safeWriteMessage writes a raw WebSocket message while holding the
// Connection's WriteMutex, serializing with every other DATA writer on the
// same connection — safeWriteJSON and safePingLoop. terminal.go's PTY writer
// and logs.go's log-line/ping writer both go through this instead of calling
// conn.Conn.WriteMessage directly: gorilla's Conn panics on a concurrent
// write (best-effort, unsynchronized c.isWriting bool — gorilla/websocket@
// v1.5.3 conn.go:610-624), and nothing recovers a panic in a bare `go`
// goroutine like writeToWebSocket's. Note this protects data writers against
// EACH OTHER (logs.go's ping vs. log-line writer, or any future second
// writer) — the close path (safeWriteCloseMessage) does not need or take this
// mutex at all; it uses WriteControl, which gorilla documents safe to call
// concurrently with this (agent-os-teop).
func safeWriteMessage(c *Connection, messageType int, data []byte) error {
	c.WriteMutex.Lock()
	defer c.WriteMutex.Unlock()
	// A failed deadline set surfaces immediately as a write error below,
	// which the caller already handles.
	_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.Conn.WriteMessage(messageType, data)
}

func safePingLoop(ctx context.Context, c *Connection, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.WriteMutex.Lock()
			// A failed deadline set surfaces immediately as a write error
			// on the next line, which is already handled.
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err := c.Conn.WriteMessage(websocket.PingMessage, nil)
			c.WriteMutex.Unlock()
			if err != nil {
				slog.Debug("Failed to send ping", "error", err)
				return
			}
		}
	}
}

func writeCloseMessage(conn *websocket.Conn, closeCode int, reason string) {
	// A failed deadline set surfaces immediately as a write error below,
	// which is already handled.
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	msg := websocket.FormatCloseMessage(closeCode, reason)
	if err := conn.WriteMessage(websocket.CloseMessage, msg); err != nil {
		slog.Debug("Failed to send close message", "error", err)
		return
	}

	time.Sleep(100 * time.Millisecond)
}

// safeWriteCloseMessage sends a close frame via gorilla's WriteControl,
// rather than the raw-WriteMessage-based writeCloseMessage CloseAll used to
// call directly on a live connection's *websocket.Conn. That would be a
// genuine concurrent write whenever the connection has an active writer
// (terminal.go's PTY streamer, logs.go's log/ping writer): gorilla panics on a
// concurrent WriteMessage, and there is no recover() reaching a bare `go`
// goroutine (agent-os-teop).
//
// WriteControl is different: gorilla's own package doc (doc.go:133-134)
// states "The Close and WriteControl methods can be called concurrently with
// all other methods" — it takes gorilla's internal control-frame lock, not
// WriteMutex, and never touches the data-write path's isWriting bookkeeping.
// That also means this call never queues behind a data writer holding
// WriteMutex for its full write deadline (terminal.go's is 10s against a
// wedged client), so a revocation on the request path (logout, password
// change) cannot be stalled by an in-flight PTY write. Safe unilaterally, not
// just "safe if every writer in the codebase cooperates with WriteMutex" —
// the mutex conversion in terminal.go/logs.go stays anyway, since it is what
// keeps two DATA writers off each other, which WriteControl does not cover.
func safeWriteCloseMessage(c *Connection, closeCode int, reason string) {
	// Mirrors writeCloseMessage's deadline, via WriteControl instead of
	// WriteMessage. Deliberately NO post-send sleep here (unlike
	// writeCloseMessage): measured (gorilla over loopback, 20 runs each arm)
	// that the peer sees the close code 20/20 with or without a grace period
	// — WriteControl already flushes the frame to the kernel send buffer, and
	// a subsequent Close() does not race that. Any grace period a caller
	// still wants belongs in that caller, once, after writing every frame —
	// see closeMatching's grace parameter — not per-connection here, which
	// would put O(N) sleeps on a request path (agent-os-teop).
	deadline := time.Now().Add(5 * time.Second)
	msg := websocket.FormatCloseMessage(closeCode, reason)
	if err := c.Conn.WriteControl(websocket.CloseMessage, msg, deadline); err != nil {
		slog.Debug("Failed to send close message", "error", err)
	}
}

func upgradeConnection(c *gin.Context, db *database.DB, jwtSecret string, authDisabled bool) (*Connection, error) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}

	var userID string

	if authDisabled {
		userID = "anonymous"
	} else {
		cookieToken, cookieErr := c.Cookie("capstan_token")

		if cookieErr == nil && cookieToken != "" {
			userID, err = authenticateToken(cookieToken, db, jwtSecret)
			if err != nil {
				writeCloseMessage(conn, CloseCodeAuthFailure, "Auth failed")
				conn.Close()
				return nil, err
			}
		} else {
			// A failed deadline set surfaces immediately as a read error
			// below, which is already handled.
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var authMsg struct {
				Type  string `json:"type"`
				Token string `json:"token"`
			}
			if err := conn.ReadJSON(&authMsg); err != nil {
				writeCloseMessage(conn, CloseCodeAuthFailure, "Auth timeout")
				conn.Close()
				return nil, &models.AppError{Code: models.ErrSessionExpired, Message: "No auth message received", Status: 401}
			}

			if authMsg.Type != "auth" || authMsg.Token == "" {
				writeCloseMessage(conn, CloseCodeAuthFailure, "Invalid auth message")
				conn.Close()
				return nil, &models.AppError{Code: models.ErrSessionExpired, Message: "Invalid auth message", Status: 401}
			}

			userID, err = authenticateToken(authMsg.Token, db, jwtSecret)
			if err != nil {
				writeCloseMessage(conn, CloseCodeAuthFailure, "Auth failed")
				conn.Close()
				return nil, err
			}
		}
	}

	connectionID := uuid.New().String()
	connection := &Connection{
		ID: connectionID,
		// UserID and SessionID deliberately come from DIFFERENT sources, and
		// that is a documented tradeoff, not an oversight (agent-os-teop).
		// Taking UserID from the gin context (c.GetString("userID"), what
		// AuthMiddleware publishes) instead of the local var above would be
		// the more consistent design — SessionID already does this — but a
		// large, pre-existing slice of the WS test suite (terminal_scope_
		// test.go, operations_test.go, and others) wires the handler onto a
		// bare gin.New() router with NO AuthMiddleware in the chain and
		// authDisabled=true passed directly to the handler, relying on
		// upgradeConnection's own authDisabled branch above to supply
		// "anonymous". Sourcing UserID from context broke that pattern
		// wholesale (TestTerminalRefusesBeyondPerUserCap and siblings failed
		// with an empty UserID) — OBSERVED by running the suite, not
		// inferred. SessionID has no equivalent test dependency (those same
		// fixtures never set "jti" either way, so it stays "" in both
		// designs), which is why only it was switched to context. The latent
		// risk this leaves: on the one path where AuthMiddleware validated a
		// header token but upgradeConnection's own gate re-validates a
		// DIFFERENT token read from inside the WS frame (no cookie present),
		// UserID and SessionID could in principle name different sessions.
		// No current caller sends a second, different token there.
		UserID:    userID,
		SessionID: c.GetString("jti"),
		Conn:      conn,
		CreatedAt: time.Now(),
	}

	// Deadlines here govern reads the caller performs after this function
	// returns; a failed set surfaces there as a read error instead.
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	return connection, nil
}

// wsRegistration carries the per-site registration policy serveWS enforces on
// a caller's behalf: whether the connection is metered against the per-user
// cap, and what to do when it is refused. The eight WS handlers sharing a
// ConnectionManager are NOT interchangeable here — four distinct close codes
// and reason strings are wire-visible behaviour (frontend/src/lib/ws.ts
// branches on 4401/4429 to suppress its reconnect ladder) and one caller
// (backup.go's wsAttach) must never refuse at all — so this stays a
// per-call-site value, never a single shared default.
type wsRegistration struct {
	// unmetered registers the connection without consuming a per-user cap
	// slot (see ConnectionManager.AddUnmetered) and never refuses. Only
	// backup.go's wsAttach sets this (agent-os-pu4y): abandoning an
	// already-running durable operation's only viewer at the cap is worse
	// than leaving it uncapped.
	unmetered    bool
	refuseCode   int
	refuseReason string
	// onRefuse runs before the refusal close frame is written, e.g. to log
	// the refusal with site-specific fields. Optional.
	onRefuse func(conn *Connection)
}

// errWSRefused marks a serveWS error as a registration refusal (the
// per-user cap via cm.Add), as opposed to an upgrade/auth failure from
// upgradeConnection. Callers distinguish the two with errors.Is(err,
// errWSRefused): by the time serveWS returns a refusal error it has already
// run reg.onRefuse, written the refusal close frame, and closed the socket,
// so a caller must NOT additionally report it the way it reports an
// upgrade/auth failure (e.g. handleError/c.Error) — by that point
// upgrader.Upgrade has already hijacked the connection, so writing a JSON
// error body would hit an already-hijacked ResponseWriter (http.ErrHijacked)
// (agent-os-o1jp.1, adversary-caught: collapsing the two error branches
// without this distinction silently routed cap refusals through the
// upgrade-failure handling at 4 sites that never did that before).
var errWSRefused = errors.New("websocket connection refused: registration limit")

// serveWS upgrades the connection (see upgradeConnection) and, unless cm is
// nil, registers it per reg, returning a release func that undoes exactly
// what was done: a nil cm skips registration and release only closes the
// socket; otherwise release closes the socket and removes the registration.
//
// On any error the connection is already fully torn down (upgradeConnection
// itself closes on every one of its own error returns; a registration
// refusal here writes the refusal close frame and closes before returning)
// and the returned release is nil — callers must not call it. See
// errWSRefused for how callers must distinguish the two error causes.
//
// cm == nil skips registration entirely rather than failing, matching
// backup.go's pre-existing nil tolerance (the only one of the eight sites
// that guarded cm before this helper existed) rather than the other seven
// sites' unguarded h.cm.Add — extending the more defensive shape everywhere
// is a pure widening, since no test fixture for any of the eight sites
// passes a nil cm.
func serveWS(c *gin.Context, db *database.DB, jwtSecret string, authDisabled bool, cm *ConnectionManager, reg wsRegistration) (*Connection, func(), error) {
	conn, err := upgradeConnection(c, db, jwtSecret, authDisabled)
	if err != nil {
		return nil, nil, err
	}

	if cm == nil {
		return conn, sync.OnceFunc(func() {
			conn.Conn.Close()
		}), nil
	}

	if reg.unmetered {
		cm.AddUnmetered(conn.ID, conn)
	} else if err := cm.Add(conn.ID, conn); err != nil {
		if reg.onRefuse != nil {
			reg.onRefuse(conn)
		}
		writeCloseMessage(conn.Conn, reg.refuseCode, reg.refuseReason)
		conn.Conn.Close()
		return nil, nil, errors.Join(errWSRefused, err)
	}

	// Close-then-Remove, not the other way round: waitForServerSideClose
	// (monitoring_metrics_close_test.go) polls cm.Count()==0 as
	// synchronisation and then asserts the underlying socket is actually
	// closed. Remove-then-Close would let Count() reach 0 before the socket
	// closes, narrowing that test's margin (agent-os-o1jp.1, H8).
	return conn, sync.OnceFunc(func() {
		conn.Conn.Close()
		cm.Remove(conn.ID)
	}), nil
}
