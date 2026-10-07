package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// deleteCapRcloneRunner fakes rclone for the off-site delete cap
// (agent-os-z91e.9): the recursive `lsf -R` pre-flight listing returns
// listing (or listErr), any other lsf (remoteHasSnapshots) lists nothing, and
// Run records each `rclone sync` argv.
type deleteCapRcloneRunner struct {
	listing []byte
	listErr error

	mu    sync.Mutex
	syncs [][]string
}

func (r *deleteCapRcloneRunner) Run(_ context.Context, _ string, args []string, _ []string, _ chan<- services.StreamLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(args) > 0 && args[0] == "sync" {
		r.syncs = append(r.syncs, slices.Clone(args))
	}
	return nil
}

func (r *deleteCapRcloneRunner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	if slices.Contains(args, "-R") {
		return r.listing, r.listErr
	}
	return nil, nil
}

func (r *deleteCapRcloneRunner) syncArgs() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.syncs)
}

// remoteOnlyListing is an `rclone lsf -R --files-only` listing of n files the
// local repository does not have.
func remoteOnlyListing(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "data/ff/gone%04d\n", i)
	}
	return []byte(b.String())
}

// deleteCapRouter wires a BackupHandler whose rclone is runner. The restic
// repository setting points at a fresh directory with localFiles files, so
// the cap is the floor of 100 for any localFiles under 500.
func deleteCapRouter(t *testing.T, runner *deleteCapRcloneRunner, remote string, localFiles int) (*gin.Engine, *services.BackupService) {
	t.Helper()

	db := newBackupHandlerDB(t)
	repo := t.TempDir()
	for i := range localFiles {
		require.NoError(t, os.WriteFile(filepath.Join(repo, fmt.Sprintf("f%d", i)), nil, 0o600))
	}
	require.NoError(t, db.SetSetting("restic_repository", repo))
	require.NoError(t, db.SetSetting("restic_password", "test-repo-password"))
	if remote != "" {
		require.NoError(t, db.SetSetting("rclone_remote", remote))
	}

	svc := buildBackupSvc(t, db, true, true)
	resticRunner := &recordingResticRunner{}
	svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
		return services.NewResticManagerForTest(bc, resticRunner, slog.Default())
	})
	svc.SetRcloneMgrFactory(func(bc services.BackupConfig) *services.RcloneManager {
		return services.NewRcloneManagerForTest(bc, runner, slog.Default())
	})

	h := NewBackupHandler(svc, db, slog.Default())
	// See agent-os-80n: h.Stop() must run before the DB and TempDir cleanups
	// registered above, and t.Cleanup runs LIFO.
	t.Cleanup(h.Stop)
	return newBackupRouter(h), svc
}

func TestSyncPreflight_ReportsRemoteOnlyCountAndCap(t *testing.T) {
	t.Parallel()

	r, _ := deleteCapRouter(t, &deleteCapRcloneRunner{listing: remoteOnlyListing(137)}, "myremote", 3)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backups/sync/preflight", nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"remoteOnly":137,"cap":100}`, w.Body.String())
}

func TestSyncPreflight_RefusesWhileABackupOperationRuns(t *testing.T) {
	t.Parallel()

	runner := &deleteCapRcloneRunner{listing: remoteOnlyListing(1)}
	r, svc := deleteCapRouter(t, runner, "myremote", 1)

	for _, busy := range []bool{true, false} {
		svc.ForceSetBusy(busy)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backups/sync/preflight", nil))
		if busy {
			require.Equal(t, http.StatusConflict, w.Code)
			assert.Equal(t, "BACKUP_BUSY", decodeBody(t, w)["code"])
		} else {
			assert.Equal(t, http.StatusOK, w.Code, "the same router answers once the guard is free (control)")
		}
	}
}

func TestSyncPreflight_ErrorShapes(t *testing.T) {
	t.Parallel()

	t.Run("no remote configured", func(t *testing.T) {
		t.Parallel()
		r, _ := deleteCapRouter(t, &deleteCapRcloneRunner{}, "", 1)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backups/sync/preflight", nil))
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Equal(t, "rclone remote is not configured", decodeBody(t, w)["message"])
	})

	t.Run("remote cannot be listed", func(t *testing.T) {
		t.Parallel()
		r, _ := deleteCapRouter(t, &deleteCapRcloneRunner{listErr: fmt.Errorf("rclone: %w", fakeExitError{code: 1})}, "myremote", 1)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backups/sync/preflight", nil))
		require.Equal(t, http.StatusBadGateway, w.Code, w.Body.String())
		assert.Equal(t, "SYNC_PREFLIGHT_FAILED", decodeBody(t, w)["code"])
	})
}

// TestRunSync_AllowDeleteCountReachesTheSync pins that the count confirmed in
// the request body is the one the run is held to, both ways on one fixture:
// 150 remote-only files against the floor cap of 100.
func TestRunSync_AllowDeleteCountReachesTheSync(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		body     map[string]interface{}
		wantSync bool
	}{
		{"confirmed 150", map[string]interface{}{"allowDeleteCount": 150}, true},
		{"no body field (control)", map[string]interface{}{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runner := &deleteCapRcloneRunner{listing: remoteOnlyListing(150)}
			db := newBackupHandlerDB(t)
			repo := t.TempDir()
			require.NoError(t, db.SetSetting("restic_repository", repo))
			require.NoError(t, db.SetSetting("restic_password", "test-repo-password"))
			require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
			svc := buildBackupSvc(t, db, true, true)
			svc.SetResticMgrFactory(func(bc services.BackupConfig) *services.ResticManager {
				return services.NewResticManagerForTest(bc, &recordingResticRunner{}, slog.Default())
			})
			svc.SetRcloneMgrFactory(func(bc services.BackupConfig) *services.RcloneManager {
				return services.NewRcloneManagerForTest(bc, runner, slog.Default())
			})
			h := NewBackupHandler(svc, db, slog.Default())
			r := newBackupRouter(h)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/sync", tc.body))
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			runID := decodeBody(t, w)["runId"].(string)

			h.Stop() // waits for the durable run to finish
			run, err := db.GetBackupRunByID(runID)
			require.NoError(t, err)

			if tc.wantSync {
				assert.Equal(t, "success", run.Status, run.ErrorMessage)
				syncs := runner.syncArgs()
				require.Len(t, syncs, 1)
				i := slices.Index(syncs[0], "--max-delete")
				require.GreaterOrEqual(t, i, 0)
				assert.Equal(t, "150", syncs[0][i+1])
				return
			}
			assert.Equal(t, "failed", run.Status)
			assert.Contains(t, run.ErrorMessage, "it would delete 150 remote files, cap is 100")
			assert.Empty(t, runner.syncArgs())
		})
	}
}

func TestRunSync_RejectsANegativeAllowDeleteCount(t *testing.T) {
	t.Parallel()

	r, _ := deleteCapRouter(t, &deleteCapRcloneRunner{}, "myremote", 1)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonReq(t, http.MethodPost, "/api/backups/sync", map[string]interface{}{"allowDeleteCount": -1}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSyncPreflight_RcloneAbsentIsUnavailable(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", "myremote"))
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)

	w := httptest.NewRecorder()
	newBackupRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/backups/sync/preflight", nil))
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Equal(t, "BACKUP_UNAVAILABLE", decodeBody(t, w)["code"])
}
