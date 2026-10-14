package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-wdlf: the backup host used to default to os.Hostname(), which in a
// container is the container id and changes on every redeploy. restic's
// `snapshots --latest 1` returns the latest snapshot PER host and path, oldest
// group first (restic 0.18.0, OBSERVED with two hosts: [oldhost, newhost]), so
// after a redeploy element [0] was the PREVIOUS deploy's snapshot. backupStack
// recorded that id in the run item and Verify listed that snapshot, while the
// one just written went unrecorded and unverified (prod 2026-10-14: run
// def02076 saved fca9c8d4 and recorded 2874b3df).

const (
	wdlfStack   = "wdlf-app"
	wdlfOldID   = "aaaaaaaa11111111111111111111111111111111111111111111111111111111"
	wdlfNewID   = "bbbbbbbb22222222222222222222222222222222222222222222222222222222"
	wdlfOldHost = "bf0d6d0d915c"
	wdlfNewHost = "4ddf23a207a9"
)

// wdlfRunner answers like a repository holding one snapshot of the stack per
// container host: `snapshots --latest 1` lists [old host, new host], and
// `backup` reports the new snapshot in its JSON summary.
type wdlfRunner struct {
	mu    sync.Mutex
	calls [][]string

	lsLines      []string // printed by `restic ls`
	lsErr        error
	noSummary    bool // `restic backup` prints no summary line
	failDBForget bool // `restic forget --tag capstan-database` fails
}

func (r *wdlfRunner) record(args []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slices.Clone(args))
}

func (r *wdlfRunner) callsOf(sub string) [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out [][]string
	for _, c := range r.calls {
		if len(c) > 0 && c[0] == sub {
			out = append(out, c)
		}
	}
	return out
}

func (r *wdlfRunner) Run(_ context.Context, _ string, args []string, _ []string, out chan<- StreamLine) error {
	r.record(args)
	switch args[0] {
	case "backup":
		if !r.noSummary {
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(
				`{"message_type":"summary","files_new":1,"data_added":10,"snapshot_id":%q}`, wdlfNewID)}
		}
		return nil
	case "ls":
		for _, l := range r.lsLines {
			out <- StreamLine{Type: "data", Line: l}
		}
		return r.lsErr
	case "forget":
		if r.failDBForget && argPairContains(args, "--tag", DatabaseBackupTag) {
			return errors.New("forget refused")
		}
		return nil
	}
	return errors.New("wdlf runner: unexpected restic subcommand " + args[0])
}

func (r *wdlfRunner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	r.record(args)
	if args[0] != "snapshots" {
		return nil, errors.New("wdlf runner: unexpected Output subcommand " + args[0])
	}
	snaps := []resticSnapshot{
		{ID: wdlfOldID, ShortID: wdlfOldID[:8], Time: "2026-10-11T00:00:00Z", Host: wdlfOldHost, Tags: []string{wdlfStack}, Paths: []string{"/opt/stacks/" + wdlfStack}},
		{ID: wdlfNewID, ShortID: wdlfNewID[:8], Time: "2026-10-14T00:00:00Z", Host: wdlfNewHost, Tags: []string{wdlfStack}, Paths: []string{"/opt/stacks/" + wdlfStack}},
	}
	return json.Marshal(snaps)
}

// wdlfBackupOneStack runs backupStack once and returns its stream lines and
// the run item it recorded.
func wdlfBackupOneStack(t *testing.T, runner *wdlfRunner) ([]StreamLine, models.BackupRunItem, stackResult) {
	t.Helper()

	db := newBackupTestDB(t)
	seedStack(t, db, wdlfStack, "hot")
	svc := buildSvc(t, db, &fakeDocker{statusStr: "stopped"}, runner, runner)

	runID := uuid.New().String()
	require.NoError(t, db.CreateBackupRun(&models.BackupRun{
		ID: runID, Kind: "backup", Trigger: "manual", Status: "running",
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}))

	// Large enough that stream() never drops a line: an absent file name must
	// mean "not emitted", not "buffer full".
	out := make(chan StreamLine, 4096)
	restic := newResticManagerWithRunner(BackupConfig{
		ResticRepository: "/tmp/wdlf-repo",
		ResticPassword:   "wdlf-password",
		KeepDaily:        7,
		BackupHostname:   DefaultBackupHostname,
	}, runner, nil)

	res, err := svc.backupStack(context.Background(), restic, wdlfStack, "hot", false, runID, out)
	require.NoError(t, err)
	close(out)
	var lines []StreamLine
	for l := range out {
		lines = append(lines, l)
	}
	require.Less(t, len(lines), 4096, "stream buffer filled; absence would be unprovable")

	items, err := db.GetBackupRunItems(runID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	return lines, items[0], res
}

func TestBackupStack_RecordsAndVerifiesTheSnapshotItTook(t *testing.T) {
	t.Parallel()

	runner := &wdlfRunner{lsLines: []string{"snapshot bbbbbbbb of [/opt/stacks/wdlf-app]:", "/opt/stacks/wdlf-app"}}
	_, item, _ := wdlfBackupOneStack(t, runner)

	assert.Equal(t, wdlfNewID[:8], item.SnapshotID,
		"the run item must record the snapshot this backup wrote, not the latest of an earlier host group")

	ls := runner.callsOf("ls")
	require.Len(t, ls, 1, "verify must list exactly one snapshot")
	assert.True(t, slices.ContainsFunc(ls[0], func(a string) bool { return strings.HasPrefix(a, wdlfNewID[:8]) }),
		"verify must list the snapshot this backup wrote, got argv %v", ls[0])
	assert.False(t, slices.ContainsFunc(ls[0], func(a string) bool { return strings.HasPrefix(a, wdlfOldID[:8]) }),
		"verify listed the earlier host's snapshot, argv %v", ls[0])
}

// TestBackupStack_VerifyReportsOneLine: verify used to stream every file name
// of the snapshot into the run output, which the scheduler logs line by line
// (1476 lines, document names included, in one prod run).
func TestBackupStack_VerifyReportsOneLine(t *testing.T) {
	t.Parallel()

	lsLines := []string{"snapshot bbbbbbbb of [/opt/stacks/wdlf-app] at 2026-10-14 by root@capstan:"}
	for i := range 1000 {
		lsLines = append(lsLines, fmt.Sprintf("/opt/stacks/wdlf-app/private-file-%04d.pdf", i))
	}
	lines, _, _ := wdlfBackupOneStack(t, &wdlfRunner{lsLines: lsLines})

	var verified []string
	leaked := 0
	for _, l := range lines {
		if strings.Contains(l.Line, "private-file-") {
			leaked++
		}
		if strings.Contains(l.Line, "verified snapshot") {
			verified = append(verified, l.Line)
		}
	}
	assert.Zero(t, leaked, "file names reached the run output")
	require.Len(t, verified, 1, "verify must report exactly one result line")
	assert.Contains(t, verified[0], "1000 entries")
	assert.Contains(t, verified[0], wdlfNewID[:8])
}

// agent-os-3st7: a snapshot whose verify failed, or that could not be
// identified so could not be verified, read green at run level.
func TestBackupStack_UnverifiedSnapshotIsFlagged(t *testing.T) {
	t.Parallel()

	t.Run("verify fails", func(t *testing.T) {
		t.Parallel()
		_, item, res := wdlfBackupOneStack(t, &wdlfRunner{lsErr: errors.New("pack file missing")})
		assert.True(t, res.verifyFailed)
		assert.Equal(t, "success", item.Status, "the snapshot exists; the item stays success")
		assert.Contains(t, item.ErrorMessage, "pack file missing")
	})
	t.Run("restic printed no snapshot id", func(t *testing.T) {
		t.Parallel()
		runner := &wdlfRunner{noSummary: true}
		_, item, res := wdlfBackupOneStack(t, runner)
		assert.True(t, res.verifyFailed)
		assert.Empty(t, item.SnapshotID, "no id must be invented from a re-listing")
		assert.Contains(t, item.ErrorMessage, "no snapshot id")
		assert.Empty(t, runner.callsOf("ls"), "nothing to verify without an id")
	})
	t.Run("control: verify passes", func(t *testing.T) {
		t.Parallel()
		_, item, res := wdlfBackupOneStack(t, &wdlfRunner{})
		assert.False(t, res.verifyFailed)
		assert.Empty(t, item.ErrorMessage)
	})
}

// TestResticManager_ApplyRetention_CoversEveryHostOfTheStack: forget used to
// pass `--hostname <current host>`, so every snapshot taken under an earlier
// container id was outside retention forever. It must group by path only,
// scoped by the stack tag. Not by tags: every snapshot carries a date tag, so
// grouping by tags makes one group per day and keeps them all (OBSERVED,
// restic 0.18.0 dry-run).
func TestResticManager_ApplyRetention_CoversEveryHostOfTheStack(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)
	out, stop := drainVerifyLines()
	require.NoError(t, m.ApplyRetention(context.Background(), wdlfStack, out))
	stop()

	args := runner.lastCall().Args
	assert.True(t, argPairContains(args, "--tag", wdlfStack), "argv %v", args)
	assert.True(t, argPairContains(args, "--group-by", "paths"), "argv %v", args)
	assert.False(t, argContains(args, "--host") || argContains(args, "--hostname"),
		"a host filter leaves earlier hosts' snapshots out of retention: %v", args)
}

// The database snapshot (agent-os-wdlf scope addition) was never passed to
// retention: one copy per run, kept forever.
func TestRunBackup_DatabaseSnapshotGetsRetention(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	runner := &wdlfRunner{}
	svc := buildSvc(t, db, &fakeDocker{}, runner, runner)

	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)

	var dbForget [][]string
	for _, c := range runner.callsOf("forget") {
		if argPairContains(c, "--tag", DatabaseBackupTag) {
			dbForget = append(dbForget, c)
		}
	}
	require.Len(t, dbForget, 1, "the database snapshot must go through retention once per run")
	assert.True(t, argPairContains(dbForget[0], "--group-by", "paths"), "argv %v", dbForget[0])

	stored, _ := readRun(t, db, run.ID)
	assert.Equal(t, "success", stored.Status)
}

func TestRunBackup_DatabaseRetentionFailureMakesRunPartial(t *testing.T) {
	t.Parallel()

	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{}, &wdlfRunner{failDBForget: true}, &wdlfRunner{})

	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)

	stored, _ := readRun(t, db, run.ID)
	assert.Equal(t, "partial", stored.Status)
	assert.Equal(t, "old database snapshots were not cleaned up: retention failed: forget refused", stored.ErrorMessage)
}
