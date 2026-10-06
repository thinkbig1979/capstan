package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// fakeContainerOps stands in for the Docker daemon behind the single-container
// action routes. Containers are known by id; their labels are what inspect
// reports. Every mutation that reaches it is recorded, so a refused action can
// be shown to have left the container untouched.
type fakeContainerOps struct {
	mu         sync.Mutex
	labels     map[string]map[string]string
	inspectErr error
	actionErr  error
	// during runs inside each mutation, while the handler is mid-action.
	during func()
	calls  []string
}

func (f *fakeContainerOps) InspectContainer(_ context.Context, id string) (container.InspectResponse, error) {
	if f.inspectErr != nil {
		return container.InspectResponse{}, f.inspectErr
	}
	return container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{ID: id},
		Config:            &container.Config{Labels: f.labels[id]},
	}, nil
}

func (f *fakeContainerOps) record(action, id string) error {
	if f.during != nil {
		f.during()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, action+" "+id)
	return f.actionErr
}

func (f *fakeContainerOps) StartContainer(_ context.Context, id string) error {
	return f.record("start", id)
}

func (f *fakeContainerOps) StopContainer(_ context.Context, id string) error {
	return f.record("stop", id)
}

func (f *fakeContainerOps) RestartContainer(_ context.Context, id string) error {
	return f.record("restart", id)
}

func (f *fakeContainerOps) DeleteContainer(_ context.Context, id string, _ bool) error {
	return f.record("delete", id)
}

func (f *fakeContainerOps) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type containerAction struct {
	name, method, path string
}

func containerActions(id string) []containerAction {
	base := "/api/resources/containers/" + id
	return []containerAction{
		{"start", http.MethodPost, base + "/start"},
		{"stop", http.MethodPost, base + "/stop"},
		{"restart", http.MethodPost, base + "/restart"},
		{"delete", http.MethodDelete, base},
	}
}

// newContainerLockFixture builds a ResourcesHandler with a real lock, a
// managed stack "s1" (compose project "web"), and a fake Docker that knows
// three containers: "managed" (project web), "standalone" (no compose label)
// and "foreign" (a compose project no stack here owns).
func newContainerLockFixture(t *testing.T) (*ResourcesHandler, *services.OperationLock, *fakeContainerOps) {
	t.Helper()
	h := newTestResourcesHandler(t)
	lock := services.NewOperationLock()
	h.SetOperationLock(lock)
	seedStack(t, h.db, "s1", "web")
	fake := &fakeContainerOps{labels: map[string]map[string]string{
		"managed":    {"com.docker.compose.project": "web", "com.docker.compose.service": "app"},
		"standalone": {},
		"foreign":    {"com.docker.compose.project": "elsewhere"},
	}}
	h.containerOps = fake
	return h, lock, fake
}

func doContainerAction(h *ResourcesHandler, a containerAction) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	setupResourcesRouter(h).ServeHTTP(w, httptest.NewRequest(a.method, a.path, nil))
	return w
}

// TestContainerActions_LockedStackAnswers409 is agent-os-w1cj: the Resources
// page's start/stop/restart/delete called Docker directly, so a container
// action on a managed stack could interleave with a backup (which stops the
// stack), a restore or a compose up.
func TestContainerActions_LockedStackAnswers409(t *testing.T) {
	for _, a := range containerActions("managed") {
		t.Run(a.name, func(t *testing.T) {
			h, lock, fake := newContainerLockFixture(t)
			backupToken, err := lock.Acquire("s1", services.OpKindBackup)
			require.NoError(t, err)

			w := doContainerAction(h, a)

			require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrOperationInProgress, body["code"])
			assert.Contains(t, body["message"], "backup in progress since")
			assert.Empty(t, fake.recorded(), "a refused action must leave the container untouched")

			// The refusal must not have disturbed the backup's hold.
			_, err = lock.Acquire("s1", services.OpKindStart)
			require.Error(t, err, "the backup's lock was lost")
			lock.Release("s1", backupToken)
		})
	}
}

// TestContainerActions_UnmanagedContainersTakeNoLock is the other side: with
// the same stack held, a container no managed stack owns is acted on as before.
func TestContainerActions_UnmanagedContainersTakeNoLock(t *testing.T) {
	for _, id := range []string{"standalone", "foreign"} {
		for _, a := range containerActions(id) {
			t.Run(id+"/"+a.name, func(t *testing.T) {
				h, lock, fake := newContainerLockFixture(t)
				_, err := lock.Acquire("s1", services.OpKindBackup)
				require.NoError(t, err)

				w := doContainerAction(h, a)

				require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				assert.Equal(t, []string{a.name + " " + id}, fake.recorded())
			})
		}
	}
}

// TestContainerActions_HoldLockDuringActionAndRelease: on a free stack the
// action runs, holds the stack's lock under the "container action" kind while
// Docker works, and frees it afterwards, on success and on a Docker error.
func TestContainerActions_HoldLockDuringActionAndRelease(t *testing.T) {
	for _, dockerErr := range []error{nil, errors.New("daemon said no")} {
		for _, a := range containerActions("managed") {
			t.Run(a.name+"/err="+errString(dockerErr), func(t *testing.T) {
				h, lock, fake := newContainerLockFixture(t)
				fake.actionErr = dockerErr
				var heldErr error
				fake.during = func() { _, heldErr = lock.Acquire("s1", services.OpKindBackup) }

				w := doContainerAction(h, a)

				if dockerErr == nil {
					require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				} else {
					require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
				}
				assert.Equal(t, []string{a.name + " managed"}, fake.recorded())
				require.Error(t, heldErr, "the stack was not locked while Docker acted on its container")
				assert.Contains(t, heldErr.Error(), services.OpKindContainer+" in progress since")

				// Released on every exit path: the next action on the same
				// stack is not refused.
				fake.during = nil
				w = doContainerAction(h, a)
				assert.NotEqual(t, http.StatusConflict, w.Code, "the first action left the stack locked: %s", w.Body.String())
			})
		}
	}
}

func errString(err error) string {
	if err == nil {
		return "nil"
	}
	return "set"
}

// TestContainerActions_StackLookupFailureRefuses: a stacks table that cannot be
// read makes managed and standalone indistinguishable, so the action is
// refused rather than run unlocked (fail closed, as agent-os-g482 does for
// updates).
func TestContainerActions_StackLookupFailureRefuses(t *testing.T) {
	for _, a := range containerActions("managed") {
		t.Run(a.name, func(t *testing.T) {
			h, _, fake := newContainerLockFixture(t)
			require.NoError(t, h.db.Close())

			w := doContainerAction(h, a)

			require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
			assert.Empty(t, fake.recorded(), "an unlocked action ran on a container whose stack was unknown")
		})
	}
}

// TestContainerActions_InspectErrorKeepsTheActionsErrorMapping: the inspect
// that finds the stack now runs first, so its failure answers what the
// action's own Docker failure answered before: 503 for an unreachable daemon,
// the action's 500 otherwise (a missing container included: these routes
// never mapped Docker's not-found to 404).
func TestContainerActions_InspectErrorKeepsTheActionsErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{services.ErrDockerUnavailable, http.StatusServiceUnavailable},
		{errors.New("No such container: managed"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		for _, a := range containerActions("managed") {
			t.Run(a.name+"/"+tc.err.Error(), func(t *testing.T) {
				h, _, fake := newContainerLockFixture(t)
				fake.inspectErr = tc.err

				w := doContainerAction(h, a)

				require.Equal(t, tc.status, w.Code, "body: %s", w.Body.String())
				assert.Empty(t, fake.recorded())
				if a.name != "delete" && tc.status == http.StatusInternalServerError {
					assert.Equal(t, "DOCKER_OPERATION", decodeBody(t, w)["code"])
				}
			})
		}
	}
}
