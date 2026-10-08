package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// pruneRoutes are the three cross-stack prunes agent-os-qags.30 put behind the
// stack turn. Probed against a real daemon (Docker 26.1.5, compose 5.6.0, every
// prune label-filtered to the probe's own objects):
//   - network: a `created` container does NOT hold its network, so a prune
//     during `compose up`'s health wait removed it and the start failed with
//     "network ... not found" (30 of 30).
//   - image (all): an image pulled and not yet used is prunable, so a prune
//     between a pull and the up that needs it removed it (30 of 30,
//     pull_policy never: "No such image").
//   - volume (all): removed 30 of 30 between compose creating it and the
//     container using it; the daemon then recreated it unlabelled and every
//     later `compose up` warned "already exists but was not created by
//     Docker Compose".
var pruneRoutes = []struct {
	name, path, kind string
}{
	{"volume", "/api/resources/volumes/prune", services.OpKindVolumePrune},
	{"network", "/api/resources/networks/prune", services.OpKindNetworkPrune},
	{"image", "/api/resources/images/prune", services.OpKindImagePrune},
}

func doPrune(h *ResourcesHandler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	setupResourcesRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	return w
}

// TestResourcePrune_BusyStackAnswers409: a prune takes turns with stack
// operations, refuses while one holds its lock, and leaves Docker untouched.
func TestResourcePrune_BusyStackAnswers409(t *testing.T) {
	for _, rt := range pruneRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newPruneLockFixture(t)
			startToken, err := lock.Acquire("s1", services.OpKindStart)
			require.NoError(t, err)

			w := doPrune(h, rt.path)

			require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrOperationInProgress, body["code"])
			assert.Contains(t, body["message"], "start in progress since")
			assert.Zero(t, fake.callCount(), "a refused prune must not reach Docker")

			_, err = lock.Acquire("s1", services.OpKindStop)
			require.Error(t, err, "the start's lock was lost")
			lock.Release("s1", startToken)
		})
	}
}

// TestResourcePrune_IdleStacksPruneAndRelease is the other side on the same
// instrument: with no stack operation the prune goes through, and its turn ends
// with the request.
func TestResourcePrune_IdleStacksPruneAndRelease(t *testing.T) {
	for _, rt := range pruneRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newPruneLockFixture(t)

			w := doPrune(h, rt.path)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, 1, fake.callCount())
			token, err := lock.Acquire("s1", services.OpKindStart)
			require.NoError(t, err, "the prune kept its turn after answering")
			lock.Release("s1", token)
		})
	}
}

// TestResourcePrune_StackOpsRefusedWhilePruning: the turn is held for the whole
// Docker call, and a stack operation arriving mid-prune is refused naming it.
func TestResourcePrune_StackOpsRefusedWhilePruning(t *testing.T) {
	for _, rt := range pruneRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newPruneLockFixture(t)
			var midPruneErr error
			fake.during = func() { _, midPruneErr = lock.Acquire("s1", services.OpKindStart) }

			w := doPrune(h, rt.path)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			require.Error(t, midPruneErr, "a stack operation started in the middle of a prune")
			assert.Contains(t, midPruneErr.Error(), rt.kind+" in progress since")
		})
	}
}

// TestResourcePrune_FailedPruneReleasesTurn: a Docker error answers as before
// and must not leave the stacks locked out.
func TestResourcePrune_FailedPruneReleasesTurn(t *testing.T) {
	for _, rt := range pruneRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h, lock, fake := newPruneLockFixture(t)
			fake.err = errors.New("daemon fell over")

			w := doPrune(h, rt.path)

			require.GreaterOrEqual(t, w.Code, 400, "body: %s", w.Body.String())
			token, err := lock.Acquire("s1", services.OpKindStart)
			require.NoError(t, err, "a failed prune kept its turn")
			lock.Release("s1", token)
		})
	}
}

// TestResourcePrune_NoLockWiredStillPrunes: handlers built bare (tests) take no
// lock, the same convention as acquireStackLock.
func TestResourcePrune_NoLockWiredStillPrunes(t *testing.T) {
	for _, rt := range pruneRoutes {
		t.Run(rt.name, func(t *testing.T) {
			h := newTestResourcesHandler(t)
			fake := &fakePruner{}
			h.pruner = fake

			w := doPrune(h, rt.path)

			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, 1, fake.callCount())
		})
	}
}

// TestResourcePrune_OnePruneAtATime: two prunes of different kinds also take
// turns, because each holds the exclusive turn.
func TestResourcePrune_OnePruneAtATime(t *testing.T) {
	h, lock, fake := newPruneLockFixture(t)
	var innerCode int
	fake.during = func() {
		fake.mu.Lock()
		fake.during = nil
		fake.mu.Unlock()
		innerCode = doPrune(h, "/api/resources/networks/prune").Code
	}

	w := doPrune(h, "/api/resources/volumes/prune")

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, http.StatusConflict, innerCode, "a second prune ran while the first held the turn")
	token, err := lock.Acquire("s1", services.OpKindStart)
	require.NoError(t, err)
	lock.Release("s1", token)
}
