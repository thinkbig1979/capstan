package handlers

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// fakeStackStatusReader reports what Docker would say about the stack: "pre"
// until the container action has reached Docker, "post" after it, so a store
// that reads before the action is told apart from one that reads after.
type fakeStackStatusReader struct {
	mu     sync.Mutex
	ops    *fakeContainerOps
	stacks []string
}

func (r *fakeStackStatusReader) Status(stack models.Stack) (string, []models.Container, error) {
	r.mu.Lock()
	r.stacks = append(r.stacks, stack.ID)
	r.mu.Unlock()
	if len(r.ops.recorded()) == 0 {
		return "pre", nil, nil
	}
	return "post", nil, nil
}

func (r *fakeStackStatusReader) reads() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stacks...)
}

// TestContainerActions_StoreStackStatus_Nyt5 is agent-os-nyt5: starting,
// stopping, restarting or deleting one container of a managed stack changed
// the stack's state and stored no stack status, so the stack list kept the
// old one. The stack is re-read after the action and that status is stored,
// on a Docker error too, since a failed stop or restart can still have
// changed what runs.
func TestContainerActions_StoreStackStatus_Nyt5(t *testing.T) {
	for _, dockerErr := range []error{nil, errors.New("daemon said no")} {
		for _, a := range containerActions("managed") {
			t.Run(a.name+"/err="+errString(dockerErr), func(t *testing.T) {
				h, _, fake := newContainerLockFixture(t)
				fake.actionErr = dockerErr
				reader := &fakeStackStatusReader{ops: fake}
				h.statusReader = reader
				require.NoError(t, h.db.UpdateStackStatus("s1", "running"))

				w := doContainerAction(h, a)

				if dockerErr == nil {
					require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				}
				stack, err := h.db.GetStack("s1")
				require.NoError(t, err)
				assert.Equal(t, "post", stack.Status, "the stack's status read after the action must be stored")
				assert.Equal(t, []string{"s1"}, reader.reads())
			})
		}
	}
}

// TestContainerActions_UnmanagedStoreNoStatus_Nyt5: a container no managed
// stack owns reads no stack and changes no stored status.
func TestContainerActions_UnmanagedStoreNoStatus_Nyt5(t *testing.T) {
	for _, id := range []string{"standalone", "foreign"} {
		for _, a := range containerActions(id) {
			t.Run(id+"/"+a.name, func(t *testing.T) {
				h, _, fake := newContainerLockFixture(t)
				reader := &fakeStackStatusReader{ops: fake}
				h.statusReader = reader
				require.NoError(t, h.db.UpdateStackStatus("s1", "running"))

				w := doContainerAction(h, a)

				require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
				assert.Empty(t, reader.reads(), "no stack owns this container, so none is read")
				stack, err := h.db.GetStack("s1")
				require.NoError(t, err)
				assert.Equal(t, "running", stack.Status)
			})
		}
	}
}
