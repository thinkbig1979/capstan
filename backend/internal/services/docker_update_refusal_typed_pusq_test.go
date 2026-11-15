package services

import (
	"context"
	"fmt"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
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

// pusqSecondCallRefuses answers the first lookup (the pull's
// mutatingComposeArgs) and refuses the second (the up's), so the refusal
// wrap on the up branch is reached too (review F5).
type pusqSecondCallRefuses struct{ calls *int }

func (d pusqSecondCallRefuses) GetStackByProjectName(projectName string) (*models.Stack, error) {
	*d.calls++
	if *d.calls >= 2 {
		return nil, fmt.Errorf("two stacks: %w", errdefs.ErrAmbiguous)
	}
	return pusqStackDB{}.GetStackByProjectName(projectName)
}

func TestUpdateComposeRefusalAtUp_StartsNothing_Pusq(t *testing.T) {
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
			// The pull's args are built for real here, which reads the config.
			dir := t.TempDir()
			svc.config = &config.Config{StacksDir: dir, DataDir: dir}
			calls := 0
			svc.stackLookup = pusqSecondCallRefuses{calls: &calls}

			reason, nothing := call(svc)

			if calls != 2 {
				t.Fatalf("lookups = %d, want 2: the up's refusal was not the one reached", calls)
			}
			if !nothing {
				t.Errorf("startedNothing = false for a compose update refused at its up args (reason %q)", reason)
			}
			if applied := fake.applyCalls(); len(applied) != 0 {
				t.Errorf("apply calls on the refusal path: %v", applied)
			}
		})
	}
}
