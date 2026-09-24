package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestSymj_StackRoutes_AbsentVsFault drives the four routes agent-os-symj
// converged through their real routers, both arms on one instrument. The
// census in notfound_boundary_test.go proves each route CALLS the shared
// mapping; this proves what each route actually puts on the wire: an absent
// stack is 404 STACK_NOT_FOUND (it was NOT_FOUND), and a database that cannot
// answer is still 500 INTERNAL_ERROR with the route's own message, not a 404.
func TestSymj_StackRoutes_AbsentVsFault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	routers := map[string]func(db *database.DB) (*gin.Engine, string, string){
		"updates.go updateStack": func(db *database.DB) (*gin.Engine, string, string) {
			jm := services.NewUpdateJobManager(15 * time.Minute)
			t.Cleanup(jm.Stop)
			r := gin.New()
			NewResourcesHandlerWithJobManager(nil, db, nil, jm).RegisterRoutes(r.Group("/api"))
			return r, http.MethodPost, "/api/resources/stacks/does-not-exist/update"
		},
		"git.go resolvePathFromStack": func(db *database.DB) (*gin.Engine, string, string) {
			cfg := &config.Config{StacksDir: t.TempDir()}
			r := gin.New()
			NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg).RegisterRoutes(r.Group("/api/git"))
			return r, http.MethodGet, "/api/git?stackId=does-not-exist"
		},
		"monitoring.go getStackContainers": func(db *database.DB) (*gin.Engine, string, string) {
			r := gin.New()
			NewMonitoringHandler(nil, nil, db, NewConnectionManager(10), NewEventBus()).RegisterRoutes(r.Group("/api"), "test-secret-key-32-chars-long!!!", false)
			return r, http.MethodGet, "/api/stacks/does-not-exist/containers"
		},
		"monitoring.go handleMetricsWebSocket": func(db *database.DB) (*gin.Engine, string, string) {
			r := gin.New()
			NewMonitoringHandler(nil, nil, db, NewConnectionManager(10), NewEventBus()).RegisterRoutes(r.Group("/api"), "test-secret-key-32-chars-long!!!", false)
			return r, http.MethodGet, "/api/ws/metrics/does-not-exist"
		},
	}

	call := func(t *testing.T, r *gin.Engine, method, path string) (int, models.AppError) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		var body models.AppError
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body %q", w.Body.String())
		return w.Code, body
	}

	for name, build := range routers {
		t.Run(name, func(t *testing.T) {
			healthy, err := database.NewWithMigrations(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { _ = healthy.Close() })

			r, method, path := build(healthy)
			status, body := call(t, r, method, path)
			require.Equal(t, http.StatusNotFound, status)
			require.Equal(t, models.ErrStackNotFound, body.Code)
			require.Equal(t, "Stack not found", body.Message)

			r, method, path = build(faultyDB(t))
			status, body = call(t, r, method, path)
			require.Equal(t, http.StatusInternalServerError, status, "a database fault is not an absent stack")
			require.Equal(t, "INTERNAL_ERROR", body.Code)
			require.Equal(t, "Failed to load stack", body.Message)
		})
	}
}
