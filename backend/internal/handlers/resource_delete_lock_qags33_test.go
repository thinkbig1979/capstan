package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// deleteRoutes are the three single-object deletes agent-os-qags.33 put behind
// the stack turn, on the same exclusive turn the prunes take (qags.27, qags.30).
// Probed against a real daemon (Docker 26.1.5, every object labelled
// qags33probe=1; probe output in the bead's close):
//   - network: a `created` container does NOT hold its network, so a delete of
//     it succeeded and the container's start then failed with "network ... not
//     found". With a RUNNING container the daemon refused it ("has active
//     endpoints"), which is what makes the Created window the exposed one.
//   - volume: refused while any container, Created or not, mounts it ("volume is
//     in use", with force too), but deleted when it exists and no container has
//     been created yet; the daemon then recreates it unlabelled.
//   - image: an unused image was deleted between "present" and `create`, which
//     then failed "No such image" (pull_policy never). With a Created container
//     using it a plain delete is refused; force removes it and the container
//     still starts.
var deleteRoutes = []struct {
	name, path, kind string
}{
	{"network", "/api/resources/networks/n1", services.OpKindNetworkDelete},
	{"volume", "/api/resources/volumes/v1", services.OpKindVolumeDelete},
	{"image", "/api/resources/images/i1", services.OpKindImageDelete},
}

// fakeDeleter stands in for the Docker daemon behind the three delete routes.
type fakeDeleter struct {
	mu     sync.Mutex
	calls  int
	err    error
	during func() // runs inside a delete, while the handler holds its turn
}

func (f *fakeDeleter) enter() error {
	f.mu.Lock()
	f.calls++
	during, err := f.during, f.err
	f.mu.Unlock()
	if during != nil {
		during()
	}
	return err
}

func (f *fakeDeleter) DeleteImage(_ context.Context, _ string, _ bool) ([]image.DeleteResponse, error) {
	return []image.DeleteResponse{{Deleted: "sha256:aaa"}}, f.enter()
}

func (f *fakeDeleter) DeleteVolume(_ context.Context, _ string, _ bool) error { return f.enter() }

func (f *fakeDeleter) DeleteNetwork(_ context.Context, _ string) error { return f.enter() }

func (f *fakeDeleter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newDeleteLockFixture(t *testing.T) (*ResourcesHandler, *services.OperationLock, *fakeDeleter) {
	t.Helper()
	h := newTestResourcesHandler(t)
	lock := services.NewOperationLock()
	h.SetOperationLock(lock)
	fake := &fakeDeleter{}
	h.deleter = fake
	return h, lock, fake
}

func doDelete(h *ResourcesHandler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	setupResourcesRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodDelete, path, nil))
	return w
}

// TestResourceDelete_BusyStackAnswers409: a delete takes turns with stack
// operations, refuses while one holds its lock, and leaves Docker untouched.
func TestResourceDelete_BusyStackAnswers409(t *testing.T) {
	for _, rt := range deleteRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newDeleteLockFixture(t)
			startToken, err := lock.Acquire("s1", services.OpKindStart)
			require.NoError(t, err)

			w := doDelete(h, rt.path)

			require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrOperationInProgress, body["code"])
			assert.Contains(t, body["message"], "start in progress since")
			assert.Zero(t, fake.callCount(), "a refused delete must not reach Docker")

			_, err = lock.Acquire("s1", services.OpKindStop)
			require.Error(t, err, "the start's lock was lost")
			lock.Release("s1", startToken)
		})
	}
}

// TestResourceDelete_IdleStacksDeleteAndRelease is the other side on the same
// instrument: with no stack operation the delete goes through, and its turn ends
// with the request.
func TestResourceDelete_IdleStacksDeleteAndRelease(t *testing.T) {
	for _, rt := range deleteRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newDeleteLockFixture(t)

			w := doDelete(h, rt.path)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, 1, fake.callCount())
			token, err := lock.Acquire("s1", services.OpKindStart)
			require.NoError(t, err, "the delete kept its turn after answering")
			lock.Release("s1", token)
		})
	}
}

// TestResourceDelete_StackOpsRefusedWhileDeleting: the turn is held for the
// whole Docker call, and a stack operation arriving mid-delete is refused naming
// it.
func TestResourceDelete_StackOpsRefusedWhileDeleting(t *testing.T) {
	for _, rt := range deleteRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newDeleteLockFixture(t)
			var midDeleteErr error
			fake.during = func() { _, midDeleteErr = lock.Acquire("s1", services.OpKindStart) }

			w := doDelete(h, rt.path)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			require.Error(t, midDeleteErr, "a stack operation started in the middle of a delete")
			assert.Contains(t, midDeleteErr.Error(), rt.kind+" in progress since")
		})
	}
}

// TestResourceDelete_FailedDeleteReleasesTurn: a Docker error answers as before
// and must not leave the stacks locked out.
func TestResourceDelete_FailedDeleteReleasesTurn(t *testing.T) {
	for _, rt := range deleteRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newDeleteLockFixture(t)
			fake.err = errors.New("daemon fell over")

			w := doDelete(h, rt.path)

			require.GreaterOrEqual(t, w.Code, 400, "body: %s", w.Body.String())
			token, err := lock.Acquire("s1", services.OpKindStart)
			require.NoError(t, err, "a failed delete kept its turn")
			lock.Release("s1", token)
		})
	}
}

// TestResourceDelete_NoLockWiredStillDeletes: handlers built bare (tests) take
// no lock, the same convention as acquireStackLock.
func TestResourceDelete_NoLockWiredStillDeletes(t *testing.T) {
	for _, rt := range deleteRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h := newTestResourcesHandler(t)
			fake := &fakeDeleter{}
			h.deleter = fake

			w := doDelete(h, rt.path)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, 1, fake.callCount())
		})
	}
}

// TestResourceDelete_TakesTurnsWithPrunes: a delete and a prune also take turns,
// because each holds the exclusive turn: every manual remove or prune on the
// Resources page waits for the one before it.
func TestResourceDelete_TakesTurnsWithPrunes(t *testing.T) {
	h, lock, fake := newDeleteLockFixture(t)
	h.pruner = &fakePruner{}
	var innerCode int
	fake.during = func() {
		fake.mu.Lock()
		fake.during = nil
		fake.mu.Unlock()
		innerCode = doPrune(h, "/api/resources/networks/prune").Code
	}

	w := doDelete(h, "/api/resources/volumes/v1")

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, http.StatusConflict, innerCode, "a prune ran while a delete held the turn")
	token, err := lock.Acquire("s1", services.OpKindStart)
	require.NoError(t, err)
	lock.Release("s1", token)
}
