package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-txqx: update jobs changed a stack's containers and never stored
// the stack's status. After an update the stack is re-read and that status
// stored; an update refused before anything ran stores nothing.

type txqxStore struct {
	mu     sync.Mutex
	stored map[string]string
}

func (s *txqxStore) UpdateStackStatus(id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stored == nil {
		s.stored = map[string]string{}
	}
	s.stored[id] = status
	return nil
}

type txqxReader struct {
	status string
	err    error
	reads  int
}

func (r *txqxReader) Status(models.Stack) (string, []models.Container, error) {
	r.reads++
	return r.status, nil, r.err
}

func TestStoreObservedStackStatus_Txqx(t *testing.T) {
	stack := models.Stack{ID: "s1", ProjectName: "web"}
	refusedUpdate := truth.Failed(refusedUpdateReason, updateRefusedError{errors.New("database is closed")})
	refusedComposeAR := refusedCompose(errors.New("shared project name"))
	noDocker := truth.Failed(dockerUnavailableReason, ErrDockerUnavailable)

	cases := []struct {
		name      string
		results   []truth.ActionResult
		readErr   error
		wantStore bool
	}{
		{"success stores the re-read, not the mapping", []truth.ActionResult{truth.Success("Updated")}, nil, true},
		{"a failed update stores the re-read too", []truth.ActionResult{truth.Failed("compose up failed", errors.New("exit 1"))}, nil, true},
		{"one service ran, the next was refused", []truth.ActionResult{truth.Success("Updated"), refusedComposeAR}, nil, true},
		{"refused update stores nothing", []truth.ActionResult{refusedUpdate}, nil, false},
		{"refused compose stores nothing", []truth.ActionResult{refusedComposeAR}, nil, false},
		{"no Docker stores nothing", []truth.ActionResult{noDocker}, nil, false},
		{"no results stores nothing", nil, nil, false},
		{"a failed read stores nothing", []truth.ActionResult{truth.Success("Updated")}, errors.New("compose ps timed out"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &txqxStore{}
			// "stopped" is what a success would never map to, so a stored
			// "stopped" can only have come from the re-read.
			reader := &txqxReader{status: "stopped", err: tc.readErr}
			StoreObservedStackStatus(store, reader, stack, tc.results...)
			if tc.wantStore {
				assert.Equal(t, map[string]string{"s1": "stopped"}, store.stored)
			} else {
				assert.Empty(t, store.stored)
			}
		})
	}
}

// The two strategy refusals must carry updateRefusedError, so the stores
// above see them as refused, and must still unwrap to their cause.
func TestUpdateRefusalIsTyped_Txqx(t *testing.T) {
	paths := map[string]func(*DockerService, DashboardDB) truth.ActionResult{
		"UpdateContainer": func(s *DockerService, db DashboardDB) truth.ActionResult {
			_, ar := s.UpdateContainer(context.Background(), bx43ContainerID, db)
			return ar
		},
		"UpdateContainerStreaming": func(s *DockerService, db DashboardDB) truth.ActionResult {
			_, ar := s.UpdateContainerStreaming(context.Background(), bx43ContainerID, db, func(LogLine) {}, func(Status) {})
			return ar
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			svc, fake := bx43Service(bx43ComposeLabels())
			ar := run(svc, g482ClosedDB(t))
			bx43AssertRefused(t, ar, fake)

			var refused updateRefusedError
			assert.True(t, errors.As(ar.Err, &refused), "the refusal is typed: %v", ar.Err)
			store := &txqxStore{}
			StoreObservedStackStatus(store, &txqxReader{status: "running"}, models.Stack{ID: "s1"}, ar)
			assert.Empty(t, store.stored, "a refused update stores nothing")
		})
	}
}

// txqxChecker is fakeUpdateChecker with a Status, as *DockerService has.
type txqxChecker struct {
	*fakeUpdateChecker
	reader txqxReader
}

func (c *txqxChecker) Status(stack models.Stack) (string, []models.Container, error) {
	return c.reader.Status(stack)
}

func TestAutoUpdateStoresStackStatus_Txqx(t *testing.T) {
	cases := []struct {
		name   string
		ar     truth.ActionResult
		want   string
		reread bool
	}{
		{"success", truth.Success("Updated"), "partial", true},
		{"failed", truth.Failed("update apply failed", errors.New("compose up failed")), "partial", true},
		{"refused", truth.Failed(refusedUpdateReason, updateRefusedError{errors.New("database is closed")}), "stopped", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checker := &txqxChecker{
				fakeUpdateChecker: &fakeUpdateChecker{
					updateFn: func(string) (models.UpdateResult, truth.ActionResult) { return models.UpdateResult{}, tc.ar },
				},
				reader: txqxReader{status: "partial"},
			}
			checker.checkFn = func(context.Context) ([]models.ContainerUpdateInfo, error) {
				return []models.ContainerUpdateInfo{hungFinding()}, nil
			}
			svc := newApplyFixture(t, checker)
			require.NoError(t, svc.db.UpsertDirectory(models.Directory{Path: "/srv/stacks/web", Name: "web",
				RootDir: "/srv/stacks", ScannedAt: time.Now()}))
			require.NoError(t, svc.db.UpsertStack(models.Stack{ID: "s1", ProjectName: "web", Directory: "/srv/stacks/web",
				ComposeFile: "compose.yaml", Status: "stopped"}))
			seedContainerPolicy(t, svc, "web")

			svc.runCycle(context.Background())

			require.Equal(t, []string{"c1"}, checker.updatedIDs(), "the auto-update ran")
			got, err := svc.db.GetStack("s1")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.Status)
			assert.Equal(t, tc.reread, checker.reader.reads > 0)
		})
	}
}
