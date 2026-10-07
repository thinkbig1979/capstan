package services

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubSlowReap redirects execCommandContext (which reapContainerShell builds
// its in-container kill with) at a `cat` that runs until the test closes its
// stdin, so a reap stays in progress for as long as the test needs. started
// receives once per reap; calls counts them. Every goroutine that can reap is
// run through goReap, so cleanup ends the reaps and waits for them before it
// closes the pipe they read and restores the stub.
func stubSlowReap(t *testing.T) (started chan struct{}, calls *atomic.Int32, goReap func(func())) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	started = make(chan struct{}, 8)
	calls = &atomic.Int32{}
	var inflight sync.WaitGroup
	goReap = func(f func()) {
		inflight.Add(1)
		go func() {
			defer inflight.Done()
			f()
		}()
	}

	orig := execCommandContext
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		calls.Add(1)
		started <- struct{}{}
		cmd := exec.CommandContext(ctx, "cat")
		cmd.Stdin = r
		return cmd
	}
	t.Cleanup(func() {
		// Closing the writer ends every in-flight reap.
		w.Close()
		inflight.Wait()
		execCommandContext = orig
		r.Close()
	})
	return started, calls, goReap
}

// requireReturnsWhileReaping fails if UpdateActivity on an unrelated session
// cannot get through while another session's reap is in progress.
func requireReturnsWhileReaping(t *testing.T, s *TerminalService, otherID string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		s.UpdateActivity(otherID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second): // wall-clock ok: the fixed call returns in microseconds; the broken one waits for the whole reap
		t.Fatal("UpdateActivity on another session blocked while a session was being reaped: s.mu is held across reapContainerShell")
	}
}

// TestTerminalReapDoesNotHoldLock is agent-os-z91e.7. Tearing a session down
// runs a `docker exec` inside its container (up to 5s); before the fix both
// CloseSession and reapExpiredSessions held s.mu across it, so every other
// terminal's keystrokes (UpdateActivity) waited on it.
func TestTerminalReapDoesNotHoldLock(t *testing.T) {
	t.Run("CloseSession", func(t *testing.T) {
		started, _, goReap := stubSlowReap(t)
		s := NewTerminalService(nil)
		s.sessions["closing"] = &TerminalSession{ID: "closing", ContainerName: "c1", lastActivity: time.Now()}
		s.sessions["other"] = &TerminalSession{ID: "other", ContainerName: "c2", lastActivity: time.Now()}

		goReap(func() { s.CloseSession("closing") })
		<-started
		requireReturnsWhileReaping(t, s, "other")
	})

	t.Run("reapExpiredSessions", func(t *testing.T) {
		started, _, goReap := stubSlowReap(t)
		s := NewTerminalService(nil)
		s.sessions["expired"] = &TerminalSession{ID: "expired", ContainerName: "c1", lastActivity: time.Now().Add(-2 * SessionTimeout)}
		s.sessions["other"] = &TerminalSession{ID: "other", ContainerName: "c2", lastActivity: time.Now()}

		goReap(func() { s.reapExpiredSessions(context.Background()) })
		<-started
		requireReturnsWhileReaping(t, s, "other")
	})
}

// TestTerminalCloseRacingReaperTerminatesOnce guards the move above, it is not
// a fail-first test (it passes before and after): once the slow work runs
// outside s.mu, the lookup and the delete must stay in one critical section,
// or CloseSession and the reaper could both claim the same session and reap
// it twice.
func TestTerminalCloseRacingReaperTerminatesOnce(t *testing.T) {
	_, calls, goReap := stubSlowReap(t)
	s := NewTerminalService(nil)
	s.sessions["x"] = &TerminalSession{ID: "x", ContainerName: "c1", lastActivity: time.Now().Add(-2 * SessionTimeout)}

	goReap(func() { s.CloseSession("x") })
	goReap(func() { s.reapExpiredSessions(context.Background()) })

	// Let both callers reach the lock and either claim or miss the session.
	deadline := time.Now().Add(time.Second) // wall-clock ok: bounds a wait for a second claim that a correct service never makes
	for time.Now().Before(deadline) && calls.Load() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, int32(1), calls.Load(), "the same session was reaped more than once")
}
