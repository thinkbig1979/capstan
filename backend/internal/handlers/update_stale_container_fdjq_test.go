package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// newStaleContainerFixture is a daemon that knows only the recreated
// container: the old ID gets the 404 body a real daemon sends for it.
func newStaleContainerFixture(t *testing.T) *ResourcesHandler {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/gone/json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container: gone"}`)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	docker := newTestDockerServiceAgainst(t, srv)
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	jm := services.NewUpdateJobManager(15 * time.Minute)
	t.Cleanup(func() { jm.Stop() })
	h := NewResourcesHandlerWithJobManager(docker, db, nil, jm)
	h.SetOperationLock(services.NewOperationLock())
	seedStackInDir(t, db, "s-web", "web", "/srv/stacks/web")
	return h
}

// TestUpdateContainer_StaleCachedIDIsNotAServerFault is agent-os-fdjq: the
// Updates list is served from the scan cache, so after a stack's containers are
// recreated (a backup's stop/start, a redeploy) the row still carries the old
// container ID. "Update & Restart" posts that ID, the daemon answers 404 "No
// such container", and the handler answered 500 INTERNAL_ERROR for what is a
// stale client view, not a server fault.
func TestUpdateContainer_StaleCachedIDIsNotAServerFault(t *testing.T) {
	h := newStaleContainerFixture(t)
	router := setupResourcesRouter(h)
	require.NoError(t, h.db.SetCachedUpdates([]models.CachedUpdate{{
		ID: "cu-1", ContainerID: "gone", ContainerName: "web-app-1", Image: "nginx:latest",
		ProjectName: "web", ServiceName: "app", StackID: "s-web", IsCompose: true,
	}}))

	// The broadcast is what refreshes the list: the client's mutation
	// invalidates the updates query only on success.
	events, settle, stop := captureBroadcasts(t)
	defer stop()

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/containers/gone/update", nil))

	require.Equal(t, http.StatusNotFound, w.Code, "a container that no longer exists is not a server fault; body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrNotFound, body["code"])
	assert.Equal(t, staleUpdateContainerMessage, body["message"])
	assert.Empty(t, h.jobManager.List(), "nothing may be queued for a container that is gone")

	cached, err := h.db.GetCachedUpdates()
	require.NoError(t, err)
	assert.Empty(t, cached, "the stale row must be evicted so the list stops offering it")

	settle(t)
	var changed []models.StackEvent
	for _, e := range events() {
		if e.Type == "updates_changed" {
			changed = append(changed, e)
		}
	}
	require.Len(t, changed, 1, "exactly one updates_changed must tell open lists to drop the row; got %+v", events())
	assert.Equal(t, "gone", changed[0].ContainerID)
}
