package services

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// stubHangingChild points both exec indirections at `sh -c <script>`, so a
// call site runs the stand-in whichever of the two it uses. Stubbing only
// execCommandContext would let a site still on execCommand run a real
// `docker` binary.
func stubHangingChild(t *testing.T, script string) {
	t.Helper()
	origCmd, origCtx := execCommand, execCommandContext
	//nolint:gosec // G204: script is a literal from this file
	execCommand = func(string, ...string) *exec.Cmd { return exec.Command("sh", "-c", script) }
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		//nolint:gosec // G204: script is a literal from this file
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	t.Cleanup(func() { execCommand, execCommandContext = origCmd, origCtx })
}

// TestStartVerified_HungChildTimesOutAndFreesTheLock is agent-os-a1ye.3's
// acceptance (a). The child never exits on its own. The lock is taken and
// released exactly as handlers/stack_lifecycle.go's StartStack does (Acquire,
// then a deferred Release around StartVerified), so "free afterwards" means
// the call came back, which is what a hung child used to prevent.
//
// Two shapes, because they fail differently: `exec sleep` is killed directly
// at the deadline; plain `sleep` is a GRANDCHILD that keeps the output pipe
// open after its parent shell is killed, which is how the docker CLI and its
// compose plugin behave. Without WaitDelay, CombinedOutput waits on that pipe
// for the full sleep even though the deadline fired.
func TestStartVerified_HungChildTimesOutAndFreesTheLock(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, tc := range []struct {
		name   string
		script string
		bound  time.Duration // timeout + WaitDelay + slack
	}{
		{"direct child", "exec sleep 60", 3 * time.Second},
		{"grandchild holds the pipe", "sleep 60", commandWaitDelay + 3*time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubHangingChild(t, tc.script)
			dir := t.TempDir()
			svc := &DockerService{config: &config.Config{StacksDir: dir, ComposeTimeout: timeout}}
			stack := models.Stack{ID: "s1", Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}
			lock := NewOperationLock()

			done := make(chan truth.ActionResult, 1)
			start := time.Now()
			go func() {
				token, err := lock.Acquire(stack.ID, OpKindStart)
				if err != nil {
					t.Errorf("first Acquire: %v", err)
				}
				defer lock.Release(stack.ID, token)
				ar, _ := svc.StartVerified(stack)
				done <- ar
			}()

			var ar truth.ActionResult
			select {
			case ar = <-done:
			case <-time.After(tc.bound):
				t.Fatalf("StartVerified still running %s after a %s deadline: the child is unbounded and the stack lock is still held", tc.bound, timeout)
			}
			t.Logf("returned after %s", time.Since(start).Round(time.Millisecond))

			assert.Equal(t, truth.OutcomeFailed, ar.Outcome)
			require.Error(t, ar.Err)
			assert.Contains(t, ar.Err.Error(), "docker compose up timed out after 300ms")

			token, err := lock.Acquire(stack.ID, OpKindStart)
			require.NoError(t, err, "the lock must be free once StartVerified has returned")
			lock.Release(stack.ID, token)
		})
	}
}

// TestRunStreaming_HungChildEndsWithATimeoutDoneFrame: the streaming path
// gets the same bound from its own config, whatever context the caller passes
// (operations.go passes one that never ends, by design).
func TestRunStreaming_HungChildEndsWithATimeoutDoneFrame(t *testing.T) {
	stubHangingChild(t, "exec sleep 60")
	dir := t.TempDir()
	svc := &DockerService{config: &config.Config{StacksDir: dir, ComposeTimeout: 300 * time.Millisecond}}
	stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}

	var last StreamLine
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for line := range svc.RunStreaming(context.Background(), stack, "up", []string{"-d"}) {
			last = line
		}
	}()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("RunStreaming still running 3s after a 300ms deadline")
	}
	assert.Equal(t, "done", last.Type)
	assert.False(t, last.Success)
	assert.Equal(t, truth.OutcomeFailed, last.Outcome)
	assert.Contains(t, last.Error, "docker compose up timed out after 300ms")
}

// TestUpdateJob_RunPastDeadlineEndsAsErrorNamingTheTimeout: a manual update
// job's run func receives a context that ends at the job timeout, so the
// docker calls inside it (all context-aware) stop, and the job records why.
func TestUpdateJob_RunPastDeadlineEndsAsErrorNamingTheTimeout(t *testing.T) {
	m := NewUpdateJobManager(time.Minute)
	t.Cleanup(m.Stop)
	m.SetJobTimeout(200 * time.Millisecond)

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "c1", Name: "web"},
		func(ctx context.Context, _ string, _ func(LogLine), _ func(Status)) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(30 * time.Second):
				return nil
			}
		})

	deadline := time.Now().Add(3 * time.Second)
	var got *Job
	for time.Now().Before(deadline) {
		got = m.Get(job.ID)
		if got != nil && got.Status == StatusError {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotNil(t, got)
	require.Equal(t, StatusError, got.Status, "job still %q 3s after a 200ms timeout", got.Status)
	assert.True(t, strings.Contains(got.Error, "timed out after 200ms"), "job error %q does not name the timeout", got.Error)
}
