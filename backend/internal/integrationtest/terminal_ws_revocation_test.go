//go:build integration

// Package integrationtest — WebSocket-level forced close on session
// revocation (agent-os-eklo).
//
// WHAT THIS UNIQUELY COVERS, stated narrowly: the link from a socket closed by
// another goroutine, through handleTerminalWS unwinding, to its deferred
// CloseSession actually running. That link is the entire mechanism by which
// revoking a session stops a terminal WebSocket from being a live shell, and
// nothing exercised it before.
//
// It is NOT new coverage of the reap itself. terminal_reap_test.go already
// covers CreateSession/CloseSession directly on alpine:3.21
// (TestCloseSession_ReapsShellInsideContainer) and on an image with no `ps`
// binary (TestCloseSession_ReapsShellOnImageWithoutPs). What those cannot show
// is that a revocation arriving from ConnectionManager.CloseForSession ever
// reaches that code path at all — they call CloseSession themselves. Likewise
// handlers/ws_test.go drives CloseForSession against synthetic Connection
// values that have no session, no PTY and no container behind them. This test
// is the join, and only the join.
//
// ── WHY ONE CONTAINER AND NOT TWO ──────────────────────────────────────────
//
// The reap works by exec'ing into the target container and scanning THAT
// container's own /proc for the CAPSTAN_SESSION=<uuid> marker
// (services/terminal.go, reapContainerShell). Put the two sessions in two
// containers and the survivor's shell is out of reach of that scan no matter
// what the marker logic does — it would be protected by the PID/mount
// namespace boundary, not by the code under test, and the control arm would
// degrade to "an untouched container is untouched": a check that cannot fail.
//
// Both sessions therefore share ONE container, where both shells live in one
// /proc and the per-session marker is the only thing separating them. That is
// exactly the discrimination the reap ships.
//
// ── HOW A COUNT IS GIVEN PER-SESSION IDENTITY ──────────────────────────────
//
// dockerTopShellCount returns a bare count, so 2→1 shows "one shell was
// reaped" and not "the RIGHT one was reaped". Each session therefore
// backgrounds a child with a distinct command name; a child inherits its
// parent's environment, so it carries the session marker and shares the
// session's fate:
//
//	PID 1 of the container   sleep    (terminalReapStackYAML)
//	revoked session's child  tail
//	survivor session's child watch
//
// All three names must differ. terminalReapChildStackYAML exists because a
// container whose PID 1 is `sleep` can never see a `sleep` child count reach
// zero; the same trap applies to every name here, which is why PID 1 stays
// `sleep` while the children are `tail` and `watch`. Both children are idle:
// verified live that `tail -f /dev/null` + `watch -n 9999 true` hold the
// container at 0.00% CPU, where the obvious alternative `yes > /dev/null`
// pinned a core at 101%.
//
// The survivor's child is not redundant with the survivor's own liveness
// check. A reap that matched on the marker's NAME while ignoring its per-
// session VALUE would kill the survivor's shell too, which the echo below
// catches — but one that over-matched only on descendants would leave the
// shell and take the child, and only a child count catches that.
//
// ── THE 4401 TRAP ──────────────────────────────────────────────────────────
//
// Close code 4401 has THREE emitters on this one route, and two of them fire
// BEFORE CreateSession — producing 4401 AND a zero shell count, which is
// byte-for-byte the pair of assertions this bead asks for, with the revocation
// code never invoked:
//
//  1. upgradeConnection — "Auth failed", "Auth timeout", "Invalid auth message"
//  2. assertContainerInStack's deny closure — "Container does not belong to this stack"
//  3. closeMatching — "Session revoked"   ← the only one under test
//
// Two defences. First, the close TEXT is asserted, not just the code; no other
// emitter produces "Session revoked". Second, both shells are observed live
// and answering before anything is revoked, which rules out emitters 1 and 2
// empirically rather than by argument.
//
// Emitter 1 is also the reason authentication here is by capstan_token COOKIE
// and not an Authorization header. AuthMiddleware accepts either, but
// upgradeConnection re-authenticates independently and reads ONLY the cookie:
// header-only auth would pass the middleware, publish a jti, then strand
// upgradeConnection waiting 5s for an in-band auth frame and emit 4401 "Auth
// timeout" — green test, nothing proven. The cookie is the only configuration
// where both gates pass on the same token and the same session.
//
// ── WHY THE REAL AuthMiddleware ────────────────────────────────────────────
//
// Connection.SessionID is populated in upgradeConnection as c.GetString("jti"),
// a key only middleware.AuthMiddleware ever publishes. Every pre-existing
// WebSocket fixture in this repo (handlers/terminal_scope_test.go's
// newTerminalFixture, compose_env_test.go's authCtx) builds a bare gin.New()
// router with no AuthMiddleware, leaving SessionID == "". CloseForSession("")
// deliberately matches nothing, so a test ported onto one of those fixtures
// would revoke nothing and prove nothing. This fixture runs the real
// middleware against a real minted JWT and a real session row.
package integrationtest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/handlers"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

const (
	// terminalWSJWTSecret only ever signs tokens for this test's own
	// httptest server.
	terminalWSJWTSecret = "test-secret-key-32-chars-long!!!"

	// terminalWSJWTIssuer mirrors middleware.jwtIssuer, which is unexported.
	// ValidateJWT is built with jwt.WithIssuer, so a token missing this claim
	// is rejected before AuthMiddleware ever publishes a jti — which would
	// surface as a revocation failure rather than as a bad token, hence
	// stating it explicitly rather than leaving it as a copied literal.
	terminalWSJWTIssuer = "capstan"

	terminalWSUserID = "user-eklo"
	terminalWSStack  = "stack-eklo"

	// Close text emitted by closeMatching, and by nothing else on this route.
	terminalWSRevokedText = "Session revoked"

	// Distinct command names for the two sessions' backgrounded children —
	// see the per-session identity note in the package doc above.
	// The trailing "&" is essential, not stylistic: without it the child runs
	// in the FOREGROUND and blocks its shell, so the session can never answer
	// another command and every later echo is swallowed as that child's stdin.
	terminalWSRevokedChildCmd  = "tail -f /dev/null &"
	terminalWSRevokedChildComm = "tail"
	terminalWSSurvivorChildCmd = "watch -n 9999 true >/dev/null 2>&1 &"
	//nolint:gosec // G101 false positive: a busybox applet name, not a credential
	terminalWSSurvivorChildComm = "watch"
)

// ── fixture ─────────────────────────────────────────────────────────────────

// terminalWSLister answers the compose-project membership lookup that
// handleTerminalWS performs via assertContainerInStack, without a real
// DockerService. It must report the REAL container name of the REAL running
// container: otherwise the handler denies before CreateSession is reached and
// every later shell count is a trivially uncontrolled zero.
type terminalWSLister struct {
	project   string
	container string
}

func (l *terminalWSLister) GetContainerList(projectName string) ([]models.Container, error) {
	if projectName != l.project {
		return []models.Container{}, nil
	}
	return []models.Container{{ID: "id-" + l.container, Name: l.container}}, nil
}

// terminalWSSession is one of the two WebSockets into the shared container.
type terminalWSSession struct {
	label string
	jti   string
	token string

	// childCmd is typed into the shell; childComm is the command name that
	// child then reports to `docker top`, giving this session an identity a
	// bare shell count cannot.
	childCmd  string
	childComm string

	client *terminalWSClient
}

type terminalWSFixture struct {
	server    *httptest.Server
	db        *database.DB
	cm        *handlers.ConnectionManager
	svc       *services.TerminalService
	container string
}

// newTerminalWSFixture stands up a real HTTP server carrying the real
// AuthMiddleware, so the upgrade is a genuine authenticated request and
// Connection.SessionID is populated exactly as in production. authDisabled is
// false throughout: that branch of both AuthMiddleware and upgradeConnection
// skips the token entirely and never sets a jti.
//
// InitUpgrader is deliberately NOT called. It is a global write to handlers
// package state, and the zero-value upgrader already accepts this dial —
// CheckOrigin is nil, so gorilla falls back to checkSameOrigin, which returns
// true when the request carries no Origin header. websocket.DefaultDialer
// sends none, and none is set below.
func newTerminalWSFixture(t *testing.T, cm *handlers.ConnectionManager, dir, project, container string, sessions ...*terminalWSSession) *terminalWSFixture {
	t.Helper()

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	now := time.Now()
	require.NoError(t, db.CreateUser(models.User{
		ID:        terminalWSUserID,
		Username:  "eklo",
		Password:  "unused-this-test-never-logs-in",
		CreatedAt: now,
		UpdatedAt: now,
	}))
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path:      dir,
		Name:      filepath.Base(dir),
		ScannedAt: now,
	}))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          terminalWSStack,
		Directory:   dir,
		ComposeFile: "compose.yaml",
		ProjectName: project,
		Status:      "running",
	}))

	for _, s := range sessions {
		// The session row AuthMiddleware looks up for this jti. Without it the
		// middleware 401s the upgrade and there is no WebSocket to revoke.
		require.NoError(t, db.CreateSession(models.Session{
			ID:        s.jti,
			UserID:    terminalWSUserID,
			ExpiresAt: now.Add(time.Hour),
			CreatedAt: now,
		}))
		s.token = mintTerminalWSToken(t, s.jti)
	}

	svc := services.NewTerminalService(&config.Config{})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	group.Use(middleware.AuthMiddleware(db, terminalWSJWTSecret, false, ""))
	handlers.NewTerminalHandler(
		svc,
		&terminalWSLister{project: project, container: container},
		db, cm, services.NewActionLogger(db),
	).RegisterRoutes(group, terminalWSJWTSecret, false)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &terminalWSFixture{server: srv, db: db, cm: cm, svc: svc, container: container}
}

// mintTerminalWSToken signs the token that both AuthMiddleware (via
// extractBearerToken's capstan_token cookie fallback) and upgradeConnection
// (via the same cookie) will validate for this session.
func mintTerminalWSToken(t *testing.T, jti string) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":      terminalWSUserID,
		"jti":      jti,
		"iss":      terminalWSJWTIssuer,
		"username": "eklo",
		// ValidateJWT is built WithExpirationRequired; a token with no "exp"
		// is rejected outright.
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(terminalWSJWTSecret))
	require.NoError(t, err)
	return signed
}

// ── client ──────────────────────────────────────────────────────────────────

// terminalWSClient wraps a dialed terminal WebSocket with a permanently
// running reader, because both halves of this test need the connection
// readable for its whole life: the revoked session must observe a close code
// whenever it arrives, and the survivor must be shown still carrying PTY
// output afterwards. handlers/terminal_scope_test.go's dialTerminal reads
// until the first error and can serve neither.
type terminalWSClient struct {
	conn *websocket.Conn

	mu        sync.Mutex
	out       bytes.Buffer
	closeCode int
	closeText string

	closed chan struct{}
}

func newTerminalWSClient(conn *websocket.Conn) *terminalWSClient {
	c := &terminalWSClient{conn: conn, closed: make(chan struct{})}
	// Dual capture, as dialTerminal does: the close handler catches a proper
	// close frame, the CloseError fallback in readLoop catches the rest.
	// With only one of the two, a plain EOF reports code 0.
	conn.SetCloseHandler(func(code int, text string) error {
		c.mu.Lock()
		c.closeCode, c.closeText = code, text
		c.mu.Unlock()
		return nil
	})
	go c.readLoop()
	return c
}

// readLoop deliberately sets NO read deadline. A deadline would close the
// survivor's connection on its own schedule, which is precisely the outcome
// the survivor exists to rule out.
func (c *terminalWSClient) readLoop() {
	defer close(c.closed)

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			if c.closeCode == 0 {
				if ce, ok := err.(*websocket.CloseError); ok {
					c.closeCode, c.closeText = ce.Code, ce.Text
				}
			}
			c.mu.Unlock()
			return
		}
		c.mu.Lock()
		c.out.Write(data)
		c.mu.Unlock()
	}
}

func (c *terminalWSClient) output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

func (c *terminalWSClient) closeInfo() (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCode, c.closeText
}

func (c *terminalWSClient) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

func (c *terminalWSClient) typeLine(t *testing.T, line, when string) {
	t.Helper()
	require.NoErrorf(t, c.conn.WriteMessage(websocket.BinaryMessage, []byte(line+"\n")),
		"%s: writing to the WebSocket failed", when)
}

// dialTerminalWS opens the real terminal WebSocket for one session,
// authenticated by that session's cookie. No Origin header is set — see
// newTerminalWSFixture.
func dialTerminalWS(t *testing.T, f *terminalWSFixture, s *terminalWSSession) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(f.server.URL, "http") +
		"/api/ws/terminal/" + terminalWSStack + "/" + f.container

	header := http.Header{}
	header.Set("Cookie", "capstan_token="+s.token)

	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	require.NoErrorf(t, err, "dialing %s (session %s)", url, s.label)
	require.NoError(t, resp.Body.Close())

	s.client = newTerminalWSClient(conn)
	t.Cleanup(func() { _ = conn.Close() })
}

// requireShellEcho proves a terminal WebSocket is not merely un-closed but
// still joined end-to-end to a live shell: it types a command and waits for
// the SHELL'S OWN OUTPUT to come back.
//
// The marker is typed with a quote in the middle (capstan-'alive'-x) and
// searched for without it (capstan-alive-x). A PTY echoes typed characters
// back verbatim, so searching for the literal text typed would match that echo
// alone and pass against a shell that had already died. The quotes are what
// make a match proof that a shell parsed and ran the line.
func requireShellEcho(t *testing.T, c *terminalWSClient, marker, when string) {
	t.Helper()

	typed := strings.Replace(marker, "alive", "'alive'", 1)
	require.NotEqual(t, marker, typed, "marker must contain \"alive\"")
	c.typeLine(t, "echo "+typed, when)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(c.output(), marker) {
			return
		}
		if c.isClosed() {
			code, text := c.closeInfo()
			t.Fatalf("%s: the connection closed (code %d %q) before the shell answered", when, code, text)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: shell never echoed %q back within 15s; output so far: %q", when, marker, c.output())
}

func requireStillOpen(t *testing.T, c *terminalWSClient, when string) {
	t.Helper()

	if c.isClosed() {
		code, text := c.closeInfo()
		t.Fatalf("%s: connection was closed with code %d (%q), want it left open", when, code, text)
	}
}

// requireClosedWithCode waits for the close the revocation must cause, then
// asserts both the code and the text. The text is what separates
// closeMatching from the two other 4401 emitters on this route — see the
// package doc.
func requireClosedWithCode(t *testing.T, c *terminalWSClient, wantCode int, wantText string, within time.Duration, when string) {
	t.Helper()

	select {
	case <-c.closed:
	case <-time.After(within):
		t.Fatalf("%s: connection was still open after %s; the revocation never reached it", when, within)
	}

	code, text := c.closeInfo()
	if code != wantCode || text != wantText {
		t.Fatalf("%s: close = %d %q, want %d %q\n"+
			"  (4401 with different text means a DIFFERENT emitter fired: %q is upgradeConnection, "+
			"%q is assertContainerInStack; only %q is the revocation under test)",
			when, code, text, wantCode, wantText,
			"Auth timeout", "Container does not belong to this stack", terminalWSRevokedText)
	}
}

// dockerTopDump returns raw `docker top` output for a failure message.
// reapContainerShell logs at Debug and returns nothing, so without this a
// failure reads as "still 1 shell after 10s" with no diagnosis.
func dockerTopDump(containerName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "top", containerName, "-eo", "pid,comm,args").CombinedOutput() //nolint:gosec // fixed argv; containerName is compose-derived
	if err != nil {
		return "docker top failed: " + err.Error() + "\n" + string(out)
	}
	return string(out)
}

// ── the test ────────────────────────────────────────────────────────────────

// TestTerminalWSRevocationClosesSocketAndReapsShell opens two real terminal
// WebSockets into ONE real container on two different sessions, revokes one,
// and requires that
//
//	(a) that client sees close code 4401 with text "Session revoked", and
//	(b) that session's shell AND its backgrounded child are gone from the
//	    container,
//
// while the other connection stays open, still round-trips a command through
// its live shell, and keeps both its shell and its own child.
//
// Ordering is load-bearing. The reap is asynchronous — CloseForSession closes
// the socket from another goroutine, and the reap runs on handleTerminalWS's
// deferred CloseSession whenever that handler unwinds — so every survivor
// assertion is made only AFTER the revoked session's reap has been OBSERVED
// to complete. Asserted concurrently, the survivor's counts would read
// correct simply because nothing had had time to happen yet, which is a check
// that cannot fail.
func TestTerminalWSRevocationClosesSocketAndReapsShell(t *testing.T) {
	RequireDocker(t)

	// terminalReapStackYAML's PID 1 is `sleep`, which is neither child's
	// command name — see the package doc on why all three must differ.
	dir, project, _ := NewTempStack(t, terminalReapStackYAML)
	RunCompose(t, dir, project, "up", "-d")
	AssertContainerState(t, dir, project, "target", true)
	container := project + "-target-1"

	revoked := &terminalWSSession{
		label: "revoked", jti: "session-revoked",
		childCmd: terminalWSRevokedChildCmd, childComm: terminalWSRevokedChildComm,
	}
	survivor := &terminalWSSession{
		label: "survivor", jti: "session-survivor",
		childCmd: terminalWSSurvivorChildCmd, childComm: terminalWSSurvivorChildComm,
	}

	cm := handlers.NewConnectionManager(5)
	f := newTerminalWSFixture(t, cm, dir, project, container, revoked, survivor)

	// Controlled zeros. Without these, every zero after the revocation could
	// equally mean the process was never there.
	require.Equal(t, 0, dockerTopShellCount(t, container), "sanity: no shell in the container yet")
	require.Equal(t, 0, dockerTopCommandCount(t, container, revoked.childComm), "sanity: no %s yet", revoked.childComm)
	require.Equal(t, 0, dockerTopCommandCount(t, container, survivor.childComm), "sanity: no %s yet", survivor.childComm)

	dialTerminalWS(t, f, revoked)
	dialTerminalWS(t, f, survivor)

	// Both shells live. This is also the synchronisation point that closes the
	// race where CloseForSession could run before registration and match
	// nothing: handleTerminalWS calls cm.Add BEFORE CreateSession, so a live
	// shell implies Add already ran.
	waitForShellCount(t, container, 2, "both WebSockets must have spawned a shell in the shared container")
	require.Equal(t, 2, cm.Count(), "both WebSockets must be registered with the ConnectionManager")
	require.Equal(t, 2, cm.CountByUser(terminalWSUserID), "both connections must count against the user's cap")
	require.Equal(t, 2, f.svc.SessionCount(), "both PTY sessions must be live")

	// Both shells are not merely present but responsive — this is what rules
	// out 4401 emitters 1 and 2 empirically: neither can produce a shell that
	// answers, because both fire before CreateSession.
	requireShellEcho(t, revoked.client, "capstan-alive-revoked", "before any revocation, revoked session")
	requireShellEcho(t, survivor.client, "capstan-alive-survivor", "before any revocation, survivor session")

	// Each session backgrounds its own identifying child. Children inherit the
	// CAPSTAN_SESSION marker, so each shares its session's fate.
	revoked.client.typeLine(t, revoked.childCmd, "starting the revoked session's child")
	survivor.client.typeLine(t, survivor.childCmd, "starting the survivor session's child")
	waitForCommandCount(t, container, revoked.childComm, 1, "the revoked session's child must start")
	waitForCommandCount(t, container, survivor.childComm, 1, "the survivor session's child must start")

	// ── negative controls ───────────────────────────────────────────────────
	// Neither call names a live connection's session, so neither may close
	// anything. The empty-string case is the agent-os-teop guard itself; the
	// unmatched-id case is the load-bearing one, because it is what rules out
	// "CloseForSession closes everything regardless of argument" — without it,
	// the real revocation below would not discriminate.
	cm.CloseForSession("")
	cm.CloseForSession("session-matching-no-live-connection")

	// A FIXED SETTLE WINDOW, DELIBERATELY, and not a bounded poll. You cannot
	// poll for the absence of an event: a poll is the right instrument for a
	// condition expected to become true, but these two calls must leave
	// everything unchanged, and polling would let the assertions race ahead of
	// a close that was in flight and "pass" before it landed. closeMatching
	// writes its frame synchronously with grace == 0 on this path, so one
	// second over loopback is generous by orders of magnitude. Please do not
	// mechanically convert this to a poll under a "no fixed sleeps" rule.
	time.Sleep(time.Second)

	// Both still open after (i) and (ii), not merely at the end: if either
	// died here for an unrelated reason, the real revocation below would pass
	// trivially against an already-dead socket.
	requireStillOpen(t, revoked.client, "after CloseForSession(\"\") and an unmatched session id")
	requireStillOpen(t, survivor.client, "after CloseForSession(\"\") and an unmatched session id")
	require.Equal(t, 2, cm.Count(), "an unmatched CloseForSession must not deregister anything")
	require.Equal(t, 2, dockerTopShellCount(t, container), "an unmatched CloseForSession must not reap any shell")
	require.Equal(t, 1, dockerTopCommandCount(t, container, revoked.childComm), "an unmatched CloseForSession must not reap the revoked session's child")
	require.Equal(t, 1, dockerTopCommandCount(t, container, survivor.childComm), "an unmatched CloseForSession must not reap the survivor's child")
	// The survivor is checked with an echo; the revoked session deliberately
	// is NOT written to here. closeMatching passes grace == 0, so the frame
	// and the socket close back to back, and Linux sends RST rather than FIN
	// when closing a socket that still holds unread received data — a client
	// write landing just before the revocation can therefore cost the close
	// code and surface as 1006. The waitForCommandCount above already gave
	// this session a docker round trip of separation from its last write.
	requireShellEcho(t, survivor.client, "capstan-alive-survivor-neg", "after the unmatched revocations, survivor session")

	// ── the revocation under test ───────────────────────────────────────────
	cm.CloseForSession(revoked.jti)

	// (a) agent-os-teop: the auth-failure close code AND the text that
	// identifies closeMatching as the emitter.
	requireClosedWithCode(t, revoked.client, handlers.CloseCodeAuthFailure, terminalWSRevokedText,
		10*time.Second, "after revoking the session")

	// (b) agent-os-pnbj, reached THROUGH (a): closing the socket must unwind
	// handleTerminalWS far enough to run its deferred CloseSession, which is
	// what reaps the shell inside the container. This is the join, and it is
	// the only thing here that no existing test covers.
	//
	// Bounded polls with a deadline, never a fixed sleep — the reap crosses a
	// real docker exec round trip, so a fixed wait would pass on an idle
	// machine and flake on a loaded one.
	waitForShellCount(t, container, 1,
		"exactly one shell must remain: the revoked session's was reaped, the survivor's was not\n"+dockerTopDump(container))
	waitForCommandCount(t, container, revoked.childComm, 0,
		"the revoked session's child must be reaped too, not just its shell\n"+dockerTopDump(container))

	// ── the survivor, on the same instruments, only now ─────────────────────
	// Every assertion below runs AFTER the reap above was observed to
	// complete. Made any earlier, they would report "correct" simply because
	// the reap had not had time to run.
	requireStillOpen(t, survivor.client, "after revoking the OTHER session")
	requireShellEcho(t, survivor.client, "capstan-alive-survivor-post", "after revoking the OTHER session")
	require.Equal(t, 1, dockerTopCommandCount(t, container, survivor.childComm),
		"the survivor's child must outlive the other session's revocation\n"+dockerTopDump(container))
	require.Equal(t, 1, cm.Count(), "only the revoked connection may be deregistered")
	// Exactly one, not zero: a double decrement is the agent-os-pu4y bug class.
	require.Equal(t, 1, cm.CountByUser(terminalWSUserID), "the revoked connection must free exactly one cap slot")
	require.Equal(t, 1, f.svc.SessionCount(), "exactly one PTY session must remain")
}
