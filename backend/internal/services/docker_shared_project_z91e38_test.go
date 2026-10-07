package services

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-z91e.38 (owner decision D28): compose finds a project's containers by
// `-p <project name>`, and two stacks can carry one name (D25), so every
// compose command that changes containers is refused for a stack whose name
// another stack carries. These drive each mutating DockerService path with the
// docker binary replaced by a recorder, so "refused" means no compose process
// was started at all, and the unshared control proves the same fixture does
// start one.

// z91e38Execs replaces the docker binary with `exit 1` and counts the starts.
type z91e38Execs struct {
	mu    sync.Mutex
	calls int
}

func (e *z91e38Execs) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func stubDockerRecorder(t *testing.T) *z91e38Execs {
	t.Helper()
	rec := &z91e38Execs{}
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		rec.mu.Lock()
		rec.calls++
		rec.mu.Unlock()
		return exec.CommandContext(ctx, "sh", "-c", "exit 1")
	}
	t.Cleanup(func() { execCommandContext = orig })
	return rec
}

// z91e38DB holds stacks s-alpha and s-beta, both named "shared" (as the scanner
// stores two directories whose compose files both say `name: shared`), and
// s-web, named "web" alone.
func z91e38DB(t *testing.T) *database.DB {
	t.Helper()
	db := newTestDB(t)
	for _, s := range []struct{ id, project, dir string }{
		{"s-alpha", "shared", "/srv/stacks/alpha"},
		{"s-beta", "shared", "/srv/stacks/beta"},
		{"s-web", "web", "/srv/stacks/web"},
	} {
		require.NoError(t, db.UpsertDirectory(models.Directory{Path: s.dir, Name: s.dir, RootDir: "/srv/stacks", ScannedAt: time.Now()}))
		require.NoError(t, db.UpsertStack(models.Stack{ID: s.id, ProjectName: s.project, Directory: s.dir, ComposeFile: "compose.yaml", Status: "running"}))
	}
	return db
}

type z91e38FaultLookup struct{}

func (z91e38FaultLookup) GetStackByProjectName(string) (*models.Stack, error) {
	return nil, errors.New("sql: database is closed")
}

func z91e38Service(t *testing.T, lookup DashboardDB) *DockerService {
	t.Helper()
	svc := &DockerService{
		config:   &config.Config{DataDir: t.TempDir(), ComposeTimeout: 5 * time.Second},
		statusFn: func(models.Stack) (string, []models.Container, error) { return "stopped", nil, nil },
	}
	svc.SetStackLookup(lookup)
	return svc
}

func z91e38Stack(t *testing.T, id, project string) models.Stack {
	return models.Stack{ID: id, ProjectName: project, Directory: t.TempDir(), ComposeFile: "compose.yaml"}
}

// z91e38Lifecycle is every exported verb that runs a mutating compose command
// and returns an ActionResult.
var z91e38Lifecycle = []struct {
	name string
	run  func(*DockerService, models.Stack) truth.ActionResult
}{
	{"StartVerified", func(s *DockerService, st models.Stack) truth.ActionResult { ar, _ := s.StartVerified(st); return ar }},
	{"StopVerified", func(s *DockerService, st models.Stack) truth.ActionResult { ar, _ := s.StopVerified(st); return ar }},
	{"RestartVerified", func(s *DockerService, st models.Stack) truth.ActionResult { ar, _ := s.RestartVerified(st); return ar }},
	{"PullVerified", func(s *DockerService, st models.Stack) truth.ActionResult { ar, _ := s.PullVerified(st); return ar }},
	{"DeleteVerified", func(s *DockerService, st models.Stack) truth.ActionResult { ar, _ := s.DeleteVerified(st); return ar }},
	{"UpdateComposeServiceStreaming", func(s *DockerService, st models.Stack) truth.ActionResult {
		s.updateClient = &z91e38ListingDocker{}
		_, _, _, ar := s.UpdateComposeServiceStreaming(context.Background(), st, "web", func(LogLine) {}, func(Status) {})
		return ar
	}},
	{"updateComposeContainer", func(s *DockerService, st models.Stack) truth.ActionResult {
		err := s.updateComposeContainer(context.Background(), st, "web", false)
		return truth.Failed("updateComposeContainer", err)
	}},
	{"updateComposeContainerStreaming", func(s *DockerService, st models.Stack) truth.ActionResult {
		err := s.updateComposeContainerStreaming(context.Background(), st, "web", false, func(LogLine) {}, func(Status) {})
		return truth.Failed("updateComposeContainerStreaming", err)
	}},
}

// z91e38ListingDocker is bx43FakeDocker whose ContainerList finds the
// service's container, so UpdateComposeServiceStreaming gets past its reads to
// the compose pull. Every write still fails and records itself.
type z91e38ListingDocker struct{ bx43FakeDocker }

func (f *z91e38ListingDocker) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	f.record("ContainerList")
	return []container.Summary{{ID: bx43ContainerID}}, nil
}

func TestMutatingCompose_SharedProjectNameIsRefusedBeforeAnythingRuns(t *testing.T) {
	for _, op := range z91e38Lifecycle {
		t.Run(op.name, func(t *testing.T) {
			rec := stubDockerRecorder(t)
			svc := z91e38Service(t, z91e38DB(t))

			ar := op.run(svc, z91e38Stack(t, "s-alpha", "shared"))

			require.ErrorIs(t, ar.Err, errdefs.ErrAmbiguous, "reason: %s", ar.Reason)
			assert.Equal(t, truth.OutcomeFailed, ar.Outcome)
			assert.Equal(t, 0, rec.count(), "a refused command must not start compose")
			assert.Contains(t, ar.Err.Error(), "/srv/stacks/alpha")
			assert.Contains(t, ar.Err.Error(), "/srv/stacks/beta")
			if fake, ok := svc.updateClient.(*z91e38ListingDocker); ok {
				for _, c := range fake.applyCalls() {
					assert.Equal(t, "ContainerList", c, "a refused stack update must change nothing")
				}
			}
		})
	}
}

// The other side, same fixture and path: a stack whose name is its own does
// reach compose (or, for the stack-service update, its first Docker call), so
// the refusal above is the check and not a fixture that cannot run.
func TestMutatingCompose_UnsharedProjectNameRuns(t *testing.T) {
	for _, op := range z91e38Lifecycle {
		t.Run(op.name, func(t *testing.T) {
			rec := stubDockerRecorder(t)
			svc := z91e38Service(t, z91e38DB(t))

			ar := op.run(svc, z91e38Stack(t, "s-web", "web"))

			assert.NotErrorIs(t, ar.Err, errdefs.ErrAmbiguous)
			assert.Positive(t, rec.count(), "the unshared stack must start compose")
		})
	}
}

// A stacks table that cannot be read refuses too: running would mean not
// knowing whose containers the command touches.
func TestMutatingCompose_LookupFaultRefuses(t *testing.T) {
	for _, op := range z91e38Lifecycle {
		t.Run(op.name, func(t *testing.T) {
			rec := stubDockerRecorder(t)
			svc := z91e38Service(t, z91e38FaultLookup{})

			ar := op.run(svc, z91e38Stack(t, "s-web", "web"))

			require.Error(t, ar.Err)
			assert.Contains(t, ar.Err.Error(), "database is closed")
			assert.Contains(t, ar.Err.Error(), "the command was not run")
			assert.Equal(t, 0, rec.count(), "a refused command must not start compose")
		})
	}
}

// RunStreaming is the WebSocket operations path. A refusal is one error frame
// naming both stacks, and no compose process.
func TestRunStreaming_SharedProjectNameIsRefused(t *testing.T) {
	for _, sub := range []string{"up", "down", "pull", "restart"} {
		t.Run(sub, func(t *testing.T) {
			rec := stubDockerRecorder(t)
			svc := z91e38Service(t, z91e38DB(t))

			var lines []StreamLine
			for line := range svc.RunStreaming(context.Background(), z91e38Stack(t, "s-alpha", "shared"), sub, nil) {
				lines = append(lines, line)
			}

			require.Len(t, lines, 1, "frames: %+v", lines)
			assert.Equal(t, "error", lines[0].Type)
			assert.Contains(t, lines[0].Error, "/srv/stacks/alpha")
			assert.Contains(t, lines[0].Error, "/srv/stacks/beta")
			assert.Contains(t, lines[0].Error, "give each stack its own compose project name")
			assert.Equal(t, 0, rec.count(), "a refused command must not start compose")

			// Other side: the uniquely named stack starts compose.
			for range svc.RunStreaming(context.Background(), z91e38Stack(t, "s-web", "web"), sub, nil) {
			}
			assert.Positive(t, rec.count())
		})
	}
}

// With no lookup installed the check is off, which is what every other unit
// test in this package relies on; stacklookup_wiring_test.go in cmd/server
// proves production installs one.
func TestMutatingCompose_NoLookupRuns(t *testing.T) {
	rec := stubDockerRecorder(t)
	svc := z91e38Service(t, nil)
	ar, _ := svc.StartVerified(z91e38Stack(t, "s-alpha", "shared"))
	assert.NotErrorIs(t, ar.Err, errdefs.ErrAmbiguous)
	assert.Positive(t, rec.count())
}
