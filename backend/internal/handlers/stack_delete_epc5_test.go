package handlers

import (
	"errors"
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

// agent-os-epc5: a stack row whose compose file is gone could not be deleted:
// Delete ran `compose down -v -f <missing file>`, which fails, and returned
// before the row delete. Producer: a compose file disappearing while its row
// stays. Consumer: Delete -> DeleteVerified.

// epc5Docker fails DeleteVerified the way compose does on a missing -f file,
// and answers the project-label container lookup from containers/listErr.
type epc5Docker struct {
	fakeStackDocker
	containers  []models.Container
	listErr     error
	deleteCalls int
}

func (f *epc5Docker) DeleteVerified(models.Stack) (truth.ActionResult, string) {
	f.deleteCalls++
	return truth.Failed("compose down failed", errors.New("open compose.worker.yaml: no such file or directory")), ""
}

func (f *epc5Docker) GetContainerList(string) ([]models.Container, error) {
	return f.containers, f.listErr
}

type epc5Fixture struct {
	app      string
	workerID string
	db       *database.DB
	router   *gin.Engine
}

// newEpc5Fixture registers app/compose.yaml and app/compose.worker.yaml, then
// removes compose.worker.yaml without a rescan: the row the bug leaves behind.
func newEpc5Fixture(t *testing.T, docker *epc5Docker) *epc5Fixture {
	t.Helper()
	root := t.TempDir()
	app := filepath.Join(root, "app")
	require.NoError(t, os.Mkdir(app, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(app, "compose.yaml"), []byte(deleteSiblingCompose), 0o644))
	worker := filepath.Join(app, "compose.worker.yaml")
	require.NoError(t, os.WriteFile(worker, []byte(deleteSiblingCompose), 0o644))

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, db.CreateUser(models.User{ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime}))
	cfg := &config.Config{StacksDir: root}
	scanner := services.NewScannerService(cfg, db)
	require.NoError(t, scanner.ScanDirectory(app))
	require.NoError(t, os.Remove(worker))

	workerID := ""
	rows, err := db.ListStacksByDirectory(app)
	require.NoError(t, err)
	for _, r := range rows {
		if r.ComposeFile == "compose.worker.yaml" {
			workerID = r.ID
		}
	}
	require.NotEmpty(t, workerID, "setup: the worker stack's row exists")

	handler := NewStacksHandler(docker, scanner, services.NewLinterService(), db, cfg,
		services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)
	return &epc5Fixture{app: app, workerID: workerID, db: db, router: router}
}

func (f *epc5Fixture) delete(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+f.workerID+"?confirm=true", nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func (f *epc5Fixture) rowExists(t *testing.T) bool {
	t.Helper()
	_, err := f.db.GetStack(f.workerID)
	return err == nil
}

func TestDeleteEpc5_GoneComposeFileWithNoContainersDeletesTheRow(t *testing.T) {
	docker := &epc5Docker{}
	f := newEpc5Fixture(t, docker)

	w := f.delete(t)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.False(t, f.rowExists(t), "the row must be gone")
	assert.Zero(t, docker.deleteCalls, "compose down -f <missing file> must not run")
	assert.Contains(t, w.Body.String(), "networks and named volumes left in place",
		"skipping compose down leaves the project's networks and volumes; the response says so")
	assert.FileExists(t, filepath.Join(f.app, "compose.yaml"), "the sibling's compose file stays")
}

func TestDeleteEpc5_GoneComposeFileWithContainersRefuses(t *testing.T) {
	docker := &epc5Docker{containers: []models.Container{{Name: "app-worker-1", State: "running"}}}
	f := newEpc5Fixture(t, docker)

	w := f.delete(t)

	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "app-worker-1", "the refusal names the containers")
	assert.Contains(t, w.Body.String(), "docker compose -p", "the refusal says how to remove them")
	assert.True(t, f.rowExists(t), "the row stays while its containers exist")
	assert.Zero(t, docker.deleteCalls)
}

func TestDeleteEpc5_GoneComposeFileWithFailedLookupRefuses(t *testing.T) {
	docker := &epc5Docker{listErr: errors.New("docker daemon unreachable")}
	f := newEpc5Fixture(t, docker)

	w := f.delete(t)

	assert.GreaterOrEqual(t, w.Code, http.StatusInternalServerError, "body=%s", w.Body.String())
	assert.True(t, f.rowExists(t), "an unanswered lookup keeps the row (fail closed)")
	assert.Zero(t, docker.deleteCalls)
}
