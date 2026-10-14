package services

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-4ex6: every rclone child process runs under an operation
// deadline and reports a timeout as one (safe-defaults rule 1). ---

// hangingRcloneRunner blocks until its context ends, like an rclone stuck on
// an unreachable remote. hangOutput picks which call hangs; the other answers
// at once with outputData, so a test can get past a probe and hang the sync.
type hangingRcloneRunner struct {
	hangOutput bool
	outputData []byte
}

func (r hangingRcloneRunner) Run(ctx context.Context, _ string, _ []string, _ []string, _ chan<- StreamLine) error {
	<-ctx.Done()
	return fmt.Errorf("rclone exited: %w", errors.New("signal: killed"))
}

func (r hangingRcloneRunner) Output(ctx context.Context, _ string, _ []string, _ []string) ([]byte, error) {
	if !r.hangOutput {
		return r.outputData, nil
	}
	<-ctx.Done()
	return nil, fmt.Errorf("rclone exited: %w", errors.New("signal: killed"))
}

func TestRcloneManager_HungCallTimesOutAndSaysSo(t *testing.T) {
	t.Parallel()

	ops := []struct {
		what   string
		runner hangingRcloneRunner
		run    func(ctx context.Context, m *RcloneManager) error
	}{
		{"rclone lsd", hangingRcloneRunner{}, func(ctx context.Context, m *RcloneManager) error {
			return m.TestConnectivity(ctx, "r")
		}},
		{"rclone lsf", hangingRcloneRunner{hangOutput: true}, func(ctx context.Context, m *RcloneManager) error {
			return m.probeRestoreSource(ctx, "r", "p")
		}},
		{"rclone lsf", hangingRcloneRunner{hangOutput: true}, func(ctx context.Context, m *RcloneManager) error {
			_, err := m.remoteHasSnapshots(ctx, "r", "p")
			return err
		}},
		{"rclone sync", hangingRcloneRunner{}, func(ctx context.Context, m *RcloneManager) error {
			return m.Sync(ctx, t.TempDir(), "r", "p", 4, 3, 0, nil)
		}},
		{"rclone sync", hangingRcloneRunner{outputData: []byte("config\n")}, func(ctx context.Context, m *RcloneManager) error {
			return m.RestoreRepo(ctx, "r", "p", t.TempDir(), "", 3, nil)
		}},
	}
	for i, op := range ops {
		t.Run(fmt.Sprintf("%d %s", i, op.what), func(t *testing.T) {
			t.Parallel()
			m := testRcloneManager(op.runner)
			m.testDeadlineCap = 200 * time.Millisecond
			// The outer bound only stops a call with no deadline of its own
			// from hanging the test; it is a plain parent, not a timeout.
			outer, cancelOuter := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelOuter()
			err := op.run(outer, m)
			require.Error(t, err)
			assert.Contains(t, err.Error(), op.what+" timed out after 200ms")

			// Control: a parent cancellation is not reported as a timeout.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err = op.run(ctx, m)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "timed out")
		})
	}
}

// failThenHangRunner fails the first sync attempt at once, so Sync and
// RestoreRepo enter their backoff wait, and the deadline has to end that wait.
type failThenHangRunner struct{ hangingRcloneRunner }

func (r failThenHangRunner) Run(context.Context, string, []string, []string, chan<- StreamLine) error {
	return errors.New("rclone: transient error")
}

// The deadline covers the retry loop as a whole, backoff included: a timeout
// that lands in the wait between attempts reads as one too.
func TestRcloneManager_DeadlineDuringBackoffSaysSo(t *testing.T) {
	t.Parallel()
	ops := []struct {
		name string
		run  func(ctx context.Context, m *RcloneManager) error
	}{
		{"sync", func(ctx context.Context, m *RcloneManager) error {
			return m.Sync(ctx, t.TempDir(), "r", "p", 4, 3, 0, nil)
		}},
		{"restore", func(ctx context.Context, m *RcloneManager) error {
			return m.RestoreRepo(ctx, "r", "p", t.TempDir(), "", 3, nil)
		}},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			m := testRcloneManager(failThenHangRunner{hangingRcloneRunner{outputData: []byte("config\n")}})
			m.testDeadlineCap = 200 * time.Millisecond
			outer, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := op.run(outer, m)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "rclone sync timed out after 200ms")
		})
	}
}

// Sync and RestoreRepo hold the global backup lock; their bound is the long
// one, matching restic's backup and restore.
func TestRcloneManager_SyncAndRestoreRunUnderTheLongDeadline(t *testing.T) {
	t.Parallel()
	ops := []struct {
		name string
		run  func(m *RcloneManager) error
		want time.Duration
	}{
		{"sync", func(m *RcloneManager) error {
			return m.Sync(context.Background(), t.TempDir(), "r", "p", 4, 1, 0, nil)
		}, syncTimeout},
		{"restore", func(m *RcloneManager) error {
			return m.RestoreRepo(context.Background(), "r", "p", t.TempDir(), "", 1, nil)
		}, restoreRepoTimeout},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			runner := &ctxRecordingRunner{fakeRunner: fakeRunner{outputData: []byte("config\n"), onRun: func(string, []string, chan<- StreamLine) {}}}
			m := testRcloneManager(runner)
			before := time.Now()
			require.NoError(t, op.run(m))
			require.True(t, runner.hasDeadline, "rclone sync ran with no deadline")
			assert.WithinDuration(t, before.Add(op.want), runner.deadline, time.Minute)
		})
	}
}
