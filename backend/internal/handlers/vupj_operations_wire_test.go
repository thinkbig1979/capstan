package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestVupj_OperationsRefusals_AreAppErrors drives the operations route's
// pre-upgrade refusals through its real router. Each one used to write a bare
// {"error": ...} body with no code (agent-os-vupj): an absent stack is now 404
// STACK_NOT_FOUND like every other stack-by-id route, a database fault is still
// 500 INTERNAL_ERROR, a held stack lock is 409 OPERATION_IN_PROGRESS like the
// REST lifecycle routes, and an unknown action is 400 VALIDATION_ERROR.
//
// A plain GET is enough: every refusal happens before the WebSocket upgrade.
func TestVupj_OperationsRefusals_AreAppErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	build := func(db *database.DB, lock *services.OperationLock) *gin.Engine {
		r := gin.New()
		NewOperationsHandler(&fakeStreamer{}, db, lock, NewConnectionManager(5)).
			RegisterRoutes(r.Group("/api"), "test-secret-key-32-chars-long!!!", true)
		return r
	}
	call := func(t *testing.T, r *gin.Engine, path string) (int, models.AppError) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body models.AppError
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body %q", w.Body.String())
		return w.Code, body
	}
	seeded := func(t *testing.T) *database.DB {
		t.Helper()
		db, err := database.NewWithMigrations(":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		createTestDirectory(t, db, "/test/dir")
		require.NoError(t, db.UpsertStack(models.Stack{
			ID: "stack-a", Directory: "/test/dir", ComposeFile: "compose.yaml",
			ProjectName: "proj-a", Status: "running",
		}))
		return db
	}

	t.Run("absent stack is 404 STACK_NOT_FOUND", func(t *testing.T) {
		status, body := call(t, build(seeded(t), services.NewOperationLock()), "/api/ws/operations/does-not-exist/pull")
		require.Equal(t, http.StatusNotFound, status)
		require.Equal(t, models.ErrStackNotFound, body.Code)
		require.Equal(t, "Stack not found", body.Message)
	})

	t.Run("database fault is 500 INTERNAL_ERROR, not a 404", func(t *testing.T) {
		status, body := call(t, build(faultyDB(t), services.NewOperationLock()), "/api/ws/operations/stack-a/pull")
		require.Equal(t, http.StatusInternalServerError, status)
		require.Equal(t, "INTERNAL_ERROR", body.Code)
		require.Equal(t, "Failed to load stack", body.Message)
	})

	t.Run("held lock is 409 OPERATION_IN_PROGRESS", func(t *testing.T) {
		lock := services.NewOperationLock()
		_, err := lock.Acquire("stack-a")
		require.NoError(t, err)
		t.Cleanup(func() { lock.Release("stack-a") })

		status, body := call(t, build(seeded(t), lock), "/api/ws/operations/stack-a/pull")
		require.Equal(t, http.StatusConflict, status)
		require.Equal(t, models.ErrOperationInProgress, body.Code)
		require.NotEmpty(t, body.Message)
	})

	t.Run("unknown action is 400 VALIDATION_ERROR", func(t *testing.T) {
		status, body := call(t, build(seeded(t), services.NewOperationLock()), "/api/ws/operations/stack-a/bogus")
		require.Equal(t, http.StatusBadRequest, status)
		require.Equal(t, models.ErrValidation, body.Code)
		require.Equal(t, "Unknown action: bogus", body.Message)
	})
}
