package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cancellingRunner wraps multiCallRunner and ends the run's context from inside
// a restic call: on the n-th `backup` (cancelOnBackup, returning the context's
// error as a killed restic would) or right after the first `forget`
// (cancelAfterForget, a stack that finished cleanly). Everything else is
// multiCallRunner's positional script.
type cancellingRunner struct {
	*multiCallRunner
	cancel            context.CancelFunc
	cancelOnBackup    int
	cancelAfterForget bool

	mu      sync.Mutex
	backups int
}

func (c *cancellingRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	// exec.CommandContext refuses to start on a dead context; a fake that
	// ignored it would let a not-started stack "succeed" and hide the bug.
	if err := ctx.Err(); err != nil {
		return err
	}
	first := ""
	if len(args) > 0 {
		first = args[0]
	}
	if name == "restic" && first == "backup" {
		c.mu.Lock()
		c.backups++
		n := c.backups
		c.mu.Unlock()
		if c.cancelOnBackup != 0 && n == c.cancelOnBackup {
			c.cancel()
			return context.Canceled
		}
	}
	err := c.multiCallRunner.Run(ctx, name, args, env, out)
	if name == "restic" && first == "forget" && c.cancelAfterForget {
		c.cancel()
	}
	return err
}

// itemStatuses maps stack id -> backup_run_items.status for a run.
func itemStatuses(t *testing.T, svc *BackupService, runID string) map[string]string {
	t.Helper()
	items, err := svc.db.GetBackupRunItems(runID)
	require.NoError(t, err)
	got := map[string]string{}
	for _, it := range items {
		got[it.StackID] = it.Status
	}
	return got
}

// TestRunBackup_StackNeverStartedIsNotCountedAsFailed is agent-os-z91e.33's
// regression test. executeBackupRun called backupStack for every remaining
// stack after the run's context had ended. Each one failed at once on the dead
// context, so a stack that never started was counted in StacksFailed, reported
// as "stack X failed", and written as a failed item.
//
// PRODUCER: the per-stack loop in executeBackupRun with no ctx check between
// items. CONSUMER: run.StacksFailed and the status switch that reads it.
func TestRunBackup_StackNeverStartedIsNotCountedAsFailed(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	// The first `backup` is the capstan.db snapshot, the second is stack-a's: it
	// is the one in flight when the context ends.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &cancellingRunner{
		multiCallRunner: &multiCallRunner{responses: []multiCallResponse{
			{binary: "restic", argPrefix: "backup"},
		}},
		cancel:         cancel,
		cancelOnBackup: 2,
	}
	svc := buildSvc(t, db, &fakeDocker{statusStr: "stopped"}, runner, runner)
	seedStack(t, db, "stack-a", "hot")
	seedStack(t, db, "stack-b", "hot")

	out := make(chan StreamLine, 512)
	run, err := svc.RunBackup(ctx, nil, false, "manual", out)
	require.NoError(t, err)
	close(out)

	assert.Equal(t, 2, run.StacksTotal)
	assert.Equal(t, 1, run.StacksFailed, "only stack-a, the stack in flight when the context ended, failed; stack-b never started")
	assert.Equal(t, 0, run.StacksOK)
	assert.Equal(t, "failed", run.Status)
	assert.Contains(t, run.ErrorMessage, "stack-b", "the run names the stack it never started")
	assert.Equal(t, map[string]string{"stack-a": "failed", "stack-b": "skipped"}, itemStatuses(t, svc, run.ID))

	var failedLines []string
	for l := range out {
		if l.Type == "error" && strings.HasPrefix(l.Line, "stack stack-b failed") {
			failedLines = append(failedLines, l.Line)
		}
	}
	assert.Empty(t, failedLines, "stack-b must not be streamed as a failure")
}

// TestRunBackup_StacksLeftAfterContextEndIsPartialNotSuccess: stack-a backs up
// cleanly, then the context ends. StacksFailed is 0, which the status switch
// reads as success, but stack-b has no snapshot. Same principle as the
// unresolved-stack arm (agent-os-6wr): work that did not happen is not success.
func TestRunBackup_StacksLeftAfterContextEndIsPartialNotSuccess(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := &cancellingRunner{
		multiCallRunner: &multiCallRunner{responses: []multiCallResponse{
			{binary: "restic", argPrefix: "backup"}, // capstan.db
			{binary: "restic", argPrefix: "backup"}, // stack-a
			{binary: "restic", argPrefix: "snapshots", output: snapshotJSON("abc", "ab1", "stack-a")},
			{binary: "restic", argPrefix: "forget"},
			{binary: "restic", argPrefix: "snapshots", output: snapshotJSON("abc", "ab1", "stack-a")},
		}},
		cancel:            cancel,
		cancelAfterForget: true,
	}
	svc := buildSvc(t, db, &fakeDocker{statusStr: "stopped"}, runner, runner)
	seedStack(t, db, "stack-a", "hot")
	seedStack(t, db, "stack-b", "hot")

	run, err := svc.RunBackup(ctx, nil, false, "manual", make(chan StreamLine, 512))
	require.NoError(t, err)

	assert.Equal(t, 1, run.StacksOK)
	assert.Equal(t, 0, run.StacksFailed)
	assert.Equal(t, "partial", run.Status, "a run that left a stack unstarted is not a success")
	assert.Contains(t, run.ErrorMessage, "stack-b")
	assert.Equal(t, map[string]string{"stack-a": "success", "stack-b": "skipped"}, itemStatuses(t, svc, run.ID))
}

// TestRunBackup_ContextEndedBeforeAnyStackStartsFailsTheRun: nothing was backed
// up and nothing failed, which is still a failed run, as the unresolved arm
// says when StacksOK == 0. The skipped item rows and the run row are written
// with the context already dead, which is the case the DB writes must survive:
// they take no context.
func TestRunBackup_ContextEndedBeforeAnyStackStartsFailsTheRun(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{statusStr: "stopped"}, &fakeRunner{runErr: errors.New("restic: context canceled")}, &fakeRunner{})
	seedStack(t, db, "stack-a", "hot")
	seedStack(t, db, "stack-b", "hot")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run, err := svc.RunBackup(ctx, nil, false, "manual", make(chan StreamLine, 512))
	require.NoError(t, err)

	assert.Equal(t, 2, run.StacksTotal)
	assert.Equal(t, 0, run.StacksFailed, "no stack started, so none failed")
	assert.Equal(t, 0, run.StacksOK)
	assert.Equal(t, "failed", run.Status)
	assert.Equal(t, map[string]string{"stack-a": "skipped", "stack-b": "skipped"}, itemStatuses(t, svc, run.ID))

	stored, err := db.GetBackupRunByID(run.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", stored.Status, "the run row was finalised even though the context was dead")
	assert.NotNil(t, stored.FinishedAt)
}
