package services

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-sufn: restic exits 3 when it wrote the snapshot but could not read
// some source files. OBSERVED with restic 0.18.0 (`restic backup --json` over a
// directory holding two chmod-000 files): exit 3, the summary line with its
// snapshot_id still on stdout, one `"message_type":"error"` line per unreadable
// file carrying its path in "item", and the snapshot listed by `restic
// snapshots`. Any non-nil error used to mark the stack failed and skip verify
// and retention, so a stack with one permanently unreadable file failed every
// night and its snapshots were never pruned.

const (
	sufnStack      = "sufn-app"
	sufnSnapshotID = "cccccccc33333333333333333333333333333333333333333333333333333333"
)

// sufnRunner plays restic for one stack whose backup exits with exitCode after
// reporting unreadable as error lines. The database backup always succeeds
// unless dbExitCode is set.
type sufnRunner struct {
	exitCode   int
	noSummary  bool
	unreadable []string
	dbExitCode int

	mu    sync.Mutex
	calls [][]string
}

func (r *sufnRunner) callsOf(sub, tag string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c[0] == sub && (tag == "" || argPairContains(c, "--tag", tag)) {
			n++
		}
	}
	return n
}

func sufnExit(code int) error {
	if code == 0 {
		return nil
	}
	return fmt.Errorf("restic exited: %w", fakeExitError{code: code})
}

func (r *sufnRunner) Run(_ context.Context, _ string, args []string, _ []string, out chan<- StreamLine) error {
	r.mu.Lock()
	r.calls = append(r.calls, slices.Clone(args))
	r.mu.Unlock()

	switch {
	case args[0] == "backup" && slices.Contains(args, DatabaseBackupTag):
		out <- StreamLine{Type: "data", Line: `{"message_type":"summary","snapshot_id":"dddddddd44444444"}`}
		return sufnExit(r.dbExitCode)
	case args[0] == "backup":
		for _, p := range r.unreadable {
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(
				`{"message_type":"error","error":{"message":"%s: open %s: permission denied"},"during":"archival","item":%q}`, p, p, p)}
		}
		if !r.noSummary {
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(
				`{"message_type":"summary","files_new":1,"data_added":10,"snapshot_id":%q}`, sufnSnapshotID)}
		}
		if r.exitCode == 3 {
			out <- StreamLine{Type: "data", Line: `{"message_type":"exit_error","code":3,"message":"Warning: at least one source file could not be read"}`}
		}
		return sufnExit(r.exitCode)
	case args[0] == "ls":
		out <- StreamLine{Type: "data", Line: "/opt/stacks/" + sufnStack}
		return nil
	}
	return nil
}

func (r *sufnRunner) Output(_ context.Context, _ string, _ []string, _ []string) ([]byte, error) {
	return snapshotJSON(sufnSnapshotID, sufnSnapshotID[:8], sufnStack), nil
}

func sufnPaths(n int) []string {
	paths := make([]string, n)
	for i := range n {
		paths[i] = fmt.Sprintf("/opt/stacks/%s/locked-%02d.db", sufnStack, i)
	}
	return paths
}

// sufnRun runs one backup of sufnStack and returns the stored run and its one
// stack item.
func sufnRun(t *testing.T, runner *sufnRunner) (status, reason string, okCount, failedCount int, itemStatus, itemSnapshot, itemNote string) {
	t.Helper()
	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, runner, runner)
	seedStack(t, db, sufnStack, "hot")

	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)

	stored, items := readRun(t, db, run.ID)
	require.Len(t, items, 1)
	return stored.Status, stored.ErrorMessage, stored.StacksOK, stored.StacksFailed,
		items[0].Status, items[0].SnapshotID, items[0].ErrorMessage
}

// TestRunBackup_Exit3WithSnapshotIsPartial: the snapshot exists, so the stack
// keeps it, verifies it, applies retention and names what it could not read.
func TestRunBackup_Exit3WithSnapshotIsPartial(t *testing.T) {
	t.Parallel()

	paths := sufnPaths(2)
	runner := &sufnRunner{exitCode: 3, unreadable: paths}
	status, reason, ok, failed, itemStatus, itemSnapshot, itemNote := sufnRun(t, runner)

	assert.Equal(t, "success", itemStatus, "the snapshot was written; the item note: %q", itemNote)
	assert.Equal(t, sufnSnapshotID[:8], itemSnapshot, "the item must record the snapshot restic reported")
	for _, p := range paths {
		assert.Contains(t, itemNote, p, "the item must name every unreadable file")
	}
	assert.Contains(t, itemNote, "2 unreadable file(s)")

	assert.Equal(t, 1, runner.callsOf("ls", ""), "a partial snapshot is verified like a complete one")
	assert.Equal(t, 1, runner.callsOf("forget", sufnStack), "a partial snapshot runs the stack's retention")

	assert.Equal(t, "partial", status, "a run with unreadable files must not read as success")
	assert.Equal(t, 1, ok)
	assert.Equal(t, 0, failed)
	assert.Contains(t, reason, sufnStack)
	assert.Contains(t, reason, "could not be read")
}

// TestRunBackup_Exit3PartialListsAtMost20Paths: the paths are file names from
// the stack's directory; the note names the first 20 and counts the rest.
func TestRunBackup_Exit3PartialListsAtMost20Paths(t *testing.T) {
	t.Parallel()

	paths := sufnPaths(25)
	_, _, _, _, itemStatus, _, itemNote := sufnRun(t, &sufnRunner{exitCode: 3, unreadable: paths})

	assert.Equal(t, "success", itemStatus, "item note: %q", itemNote)
	assert.Contains(t, itemNote, "25 unreadable file(s)")
	for _, p := range paths[:20] {
		assert.Contains(t, itemNote, p)
	}
	for _, p := range paths[20:] {
		assert.NotContains(t, itemNote, p, "past the cap, paths are counted, not listed")
	}
	assert.Contains(t, itemNote, "and 5 more")
}

// TestRunBackup_Exit3FailuresStayFailed is the other side of the same
// instrument: every exit that is not "3 with a reported snapshot" fails the
// stack exactly as before, and skips its retention.
func TestRunBackup_Exit3FailuresStayFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		runner *sufnRunner
	}{
		{"exit 1 with a summary", &sufnRunner{exitCode: 1, unreadable: sufnPaths(1)}},
		{"exit 2 with a summary", &sufnRunner{exitCode: 2}},
		{"exit 3 without a snapshot id", &sufnRunner{exitCode: 3, noSummary: true, unreadable: sufnPaths(1)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, _, ok, failed, itemStatus, itemSnapshot, itemNote := sufnRun(t, tc.runner)

			assert.Equal(t, "failed", itemStatus)
			assert.Empty(t, itemSnapshot)
			assert.Contains(t, itemNote, "restic backup")
			assert.Equal(t, 0, tc.runner.callsOf("ls", ""))
			assert.Equal(t, 0, tc.runner.callsOf("forget", sufnStack))
			assert.Equal(t, "failed", status)
			assert.Equal(t, 0, ok)
			assert.Equal(t, 1, failed)
		})
	}
}

// TestRunBackup_Exit0WithoutPartialUnchanged: a clean backup carries no
// partial note and the run stays a success.
func TestRunBackup_Exit0WithoutPartialUnchanged(t *testing.T) {
	t.Parallel()

	runner := &sufnRunner{}
	status, reason, ok, failed, itemStatus, itemSnapshot, itemNote := sufnRun(t, runner)

	assert.Equal(t, "success", itemStatus)
	assert.Equal(t, sufnSnapshotID[:8], itemSnapshot)
	assert.Empty(t, itemNote)
	assert.Equal(t, 1, runner.callsOf("forget", sufnStack))
	assert.Equal(t, "success", status)
	assert.Empty(t, reason)
	assert.Equal(t, 1, ok)
	assert.Equal(t, 0, failed)
}

// TestRunBackup_DatabaseExit3StaysFailed: the database snapshot backs up one
// staged file, so exit 3 there means the database itself was not read. It
// stays a failed database snapshot, and its retention does not run.
func TestRunBackup_DatabaseExit3StaysFailed(t *testing.T) {
	t.Parallel()

	runner := &sufnRunner{dbExitCode: 3}
	status, reason, _, _, itemStatus, _, _ := sufnRun(t, runner)

	assert.Equal(t, "success", itemStatus)
	assert.Equal(t, "partial", status)
	assert.True(t, strings.HasPrefix(reason, "database snapshot failed: "), "got %q", reason)
	assert.Equal(t, 0, runner.callsOf("forget", DatabaseBackupTag))
}
