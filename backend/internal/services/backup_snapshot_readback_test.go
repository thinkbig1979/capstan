package services

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-qyg7.2 — THE FAIL-FIRST ARM FOR backupStack's snapshot read-back.
//
// THE DEFECT. The pre-fix line was
//
//	if snaps, listErr := restic.ListSnapshots(ctx, stackID, 1); listErr == nil && len(snaps) > 0 {
//	    snapshotID = snaps[0].ShortID
//	}
//
// and four lines later `s.recordItem(runID, stackID, "success", snapshotID, ...)`.
// A ListSnapshots that FAILED and a repository that genuinely holds NO
// snapshots both leave snapshotID empty and both record the run item as
// "success" -- so a snapshot that EXISTS but could not be read back is
// indistinguishable, in the run history and in the stream, from one that was
// never taken. The error was the only thing that could have told them apart
// and it was dropped at the assignment.
//
// WHY BOTH ARMS. Asserting only that a warning appears on the failure arm
// cannot tell this fix from one that warns unconditionally, which would be a
// different and worse bug -- every clean backup of an empty repository would
// grow a spurious error line. So the empty-repository arm asserts the warning
// is ABSENT, and the two arms together pin the discrimination rather than the
// message.
//
// The backup itself SUCCEEDS in both arms and the run item stays "success",
// which is deliberate: restic.Backup returned nil, so the data is on disk. The
// fix makes the read-back failure visible; it does not turn a successful backup
// into a failed one.

// qyg72ResticRunner routes by the restic subcommand, which is args[0]:
//
//	Run    "backup"    -> Backup
//	Run    "ls"        -> Verify's listing of the snapshot it found
//	Run    "forget"    -> ApplyRetention
//	Output "snapshots" -> ListSnapshots, the call under test
//
// NOTE, because it shapes what the arms can assert: ResticManager.Verify
// (backup_restic.go:479) calls ListSnapshots itself, so the SAME Output
// handler answers Verify's list and the read-back. Both arms therefore also
// produce a "verify warning" line. That is pre-existing behaviour and the
// assertions below key on the read-back's own message, never on the count of
// error lines.
type qyg72ResticRunner struct {
	snapshotsOut []byte
	snapshotsErr error
	outputCalls  int
}

func (r *qyg72ResticRunner) Run(_ context.Context, _ string, args []string, _ []string, _ chan<- StreamLine) error {
	if len(args) == 0 {
		return errors.New("qyg7.2 runner: empty restic argv")
	}
	switch args[0] {
	case "backup", "ls", "forget":
		return nil
	default:
		return errors.New("qyg7.2 runner: unexpected restic subcommand " + args[0])
	}
}

func (r *qyg72ResticRunner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	if len(args) == 0 || args[0] != "snapshots" {
		return nil, errors.New("qyg7.2 runner: unexpected restic Output subcommand")
	}
	r.outputCalls++
	return r.snapshotsOut, r.snapshotsErr
}

// qyg72BackupOneStack runs backupStack for one stack with the given
// snapshots-listing behaviour and returns every stream line it emitted plus the
// run item it recorded.
func qyg72BackupOneStack(t *testing.T, runner *qyg72ResticRunner) ([]StreamLine, models.BackupRunItem) {
	t.Helper()

	db := newBackupTestDB(t)
	stackID := "qyg72-readback"
	seedStack(t, db, stackID, "hot")

	docker := &fakeDocker{statusStr: "stopped"}
	svc := buildSvc(t, db, docker, runner, runner)

	runID := uuid.New().String()
	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID:        runID,
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}))

	// Buffered generously: stream() DROPS lines when the channel is full, so a
	// small buffer would make an absent warning mean "buffer overflowed"
	// rather than "the code did not emit it" -- a false green on arm B.
	out := make(chan StreamLine, 512)
	restic := newResticManagerWithRunner(BackupConfig{
		ResticRepository: "/tmp/qyg72-repo",
		ResticPassword:   "qyg72-password",
	}, runner, nil)

	_, err := svc.backupStack(context.Background(), restic, stackID, "hot", false, runID, out)
	require.NoError(t, err, "the backup itself must succeed in both arms; only the read-back differs")
	close(out)

	var lines []StreamLine
	for l := range out {
		lines = append(lines, l)
	}
	require.Less(t, len(lines), 512, "stream buffer filled; an absent line would be unprovable")

	items, itemErr := db.GetBackupRunItems(runID)
	require.NoError(t, itemErr)
	require.Len(t, items, 1, "backupStack must record exactly one run item")
	return lines, items[0]
}

// qyg72ReadBackWarning returns the read-back warning lines, keyed on the
// message the fix emits rather than on the level alone -- Verify's own warning
// is also an "error" line in both arms.
func qyg72ReadBackWarning(lines []StreamLine) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l.Line, "snapshot id unavailable") {
			out = append(out, l.Line)
		}
	}
	return out
}

func TestBackupStack_SnapshotReadBackFaultIsStreamed(t *testing.T) {
	t.Run("read-back fails: the fault is streamed and the run still succeeds", func(t *testing.T) {
		runner := &qyg72ResticRunner{snapshotsErr: errors.New("repository is locked by another process")}
		lines, item := qyg72BackupOneStack(t, runner)

		require.Positive(t, runner.outputCalls, "ListSnapshots was never called; this arm measures nothing")

		warnings := qyg72ReadBackWarning(lines)
		if len(warnings) == 0 {
			t.Fatalf("a failed snapshot read-back emitted no warning, so it is indistinguishable "+
				"from an empty repository: the run recorded status=%q snapshotID=%q and said nothing. "+
				"Stream lines were: %v", item.Status, item.SnapshotID, qyg72Lines(lines))
		}
		require.Contains(t, warnings[0], "repository is locked by another process",
			"the warning must carry the underlying cause, not just announce that something failed")

		require.Equal(t, "success", item.Status, "the backup succeeded; only the read-back failed")
		require.Empty(t, item.SnapshotID)
	})

	t.Run("repository is genuinely empty: no warning", func(t *testing.T) {
		// "null" is what restic prints for an empty repository, handled
		// explicitly at backup_restic.go's ListSnapshots.
		runner := &qyg72ResticRunner{snapshotsOut: []byte("null")}
		lines, item := qyg72BackupOneStack(t, runner)

		require.Positive(t, runner.outputCalls, "ListSnapshots was never called; this arm measures nothing")

		warnings := qyg72ReadBackWarning(lines)
		require.Empty(t, warnings,
			"an empty repository is not a fault and must not be reported as one; "+
				"a warning here means the fix warns unconditionally")

		require.Equal(t, "success", item.Status)
		require.Empty(t, item.SnapshotID)
	})
}

func qyg72Lines(lines []StreamLine) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Type+": "+l.Line)
	}
	return out
}
