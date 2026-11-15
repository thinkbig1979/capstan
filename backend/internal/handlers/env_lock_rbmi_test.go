package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// agent-os-rbmi: a session that has not unlocked env values could read them
// back through the container terminal (`env`) and the container logs. These
// tests run the handlers behind the real middleware.EnvUnlock, the way
// main.go wires them, with auth ON for the unlock check (the handler's own
// upgrade auth stays disabled so no JWT is needed).

const (
	rbmiUser = "user-1"
	// rbmiSecret is a value of a sensitive key in the stack's .env. A log
	// line that prints it must show "***" to a locked session.
	rbmiSecret = "s3cr3tvalue42"
	// rbmiFlagSecret is named only by its flag: `--api-token=...`.
	rbmiFlagSecret = "tok9876flagged"
)

// rbmiLogLine is what the fake `docker compose logs` prints: the stack's
// secret value, unnamed, and a second secret behind a sensitive flag.
const rbmiLogLine = "web-1  | 2026-01-01T00:00:00Z connecting with " + rbmiSecret + " and --api-token=" + rbmiFlagSecret + " ok"

// rbmiRouter installs what main.go puts in front of every protected route
// that matters here: a userID (AuthMiddleware's job) and middleware.EnvUnlock.
func rbmiRouter(store *services.EnvUnlockStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("userID", rbmiUser)
		c.Next()
	})
	router.Use(middleware.EnvUnlock(store, false))
	return router
}

// rbmiFakeDocker puts a `docker` on PATH. `docker compose ... logs` prints
// rbmiLogLine (and, when follow is set, stays up so the stream stays open);
// `docker exec -it` sleeps, which is a live terminal session; anything else
// exits 0.
func rbmiFakeDocker(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$2\" = \"-it\" ] && exec sleep 30\n" +
		"logs=; follow=\n" +
		"for a in \"$@\"; do\n" +
		"  [ -n \"$logs\" ] && [ \"$a\" = -f ] && follow=1\n" +
		"  [ \"$a\" = logs ] && logs=1\n" +
		"done\n" +
		"[ -z \"$logs\" ] && exit 0\n" +
		"echo '" + rbmiLogLine + "'\n" +
		"[ -n \"$follow\" ] && exec sleep 30\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)) //nolint:gosec // test fixture must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// rbmiLogsServer serves LogsHandler for one stack whose .env holds rbmiSecret.
func rbmiLogsServer(t *testing.T, store *services.EnvUnlockStore) *httptest.Server {
	t.Helper()
	rbmiFakeDocker(t)

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	stackDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte("DB_PASSWORD="+rbmiSecret+"\n"), 0o600))
	createTestDirectory(t, db, stackDir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: stackDir, ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))

	router := rbmiRouter(store)
	NewLogsHandler(docker, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(5)).
		RegisterRoutes(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func rbmiMint(t *testing.T, store *services.EnvUnlockStore) string {
	t.Helper()
	token, _, err := store.Mint(rbmiUser)
	require.NoError(t, err)
	return token
}

func rbmiGetLogs(t *testing.T, srv *httptest.Server, token string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/stacks/stack-a/logs", nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set(middleware.EnvUnlockHeader, token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct {
		Lines []LogLine `json:"lines"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Lines, 1, "the fake docker prints one line")
	return body.Lines[0].Message
}

// rbmiFirstStreamedLine dials the log WebSocket and returns the first line's
// message.
func rbmiFirstStreamedLine(t *testing.T, srv *httptest.Server, header http.Header) string {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/logs/stack-a"
	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	require.NoError(t, err)
	resp.Body.Close()
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var line LogLine
	require.NoError(t, conn.ReadJSON(&line), "no log line arrived")
	return line.Message
}

func requireMasked(t *testing.T, msg string) {
	t.Helper()
	require.NotContains(t, msg, rbmiSecret, "a locked session read the stack's secret value in a log line")
	require.NotContains(t, msg, rbmiFlagSecret, "a locked session read a --api-token value in a log line")
	require.Contains(t, msg, services.SecretMask)
}

func requireRaw(t *testing.T, msg string) {
	t.Helper()
	require.Contains(t, msg, rbmiSecret)
	require.Contains(t, msg, rbmiFlagSecret)
}

// ── Logs, REST ─────────────────────────────────────────────────────────────

func TestGetLogs_LockedSessionSeesSecretsMasked(t *testing.T) {
	srv := rbmiLogsServer(t, services.NewEnvUnlockStore())
	requireMasked(t, rbmiGetLogs(t, srv, ""))
}

func TestGetLogs_UnlockedSessionSeesLinesRaw(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv := rbmiLogsServer(t, store)
	requireRaw(t, rbmiGetLogs(t, srv, rbmiMint(t, store)))
}

// ── Logs, WebSocket ────────────────────────────────────────────────────────

func TestStreamLogs_LockedSessionSeesSecretsMasked(t *testing.T) {
	srv := rbmiLogsServer(t, services.NewEnvUnlockStore())
	requireMasked(t, rbmiFirstStreamedLine(t, srv, nil))
}

func TestStreamLogs_UnlockedSessionSeesLinesRaw(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv := rbmiLogsServer(t, store)
	h := http.Header{}
	h.Set(middleware.EnvUnlockHeader, rbmiMint(t, store))
	requireRaw(t, rbmiFirstStreamedLine(t, srv, h))
}

// A browser cannot set X-Unlock-Token on an upgrade, so it offers the token
// as a WebSocket subprotocol. The server must echo only "capstan": echoing
// the token would hand it back in a response header.
func TestStreamLogs_UnlockBySubprotocolEchoesOnlyCapstan(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv := rbmiLogsServer(t, store)
	token := rbmiMint(t, store)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/logs/stack-a"
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"capstan", "capstan-unlock." + token}
	conn, resp, err := dialer.Dial(url, nil)
	require.NoError(t, err)
	defer conn.Close()
	resp.Body.Close()

	require.Equal(t, "capstan", resp.Header.Get("Sec-WebSocket-Protocol"))
	require.Equal(t, "capstan", conn.Subprotocol())
	for name, values := range resp.Header {
		for _, v := range values {
			require.NotContains(t, v, token, "the unlock token came back in response header %s", name)
		}
	}

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var line LogLine
	require.NoError(t, conn.ReadJSON(&line))
	requireRaw(t, line.Message)
}

// ── Terminal ───────────────────────────────────────────────────────────────

type rbmiTerminal struct {
	url   string
	store *services.EnvUnlockStore
}

func rbmiTerminalServer(t *testing.T) *rbmiTerminal {
	t.Helper()
	rbmiFakeDocker(t)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	createTestDirectory(t, db, "/test/dir")
	require.NoError(t, db.UpsertStack(models.Stack{ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml", ProjectName: "proj-a", Status: "running"}))

	lister := &fakeContainerLister{containersByProject: map[string][]string{"proj-a": {"proj-a-web-1"}}}
	store := services.NewEnvUnlockStore()
	router := rbmiRouter(store)
	NewTerminalHandler(services.NewTerminalService(&config.Config{}), lister, db, NewConnectionManager(5), services.NewActionLogger(db)).
		RegisterRoutes(router.Group("/api"), "test-secret-key-32-chars-long!!!", true)
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return &rbmiTerminal{url: "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/terminal/stack-a/proj-a-web-1", store: store}
}

// AC1: refused with 403 BEFORE the upgrade, so no shell is ever started.
func TestTerminalWS_LockedSessionRefused403BeforeUpgrade(t *testing.T) {
	f := rbmiTerminalServer(t)
	conn, resp, err := websocket.DefaultDialer.Dial(f.url, nil)
	if conn != nil {
		conn.Close()
	}
	require.Error(t, err, "a locked session got a terminal")
	require.NotNil(t, resp)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	var appErr models.AppError
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&appErr))
	require.Equal(t, models.ErrForbidden, appErr.Code)
}

func TestTerminalWS_UnlockedSessionOpens(t *testing.T) {
	f := rbmiTerminalServer(t)
	h := http.Header{}
	h.Set(middleware.EnvUnlockHeader, rbmiMint(t, f.store))
	conn, resp, err := websocket.DefaultDialer.Dial(f.url, h)
	require.NoError(t, err)
	defer conn.Close()
	resp.Body.Close()
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// The subprotocol route opens the terminal, and the token is in no log line
// and no response header.
func TestTerminalWS_UnlockBySubprotocolOpensAndTokenStaysOut(t *testing.T) {
	buf := captureHandlerLogs(t)
	f := rbmiTerminalServer(t)
	token := rbmiMint(t, f.store)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"capstan", "capstan-unlock." + token}
	conn, resp, err := dialer.Dial(f.url, nil)
	require.NoError(t, err)
	defer conn.Close()
	resp.Body.Close()
	require.Equal(t, "capstan", resp.Header.Get("Sec-WebSocket-Protocol"))
	conn.Close()
	require.NotContains(t, buf.String(), token)
}

// A refusal body must not quote the token it refused, whichever way it came.
func TestTerminalWS_DeadTokenRefusedWithoutQuotingIt(t *testing.T) {
	f := rbmiTerminalServer(t)
	token := rbmiMint(t, f.store)
	f.store.RevokeUser(rbmiUser)
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"capstan", "capstan-unlock." + token}
	conn, resp, err := dialer.Dial(f.url, nil)
	if conn != nil {
		conn.Close()
	}
	require.Error(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), token)
}

// Safe-defaults rule 15: an open terminal re-checks the unlock it was opened
// under. Once the token is revoked the socket closes with 4403.
func TestTerminalWS_ClosesWhenUnlockIsRevoked(t *testing.T) {
	f := rbmiTerminalServer(t)
	h := http.Header{}
	h.Set(middleware.EnvUnlockHeader, rbmiMint(t, f.store))
	conn, resp, err := websocket.DefaultDialer.Dial(f.url, h)
	require.NoError(t, err)
	defer conn.Close()
	resp.Body.Close()

	f.store.RevokeUser(rbmiUser)

	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	start := time.Now()
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		ce, ok := err.(*websocket.CloseError)
		require.True(t, ok, "the terminal ended without a close frame after %s: %v", time.Since(start), err)
		require.Equal(t, 4403, ce.Code, "close reason %q", ce.Text)
		return
	}
}
