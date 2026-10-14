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

// --- agent-os-j6ob: forget and prune hold the global backup lock, so each
// runs under an operation deadline. ---

func TestResticManager_RetentionAndPruneRunUnderADeadline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		run  func(m *ResticManager, out chan<- StreamLine) error
		cfg  func(*BackupConfig)
		want time.Duration
	}{
		{
			name: "forget without --prune",
			cfg:  func(c *BackupConfig) { c.AutoPrune = false },
			run: func(m *ResticManager, out chan<- StreamLine) error {
				return m.ApplyRetention(context.Background(), "x", out)
			},
			want: retentionTimeout,
		},
		{
			name: "forget --prune",
			cfg:  func(c *BackupConfig) { c.AutoPrune = true },
			run: func(m *ResticManager, out chan<- StreamLine) error {
				return m.ApplyRetention(context.Background(), "x", out)
			},
			want: pruneTimeout,
		},
		{
			name: "prune",
			cfg:  func(*BackupConfig) {},
			run:  func(m *ResticManager, out chan<- StreamLine) error { return m.Prune(context.Background(), false, out) },
			want: pruneTimeout,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := testBackupConfig()
			tc.cfg(&cfg)
			runner := &ctxRecordingRunner{}
			m := newResticManagerWithRunner(cfg, runner, nil)
			out, stop := drainVerifyLines()
			before := time.Now()
			require.NoError(t, tc.run(m, out))
			stop()
			require.True(t, runner.hasDeadline, "restic ran with no deadline")
			assert.WithinDuration(t, before.Add(tc.want), runner.deadline, time.Minute)
		})
	}
	assert.Less(t, retentionTimeout, pruneTimeout, "a plain forget is bounded tighter than a prune")
}

// hangingRunner blocks until its context ends, like a restic stuck on a
// network repository.
type hangingRunner struct{ fakeRunner }

func (r *hangingRunner) Run(ctx context.Context, _ string, _ []string, _ []string, _ chan<- StreamLine) error {
	<-ctx.Done()
	return fmt.Errorf("restic exited: %w", errors.New("signal: killed"))
}

func TestResticManager_RetentionAndPruneTimeoutReadsAsTimeout(t *testing.T) {
	t.Parallel()

	ops := []struct {
		what string
		run  func(ctx context.Context, m *ResticManager) error
	}{
		{"restic forget", func(ctx context.Context, m *ResticManager) error { return m.ApplyRetention(ctx, "x", nil) }},
		{"restic prune", func(ctx context.Context, m *ResticManager) error { return m.Prune(ctx, false, nil) }},
	}
	for _, op := range ops {
		t.Run(op.what, func(t *testing.T) {
			t.Parallel()
			m := newResticManagerWithRunner(testBackupConfig(), &hangingRunner{}, nil)
			m.testDeadlineCap = 200 * time.Millisecond
			err := op.run(context.Background(), m)
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
