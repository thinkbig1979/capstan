package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner records all calls made to it and returns configurable responses.
// It is shared between restic and rclone tests.
type fakeRunner struct {
	// calls accumulates each Run/Output invocation for assertion.
	calls []fakeCall

	// runErr, if non-nil, is returned from Run.
	runErr error

	// outputData is returned by Output.
	outputData []byte
	// outputErr is returned by Output.
	outputErr error

	// onRun is called before the result is returned, allowing tests to send
	// lines into out before Run returns.
	onRun func(name string, args []string, out chan<- StreamLine)
}

type fakeCall struct {
	Binary string
	Args   []string
	Env    []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	f.calls = append(f.calls, fakeCall{Binary: name, Args: args, Env: env})
	if f.onRun != nil {
		f.onRun(name, args, out)
	}
	return f.runErr
}

func (f *fakeRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	f.calls = append(f.calls, fakeCall{Binary: name, Args: args, Env: env})
	return f.outputData, f.outputErr
}

// lastCall returns the most recent recorded call.
func (f *fakeRunner) lastCall() fakeCall {
	if len(f.calls) == 0 {
		panic("fakeRunner: no calls recorded")
	}
	return f.calls[len(f.calls)-1]
}

// argContains returns true if s appears in the call's args slice.
func argContains(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// argPairContains returns true if flag immediately precedes value in args.
func argPairContains(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func testBackupConfig() BackupConfig {
	return BackupConfig{
		ResticRepository: "/tmp/test-repo",
		ResticPassword:   "supersecret",
		KeepDaily:        7,
		KeepWeekly:       4,
		KeepMonthly:      6,
		KeepYearly:       0,
		AutoPrune:        true,
		BackupHostname:   "test-host",
		RcloneRemote:     "myremote",
		RclonePath:       "backup/path",
		RcloneTransfers:  4,
	}
}

// --- Password file lifecycle tests ---

func TestResticManager_PasswordFile_CreatedWith0600(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	m := newResticManagerWithRunner(cfg, &fakeRunner{}, nil)

	pwFile, cleanup, err := m.withPasswordFile()
	require.NoError(t, err)
	defer cleanup()

	info, err := os.Stat(pwFile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "password file must be 0600")
}

func TestResticManager_PasswordFile_ContentsCorrect(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	m := newResticManagerWithRunner(cfg, &fakeRunner{}, nil)

	pwFile, cleanup, err := m.withPasswordFile()
	require.NoError(t, err)
	defer cleanup()

	//nolint:gosec // pwFile is a test fixture created by withPasswordFile() under the test's config DataDir (t.TempDir())
	data, err := os.ReadFile(pwFile)
	require.NoError(t, err)
	assert.Equal(t, cfg.ResticPassword, string(data))
}

func TestResticManager_PasswordFile_RemovedAfterCleanup(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	m := newResticManagerWithRunner(cfg, &fakeRunner{}, nil)

	pwFile, cleanup, err := m.withPasswordFile()
	require.NoError(t, err)
	cleanup()

	_, err = os.Stat(pwFile)
	assert.True(t, os.IsNotExist(err), "password file must be removed after cleanup")
}

func TestResticManager_PasswordFile_EnvUsedNotArgv(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()

	_, err := m.Backup(context.Background(), "/srv/stacks/mystack", []string{"mystack"}, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()

	// Password must NOT appear in argv.
	for _, a := range call.Args {
		assert.NotContains(t, a, cfg.ResticPassword, "password must not be in argv")
	}

	// RESTIC_PASSWORD_FILE must be in env; RESTIC_PASSWORD must not.
	hasPasswordFile := false
	for _, e := range call.Env {
		assert.False(t, strings.HasPrefix(e, "RESTIC_PASSWORD="), "raw RESTIC_PASSWORD must not be in env")
		if strings.HasPrefix(e, "RESTIC_PASSWORD_FILE=") {
			hasPasswordFile = true
		}
	}
	assert.True(t, hasPasswordFile, "RESTIC_PASSWORD_FILE must be set in env")
}

// --- Backup argv construction tests ---

func TestResticManager_Backup_RequiredFlags(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	_, err := m.Backup(context.Background(), "/data/stack1", []string{"stack1"}, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "restic", call.Binary)
	assert.Equal(t, "backup", call.Args[0])
	assert.True(t, argContains(call.Args, "--one-file-system"), "--one-file-system must be present")
	assert.True(t, argContains(call.Args, "--exclude-caches"), "--exclude-caches must be present")
	assert.True(t, argContains(call.Args, "/data/stack1"), "stack dir must be last arg")
}

func TestResticManager_Backup_Tags(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	stackID := "mystack-abc"
	out := make(chan StreamLine, 64)
	go func() {
		for range out {
		}
	}()
	_, err := m.Backup(context.Background(), "/data/mystack", []string{stackID}, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.True(t, argPairContains(call.Args, "--tag", stackID), "--tag <stackID> must be present")
	assert.True(t, argPairContains(call.Args, "--tag", "capstan-backup"), "--tag capstan-backup must be present")

	dateTag := time.Now().Format("2006-01-02")
	assert.True(t, argPairContains(call.Args, "--tag", dateTag), "--tag YYYY-MM-DD must be present")
}

func TestResticManager_Backup_Hostname(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	_, _ = m.Backup(context.Background(), "/srv/s", []string{"s"}, out)
	close(out)

	call := runner.lastCall()
	assert.True(t, argPairContains(call.Args, "--hostname", cfg.BackupHostname))
}

// --- ApplyRetention argv tests ---

func TestResticManager_ApplyRetention_Flags(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.ApplyRetention(context.Background(), "mystack", out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "forget", call.Args[0])
	assert.True(t, argPairContains(call.Args, "--keep-daily", "7"))
	assert.True(t, argPairContains(call.Args, "--keep-weekly", "4"))
	assert.True(t, argPairContains(call.Args, "--keep-monthly", "6"))
	assert.True(t, argContains(call.Args, "--prune"), "--prune must be present when AutoPrune=true")
}

func TestResticManager_ApplyRetention_NoRetentionSkips(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	cfg.KeepDaily = 0
	cfg.KeepWeekly = 0
	cfg.KeepMonthly = 0
	cfg.KeepYearly = 0
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.ApplyRetention(context.Background(), "tag", out)
	require.NoError(t, err) // should return nil, not an error
	close(out)

	// No call should have been made when there are no retention settings.
	assert.Empty(t, runner.calls)
}

// --- Prune argv tests ---

func TestResticManager_Prune_DryRun(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.Prune(context.Background(), true, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "prune", call.Args[0])
	assert.True(t, argContains(call.Args, "--dry-run"))
}

func TestResticManager_Prune_NoDryRun(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.Prune(context.Background(), false, out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.False(t, argContains(call.Args, "--dry-run"), "--dry-run must not be present when dryRun=false")
}

// --- ListSnapshots JSON parsing tests ---

func TestResticManager_ListSnapshots_ParsesJSON(t *testing.T) {
	t.Parallel()

	snaps := []resticSnapshot{
		{
			ID:      "abc123def456",
			ShortID: "abc123",
			Time:    "2026-05-30T10:00:00Z",
			Host:    "myhost",
			Tags:    []string{"mystack", "capstan-backup", "2026-05-30"},
			Paths:   []string{"/srv/stacks/mystack"},
		},
	}
	raw, _ := json.Marshal(snaps)

	runner := &fakeRunner{outputData: raw}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	result, err := m.ListSnapshots(context.Background(), "mystack", 1)
	require.NoError(t, err)
	require.Len(t, result, 1)

	s := result[0]
	assert.Equal(t, "abc123def456", s.ID)
	assert.Equal(t, "abc123", s.ShortID)
	assert.Equal(t, "2026-05-30T10:00:00Z", s.Time)
	assert.Equal(t, "myhost", s.Hostname)
	assert.Equal(t, []string{"mystack", "capstan-backup", "2026-05-30"}, s.Tags)
	assert.Equal(t, []string{"/srv/stacks/mystack"}, s.Paths)
}

func TestResticManager_ListSnapshots_NullResponse(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte("null")}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	result, err := m.ListSnapshots(context.Background(), "", 0)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestResticManager_ListSnapshots_EmptyResponse(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte("")}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	result, err := m.ListSnapshots(context.Background(), "", 0)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestResticManager_ListSnapshots_TagAndLimitArgs(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte("[]")}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	_, _ = m.ListSnapshots(context.Background(), "mytag", 5)

	call := runner.lastCall()
	assert.True(t, argPairContains(call.Args, "--tag", "mytag"))
	assert.True(t, argPairContains(call.Args, "--latest", "5"))
}

func TestResticManager_ListSnapshots_NoTagNoLimit(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputData: []byte("[]")}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	_, _ = m.ListSnapshots(context.Background(), "", 0)

	call := runner.lastCall()
	assert.False(t, argContains(call.Args, "--tag"), "--tag must not be present when tag is empty")
	assert.False(t, argContains(call.Args, "--latest"), "--latest must not be present when limit is 0")
}

// --- CheckRepository argv tests ---

func TestResticManager_CheckRepository_QuietFlag(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	_ = m.CheckRepository(context.Background())

	call := runner.lastCall()
	assert.Equal(t, "restic", call.Binary)
	assert.Equal(t, "snapshots", call.Args[0])
	assert.True(t, argContains(call.Args, "--quiet"))
}

func TestResticManager_CheckRepository_RepoInEnv(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	_ = m.CheckRepository(context.Background())

	call := runner.lastCall()
	hasRepo := false
	for _, e := range call.Env {
		if e == "RESTIC_REPOSITORY="+cfg.ResticRepository {
			hasRepo = true
		}
	}
	assert.True(t, hasRepo, "RESTIC_REPOSITORY must be set in env")
}

// --- Restore argv tests ---

func TestResticManager_Restore_Args(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.Restore(context.Background(), "abc123", "/orig/src", "/restore/here", out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "restore", call.Args[0])
	// The snapshot ref carries the stored source prefix so restic strips it.
	assert.Equal(t, "abc123:/orig/src", call.Args[1])
	assert.True(t, argPairContains(call.Args, "--target", "/restore/here"))
}

// --- Timeout handling test ---

func TestResticManager_Backup_ContextCancel(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		runErr: context.DeadlineExceeded,
	}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	_, err := m.Backup(ctx, "/data/s", []string{"s"}, out)
	close(out)

	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// --- EnsureRepository tests ---

func TestResticManager_EnsureRepository_InitsWhenCheckFails(t *testing.T) {
	t.Parallel()

	callCount := 0
	runner := &fakeRunner{}
	// First call (CheckRepository / snapshots --quiet) fails; second call (init) succeeds.
	runner.onRun = func(name string, args []string, out chan<- StreamLine) {
		callCount++
	}

	// Simulate CheckRepository failure on first Run call.
	firstCall := true
	customRunner := &conditionalRunner{
		onRun: func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
			if firstCall {
				firstCall = false
				return fmt.Errorf("repository not initialized")
			}
			return nil
		},
		onOutput: func(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
			return nil, nil
		},
	}

	m := newResticManagerWithRunner(testBackupConfig(), customRunner, nil)
	err := m.EnsureRepository(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, customRunner.runCalls, "EnsureRepository should call Run twice: check then init")
}

// conditionalRunner allows per-call control in tests.
type conditionalRunner struct {
	runCalls    int
	outputCalls int
	onRun       func(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error
	onOutput    func(ctx context.Context, name string, args []string, env []string) ([]byte, error)
}

func (r *conditionalRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	r.runCalls++
	return r.onRun(ctx, name, args, env, out)
}

func (r *conditionalRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	r.outputCalls++
	return r.onOutput(ctx, name, args, env)
}

// --- RestorePreview argv tests ---

func TestResticManager_RestorePreview_Args(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	err := m.RestorePreview(context.Background(), "abc123", out)
	require.NoError(t, err)
	close(out)

	call := runner.lastCall()
	assert.Equal(t, "restic", call.Binary)
	assert.Equal(t, "ls", call.Args[0])
	// "--" terminates flag parsing so a "-"-prefixed snapshot ID is treated as a
	// positional argument, not a restic flag (M5).
	assert.Equal(t, "--", call.Args[1])
	assert.Equal(t, "abc123", call.Args[2])
}

// --- Stats tests ---

func TestResticManager_Stats_ParsesTotalSize(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"total_size": 1048576, "total_file_count": 42}`)
	runner := &fakeRunner{outputData: raw}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	size, err := m.Stats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1048576), size)
}

func TestResticManager_Stats_UsesRawDataMode(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"total_size": 0}`)
	runner := &fakeRunner{outputData: raw}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	_, err := m.Stats(context.Background())
	require.NoError(t, err)

	call := runner.lastCall()
	assert.Equal(t, "restic", call.Binary)
	assert.Equal(t, "stats", call.Args[0])
	assert.True(t, argPairContains(call.Args, "--mode", "raw-data"), "--mode raw-data must be present")
	assert.True(t, argContains(call.Args, "--json"), "--json must be present")
}

func TestResticManager_Stats_RunnerErrorPropagated(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{outputErr: fmt.Errorf("restic not found")}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	_, err := m.Stats(context.Background())
	assert.Error(t, err)
}

func TestResticManager_Stats_MissingPassword(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	cfg.ResticPassword = ""
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	_, err := m.Stats(context.Background())
	assert.Error(t, err, "Stats must fail when ResticPassword is empty")
	assert.Empty(t, runner.calls)
}

// --- FLAG 3: restic --json summary parsing / friendly stream translation ---

func TestHumanizeBytes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.000 KiB"},
		{5242880, "5.000 MiB"},
		{1073741824, "1.000 GiB"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanizeBytes(c.in), "humanizeBytes(%d)", c.in)
	}
}

func TestResticJSONToLine(t *testing.T) {
	t.Parallel()

	// summary message: captured into summary + rendered as a friendly line.
	var s ResticBackupSummary
	line, handled := resticJSONToLine(
		`{"message_type":"summary","files_new":12,"files_changed":3,"data_added":5242880,"data_added_packed":2097152,"snapshot_id":"a1b2c3d4e5f6deadbeef"}`, &s)
	assert.True(t, handled)
	assert.Equal(t, int64(5242880), s.BytesAdded)
	assert.Equal(t, int64(2097152), s.BytesStored)
	assert.Equal(t, "a1b2c3d4e5f6deadbeef", s.SnapshotID)
	assert.Equal(t, 12, s.FilesNew)
	assert.Contains(t, line, "Added 5.000 MiB (2.000 MiB stored), 12 new / 3 changed files")
	assert.Contains(t, line, "snapshot a1b2c3d4 saved")

	// zero data_added → "No change" rather than "Added 0 B".
	var s2 ResticBackupSummary
	line, handled = resticJSONToLine(
		`{"message_type":"summary","data_added":0,"snapshot_id":"deadbeefcafe"}`, &s2)
	assert.True(t, handled)
	assert.Equal(t, int64(0), s2.BytesAdded)
	assert.Contains(t, line, "No change")
	assert.Contains(t, line, "snapshot deadbeef saved")

	// status progress is suppressed (handled, empty line).
	line, handled = resticJSONToLine(`{"message_type":"status","percent_done":0.5,"bytes_done":10}`, nil)
	assert.True(t, handled)
	assert.Empty(t, line)

	// error message → friendly Error: line including the item.
	line, handled = resticJSONToLine(
		`{"message_type":"error","error":{"message":"permission denied"},"item":"/data/x"}`, nil)
	assert.True(t, handled)
	assert.Equal(t, "Error: /data/x: permission denied", line)

	// non-JSON (e.g. a stderr warning) is not handled → caller forwards verbatim.
	line, handled = resticJSONToLine("open repository", nil)
	assert.False(t, handled)
	assert.Empty(t, line)
}

// TestResticManager_Backup_ParsesSummaryAndTranslates reproduces FLAG 3 (ST-3):
// the backup must capture real bytesAdded from restic's --json summary and the
// live stream must show friendly text, not raw restic JSON.
func TestResticManager_Backup_ParsesSummaryAndTranslates(t *testing.T) {
	t.Parallel()

	summaryJSON := `{"message_type":"summary","files_new":12,"files_changed":3,"data_added":5242880,"data_added_packed":2097152,"snapshot_id":"a1b2c3d4e5f6deadbeef"}`
	runner := &fakeRunner{
		onRun: func(name string, args []string, out chan<- StreamLine) {
			// restic emits frequent status progress, then a final summary.
			out <- StreamLine{Type: "data", Line: `{"message_type":"status","percent_done":0.5,"bytes_done":10}`}
			out <- StreamLine{Type: "data", Line: summaryJSON}
		},
	}
	m := newResticManagerWithRunner(testBackupConfig(), runner, nil)

	out := make(chan StreamLine, 64)
	var lines []string
	collected := make(chan struct{})
	go func() {
		for sl := range out {
			lines = append(lines, sl.Line)
		}
		close(collected)
	}()

	summary, err := m.Backup(context.Background(), "/data/s", []string{"s"}, out)
	close(out)
	<-collected

	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, int64(5242880), summary.BytesAdded)
	assert.Equal(t, "a1b2c3d4e5f6deadbeef", summary.SnapshotID)

	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "Added 5.000 MiB (2.000 MiB stored), 12 new / 3 changed files")
	assert.Contains(t, joined, "snapshot a1b2c3d4 saved")
	assert.NotContains(t, joined, "message_type", "raw restic JSON must not reach the live stream")

	// --json must be passed to restic (not --verbose).
	assert.True(t, argContains(runner.lastCall().Args, "--json"), "--json must be present")
	assert.False(t, argContains(runner.lastCall().Args, "--verbose"), "--verbose must be gone")
}

// --- Missing password test ---

func TestResticManager_MissingPassword_ReturnsError(t *testing.T) {
	t.Parallel()

	cfg := testBackupConfig()
	cfg.ResticPassword = ""
	runner := &fakeRunner{}
	m := newResticManagerWithRunner(cfg, runner, nil)

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()
	_, err := m.Backup(context.Background(), "/data/s", []string{"s"}, out)
	close(out)

	assert.Error(t, err, "Backup must fail when ResticPassword is empty")
	assert.Empty(t, runner.calls, "no runner call should be made when password is missing")
}
