package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// standaloneUpdateFixture serves a standalone container "solo" (id "abc123", no
// compose labels). Only its second inspect, the update job's first Docker call
// after the handler's own, blocks until release() runs; that holds the update
// mid-flight without a real daemon. entered closes when the job is blocked.
func standaloneUpdateFixture(t *testing.T) (h *ResourcesHandler, lock *services.OperationLock, entered <-chan struct{}, release func()) {
	t.Helper()
	gate := make(chan struct{})
	reached := make(chan struct{})
	var inspects atomic.Int32
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/solo/json"):
			if inspects.Add(1) == 2 {
				select {
				case <-reached:
				default:
					close(reached)
				}
				<-gate
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"abc123","Name":"/solo","Image":"sha256:old","Config":{"Image":"nginx:latest"}}`)) //nolint:errcheck // Write to a httptest stub's ResponseWriter; a failure there is not the behaviour under test.
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(func() { release(); srv.Close() })
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	jm := services.NewUpdateJobManager(15 * time.Minute)
	t.Cleanup(func() { jm.Stop() })
	h = NewResourcesHandlerWithJobManager(newTestDockerServiceAgainst(t, srv), db, nil, jm)
	lock = services.NewOperationLock()
	h.SetOperationLock(lock)
	h.pruner = &fakePruner{}
	return h, lock, reached, release
}

func postStandaloneUpdate(h *ResourcesHandler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	setupResourcesRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/containers/solo/update", nil))
	return w
}

// TestUpdateContainer_StandaloneUpdateTakesATurnPrunesWaitFor is
// agent-os-qags.30 arm (d). A standalone container has no stack to lock, so its
// update ran with no lock at all; it removes the old container and then
// ContainerCreate -> ContainerStart, and a container prune landing between the
// two removes the `created` container ("container is marked for removal and
// cannot be started": 200 of 200 against a spinning prune, probed). The service
// is then gone. The update now holds a turn keyed on the container id, so a
// prune is refused while it runs and goes through after.
func TestUpdateContainer_StandaloneUpdateTakesATurnPrunesWaitFor(t *testing.T) {
	h, lock, entered, release := standaloneUpdateFixture(t)

	w := postStandaloneUpdate(h)
	require.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
	jobID, _ := decodeBody(t, w)["jobId"].(string)
	require.NotEmpty(t, jobID)

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the update job never reached Docker")
	}

	pw := doContainerPrune(h)
	require.Equal(t, http.StatusConflict, pw.Code, "a container prune ran during a standalone update; body: %s", pw.Body.String())
	assert.Contains(t, decodeBody(t, pw)["message"], "update in progress since")
	assert.Zero(t, h.pruner.(*fakePruner).callCount(), "a refused prune must not reach Docker")

	// Two updates of one standalone container no longer run at once either.
	assert.Equal(t, http.StatusConflict, postStandaloneUpdate(h).Code)

	release()
	guard := hangGuardDeadline(t)
	for {
		if j := h.jobManager.Get(jobID); j != nil && !j.FinishedAt.IsZero() {
			break
		}
		if time.Now().After(guard) {
			t.Fatal("the update job never finished")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Other side: the job ended, the turn ended with it, the prune goes through.
	pw = doContainerPrune(h)
	require.Equal(t, http.StatusOK, pw.Code, "the finished update kept its turn; body: %s", pw.Body.String())
	token, err := lock.Acquire("abc123", services.OpKindUpdate)
	require.NoError(t, err, "the finished update left its container key held")
	lock.Release("abc123", token)
}
