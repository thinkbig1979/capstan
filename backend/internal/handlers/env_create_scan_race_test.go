package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-z91e.23: EnvHandler.Create read the stack row at the top of the
// request and wrote the WHOLE row back (INSERT OR REPLACE) after creating the
// file. Create holds the stack's OperationLock, but the scanner does not take
// it, so a scan that rewrote or pruned the row in between was undone: a fresher
// project name reverted, or a pruned row came back as a ghost.

type envScanRaceFixture struct {
	router  *gin.Engine
	db      *database.DB
	scanner *services.ScannerService
	dir     string
}

func newEnvScanRaceFixture(t *testing.T) envScanRaceFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	dir := filepath.Join(root, "app")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: nginx\n"), 0o644))

	cfg := &config.Config{StacksDir: root}
	scanner := services.NewScannerService(cfg, db)
	_, err = scanner.ScanAll()
	require.NoError(t, err)

	handler := NewEnvHandler(db, cfg)
	r := gin.New()
	group := r.Group("/api/v1/stacks")
	group.Use(envUnlockedMiddleware())
	group.POST("/:id/env", handler.Create)
	return envScanRaceFixture{router: r, db: db, scanner: scanner, dir: dir}
}

func (f envScanRaceFixture) onlyStackID(t *testing.T) string {
	t.Helper()
	stacks, err := f.db.ListStacksByDirectory(f.dir)
	require.NoError(t, err)
	require.Len(t, stacks, 1, "setup: the scan must register exactly one stack")
	require.Empty(t, stacks[0].EnvFile, "setup: the stack must start with no env file")
	return stacks[0].ID
}

func (f envScanRaceFixture) createEnv(t *testing.T, id string, during func()) *httptest.ResponseRecorder {
	t.Helper()
	envCreateBeforeRecordHook = during
	t.Cleanup(func() { envCreateBeforeRecordHook = nil })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stacks/"+id+"/env", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// A rescan that lands while Create runs keeps what it found on disk; Create
// only adds the env file.
func TestEnvHandler_Create_KeepsRescanThatLandedMidRequest(t *testing.T) {
	f := newEnvScanRaceFixture(t)
	id := f.onlyStackID(t)

	rec := f.createEnv(t, id, func() {
		require.NoError(t, os.WriteFile(filepath.Join(f.dir, "compose.yaml"),
			[]byte("name: renamed\nservices:\n  web:\n    image: nginx\n"), 0o644))
		require.NoError(t, f.scanner.ScanDirectory(f.dir))
	})
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())

	got, err := f.db.GetStack(id)
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.ProjectName,
		"the project name the mid-request rescan read from disk was reverted to the one Create loaded before it")
	assert.Equal(t, ".env", got.EnvFile, "Create must still record the env file it made")
}

// A row the scanner prunes while Create runs stays pruned.
func TestEnvHandler_Create_DoesNotResurrectRowPrunedMidRequest(t *testing.T) {
	f := newEnvScanRaceFixture(t)
	currentID := f.onlyStackID(t)

	// A row for the same directory and compose file under an ID the scanner no
	// longer mints (an older ID scheme). ScanAll's pruneStaleIDStacks deletes it
	// once the current row exists alongside it.
	staleID := "legacy-app:default"
	current, err := f.db.GetStack(currentID)
	require.NoError(t, err)
	stale := *current
	stale.ID = staleID
	require.NoError(t, f.db.UpsertStack(stale))

	rec := f.createEnv(t, staleID, func() {
		_, err := f.scanner.ScanAll()
		require.NoError(t, err)
		_, err = f.db.GetStack(staleID)
		require.ErrorIs(t, err, errdefs.ErrNotFound, "setup: ScanAll must prune the stale-ID row")
	})
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())

	_, err = f.db.GetStack(staleID)
	assert.ErrorIs(t, err, errdefs.ErrNotFound,
		"the stale-ID row ScanAll pruned mid-request came back as a ghost stack")
}
