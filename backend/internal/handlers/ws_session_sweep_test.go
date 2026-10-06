package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// These tests cover agent-os-n4ca.4: an open WebSocket re-checks the session
// it was opened under, so a revocation made outside this process (the CLI
// `admin reset-password`, which deletes session rows from a separate process)
// closes it. Before the fix the session row was checked only at upgrade.

const sweepTestSecret = "test-secret-key-32-chars"

// newSweepWSServer serves one WS route behind the REAL AuthMiddleware, so the
// connection carries the jti AuthMiddleware publishes, exactly as every
// production WS route does (they all sit under main.go's `protected` group).
// The handler reads until the socket dies, like a handler with a read pump.
func newSweepWSServer(t *testing.T, db *database.DB, cm *ConnectionManager) *httptest.Server {
	t.Helper()
	router := gin.New()
	protected := router.Group("")
	protected.Use(middleware.AuthMiddleware(db, sweepTestSecret, false, ""))
	protected.GET("/ws/probe", func(c *gin.Context) {
		conn, release, err := serveWS(c, db, sweepTestSecret, false, cm, wsRegistration{
			refuseCode:   CloseCodeRateLimit,
			refuseReason: "Too many connections",
		})
		if err != nil {
			return
		}
		defer release()
		for {
			if _, _, err := conn.Conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func dialSweepWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/probe"
	header := http.Header{}
	header.Set("Cookie", "capstan_token="+token)
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	require.NoError(t, err)
	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })
	return conn
}

func seedSweepSession(t *testing.T, db *database.DB, user models.User, jti string) string {
	t.Helper()
	require.NoError(t, db.CreateSession(models.Session{
		ID:        jti,
		UserID:    user.ID,
		ExpiresAt: time.Now().Add(24 * time.Hour),
		CreatedAt: time.Now(),
	}))
	return generateTestToken(user.ID, user.Username, jti, sweepTestSecret)
}

// readCloseCode reads until the socket ends and returns the close code, or -1
// if deadline passed with the socket still open.
func readCloseCode(t *testing.T, conn *websocket.Conn, deadline time.Time) int {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(deadline))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			return closeErr.Code
		}
		var netErr interface{ Timeout() bool }
		if errors.As(err, &netErr) && netErr.Timeout() {
			return -1
		}
		t.Fatalf("unexpected read error (neither a close frame nor a timeout): %v", err)
	}
}

// TestSessionSweep_ClosesSocketWhoseSessionRowWasDeleted is the acceptance
// test: a session row deleted directly in the DB (what the CLI reset does,
// with no in-process CloseForSession call) closes the open socket with 4401,
// while a socket on a live session stays open across many sweeps. Two-sided on
// the same instrument and the same sweep, so neither a sweep that closes
// everything nor one that closes nothing can pass.
func TestSessionSweep_ClosesSocketWhoseSessionRowWasDeleted(t *testing.T) {
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	user := createTestUser(t, db, "sweepuser", "password123")
	revokedToken := seedSweepSession(t, db, user, "session-revoked-by-cli")
	liveToken := seedSweepSession(t, db, user, "session-still-live")

	cm := NewConnectionManager(10)
	srv := newSweepWSServer(t, db, cm)
	revokedConn := dialSweepWS(t, srv, revokedToken)
	liveConn := dialSweepWS(t, srv, liveToken)
	require.Eventually(t, func() bool { return cm.Count() == 2 }, time.Until(hangGuardDeadline(t)), 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const interval = 20 * time.Millisecond
	go ConnectionManagers{cm}.RunSessionSweep(ctx, db, interval)

	// The live socket survives several sweeps before anything is revoked.
	stayOpen := time.Now().Add(10 * interval) // wall-clock ok: bounded negative probe; a slow runner only runs fewer sweeps in it, it cannot close a live socket
	assert.Equal(t, -1, readCloseCode(t, liveConn, stayOpen),
		"a socket whose session row exists must stay open across sweeps")

	// What runResetPassword does (cmd/server/admin.go): delete the row, from
	// outside any handler, with no CloseForSession call.
	require.NoError(t, db.DeleteSessionsByUserExcluding(user.ID, "session-still-live"))

	assert.Equal(t, CloseCodeAuthFailure, readCloseCode(t, revokedConn, hangGuardDeadline(t)),
		"a socket whose session row was deleted must be closed with 4401 within a sweep interval")
	stayOpen = time.Now().Add(10 * interval) // wall-clock ok: bounded negative probe, as above
	assert.Equal(t, -1, readCloseCode(t, liveConn, stayOpen),
		"the live session's socket must stay open after the other session is revoked")
	assert.Equal(t, 1, cm.Count(), "only the revoked connection leaves the manager")
}

// TestSessionSweep_ClosesExpiredSession: AuthMiddleware and authenticateToken
// both reject a session past ExpiresAt, so an open socket must not outlive it.
func TestSessionSweep_ClosesExpiredSession(t *testing.T) {
	lookup := newFakeSessionLookup()
	lookup.set("expired", &models.Session{ID: "expired", ExpiresAt: time.Now().Add(-time.Minute)})
	lookup.set("live", &models.Session{ID: "live", ExpiresAt: time.Now().Add(time.Hour)})

	cm := NewConnectionManager(10)
	expired := &Connection{ID: uuid.NewString(), UserID: "u", SessionID: "expired"}
	live := &Connection{ID: uuid.NewString(), UserID: "u", SessionID: "live"}
	require.NoError(t, cm.Add(expired.ID, expired))
	require.NoError(t, cm.Add(live.ID, live))

	newSessionSweeper(ConnectionManagers{cm}, lookup).sweep()

	_, present := cm.Get(expired.ID)
	assert.False(t, present, "a connection on an expired session must be closed")
	_, present = cm.Get(live.ID)
	assert.True(t, present, "a connection on an unexpired session must stay")
}

// TestSessionSweep_SkipsConnectionsWithoutSession: AUTH_DISABLED connections
// ("anon:<ip>") carry no SessionID; there is no row to check, so the sweep
// must neither look one up nor close them.
func TestSessionSweep_SkipsConnectionsWithoutSession(t *testing.T) {
	lookup := newFakeSessionLookup()
	cm := NewConnectionManager(10)
	anon := &Connection{ID: uuid.NewString(), UserID: "anon:127.0.0.1"}
	require.NoError(t, cm.Add(anon.ID, anon))

	newSessionSweeper(ConnectionManagers{cm}, lookup).sweep()

	_, present := cm.Get(anon.ID)
	assert.True(t, present, "a connection with no SessionID must not be closed")
	assert.Zero(t, lookup.callCount(""), "an empty SessionID must never be looked up")
}

// TestSessionSweep_OneLookupPerDistinctSessionAcrossManagers: the cost of a
// sweep is one GetSession per distinct session, not per connection, and one
// sweeper covers every manager (main.go wires two).
func TestSessionSweep_OneLookupPerDistinctSessionAcrossManagers(t *testing.T) {
	lookup := newFakeSessionLookup()
	lookup.set("s1", &models.Session{ID: "s1", ExpiresAt: time.Now().Add(time.Hour)})
	shared := NewConnectionManager(10)
	terminal := NewConnectionManager(5)
	for i := 0; i < 3; i++ {
		c := &Connection{ID: uuid.NewString(), UserID: "u", SessionID: "s1"}
		require.NoError(t, shared.Add(c.ID, c))
	}
	termConn := &Connection{ID: uuid.NewString(), UserID: "u", SessionID: "s1"}
	require.NoError(t, terminal.Add(termConn.ID, termConn))

	newSessionSweeper(ConnectionManagers{shared, terminal}, lookup).sweep()
	assert.Equal(t, 1, lookup.callCount("s1"))

	lookup.remove("s1")
	newSessionSweeper(ConnectionManagers{shared, terminal}, lookup).sweep()
	assert.Equal(t, 0, shared.Count(), "revocation must reach the shared manager")
	assert.Equal(t, 0, terminal.Count(), "revocation must reach the terminal manager")
}

// TestSessionSweep_DBErrorClosesOnlyAfterConsecutiveFailures: one SQLite blip
// must not kill every socket (a closed terminal socket kills the user's
// shell), but a sustained fault must not hide a revocation forever. The 3rd
// consecutive failed lookup closes with 1011; a success in between resets.
func TestSessionSweep_DBErrorClosesOnlyAfterConsecutiveFailures(t *testing.T) {
	t.Run("third consecutive failure closes with 1011", func(t *testing.T) {
		lookup := newFakeSessionLookup()
		lookup.failWith("s1", errors.New("database is locked"))
		cm, conn, codes := newRecordingManager(t, "s1")
		s := newSessionSweeper(ConnectionManagers{cm}, lookup)

		s.sweep()
		s.sweep()
		_, present := cm.Get(conn.ID)
		require.True(t, present, "two consecutive lookup failures must leave the socket open")

		s.sweep()
		_, present = cm.Get(conn.ID)
		assert.False(t, present, "the third consecutive lookup failure must close the socket")
		assert.Equal(t, websocket.CloseInternalServerErr, codes.wait(t),
			"a DB fault closes with 1011 (client reconnects), not 4401 (client gives up)")
	})

	t.Run("a success in between resets the count", func(t *testing.T) {
		lookup := newFakeSessionLookup()
		cm, conn, _ := newRecordingManager(t, "s1")
		s := newSessionSweeper(ConnectionManagers{cm}, lookup)

		lookup.failWith("s1", errors.New("database is locked"))
		s.sweep()
		s.sweep()
		lookup.set("s1", &models.Session{ID: "s1", ExpiresAt: time.Now().Add(time.Hour)})
		s.sweep()
		lookup.failWith("s1", errors.New("database is locked"))
		s.sweep()
		s.sweep()

		_, present := cm.Get(conn.ID)
		assert.True(t, present, "fail, fail, ok, fail, fail is never 3 in a row, so the socket must stay")
	})

	t.Run("counters for sessions with no live connection are dropped", func(t *testing.T) {
		lookup := newFakeSessionLookup()
		lookup.failWith("s1", errors.New("database is locked"))
		cm, conn, _ := newRecordingManager(t, "s1")
		s := newSessionSweeper(ConnectionManagers{cm}, lookup)

		s.sweep()
		cm.Remove(conn.ID)
		s.sweep()
		assert.Empty(t, s.failures, "a session with no live connection must not keep a failure counter")
	})
}

// TestSessionSweep_StopsWhenContextEnds: the sweep is a boot-time goroutine
// (safe-defaults rule 3), so it must return when its context ends, and not
// before. Both sides on the same goroutine.
func TestSessionSweep_StopsWhenContextEnds(t *testing.T) {
	lookup := newFakeSessionLookup()
	cm := NewConnectionManager(10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ConnectionManagers{cm}.RunSessionSweep(ctx, lookup, time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("RunSessionSweep returned while its context was still live")
	case <-time.After(50 * time.Millisecond): // wall-clock ok: bounded negative probe; a slow runner cannot make a live-ctx sweep return
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("RunSessionSweep did not return after its context was cancelled (goroutine leak on shutdown)")
	}
}

// newRecordingManager registers one connection on sessionID backed by a real
// socket pair, and returns a channel-backed recorder of the close code the
// client side receives.
func newRecordingManager(t *testing.T, sessionID string) (*ConnectionManager, *Connection, *closeCodeRecorder) {
	t.Helper()
	rec := &closeCodeRecorder{ch: make(chan int, 1)}
	serverConns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConns <- c
	}))
	t.Cleanup(srv.Close)

	client, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	resp.Body.Close()
	t.Cleanup(func() { client.Close() })
	go func() {
		for {
			if _, _, err := client.ReadMessage(); err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) {
					rec.ch <- closeErr.Code
				} else {
					rec.ch <- -1
				}
				return
			}
		}
	}()

	conn := &Connection{ID: uuid.NewString(), UserID: "u", SessionID: sessionID, Conn: <-serverConns}
	cm := NewConnectionManager(10)
	require.NoError(t, cm.Add(conn.ID, conn))
	return cm, conn, rec
}

type closeCodeRecorder struct{ ch chan int }

func (r *closeCodeRecorder) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-r.ch:
		return code
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("client never saw the socket close")
		return 0
	}
}

// fakeSessionLookup stands in for *database.DB so a test can make a lookup
// fail with a non-NotFound error, which a real in-memory DB cannot do on cue.
type fakeSessionLookup struct {
	mu       sync.Mutex
	sessions map[string]*models.Session
	errs     map[string]error
	calls    map[string]int
}

func newFakeSessionLookup() *fakeSessionLookup {
	return &fakeSessionLookup{
		sessions: map[string]*models.Session{},
		errs:     map[string]error{},
		calls:    map[string]int{},
	}
}

func (f *fakeSessionLookup) set(id string, s *models.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.errs, id)
	f.sessions[id] = s
}

func (f *fakeSessionLookup) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
}

func (f *fakeSessionLookup) failWith(id string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[id] = err
}

func (f *fakeSessionLookup) callCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

func (f *fakeSessionLookup) GetSession(id string) (*models.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[id]++
	if err, ok := f.errs[id]; ok {
		return nil, err
	}
	if s, ok := f.sessions[id]; ok {
		return s, nil
	}
	return nil, &errdefs.NotFoundError{Kind: "session", Key: id}
}
