package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// raceDockerFake is a stackDocker test double whose DeleteVerified runs onDelete
// as a side effect before reporting success. It exists to simulate a directory
// scan landing DURING compose-down: agent-os-lg2's collateral check runs before
// DeleteVerified, and removeStackFiles decides which branch to take after it, so
// anything that changes stack registration in that window is a real hazard, not
// a hypothetical one — this reproduces it deterministically instead of racing a
// real watcher goroutine.
type raceDockerFake struct {
	onDelete func()
}

func (f *raceDockerFake) GetStackStatuses(context.Context, services.DashboardDB) (map[string]services.LiveStatus, error) {
	return map[string]services.LiveStatus{}, nil
}
func (f *raceDockerFake) StartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *raceDockerFake) StopVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *raceDockerFake) RestartVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *raceDockerFake) PullVerified(models.Stack) (truth.ActionResult, string) {
	return truth.ActionResult{}, ""
}
func (f *raceDockerFake) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	if f.onDelete != nil {
		f.onDelete()
	}
	return truth.Success("stack and volumes removed"), ""
}

// TestStacksHandler_Delete_SoleStackRace_SiblingRegisteredDuringComposeDown is
// the regression guard for the review finding on agent-os-lg2: threading a
// single early "am I the sole stack?" answer through to the destructive
// os.RemoveAll branch widens the window in which that answer can go stale from
// microseconds (the pre-lg2 gap) to the full duration of compose down -v. If a
// sibling stack gets registered under the same directory in that window — a
// directory-watcher scan firing on the file activity compose-down generates, or
// a concurrent Create — the stale "no survivors" answer makes removeStackFiles
// os.RemoveAll the whole directory anyway, destroying the new sibling's compose
// file. That is the exact data loss agent-os-xa7 (PR #82) fixed, reintroduced
// through the widened race rather than through the logic xa7 guards.
func TestStacksHandler_Delete_SoleStackRace_SiblingRegisteredDuringComposeDown(t *testing.T) {
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	cfg := &config.Config{StacksDir: tempDir}
	scanner := services.NewScannerService(cfg, db)
	docker := &raceDockerFake{}
	handler := NewStacksHandler(docker, scanner, services.NewLinterService(), db, cfg,
		services.NewActionLogger(db), services.NewOperationLock())

	router := gin.New()
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	body, err := json.Marshal(map[string]any{
		"name":           "my-stack",
		"composeContent": deleteSiblingCompose,
		"envContent":     "",
		"deploy":         false,
	})
	require.NoError(t, err)
	createReq := httptest.NewRequest(http.MethodPost, "/stacks", bytes.NewReader(body))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	require.Equal(t, http.StatusCreated, createRec.Code, "setup: create must succeed, body=%s", createRec.Body.String())

	stackDir := filepath.Join(tempDir, "my-stack")
	stacks, err := db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	require.Len(t, stacks, 1, "setup: expected exactly one stack registered before the race")
	id := stacks[0].ID

	// Register the sibling from inside DeleteVerified: by the time this runs,
	// Delete's early survivor check has already captured "no survivors".
	docker.onDelete = func() {
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.api.yaml"), []byte(deleteSiblingCompose), 0o644))
		require.NoError(t, scanner.ScanDirectoryWithRoot(stackDir, tempDir))
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true", nil)
	deleteRec := httptest.NewRecorder()
	router.ServeHTTP(deleteRec, deleteReq)
	require.Equal(t, http.StatusOK, deleteRec.Code, "delete must succeed, body=%s", deleteRec.Body.String())

	assert.FileExists(t, filepath.Join(stackDir, "compose.api.yaml"),
		"a sibling registered while compose-down was running must not be destroyed by a stale sole-stack decision")
}
