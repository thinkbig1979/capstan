package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// deleteStackHookStore is the real DB with a hook run inside DeleteStack,
// before the row goes.
type deleteStackHookStore struct {
	*database.DB
	onDeleteStack func()
}

func (s *deleteStackHookStore) DeleteStack(id string) error {
	if s.onDeleteStack != nil {
		s.onDeleteStack()
	}
	return s.DB.DeleteStack(id)
}

// agent-os-z91e.23: Delete removes the stack's files and then its row. A scan
// that globbed the compose file before the removal and writes after the row
// delete resurrects the row as a ghost stack (the scanner side is pinned by
// TestScannerService_WithLock_ScanParkedBeforeUpsertDoesNotResurrectDeletedStack).
// Delete must therefore hold the scanner's lock across removal and row delete:
// a scan started inside that window has to wait until Delete is done.
func TestStacksHandler_Delete_HoldsScannerLockAcrossFileAndRowRemoval(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))

	dir := filepath.Join(root, "app")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(deleteSiblingCompose), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.api.yaml"), []byte(deleteSiblingCompose), 0o644))

	cfg := &config.Config{StacksDir: root}
	scanner := services.NewScannerService(cfg, db)
	_, err = scanner.ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacksByDirectory(dir)
	require.NoError(t, err)
	require.Len(t, stacks, 2, "setup: both compose files must register")
	apiID := ""
	for _, st := range stacks {
		if st.ComposeFile == "compose.api.yaml" {
			apiID = st.ID
		}
	}
	require.NotEmpty(t, apiID)

	store := &deleteStackHookStore{DB: db}
	scanInsideWindow := false
	scanDone := make(chan error, 1)
	store.onDeleteStack = func() {
		go func() { scanDone <- scanner.ScanDirectory(dir) }()
		select {
		case err := <-scanDone:
			scanInsideWindow = true
			scanDone <- err
		case <-time.After(200 * time.Millisecond):
		}
	}

	handler := NewStacksHandler(&raceDockerFake{}, scanner, services.NewLinterService(), store, cfg,
		services.NewActionLogger(db), services.NewOperationLock())
	router := gin.New()
	router.DELETE("/stacks/:id", authContextMiddleware("test-user-id"), handler.Delete)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/stacks/"+apiID+"?confirm=true", nil))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.NoError(t, <-scanDone)

	assert.False(t, scanInsideWindow,
		"a scan ran to completion between Delete's file removal and its row delete; one parked there writes the row back as a ghost")
	_, err = db.GetStack(apiID)
	assert.ErrorIs(t, err, errdefs.ErrNotFound)
}
