package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
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
	ID         string
	UserID     string
	Conn       *websocket.Conn
	CreatedAt  time.Time
	WriteMutex sync.Mutex
}

type ConnectionManager struct {
	connections map[string]*Connection
	userCounts  map[string]int
	mu          sync.RWMutex
	maxPerUser  int
}

func NewConnectionManager(maxPerUser int) *ConnectionManager {
	if maxPerUser <= 0 {
		maxPerUser = DefaultConnectionLimit
	}
	return &ConnectionManager{
		connections: make(map[string]*Connection),
		userCounts:  make(map[string]int),
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
	return nil
}

func (cm *ConnectionManager) Remove(id string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if conn, exists := cm.connections[id]; exists {
		cm.userCounts[conn.UserID]--
		if cm.userCounts[conn.UserID] <= 0 {
			delete(cm.userCounts, conn.UserID)
		}
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
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for id, conn := range cm.connections {
		if conn.Conn != nil {
			writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Server shutting down")
			conn.Conn.Close()
		}
		delete(cm.connections, id)
	}
	cm.userCounts = make(map[string]int)
}

func authenticateToken(token string, db *database.DB, jwtSecret string) (string, error) {
	claims, err := middleware.ValidateJWT(token, jwtSecret)
	if err != nil {
		if err.Error() == "token is expired by" {
			return "", &models.AppError{
				Code:    models.ErrSessionExpired,
				Message: "Token has expired",
				Status:  http.StatusUnauthorized,
			}
		}
		return "", &models.AppError{
			Code:    models.ErrUnauthorized,
			Message: "Invalid token",
			Status:  http.StatusUnauthorized,
		}
	}

	if jti, ok := claims["jti"].(string); ok {
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
	}

	userID, ok := claims["sub"].(string)
	if !ok || userID == "" {
		return "", &models.AppError{
			Code:    models.ErrUnauthorized,
			Message: "Invalid user ID in token",
			Status:  http.StatusUnauthorized,
		}
	}

	return userID, nil
}

func writeJSON(conn *websocket.Conn, v interface{}) error {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return conn.WriteJSON(v)
}

func readJSON(conn *websocket.Conn, v interface{}) error {
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	return conn.ReadJSON(v)
}

func safeWriteJSON(c *Connection, v interface{}) error {
	c.WriteMutex.Lock()
	defer c.WriteMutex.Unlock()
	return writeJSON(c.Conn, v)
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
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	msg := websocket.FormatCloseMessage(closeCode, reason)
	if err := conn.WriteMessage(websocket.CloseMessage, msg); err != nil {
		slog.Debug("Failed to send close message", "error", err)
		return
	}

	time.Sleep(100 * time.Millisecond)
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
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var authMsg struct {
				Type  string `json:"type"`
				Token string `json:"token"`
			}
			if err := conn.ReadJSON(&authMsg); err != nil {
				writeCloseMessage(conn, CloseCodeAuthFailure, "Auth timeout")
				conn.Close()
				return nil, &models.AppError{Code: models.ErrUnauthorized, Message: "No auth message received", Status: 401}
			}

			if authMsg.Type != "auth" || authMsg.Token == "" {
				writeCloseMessage(conn, CloseCodeAuthFailure, "Invalid auth message")
				conn.Close()
				return nil, &models.AppError{Code: models.ErrUnauthorized, Message: "Invalid auth message", Status: 401}
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
		ID:        connectionID,
		UserID:    userID,
		Conn:      conn,
		CreatedAt: time.Now(),
	}

	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	return connection, nil
}
