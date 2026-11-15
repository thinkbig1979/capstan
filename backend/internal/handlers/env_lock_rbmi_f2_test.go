package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-rbmi round-2 review F2: the compose file writes the secret as a
// default (${DB_PASSWORD:-...}), which the file list cannot read as the
// container gets it; the container's Env holds it resolved. A locked session
// reads logs and inspect masked with what the containers hold, and an
// unlocked one costs no container lookup (orch-dm-62 C1).

const (
	f2Resolved = "f2Resolved81qq"
	f2Compose  = "services:\n  app:\n    image: app:1\n    environment:\n      DB_PASSWORD: ${DB_PASSWORD:-" + f2Resolved + "}\n"
	f2Project  = "f2proj"
)

// f2DockerAPI answers the Engine API calls the lookup and inspect make:
// the stack's one container, c1, holding the resolved value in its Env, and
// c2 running a command that prints it with no name beside it. lists counts
// the /containers/json calls.
func f2DockerAPI(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var lists atomic.Int32
	inspect := map[string]string{
		"c1": `{"Id":"c1","Name":"/app","Path":"app","Args":[],"Config":{"Image":"app:1","Env":["DB_PASSWORD=` + f2Resolved + `","TZ=UTC"],` +
			`"Labels":{"com.docker.compose.project":"` + f2Project + `"}}}`,
		"c2": `{"Id":"c2","Name":"/worker","Path":"run","Args":["` + f2Resolved + `"],"Config":{"Image":"app:1","Cmd":["run","` + f2Resolved + `"],` +
			`"Labels":{"com.docker.compose.project":"` + f2Project + `"}}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", "1.47")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			lists.Add(1)
			writeOrFailYvlh(t, w, []byte(`[{"Id":"c1","Created":1},{"Id":"c2","Created":1}]`))
		case strings.HasSuffix(r.URL.Path, "/containers/c1/json"):
			writeOrFailYvlh(t, w, []byte(inspect["c1"]))
		case strings.HasSuffix(r.URL.Path, "/containers/c2/json"):
			writeOrFailYvlh(t, w, []byte(inspect["c2"]))
		default:
			writeOrFailYvlh(t, w, []byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &lists
}

func f2StackDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(f2Compose), 0o600))
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack-a", Directory: dir, ComposeFile: "compose.yaml", ProjectName: f2Project, Status: "running",
	}))
	return db
}

func f2LogsServer(t *testing.T, store *services.EnvUnlockStore) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"logs=; follow=\n" +
		"for a in \"$@\"; do\n" +
		"  [ -n \"$logs\" ] && [ \"$a\" = -f ] && follow=1\n" +
		"  [ \"$a\" = logs ] && logs=1\n" +
		"done\n" +
		"[ -z \"$logs\" ] && exit 0\n" +
		"echo 'app-1  | 2026-01-01T00:00:00Z connecting with " + f2Resolved + " ok'\n" +
		"[ -n \"$follow\" ] && exec sleep 30\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)) //nolint:gosec // test fixture must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	api, lists := f2DockerAPI(t)
	router := rbmiRouter(store)
	NewLogsHandler(newTestDockerServiceAgainst(t, api), f2StackDB(t), "test-secret-key-32-chars-long!!!", true, t.TempDir(), NewConnectionManager(5)).
		RegisterRoutes(router.Group("/api"))
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, lists
}

func TestF2_GetLogs_LockedMasksWhatTheContainerHolds(t *testing.T) {
	srv, lists := f2LogsServer(t, services.NewEnvUnlockStore())
	msg := rbmiGetLogs(t, srv, "")
	require.NotContains(t, msg, f2Resolved, "a locked session read the container's resolved secret in a log line")
	require.Positive(t, lists.Load(), "CONTROL: the locked path looked up the containers")
}

func TestF2_GetLogs_UnlockedMakesNoContainerLookup(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv, lists := f2LogsServer(t, store)
	msg := rbmiGetLogs(t, srv, rbmiMint(t, store))
	require.Contains(t, msg, f2Resolved, "CONTROL: an unlocked session reads lines raw")
	require.Zero(t, lists.Load(), "an unlocked session's logs made a Docker container lookup")
}

func TestF2_StreamLogs_LockedMasksWhatTheContainerHolds(t *testing.T) {
	srv, lists := f2LogsServer(t, services.NewEnvUnlockStore())
	msg := rbmiFirstStreamedLine(t, srv, nil)
	require.NotContains(t, msg, f2Resolved, "a locked session read the container's resolved secret in a streamed line")
	require.Positive(t, lists.Load(), "CONTROL: the locked path looked up the containers")
}

func TestF2_StreamLogs_UnlockedMakesNoContainerLookup(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv, lists := f2LogsServer(t, store)
	msg := rbmiFirstStreamedLine(t, srv, http.Header{middleware.EnvUnlockHeader: []string{rbmiMint(t, store)}})
	require.Contains(t, msg, f2Resolved, "CONTROL: an unlocked session reads lines raw")
	require.Zero(t, lists.Load(), "an unlocked session's log stream made a Docker container lookup")
}

// Inspect of c2: its argv prints the value with no name beside it, and only
// c1's Env holds it, so only the containers' own values can mask it.
func TestF2_InspectLocked_MasksWhatTheStacksContainersHold(t *testing.T) {
	api, lists := f2DockerAPI(t)
	h := NewResourcesHandler(newTestDockerServiceAgainst(t, api), f2StackDB(t), nil)
	for _, unlocked := range []bool{true, false} {
		router := gin.New()
		if unlocked {
			router.Use(unlockedSiou)
		}
		router.GET("/resources/containers/:id/inspect", h.inspectContainer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/resources/containers/c2/inspect", nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		if unlocked {
			require.Contains(t, w.Body.String(), f2Resolved, "CONTROL: unlocked inspect is unchanged")
			require.Zero(t, lists.Load(), "an unlocked inspect made a container list lookup")
			continue
		}
		require.NotContains(t, w.Body.String(), f2Resolved, "a locked inspect showed a value a sibling container holds")
	}
}

// A session that locks mid-stream, between two list refreshes, has its next
// line masked with the containers' values too: the list it streamed with
// while unlocked held only the file values.
func TestF2_StreamLogs_LockMidStreamMasksWhatTheContainerHolds(t *testing.T) {
	store := services.NewEnvUnlockStore()
	srv, lists := f2LogsServer(t, store)
	gate := filepath.Join(t.TempDir(), "go")
	bin := t.TempDir()
	line := "app-1  | 2026-01-01T00:00:00Z connecting with " + f2Resolved + " ok"
	script := "#!/bin/sh\n" +
		"echo '" + line + "'\n" +
		"while [ ! -e " + gate + " ]; do sleep 0.02; done\n" +
		"echo '" + line + "'\n" +
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

	var got LogLine
	require.NoError(t, conn.ReadJSON(&got))
	require.Contains(t, got.Message, f2Resolved, "CONTROL: unlocked, the first line is raw")
	require.Zero(t, lists.Load(), "CONTROL: unlocked, no container lookup yet")

	store.RevokeUser(rbmiUser)
	require.NoError(t, os.WriteFile(gate, nil, 0o600))
	require.NoError(t, conn.ReadJSON(&got), "the stream closed instead of switching to masked")
	require.NotContains(t, got.Message, f2Resolved, "after the lock, a line showed a value only the container holds")
}
