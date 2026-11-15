package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-pusq. A single-container compose update whose compose args are
// refused (a shared project name, or a lookup fault) runs nothing, so it must
// read as "started nothing": StoreObservedStackStatus then leaves the stack's
// stored status alone, as it does for UpdateComposeServiceStreaming's refusal.
//
// The refusal is reachable without a time-of-check race: resolveUpdateStrategy
// reads the db argument, refuseSharedProjectName reads s.stackLookup. Here the
// first resolves the stack and the second says the name is shared.

type pusqStackDB struct{ err error }

func (d pusqStackDB) GetStackByProjectName(projectName string) (*models.Stack, error) {
	if d.err != nil {
		return nil, d.err
	}
	return &models.Stack{ID: "pusq-stack", ProjectName: projectName, Directory: "/nonexistent/pusq"}, nil
}

func TestUpdateComposeRefusal_StartsNothing_Pusq(t *testing.T) {
	ambiguous := pusqStackDB{err: fmt.Errorf("two stacks: %w", errdefs.ErrAmbiguous)}
	resolving := pusqStackDB{}

	run := map[string]func(svc *DockerService) (string, bool){
		"UpdateContainer": func(svc *DockerService) (string, bool) {
			_, ar := svc.UpdateContainer(context.Background(), bx43ContainerID, resolving)
			return ar.Reason, startedNothing(ar)
		},
		"UpdateContainerStreaming": func(svc *DockerService) (string, bool) {
			_, ar := svc.UpdateContainerStreaming(context.Background(), bx43ContainerID, resolving,
				func(LogLine) {}, func(Status) {})
			return ar.Reason, startedNothing(ar)
		},
	}
	for name, call := range run {
		t.Run(name, func(t *testing.T) {
			svc, fake := bx43Service(bx43ComposeLabels())
			svc.stackLookup = ambiguous

			reason, nothing := call(svc)

			if !nothing {
				t.Errorf("startedNothing = false for a refused compose update (reason %q): the update job would store a re-read status for an update that never ran", reason)
			}
			if applied := fake.applyCalls(); len(applied) != 0 {
				t.Errorf("apply calls on the refusal path: %v", applied)
			}
		})
	}
}
