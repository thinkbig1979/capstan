package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// stubUnlock stands in for middleware.EnvUnlock with a check the test
// controls, and records every token the handler checks, so a test can wait on
// what the handler did instead of on the clock.
type stubUnlock struct {
	mu      sync.Mutex
	valid   map[string]bool
	checked chan string
}

func newStubUnlock(valid ...string) *stubUnlock {
	s := &stubUnlock{valid: map[string]bool{}, checked: make(chan string, 1024)}
	for _, v := range valid {
		s.valid[v] = true
	}
	return s
}

func (s *stubUnlock) set(token string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.valid[token] = ok
}

func (s *stubUnlock) middleware(openedWith string) gin.HandlerFunc {
	check := middleware.EnvUnlockCheck(func(token string) bool {
		s.mu.Lock()
		ok := s.valid[token]
		s.mu.Unlock()
		select {
		case s.checked <- token:
		default:
		}
		return ok
	})
	return func(c *gin.Context) {
		c.Set(middleware.CtxEnvUnlockCheck, check)
		if check(openedWith) {
			c.Set(middleware.CtxEnvUnlocked, true)
			c.Set(middleware.CtxEnvUnlockToken, openedWith)
		}
		c.Next()
	}
}

// waitChecked waits until the handler has checked token n more times.
func (s *stubUnlock) waitChecked(t *testing.T, token string, n int) {
	t.Helper()
	guard := time.After(time.Until(hangGuardDeadline(t)))
	for n > 0 {
		select {
		case got := <-s.checked:
			if got == token {
				n--
			}
		case <-guard:
			t.Fatalf("the handler never checked token %q", token)
		}
	}
}

func shortRecheck(t *testing.T) {
	t.Helper()
	old := unlockRecheckOverride.Load()
	unlockRecheckOverride.Store(int64(20 * time.Millisecond))
	t.Cleanup(func() { unlockRecheckOverride.Store(old) })
}

type liveTerminal struct {
	conn   *websocket.Conn
	closed chan *websocket.CloseError
	output chan []byte
}

func dialLiveTerminal(t *testing.T, unlock gin.HandlerFunc) *liveTerminal {
	t.Helper()
	rbmiFakeDocker(t)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml", ProjectName: "proj-a", Status: "running"}))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(unlock)
	lister := &fakeContainerLister{containersByProject: map[string][]string{"proj-a": {"proj-a-web-1"}}}
	NewTerminalHandler(services.NewTerminalService(&config.Config{}), lister, db, NewConnectionManager(5), nil).
		RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws/terminal/stack-a/proj-a-web-1", nil)
	require.NoError(t, err)
	resp.Body.Close()
	t.Cleanup(func() { conn.Close() })

	lt := &liveTerminal{conn: conn, closed: make(chan *websocket.CloseError, 1), output: make(chan []byte, 256)}
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	go func() {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				ce, _ := err.(*websocket.CloseError)
				lt.closed <- ce
				return
			}
			select {
			case lt.output <- data:
			default:
			}
		}
	}()
	return lt
}

func (lt *liveTerminal) requireOpen(t *testing.T) {
	t.Helper()
	select {
	case ce := <-lt.closed:
		t.Fatalf("the terminal closed while its unlock was live: %v", ce)
	default:
	}
}

// requireOutputContains waits for want in the terminal's output.
func (lt *liveTerminal) requireOutputContains(t *testing.T, want string) {
	t.Helper()
	var seen strings.Builder
	guard := time.After(time.Until(hangGuardDeadline(t)))
	for {
		select {
		case data := <-lt.output:
			seen.Write(data)
			if strings.Contains(seen.String(), want) {
				return
			}
		case ce := <-lt.closed:
			t.Fatalf("the terminal closed before %q came back (seen %q): %v", want, seen.String(), ce)
		case <-guard:
			t.Fatalf("%q never came back from the shell (seen %q)", want, seen.String())
		}
	}
}

func (lt *liveTerminal) requireClosedUnlockRequired(t *testing.T) {
	t.Helper()
	select {
	case ce := <-lt.closed:
		require.NotNil(t, ce, "the terminal ended without a close frame")
		require.Equal(t, CloseCodeUnlockRequired, ce.Code)
		require.Equal(t, "Unlock required", ce.Text)
	case <-time.After(time.Until(hangGuardDeadline(t))):
		t.Fatal("the terminal stayed open after its unlock ended")
	}
}

// D2: a tab that re-entered its password sends its new token in-band, and the
// shell outlives the token it was opened with; it closes when the new one
// ends.
func TestTerminalWS_InBandUnlockExtendsTheSession(t *testing.T) {
	shortRecheck(t)
	unlock := newStubUnlock("t1", "t2")
	lt := dialLiveTerminal(t, unlock.middleware("t1"))
	unlock.waitChecked(t, "t1", 2) // the open check, then a re-check

	require.NoError(t, lt.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"unlock","token":"t2"}`)))
	unlock.waitChecked(t, "t2", 1) // renew validated it

	unlock.set("t1", false)
	unlock.waitChecked(t, "t2", 3) // re-checks now run on t2
	lt.requireOpen(t)

	unlock.set("t2", false)
	lt.requireClosedUnlockRequired(t)
}

// A renewal is held to the same check as the header: a token that fails it
// extends nothing.
func TestTerminalWS_InBandUnlockWithDeadTokenExtendsNothing(t *testing.T) {
	shortRecheck(t)
	unlock := newStubUnlock("t1")
	lt := dialLiveTerminal(t, unlock.middleware("t1"))
	unlock.waitChecked(t, "t1", 1)

	require.NoError(t, lt.conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"unlock","token":"forged"}`)))
	unlock.waitChecked(t, "forged", 1)

	unlock.set("t1", false)
	lt.requireClosedUnlockRequired(t)
}

// D3: the lock is re-read per line, so a stream whose unlock ends switches to
// masked lines and stays open.
func TestStreamLogs_UnlockEndingMidStreamMasksFromTheNextLine(t *testing.T) {
	store := services.NewEnvUnlockStore()
	gate := filepath.Join(t.TempDir(), "go")
	srv := rbmiLogsServer(t, store)
	// rbmiLogsServer installed the one-line fake; replace it with one that
	// prints a second line only once the test creates gate.
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo '" + rbmiLogLine + "'\n" +
		"while [ ! -e " + gate + " ]; do sleep 0.02; done\n" +
		"echo '" + rbmiLogLine + "'\n" +
		"exec sleep 30\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)) //nolint:gosec // test fixture must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h := http.Header{}
	h.Set(middleware.EnvUnlockHeader, rbmiMint(t, store))
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/ws/logs/stack-a", h)
	require.NoError(t, err)
	resp.Body.Close()
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))

	var line LogLine
	require.NoError(t, conn.ReadJSON(&line))
	requireRaw(t, line.Message)

	store.RevokeUser(rbmiUser)
	require.NoError(t, os.WriteFile(gate, nil, 0o600))

	require.NoError(t, conn.ReadJSON(&line), "the stream closed instead of switching to masked")
	requireMasked(t, line.Message)
}

// Fails closed: a route that never passed through middleware.EnvUnlock reads
// as locked, so it can neither open a terminal nor stream raw lines.
func TestUnlockWatch_NoMiddlewareReadsLocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	w := newUnlockWatch(c)
	require.False(t, w.unlocked())
	require.False(t, w.renew("anything"))
}
