package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// These tests cover every handler main.go constructs with the possibly-nil
// dockerService (agent-os-xay). Each builds the handler with no Docker service
// and asserts a clean refusal — a status code and a message an operator can act
// on — rather than the nil-pointer panic these paths used to produce. The
// panic mattered most on the streaming paths, where it fired inside a goroutine
// that gin's RecoveryMiddleware cannot reach, killing the process.

const testWSSecret = "test-secret-key-32-chars-long!!!"

func noDockerDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func seedNoDockerStack(t *testing.T, db *database.DB) models.Stack {
	t.Helper()
	createTestDirectory(t, db, "/test/dir")
	stack := models.Stack{
		ID: "stack-nd", Directory: "/test/dir", ComposeFile: "compose.yaml",
		EnvFile: ".env", ProjectName: "proj-nd", Status: "running",
	}
	require.NoError(t, db.UpsertStack(stack))
	return stack
}

// assertUnavailable checks the response refuses with 503 and names Docker, so
// the operator learns the cause instead of reading "Internal server error".
func assertUnavailable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, http.StatusServiceUnavailable, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "docker daemon unreachable")
}

func TestResourcesHandler_NoDocker_RefusesEveryDockerRoute(t *testing.T) {
	db := noDockerDB(t)
	handler := NewResourcesHandler(nil, db, nil)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	routes := []struct {
		name   string
		method string
		path   string
	}{
		{"listImages", http.MethodGet, "/api/resources/images"},
		{"deleteImage", http.MethodDelete, "/api/resources/images/img1"},
		{"pruneImages", http.MethodPost, "/api/resources/images/prune"},
		{"listContainers", http.MethodGet, "/api/resources/containers"},
		{"inspectContainer", http.MethodGet, "/api/resources/containers/abc/inspect"},
		{"startContainer", http.MethodPost, "/api/resources/containers/abc/start"},
		{"stopContainer", http.MethodPost, "/api/resources/containers/abc/stop"},
		{"restartContainer", http.MethodPost, "/api/resources/containers/abc/restart"},
		{"deleteContainer", http.MethodDelete, "/api/resources/containers/abc"},
		{"pruneContainers", http.MethodPost, "/api/resources/containers/prune"},
		{"listVolumes", http.MethodGet, "/api/resources/volumes"},
		{"deleteVolume", http.MethodDelete, "/api/resources/volumes/vol1"},
		{"pruneVolumes", http.MethodPost, "/api/resources/volumes/prune"},
		{"listNetworks", http.MethodGet, "/api/resources/networks"},
		{"deleteNetwork", http.MethodDelete, "/api/resources/networks/net1"},
		{"pruneNetworks", http.MethodPost, "/api/resources/networks/prune"},
		{"listBuildCache", http.MethodGet, "/api/resources/build-cache"},
		{"pruneBuildCache", http.MethodPost, "/api/resources/build-cache/prune"},
		{"checkUpdatesRefresh", http.MethodGet, "/api/resources/updates?refresh=true"},
		{"updateContainer", http.MethodPost, "/api/resources/containers/abc/update"},
	}

	for _, rt := range routes {
		t.Run(rt.name, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()

			require.NotPanics(t, func() { router.ServeHTTP(w, req) })
			assertUnavailable(t, w)
		})
	}
}

func TestResourcesHandler_NoDocker_CreateNetworkRefuses(t *testing.T) {
	db := noDockerDB(t)
	handler := NewResourcesHandler(nil, db, nil)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodPost, "/api/resources/networks",
		strings.NewReader(`{"name":"testnet"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	require.NotPanics(t, func() { router.ServeHTTP(w, req) })
	assertUnavailable(t, w)
}

func TestMonitoringHandler_NoDocker_StackContainersRefuses(t *testing.T) {
	db := noDockerDB(t)
	stack := seedNoDockerStack(t, db)

	handler := NewMonitoringHandler(nil, nil, db, NewConnectionManager(10), NewEventBus())
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), testWSSecret, false)

	req := httptest.NewRequest(http.MethodGet, "/api/stacks/"+stack.ID+"/containers", nil)
	w := httptest.NewRecorder()

	require.NotPanics(t, func() { router.ServeHTTP(w, req) })
	assertUnavailable(t, w)
}

func TestLogsHandler_NoDocker_RefusesBeforeStreaming(t *testing.T) {
	db := noDockerDB(t)
	stack := seedNoDockerStack(t, db)

	handler := NewLogsHandler(nil, db, testWSSecret, true, t.TempDir(), NewConnectionManager(10))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	t.Run("GetLogs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/stacks/"+stack.ID+"/logs", nil)
		w := httptest.NewRecorder()

		require.NotPanics(t, func() { router.ServeHTTP(w, req) })
		assertUnavailable(t, w)
	})

	// The stream must be refused before the WebSocket upgrade: a socket that
	// opens and immediately closes reads to the operator as a network fault.
	t.Run("StreamLogs", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/ws/logs/"+stack.ID, nil)
		w := httptest.NewRecorder()

		require.NotPanics(t, func() { router.ServeHTTP(w, req) })
		assertUnavailable(t, w)
	})
}

func TestDashboardHandler_NoDocker_StatsDegradeAndMetricsRefuse(t *testing.T) {
	db := noDockerDB(t)
	seedNoDockerStack(t, db)

	handler := NewDashboardHandler(nil, nil, db, NewConnectionManager(10))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"), testWSSecret, false)

	// Stats deliberately degrade rather than fail: the stack counts come from
	// the database and stay useful with the daemon down.
	t.Run("stats", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/stats", nil)
		w := httptest.NewRecorder()

		require.NotPanics(t, func() { router.ServeHTTP(w, req) })
		require.Equal(t, http.StatusOK, w.Code)

		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, float64(1), body["totalStacks"])
		assert.Equal(t, float64(0), body["runningContainers"])
	})

	t.Run("metrics ws", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/ws/dashboard/metrics", nil)
		w := httptest.NewRecorder()

		require.NotPanics(t, func() { router.ServeHTTP(w, req) })
		assertUnavailable(t, w)
	})
}

func TestStacksHandler_NoDocker_LifecycleRefuses(t *testing.T) {
	db := noDockerDB(t)
	stack := seedNoDockerStack(t, db)

	cfg := &config.Config{StacksDir: t.TempDir()}
	handler := NewStacksHandler(nil, services.NewScannerService(cfg, db), services.NewLinterService(),
		db, cfg, services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/stacks"))

	for _, action := range []string{"start", "stop", "restart", "pull"} {
		t.Run(action, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/stacks/"+stack.ID+"/"+action, nil)
			w := httptest.NewRecorder()

			require.NotPanics(t, func() { router.ServeHTTP(w, req) })
			assertUnavailable(t, w)
		})
	}

	// List still answers from the database with the daemon down; it only skips
	// the live-status enrichment.
	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/stacks", nil)
		w := httptest.NewRecorder()

		require.NotPanics(t, func() { router.ServeHTTP(w, req) })
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestStacksHandler_TypedNilDocker_LifecycleRefuses covers the production
// wiring specifically: main.go passes the concrete *services.DockerService, so
// the handler holds a nil POINTER inside a NON-nil interface. The `!= nil`
// checks cannot see that; DockerService's nil-receiver guards are what refuse.
func TestStacksHandler_TypedNilDocker_LifecycleRefuses(t *testing.T) {
	db := noDockerDB(t)
	stack := seedNoDockerStack(t, db)

	cfg := &config.Config{StacksDir: t.TempDir()}
	handler := NewStacksHandler((*services.DockerService)(nil), services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/stacks"))

	req := httptest.NewRequest(http.MethodPost, "/api/stacks/"+stack.ID+"/start", nil)
	w := httptest.NewRecorder()

	require.NotPanics(t, func() { router.ServeHTTP(w, req) })
	assertUnavailable(t, w)

	// The list path takes the same typed nil through the `!= nil` check and must
	// still answer from the database instead of panicking.
	req = httptest.NewRequest(http.MethodGet, "/api/stacks", nil)
	w = httptest.NewRecorder()
	require.NotPanics(t, func() { router.ServeHTTP(w, req) })
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGitHandler_NoDocker_PullSkipsRedeploy(t *testing.T) {
	db := noDockerDB(t)
	cfg := &config.Config{StacksDir: t.TempDir()}

	// GitService.PullVerified takes the concrete pointer and already skips
	// redeploy when it is nil; this pins that a nil pointer never reaches
	// RestartVerified through the handler.
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	req := httptest.NewRequest(http.MethodPost, "/api/git/pull",
		strings.NewReader(`{"path":"/nonexistent","redeploy":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	require.NotPanics(t, func() { router.ServeHTTP(w, req) })
	assert.NotEqual(t, http.StatusOK, w.Code)
}
