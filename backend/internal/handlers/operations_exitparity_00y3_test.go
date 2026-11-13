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

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-00y3, criterion 3 (Edwin, 2026-11-13): a non-zero compose exit is
// FAILED on the operations WebSocket as on REST, so a Restart stops after a
// down that exits non-zero, even when that down removed every container.
//
// handlers cannot stub services' execCommand, so the docker CLI is a script
// first on PATH. t.Setenv makes these tests unsafe to run in parallel, which
// is why none of them calls t.Parallel.

// installFakeDocker00y3 puts an executable `docker` running script first on
// PATH for the rest of the test.
func installFakeDocker00y3(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n"+script+"\n"), 0o755)) //nolint:gosec // G306: the fake CLI must be executable
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// newOperations00y3Fixture serves /ws/operations over a real DockerService for
// one stack "s-web" whose directory exists, so compose really runs (as the
// fake CLI).
func newOperations00y3Fixture(t *testing.T) (*httptest.Server, *recordingStreamer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(dockerAPI.Close)
	svc := newTestDockerServiceAgainst(t, dockerAPI)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	seedStackInDir(t, db, "s-web", "web", t.TempDir())
	svc.SetStackLookup(db)
	rec := &recordingStreamer{svc: svc}
	r := gin.New()
	NewOperationsHandler(rec, db, services.NewOperationLock(), NewConnectionManager(5)).
		RegisterRoutes(r.Group("/api"), "test-secret-key-32-chars-long!!!", true)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, rec
}

func TestOperations_00y3_RestartStopsAtANonZeroDown(t *testing.T) {
	// down removes the container, then exits 1; ps lists nothing after.
	installFakeDocker00y3(t, `case "$*" in *" down"*) echo "Container web-app-1  Removed"; exit 1;; *) exit 0;; esac`)
	srv, rec := newOperations00y3Fixture(t)

	frames := readOperationFrames(t, srv, "s-web", "restart")

	done := framesOfType(frames, "done")
	require.Len(t, done, 1, "frames: %v", frames)
	assert.NotEqual(t, true, done[0]["success"], "frames: %v", frames)
	assert.Equal(t, "failed", done[0]["outcome"])
	assert.Equal(t, "compose down failed", done[0]["reason"])
	assert.Empty(t, framesOfType(frames, "phase"), "a failed down must not go on to start: %v", frames)
	assert.Equal(t, []string{"down"}, rec.requested())
}

// The other side, same route and fixture: the same down exiting 0 lets the
// restart go on to its up phase, so it is the exit code that stopped it above.
func TestOperations_00y3_RestartGoesOnAfterAZeroDown(t *testing.T) {
	installFakeDocker00y3(t, `case "$*" in *" down"*) echo "Container web-app-1  Removed"; exit 0;; *) exit 0;; esac`)
	srv, rec := newOperations00y3Fixture(t)

	frames := readOperationFrames(t, srv, "s-web", "restart")

	assert.NotEmpty(t, framesOfType(frames, "phase"), "frames: %v", frames)
	assert.Equal(t, []string{"down", "up"}, rec.requested())
}
