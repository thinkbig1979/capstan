package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// fakePruner stands in for the Docker daemon behind the four prune routes. All
// four methods count into calls and share err and during, so a test drives one
// route at a time (agent-os-qags.30 added the volume, network and image ones).
type fakePruner struct {
	mu     sync.Mutex
	calls  int
	err    error
	report container.PruneReport
	// during runs inside a prune method, while the handler holds its turn.
	during func()
}

func (f *fakePruner) enter() error {
	f.mu.Lock()
	f.calls++
	during, err := f.during, f.err
	f.mu.Unlock()
	if during != nil {
		during()
	}
	return err
}

func (f *fakePruner) PruneVolumes(_ context.Context, _ services.PruneOptions) (volume.PruneReport, error) {
	return volume.PruneReport{VolumesDeleted: []string{"v1"}, SpaceReclaimed: 5}, f.enter()
}

func (f *fakePruner) PruneNetworks(_ context.Context, _ services.PruneOptions) (network.PruneReport, error) {
	return network.PruneReport{NetworksDeleted: []string{"n1"}}, f.enter()
}

func (f *fakePruner) PruneImages(_ context.Context, _ services.PruneOptions) (image.PruneReport, error) {
	return image.PruneReport{ImagesDeleted: []image.DeleteResponse{{Deleted: "sha256:aaa"}}, SpaceReclaimed: 5}, f.enter()
}

func (f *fakePruner) PruneContainers(_ context.Context, _ services.PruneOptions) (container.PruneReport, error) {
	f.mu.Lock()
	f.calls++
	during := f.during
	f.mu.Unlock()
	if during != nil {
		during()
	}
	return f.report, f.err
}

func (f *fakePruner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newPruneLockFixture(t *testing.T) (*ResourcesHandler, *services.OperationLock, *fakePruner) {
	t.Helper()
	h := newTestResourcesHandler(t)
	lock := services.NewOperationLock()
	h.SetOperationLock(lock)
	fake := &fakePruner{report: container.PruneReport{ContainersDeleted: []string{"c1"}, SpaceReclaimed: 5}}
	h.pruner = fake
	return h, lock, fake
}

func doContainerPrune(h *ResourcesHandler) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	setupResourcesRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/resources/containers/prune", nil))
	return w
}

// TestContainerPrune_BusyStackAnswers409 is agent-os-qags.27: a container prune
// removes `created` containers, and `compose up` holds a dependent in `created`
// while it waits for a health check, so a prune mid-start failed the start with
// "No such container" (probed against a real daemon, 3 of 3). The prune must
// refuse while any stack operation holds its lock, and leave Docker untouched.
func TestContainerPrune_BusyStackAnswers409(t *testing.T) {
	h, lock, fake := newPruneLockFixture(t)
	startToken, err := lock.Acquire("s1", services.OpKindStart)
	require.NoError(t, err)

	w := doContainerPrune(h)

	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	body := decodeBody(t, w)
	assert.Equal(t, models.ErrOperationInProgress, body["code"])
	assert.Contains(t, body["message"], "start in progress since")
	assert.Zero(t, fake.callCount(), "a refused prune must not reach Docker")

	// The refusal must not have disturbed the start's hold.
	_, err = lock.Acquire("s1", services.OpKindStop)
	require.Error(t, err, "the start's lock was lost")
	lock.Release("s1", startToken)
}

// TestContainerPrune_IdleStacksPruneAndRelease is the other side: with no stack
// operation running the prune goes through, and its turn ends with the request.
func TestContainerPrune_IdleStacksPruneAndRelease(t *testing.T) {
	h, lock, fake := newPruneLockFixture(t)

	w := doContainerPrune(h)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, 1, fake.callCount())
	token, err := lock.Acquire("s1", services.OpKindStart)
	require.NoError(t, err, "the prune kept its turn after answering")
	lock.Release("s1", token)
}

// TestContainerPrune_StackOpsRefusedWhilePruning proves the gate holds for the
// whole Docker call: a stack operation arriving mid-prune is refused, naming
// the prune.
func TestContainerPrune_StackOpsRefusedWhilePruning(t *testing.T) {
	h, lock, fake := newPruneLockFixture(t)
	var midPruneErr error
	fake.during = func() { _, midPruneErr = lock.Acquire("s1", services.OpKindStart) }

	w := doContainerPrune(h)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Error(t, midPruneErr, "a stack operation started in the middle of a prune")
	assert.Contains(t, midPruneErr.Error(), "container prune in progress since")
}

// TestContainerPrune_FailedPruneReleasesTurn: a Docker error answers as before
// and must not leave the stacks locked out.
func TestContainerPrune_FailedPruneReleasesTurn(t *testing.T) {
	h, lock, fake := newPruneLockFixture(t)
	fake.err = errors.New("daemon fell over")

	w := doContainerPrune(h)

	require.GreaterOrEqual(t, w.Code, 400, "body: %s", w.Body.String())
	token, err := lock.Acquire("s1", services.OpKindStart)
	require.NoError(t, err, "a failed prune kept its turn")
	lock.Release("s1", token)
}

// TestContainerPrune_NoLockWiredStillPrunes: handlers built bare (tests) take
// no lock, the same convention as acquireStackLock.
func TestContainerPrune_NoLockWiredStillPrunes(t *testing.T) {
	h := newTestResourcesHandler(t)
	fake := &fakePruner{report: container.PruneReport{ContainersDeleted: []string{"c1"}}}
	h.pruner = fake

	w := doContainerPrune(h)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, 1, fake.callCount())
}
