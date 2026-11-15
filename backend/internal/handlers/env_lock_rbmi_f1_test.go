package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-rbmi review F1: the stack's secret list read only global.env, the
// project .env and the stack EnvFile, so a secret the locked COMPOSE view
// hides (an inline `environment:` value, or a service `env_file:`) was shown
// verbatim wherever the list masks by value: container logs and inspect.
// One value per source, so each source has its own red.
var f1Secrets = map[string]string{
	"inline environment map":  "inlinePw77zz",
	"inline environment list": "listPw33kk",
	"service env_file string": "svcEnvTok99qq",
	"service env_file list":   "moreEnv88jj",
}

const f1Compose = `services:
  app:
    image: app:1
    environment:
      DB_PASSWORD: inlinePw77zz
    env_file: secrets.env
  worker:
    image: app:1
    environment:
      - SMTP_PASSWORD=listPw33kk
    env_file:
      - ./more.env
`

// f1StackDir writes the stack folder: compose.yaml and its two service env
// files, and no .env, so no value is in the old list.
func f1StackDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(f1Compose), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secrets.env"), []byte("API_TOKEN=svcEnvTok99qq\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "more.env"), []byte("SECRET_KEY=moreEnv88jj\n"), 0o600))
	return dir
}

// f1Unnamed prints every value with no sensitive name beside it, so only the
// list can mask it.
const f1Unnamed = "connecting with inlinePw77zz listPw33kk svcEnvTok99qq moreEnv88jj done"

func requireF1Masked(t *testing.T, where, text string) {
	t.Helper()
	for source, v := range f1Secrets {
		require.NotContains(t, text, v, "%s: a locked session read the %s value", where, source)
	}
}

func f1LogsServer(t *testing.T) *httptest.Server {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"logs=; follow=\n" +
		"for a in \"$@\"; do\n" +
		"  [ -n \"$logs\" ] && [ \"$a\" = -f ] && follow=1\n" +
		"  [ \"$a\" = logs ] && logs=1\n" +
		"done\n" +
		"[ -z \"$logs\" ] && exit 0\n" +
		"echo 'app-1  | 2026-01-01T00:00:00Z " + f1Unnamed + "'\n" +
		"[ -n \"$follow\" ] && exec sleep 30\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)) //nolint:gosec // test fixture must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	docker := newTestDockerServiceAgainst(t, newFakeDockerMetricsServer(t, http.StatusOK, "[]", nil))
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	dir := f1StackDir(t)
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: dir, ComposeFile: "compose.yaml",
		ProjectName: "proj-a", Status: "running",
	}))
	router := rbmiRouter(services.NewEnvUnlockStore())
	NewLogsHandler(docker, db, "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(5)).
		RegisterRoutes(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

func TestF1_GetLogs_LockedMasksComposeInlineAndServiceEnvFileValues(t *testing.T) {
	srv := f1LogsServer(t)
	requireF1Masked(t, "REST logs", rbmiGetLogs(t, srv, ""))
}

func TestF1_StreamLogs_LockedMasksComposeInlineAndServiceEnvFileValues(t *testing.T) {
	srv := f1LogsServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws/logs/stack-a"
	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	resp.Body.Close()
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))
	var line LogLine
	require.NoError(t, conn.ReadJSON(&line))
	requireF1Masked(t, "WS logs", line.Message)
}

// The same list masks container inspect (resources.go inspectSecrets): a
// value in Cmd carries no sensitive name, so only the list can mask it.
func TestF1_InspectLocked_MasksComposeInlineAndServiceEnvFileValues(t *testing.T) {
	body := `{"Id":"c1","Name":"/app","Config":{"Image":"app:1","Cmd":["run","` + f1Unnamed + `"],` +
		`"Labels":{"com.docker.compose.project":"f1stack"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/containers/c1/json") {
			writeOrFailYvlh(t, w, []byte(body))
			return
		}
		writeOrFailYvlh(t, w, []byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dir := f1StackDir(t)
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "f1~stack:default", Directory: dir, ComposeFile: "compose.yaml", ProjectName: "f1stack",
	}))

	h := NewResourcesHandler(newTestDockerServiceAgainst(t, srv), db, nil)
	for _, unlocked := range []bool{false, true} {
		router := gin.New()
		if unlocked {
			router.Use(unlockedSiou)
		}
		router.GET("/resources/containers/:id/inspect", h.inspectContainer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/resources/containers/c1/inspect", nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		if unlocked {
			require.Contains(t, w.Body.String(), f1Unnamed, "CONTROL: unlocked inspect is unchanged")
			continue
		}
		requireF1Masked(t, "inspect", w.Body.String())
	}
}
