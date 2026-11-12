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

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-sufn: restic exits 3 when it wrote the snapshot but could not read
// some source files. OBSERVED with restic 0.18.0 (`restic backup --json` over
// absolute paths, as Capstan passes them):
//   - unreadable files: exit 3, the summary with its snapshot_id, one "error"
//     message per file with "during":"archival" and the path in "item";
//   - an unreadable directory under the source: the same item twice, once
//     "during":"scan" and once "during":"archival";
//   - the source directory itself unreadable: exit 3, a snapshot_id, and
//     total_files_processed 0, a snapshot that holds nothing.
//
// Any non-nil error used to mark the stack failed and skip verify and
// retention, so a stack with one permanently unreadable file failed every
// night and its snapshots were never pruned.

const (
	sufnStack      = "sufn-app"
	sufnSource     = "/opt/stacks/" + sufnStack // seedStack's directory
	sufnSnapshotID = "cccccccc33333333333333333333333333333333333333333333333333333333"
)

// sufnErr is one restic "error" message: the item and the phase it came from.
type sufnErr struct{ item, during string }

// sufnRunner plays restic for one stack whose backup exits with exitCode after
// reporting errs. The database backup always succeeds unless dbExitCode is set.
type sufnRunner struct {
	exitCode    int
	noSummary   bool
	noFilesRead bool // the summary says total_files_processed 0
	errs        []sufnErr
	dbExitCode  int

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
		out <- StreamLine{Type: "data", Line: `{"message_type":"summary","total_files_processed":1,"snapshot_id":"dddddddd44444444"}`}
		return sufnExit(r.dbExitCode)
	case args[0] == "backup":
		for _, e := range r.errs {
			during := ""
			if e.during != "" {
				during = fmt.Sprintf(`"during":%q,`, e.during)
			}
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(
				`{"message_type":"error","error":{"message":%q},%s"item":%q}`,
				e.item+": permission denied", during, e.item)}
		}
		if !r.noSummary {
			files := 1
			if r.noFilesRead {
				files = 0
			}
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(
				`{"message_type":"summary","files_new":%d,"data_added":10,"total_files_processed":%d,"snapshot_id":%q}`,
				files, files, sufnSnapshotID)}
		}
		if r.exitCode == 3 {
			out <- StreamLine{Type: "data", Line: `{"message_type":"exit_error","code":3,"message":"Warning: at least one source file could not be read"}`}
		}
		return sufnExit(r.exitCode)
	case args[0] == "ls":
		out <- StreamLine{Type: "data", Line: sufnSource}
		return nil
	}
	return nil
}

// sufnTaggedID is the id `restic tag` gives sufnSnapshotID (agent-os-95dk).
const sufnTaggedID = "eeeeeeee55555555555555555555555555555555555555555555555555555555"

// Output plays the listing TagPartial reads (the snapshot rewritten under
// sufnTaggedID, recording its original) and the forget dry-run retention
// reads after a partial backup (that snapshot alone, kept).
func (r *sufnRunner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, slices.Clone(args))
	r.mu.Unlock()
	tagged := fmt.Sprintf(`{"id":%q,"short_id":%q,"time":"2026-11-01T02:00:00Z","tags":[%q,%q],"paths":[%q],"original":%q}`,
		sufnTaggedID, sufnTaggedID[:8], sufnStack, PartialSnapshotTag, sufnSource, sufnSnapshotID)
	switch {
	case args[0] == "snapshots" && slices.Contains(args, "--no-lock") && argPairContains(args, "--tag", sufnStack) && slices.Contains(args, "--json"):
		return []byte("[" + tagged + "]"), nil
	case args[0] == "forget":
		return []byte(`[{"keep":[` + tagged + `],"remove":[]}]`), nil
	}
	return snapshotJSON(sufnSnapshotID, sufnSnapshotID[:8], sufnStack), nil
}

// sufnFiles is n unreadable files under the source, as restic reports them.
func sufnFiles(n int) []sufnErr {
	errs := make([]sufnErr, n)
	for i := range n {
		errs[i] = sufnErr{fmt.Sprintf("%s/locked-%02d.db", sufnSource, i), "archival"}
	}
	return errs
}

// sufnDir is an unreadable directory, reported at scan and again at archival.
func sufnDir(path string) []sufnErr {
	return []sufnErr{{path, "scan"}, {path, "archival"}}
}

type sufnResult struct {
	status, reason       string
	ok, failed           int
	item                 models.BackupRunItem
	stopCalls, startCall int
}

// sufnRun runs one backup of sufnStack under the hot policy.
func sufnRun(t *testing.T, runner *sufnRunner) sufnResult {
	t.Helper()
	return sufnRunPolicy(t, runner, "hot")
}

func sufnRunPolicy(t *testing.T, runner *sufnRunner, stopPolicy string) sufnResult {
	t.Helper()
	db := newBackupTestDB(t)
	docker := &fakeDocker{statusStr: "running"}
	svc := buildSvc(t, db, docker, runner, runner)
	seedStack(t, db, sufnStack, stopPolicy)

	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)

	stored, items := readRun(t, db, run.ID)
	require.Len(t, items, 1)
	docker.mu.Lock()
	defer docker.mu.Unlock()
	return sufnResult{
		status: stored.Status, reason: stored.ErrorMessage,
		ok: stored.StacksOK, failed: stored.StacksFailed,
		item:      items[0],
		stopCalls: len(docker.stopCalls), startCall: len(docker.startCalls),
	}
}

// assertPartial checks the shape every partial stack shares: the snapshot is
// kept, verified and retained, and the run says some files were not read.
func assertPartial(t *testing.T, runner *sufnRunner, r sufnResult) {
	t.Helper()
	assert.Equal(t, "success", r.item.Status, "the snapshot was written; the item note: %q", r.item.ErrorMessage)
	assert.Equal(t, sufnTaggedID[:8], r.item.SnapshotID, "the item must record the snapshot restic reported, under the id its partial mark gave it (agent-os-95dk)")
	assert.Equal(t, 1, runner.callsOf("ls", ""), "a partial snapshot is verified like a complete one")
	assert.Equal(t, 1, runner.callsOf("tag", ""), "a partial snapshot is marked "+PartialSnapshotTag)
	assert.Equal(t, 1, runner.callsOf("forget", sufnStack), "a partial snapshot runs the stack's retention (its dry-run)")
	assert.NotContains(t, r.item.ErrorMessage, "retention skipped")
	assert.Equal(t, "partial", r.status, "a run with unreadable files must not read as success")
	assert.Equal(t, 1, r.ok)
	assert.Equal(t, 0, r.failed)
	assert.Contains(t, r.reason, sufnStack)
	assert.Contains(t, r.reason, "could not be read")
}

// TestRunBackup_Exit3WithSnapshotIsPartial: the snapshot exists, so the stack
// keeps it, verifies it, applies retention and names what it could not read.
func TestRunBackup_Exit3WithSnapshotIsPartial(t *testing.T) {
	t.Parallel()

	errs := sufnFiles(2)
	runner := &sufnRunner{exitCode: 3, errs: errs}
	r := sufnRun(t, runner)

	assertPartial(t, runner, r)
	for _, e := range errs {
		assert.Contains(t, r.item.ErrorMessage, fmt.Sprintf("%q", e.item), "the item must name every unreadable path")
	}
	assert.Contains(t, r.item.ErrorMessage, "2 unreadable path(s)")
}

// TestRunBackup_Exit3PartialCountsADirectoryOnce: restic reports an
// unreadable directory at scan and again at archival; it is one path. An
// error with no phase (an older restic) still counts.
func TestRunBackup_Exit3PartialCountsADirectoryOnce(t *testing.T) {
	t.Parallel()

	dir := sufnSource + "/data/locked-dir"
	errs := append(sufnDir(dir), sufnErr{sufnSource + "/no-phase.db", ""})
	runner := &sufnRunner{exitCode: 3, errs: errs}
	r := sufnRun(t, runner)

	assertPartial(t, runner, r)
	assert.Contains(t, r.item.ErrorMessage, "2 unreadable path(s)")
	assert.Equal(t, 1, strings.Count(r.item.ErrorMessage, fmt.Sprintf("%q", dir)), "note: %q", r.item.ErrorMessage)
	assert.Contains(t, r.item.ErrorMessage, fmt.Sprintf("%q", sufnSource+"/no-phase.db"))
}

// TestRunBackup_Exit3PartialListsAtMost20Paths: the paths are file names from
// the stack's directory; the note names the first 20 and counts the rest.
func TestRunBackup_Exit3PartialListsAtMost20Paths(t *testing.T) {
	t.Parallel()

	errs := sufnFiles(25)
	r := sufnRun(t, &sufnRunner{exitCode: 3, errs: errs})

	assert.Equal(t, "success", r.item.Status, "item note: %q", r.item.ErrorMessage)
	assert.Contains(t, r.item.ErrorMessage, "25 unreadable path(s)")
	for _, e := range errs[:20] {
		assert.Contains(t, r.item.ErrorMessage, e.item)
	}
	for _, e := range errs[20:] {
		assert.NotContains(t, r.item.ErrorMessage, e.item, "past the cap, paths are counted, not listed")
	}
	assert.Contains(t, r.item.ErrorMessage, "and 5 more")
}

// TestRunBackup_Exit3PartialQuotesAndTruncatesPaths: a ", " in a file name
// cannot blur the list, and a long path cannot bloat the note.
func TestRunBackup_Exit3PartialQuotesAndTruncatesPaths(t *testing.T) {
	t.Parallel()

	comma := sufnSource + "/a, b.txt"
	long := sufnSource + "/" + strings.Repeat("x", 400)
	r := sufnRun(t, &sufnRunner{exitCode: 3, errs: []sufnErr{{comma, "archival"}, {long, "archival"}}})

	assert.Equal(t, "success", r.item.Status, "item note: %q", r.item.ErrorMessage)
	assert.Contains(t, r.item.ErrorMessage, fmt.Sprintf("%q", comma))
	assert.Contains(t, r.item.ErrorMessage, fmt.Sprintf("%q", long[:200]+"..."))
	assert.NotContains(t, r.item.ErrorMessage, long[:201])
}

// TestRunBackup_Exit3NamedNoneIsPartial: exit 3 with files read and no error
// line naming a path is still a partial snapshot, and says so.
func TestRunBackup_Exit3NamedNoneIsPartial(t *testing.T) {
	t.Parallel()

	runner := &sufnRunner{exitCode: 3}
	r := sufnRun(t, runner)

	assertPartial(t, runner, r)
	assert.Contains(t, r.item.ErrorMessage, "named none")
}

// TestRunBackup_Exit3PartialWithStopPolicyRestarts: a partial backup under the
// stop policy still brings the stack back.
func TestRunBackup_Exit3PartialWithStopPolicyRestarts(t *testing.T) {
	t.Parallel()

	runner := &sufnRunner{exitCode: 3, errs: sufnFiles(1)}
	r := sufnRunPolicy(t, runner, "stop")

	assertPartial(t, runner, r)
	assert.Equal(t, 1, r.stopCalls)
	assert.Equal(t, 1, r.startCall, "the defensive restart must fire after a partial backup")
}

// TestRunBackup_Exit3FailuresStayFailed is the other side of the same
// instrument: every exit that is not "3 with a usable snapshot" fails the
// stack exactly as before, and skips verify and retention.
func TestRunBackup_Exit3FailuresStayFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		runner *sufnRunner
	}{
		{"exit 1 with a summary", &sufnRunner{exitCode: 1, errs: sufnFiles(1)}},
		{"exit 2 with a summary", &sufnRunner{exitCode: 2}},
		{"exit 3 without a snapshot id", &sufnRunner{exitCode: 3, noSummary: true, errs: sufnFiles(1)}},
		// Each guard alone: no file read, then the source directory itself
		// unreadable although the summary counts files.
		{"exit 3 with a snapshot id but no file read", &sufnRunner{exitCode: 3, noFilesRead: true, errs: sufnFiles(1)}},
		{"exit 3 with the source directory unreadable", &sufnRunner{exitCode: 3, errs: sufnDir(sufnSource)}},
		// What restic 0.18.0 actually prints for a chmod-000 source.
		{"exit 3 with an unreadable source and nothing read", &sufnRunner{exitCode: 3, noFilesRead: true, errs: sufnDir(sufnSource)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := sufnRun(t, tc.runner)

			assert.Equal(t, "failed", r.item.Status)
			assert.Empty(t, r.item.SnapshotID)
			assert.Contains(t, r.item.ErrorMessage, "restic backup")
			assert.Equal(t, 0, tc.runner.callsOf("ls", ""))
			assert.Equal(t, 0, tc.runner.callsOf("forget", sufnStack))
			assert.Equal(t, "failed", r.status)
			assert.Equal(t, 0, r.ok)
			assert.Equal(t, 1, r.failed)
		})
	}
}

// TestRunBackup_Exit0WithoutPartialUnchanged: a clean backup carries no
// partial note and the run stays a success.
func TestRunBackup_Exit0WithoutPartialUnchanged(t *testing.T) {
	t.Parallel()

	runner := &sufnRunner{}
	r := sufnRun(t, runner)

	assert.Equal(t, "success", r.item.Status)
	assert.Equal(t, sufnSnapshotID[:8], r.item.SnapshotID)
	assert.Empty(t, r.item.ErrorMessage)
	assert.Equal(t, 1, runner.callsOf("forget", sufnStack))
	assert.Equal(t, "success", r.status)
	assert.Empty(t, r.reason)
	assert.Equal(t, 1, r.ok)
	assert.Equal(t, 0, r.failed)
}

// TestRunBackup_DatabaseExit3StaysFailed: the database snapshot backs up one
// staged file, so exit 3 there means the database itself was not read. It
// stays a failed database snapshot, and its retention does not run.
func TestRunBackup_DatabaseExit3StaysFailed(t *testing.T) {
	t.Parallel()

	runner := &sufnRunner{dbExitCode: 3}
	r := sufnRun(t, runner)

	assert.Equal(t, "success", r.item.Status)
	assert.Equal(t, "partial", r.status)
	assert.True(t, strings.HasPrefix(r.reason, "database snapshot failed: "), "got %q", r.reason)
	assert.Equal(t, 0, runner.callsOf("forget", DatabaseBackupTag))
}
