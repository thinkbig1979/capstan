package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// commandRunner is the narrow seam used by ResticManager and RcloneManager to
// execute external processes. The real implementation uses exec.CommandContext
// with process-group kill on context cancellation; tests inject a fake.
type commandRunner interface {
	// Run executes the named binary with the given arguments and environment
	// additions, streaming combined stdout+stderr lines into out. It blocks
	// until the process exits or ctx is cancelled. A non-zero exit code is
	// returned as an error.
	Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error

	// Output executes the named binary and captures stdout, returning it as
	// a byte slice. stderr is discarded. env additions are merged with the
	// process environment.
	Output(ctx context.Context, name string, args []string, env []string) ([]byte, error)
}

// CommandRunner is the exported version of commandRunner, providing the same
// interface for use by external test packages (e.g. integrationtest).
// Production code uses the unexported alias; the exported alias lets external
// tests implement fake runners without importing internal test helpers.
type CommandRunner = commandRunner

// NewResticManagerForTest creates a ResticManager with an injected runner.
// It is intended for use in external test packages (e.g. integrationtest)
// where the unexported newResticManagerWithRunner constructor is inaccessible.
func NewResticManagerForTest(cfg BackupConfig, runner CommandRunner, logger *slog.Logger) *ResticManager {
	return newResticManagerWithRunner(cfg, runner, logger)
}

// execRunner is the real commandRunner backed by os/exec.
type execRunner struct{}

// Run starts the process in its own process group (Setpgid) so that a context
// cancellation can kill the entire process tree, not just the parent PID.
//
// A grandchild that leaves the group (setsid) survives that kill and keeps the
// output pipes open. Output therefore goes through io.Pipe writers, so exec's
// own copy goroutines are bounded by WaitDelay, and Wait runs before the
// scanners finish. With StdoutPipe the scanners had to end first, and Wait (the
// only place WaitDelay acts) was never reached: Run outlived its context until
// the grandchild exited on its own (agent-os-z91e.25, same shape as
// agent-os-z91e.24).
//
// The group kill is cmd.Cancel, as in Output. It used to be a goroutine waiting
// on ctx, which outlived Run whenever ctx outlived the child, and on a later
// cancel signalled a pgid that might belong to another group by then
// (agent-os-z91e.26).
func (r *execRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	cmd := boundCommand(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// stripCapstanSecrets removes Capstan's own secrets (JWT_SECRET,
	// STORAGE_KEY, GIT_HTTPS_TOKEN, RESTIC_PASSWORD) before the rest of the
	// inherited environment is handed to restic/rclone (agent-os-iey). See
	// stripCapstanSecrets for why this is a denylist rather than the allowlist
	// used for docker/compose.
	cmd.Env = append(stripCapstanSecrets(os.Environ()), env...)

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	scanDone := make(chan struct{}, 2)
	scan := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			line := sc.Text()
			if line != "" {
				out <- StreamLine{Type: "data", Line: line}
			}
		}
		// Keep draining after a scan error (a line over the scanner's limit):
		// exec's copy goroutine blocks on a writer nobody reads.
		_, _ = io.Copy(io.Discard, r) //nolint:errcheck // Drain only: the pipe reader's only error is the writer closing, which is the end of the stream.
		scanDone <- struct{}{}
	}
	go scan(stdoutR)
	go scan(stderrR)

	waitErr := cmd.Wait()
	// Wait has returned, so exec's copy goroutines are done writing: closing
	// the writers is what ends the scanners.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	<-scanDone
	<-scanDone

	if waitErr != nil {
		return fmt.Errorf("%s exited: %w", name, waitErr)
	}
	return nil
}

// Output runs the process and captures stdout only. The group kill is
// cmd.Cancel, which exec runs once ctx ends: the goroutine this used to start
// read a pgid that was never assigned, so only the direct child was ever
// killed, and WaitDelay (via boundCommand) releases the caller if a grandchild
// that left the group still holds the pipe (agent-os-z91e.25).
func (r *execRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	cmd := boundCommand(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(stripCapstanSecrets(os.Environ()), env...)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// killProcessGroup is the cmd.Cancel of both execRunner methods: it SIGKILLs
// the child's whole process group (Setpgid), not just the child. As cmd.Cancel
// it runs only while exec is waiting on the child, so it can never signal a
// group after the child has been reaped and its pgid reused.
func killProcessGroup(cmd *exec.Cmd) error {
	// Negative PID signals the process group.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return nil
}

// ResticManager wraps restic operations for a single BackupConfig.
// It writes the repository password to a 0600 temp file and passes it via
// RESTIC_PASSWORD_FILE; the raw password is never included in argv or logs.
type ResticManager struct {
	cfg    BackupConfig
	runner commandRunner
	logger *slog.Logger

	// testDeadlineCap, when non-zero, shortens forget's and prune's operation
	// deadlines so a test can watch one expire. Production never sets it.
	testDeadlineCap time.Duration
}

// NewResticManager creates a ResticManager using the real exec-based runner.
func NewResticManager(cfg BackupConfig, logger *slog.Logger) *ResticManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResticManager{
		cfg:    cfg,
		runner: &execRunner{},
		logger: logger.With("component", "restic-manager"),
	}
}

// newResticManagerWithRunner is used in tests to inject a fake runner.
func newResticManagerWithRunner(cfg BackupConfig, runner commandRunner, logger *slog.Logger) *ResticManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &ResticManager{
		cfg:    cfg,
		runner: runner,
		logger: logger.With("component", "restic-manager"),
	}
}

// withPasswordFile creates a temporary 0600 file containing the restic
// password and returns its path plus a cleanup function. The caller must defer
// cleanup(). The password is never logged.
func (m *ResticManager) withPasswordFile() (path string, cleanup func(), err error) {
	if m.cfg.ResticPassword == "" {
		return "", func() {}, fmt.Errorf("restic password is not configured")
	}
	f, err := os.CreateTemp("", "capstan-restic-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp password file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("chmod temp password file: %w", err)
	}
	if _, err := f.WriteString(m.cfg.ResticPassword); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write temp password file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("close temp password file: %w", err)
	}
	name := f.Name()
	return name, func() { _ = os.Remove(name) }, nil
}

// resticEnv returns the base environment additions needed by restic: the
// repository path and the password file pointer.
func (m *ResticManager) resticEnv(passwordFile string) []string {
	return []string{
		"RESTIC_REPOSITORY=" + m.cfg.ResticRepository,
		"RESTIC_PASSWORD_FILE=" + passwordFile,
	}
}

// runDrained runs a command whose output nobody reads and throws the lines
// away. It owns the channel: the drain goroutine exits when out is closed, and
// the close is deferred so a failed or panicking Run cannot strand it. Three
// probes used to start the drain by hand and close out only after Run
// succeeded, so every failed probe leaked one goroutine (agent-os-z91e.2).
// Every drain-and-discard call in this package goes through here; a guard test
// fails on a hand-written one.
func runDrained(ctx context.Context, runner commandRunner, name string, args, env []string) error {
	out := make(chan StreamLine, 32)
	defer close(out)
	go func() {
		for range out {
		}
	}()
	return runner.Run(ctx, name, args, env, out)
}

// resticExitRepoLocked is restic's exit code for "repository is already
// locked" (`restic help forget|prune|check|backup`, restic 0.18.0: "Exit status
// is 11 if the repository is already locked.").
const resticExitRepoLocked = 11

// unlockTimeout bounds one `restic unlock`, which only lists and deletes small
// lock files.
const unlockTimeout = 2 * time.Minute

// runLocking runs a restic command that takes a repository lock while
// Capstan's global backup lock is held: forget, prune and check, which need
// the exclusive lock, and backup, which a stale exclusive lock refuses. On
// exit 11 it runs `restic unlock` and retries once (agent-os-zp01).
//
// restic never clears a stale lock by itself. OBSERVED with restic 0.18.0: a
// lock left by a SIGKILLed `restic backup` still made `restic forget` exit 11;
// `restic unlock` then printed "successfully removed 1 locks" and forget
// exited 0. In production (2026-10-11) two such locks, dated August, made
// retention exit 11 for every stack while the run still read "success".
//
// Plain unlock, never --remove-all: restic removes only locks it judges stale,
// i.e. older than 30 minutes (live holders refresh theirs every 5) or made on
// this host by a process that no longer exists (restic v0.18.0
// internal/restic/lock.go, Lock.Stale). OBSERVED: a lock held by a live
// `restic backup` survived `restic unlock`, and forget still exited 11. So a
// real concurrent operation, such as an external restic or one of Capstan's
// own unguarded read-only calls, keeps its lock, the retry fails again, and
// that is reported as a lock held by another process.
func (m *ResticManager) runLocking(ctx context.Context, args, env []string, out chan<- StreamLine) error {
	err := m.runner.Run(ctx, "restic", args, env, out)
	if !isExitCode(err, resticExitRepoLocked) {
		return err
	}
	// Logged, not written to out: Backup forwards its raw channel to the
	// caller's out with a blocking send, so a line written here hung a run
	// whose out is nil. `restic unlock` reports what it removed on out itself.
	m.logger.Warn("restic: repository is locked; removing stale locks (restic unlock) and retrying once",
		"subcommand", args[0])
	if unlockErr := m.unlockStale(ctx, env, out); unlockErr != nil {
		return fmt.Errorf("%w; removing stale locks failed: %w", err, unlockErr)
	}
	retryErr := m.runner.Run(ctx, "restic", args, env, out)
	if isExitCode(retryErr, resticExitRepoLocked) {
		return fmt.Errorf("repository is locked by another restic process (not a stale lock): %w", retryErr)
	}
	return retryErr
}

// unlockStale runs `restic unlock` under its own deadline.
func (m *ResticManager) unlockStale(ctx context.Context, env []string, out chan<- StreamLine) error {
	ctx, cancel := withCommandDeadline(ctx, unlockTimeout)
	defer cancel()
	return timeoutError(ctx, m.runner.Run(ctx, "restic", []string{"unlock"}, env, out), "restic unlock")
}

// CheckRepository runs `restic snapshots --quiet` to verify the repository
// is accessible. Returns nil on success.
func (m *ResticManager) CheckRepository(ctx context.Context) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := runDrained(ctx, m.runner, "restic", []string{"snapshots", "--quiet"}, m.resticEnv(pwFile)); err != nil {
		return fmt.Errorf("cannot access restic repository: %w", err)
	}
	return nil
}

// DefaultVerifyReadDataSubset is the share of pack files a verification reads
// when the caller names none.
//
// Why a subset rather than a bare --read-data: --read-data downloads and
// re-hashes EVERY pack in the repository, so on a multi-gigabyte repo it is an
// hours-long operation, and an integrity probe nobody can afford to run is
// worth nothing. A subset is not a weaker answer to the failure this exists to
// catch: `restic check` always reconciles the index against a LIST of the
// backend's pack files, and a backend listing cannot be served from the local
// cache, so a repository that has LOST its packs fails at any subset — OBSERVED
// with restic 0.18.0, `--read-data-subset=100%` and plain `check` both exit 1
// on a repo whose data/ directory was moved away, while `restic snapshots
// --quiet` (what CheckRepository runs) exits 0 on the same repo with a warm
// cache. The subset bounds only how much pack CONTENT is re-hashed, i.e. how
// much silent bitrot one run can find.
const DefaultVerifyReadDataSubset = "5%"

// verifyTimeout bounds one `restic check`. Twice backupCycleTimeout: a 100%
// read of a large remote repository is legitimately long, and in exchange a
// hung check, which holds the backup lock, costs at most four hours of
// scheduled backups (each one skipped with a history row), never a whole day.
const verifyTimeout = 4 * time.Hour

// verifySubsetPattern accepts the three forms `restic check --read-data-subset`
// documents: a percentage ("5%", "2.5%"), a group selector ("1/12"), or a size
// ("5G"). The value is validated rather than passed through because it is the
// only caller-supplied element of restic's argv on this path; everything else
// is a literal.
var verifySubsetPattern = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?%|[0-9]+/[0-9]+|[0-9]+[KMGT])$`)

// ValidateVerifySubset returns the subset string to hand restic, substituting
// DefaultVerifyReadDataSubset for the empty string, and refuses anything that
// is not one of restic's documented forms.
func ValidateVerifySubset(subset string) (string, error) {
	if subset == "" {
		return DefaultVerifyReadDataSubset, nil
	}
	if !verifySubsetPattern.MatchString(subset) {
		return "", fmt.Errorf("invalid read-data subset %q: expected a percentage (5%%), a group (1/12) or a size (5G)", subset)
	}
	return subset, nil
}

// VerifyRepositoryData runs `restic check --read-data-subset=<subset>`, which
// reads repository DATA and fails on a repository that has lost or corrupted
// its pack files.
//
// This is NOT the same probe as CheckRepository above, and the difference is
// the whole point of the method (agent-os-j1jw). CheckRepository runs `restic
// snapshots`, which reads snapshot METADATA and which restic will happily
// serve out of the local cache at /home/appuser/.cache/restic: OBSERVED with
// restic 0.18.0, against a repository whose data/ directory had been moved
// away, `restic snapshots --quiet` exits 0 and prints the snapshot, while
// `restic check --read-data` on the same repository in the same state exits 1
// with "Fatal: repository contains errors". A restore that consults only the
// metadata probe can therefore report success against a repository that can no
// longer restore anything, which is the defect this exists to remove.
//
// No --no-cache flag is passed and none is needed: `restic check` uses a
// temporary cache of its own rather than the shared one, and the observation
// above was made with the shared cache warm.
func (m *ResticManager) VerifyRepositoryData(ctx context.Context, subset string, out chan<- StreamLine) error {
	subset, err := ValidateVerifySubset(subset)
	if err != nil {
		return err
	}

	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	// Hours, not CheckRepository's 30s: this reads pack data and legitimately
	// runs for minutes. But it is bounded: the check holds the backup lock (see
	// BackupService.VerifyRepositoryData), so a hung one would otherwise stop
	// every backup until the server restarts.
	ctx, cancel := withCommandDeadline(ctx, verifyTimeout)
	defer cancel()
	args := []string{"check", "--read-data-subset=" + subset}
	if err := m.runLocking(ctx, args, m.resticEnv(pwFile), out); err != nil {
		return fmt.Errorf("repository integrity check failed: %w", timeoutError(ctx, err, "restic check"))
	}
	return nil
}

// EnsureRepository initialises the repository if it has not been initialised
// yet. It first calls CheckRepository; if that fails it runs `restic init`.
func (m *ResticManager) EnsureRepository(ctx context.Context) error {
	if err := m.CheckRepository(ctx); err == nil {
		return nil // already initialised
	}

	m.logger.Info("Initialising restic repository", "path", RedactURLUserinfo(m.cfg.ResticRepository))

	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if err := runDrained(ctx, m.runner, "restic", []string{"init"}, m.resticEnv(pwFile)); err != nil {
		return fmt.Errorf("restic init failed: %w", err)
	}
	return nil
}

// ResticBackupSummary holds the machine-readable result of a restic backup,
// parsed from the final `summary` message emitted by `restic backup --json`.
type ResticBackupSummary struct {
	SnapshotID   string
	BytesAdded   int64 // data_added: new bytes written to the repo (pre-compression)
	BytesStored  int64 // data_added_packed: bytes actually stored after dedup/compression
	FilesNew     int
	FilesChanged int
}

// resticJSONMessage is the subset of restic's `backup --json` message schema we
// care about (status progress is ignored; summary carries the result).
type resticJSONMessage struct {
	MessageType     string `json:"message_type"`
	FilesNew        int    `json:"files_new"`
	FilesChanged    int    `json:"files_changed"`
	DataAdded       int64  `json:"data_added"`
	DataAddedPacked int64  `json:"data_added_packed"`
	SnapshotID      string `json:"snapshot_id"`
	Error           struct {
		Message string `json:"message"`
	} `json:"error"`
	Item string `json:"item"`
}

// Backup backs up stackDir with the required Capstan tags plus any additional
// tags provided by the caller. It runs `restic backup --json`, translating the
// machine output into friendly human-readable lines on out while capturing the
// final summary (data_added, snapshot_id) which it returns to the caller.
func (m *ResticManager) Backup(ctx context.Context, stackDir string, tags []string, out chan<- StreamLine) (*ResticBackupSummary, error) {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	dateTag := time.Now().Format("2006-01-02")

	args := []string{
		"backup",
		"--json",
		"--one-file-system",
		"--exclude-caches",
	}

	// Required Capstan tags.
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	args = append(args, "--tag", "capstan-backup")
	args = append(args, "--tag", dateTag)

	if m.cfg.BackupHostname != "" {
		// --host, not --hostname: restic 0.18.0 prints "Flag --hostname has
		// been deprecated, use --host".
		args = append(args, "--host", m.cfg.BackupHostname)
	}

	args = append(args, stackDir)

	// Translate restic's --json stream into friendly lines while capturing the
	// summary. The runner writes raw lines to an intermediate channel; this
	// goroutine forwards human-readable lines to out and records the summary.
	summary := &ResticBackupSummary{}
	raw := make(chan StreamLine, 32)
	translateDone := make(chan struct{})
	go func() {
		defer close(translateDone)
		for sl := range raw {
			if sl.Type == "data" {
				line, handled := resticJSONToLine(sl.Line, summary)
				switch {
				case !handled:
					// Non-JSON line (e.g. a stderr warning) — forward verbatim.
				case line == "":
					continue
				default:
					sl = StreamLine{Type: "data", Line: line}
				}
			}
			// A nil out means nobody is listening, as for stream(): a send
			// on it would block forever, and the summary still has to be
			// read.
			if out != nil {
				out <- sl
			}
		}
	}()

	runErr := m.runLocking(ctx, args, m.resticEnv(pwFile), raw)
	close(raw)
	<-translateDone

	return summary, runErr
}

// resticJSONToLine converts one line of `restic backup --json` output into a
// friendly stream line. When the line is a recognised restic JSON message it
// returns handled=true (with an empty line for noisy progress that should be
// suppressed); summary is updated in place when a summary message is seen.
// Non-JSON lines return handled=false so the caller can forward them verbatim.
func resticJSONToLine(raw string, summary *ResticBackupSummary) (line string, handled bool) {
	var msg resticJSONMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil || msg.MessageType == "" { //geterrors:ignore the function's own doc comment says handled=false means "forward the line verbatim"; a non-JSON line and a JSON line with no message type are the same non-answer
		return "", false
	}

	switch msg.MessageType {
	case "summary":
		summary.SnapshotID = msg.SnapshotID
		summary.BytesAdded = msg.DataAdded
		summary.BytesStored = msg.DataAddedPacked
		summary.FilesNew = msg.FilesNew
		summary.FilesChanged = msg.FilesChanged

		short := shortSnapshotID(msg.SnapshotID)
		if msg.DataAdded == 0 {
			return fmt.Sprintf("No change: nothing new to back up; snapshot %s saved", short), true
		}
		return fmt.Sprintf("Added %s (%s stored), %d new / %d changed files; snapshot %s saved",
			humanizeBytes(msg.DataAdded), humanizeBytes(msg.DataAddedPacked),
			msg.FilesNew, msg.FilesChanged, short), true
	case "error":
		m := msg.Error.Message
		if msg.Item != "" {
			m = msg.Item + ": " + m
		}
		return "Error: " + m, true
	default:
		// status / verbose_status / other progress — suppress the noise.
		return "", true
	}
}

// humanizeBytes formats a byte count using binary (1024) units, e.g. "4.696 MiB".
func humanizeBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.3f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// shortSnapshotID is restic's short form of a snapshot id: its first 8
// characters, what `restic snapshots` prints as short_id.
func shortSnapshotID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// snapshotVerifyTimeout bounds the post-backup `restic ls` of one snapshot.
const snapshotVerifyTimeout = 10 * time.Minute

// Verify confirms the snapshot snapshotID is readable by running `restic ls`
// on it, and returns how many entries it listed.
//
// The caller passes the id restic printed for the backup it just took
// (agent-os-wdlf). It used to look the snapshot up with `snapshots --tag X
// --latest 1`, which returns the latest snapshot PER host and path: once the
// host changed, element [0] was an earlier host's snapshot, and that is what
// got verified.
//
// The listing is counted, never forwarded: it is every file name in the
// snapshot, and the run output it used to reach is logged line by line (1476
// lines in one production run). Only the last line that is not a path is
// kept, so a failure still says what restic reported.
func (m *ResticManager) Verify(ctx context.Context, snapshotID string) (entries int, err error) {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return 0, err
	}
	defer cleanup()

	ctx, cancel := withCommandDeadline(ctx, snapshotVerifyTimeout)
	defer cancel()

	lines := make(chan StreamLine, 32)
	counted := make(chan struct{})
	var lastNote string
	go func() {
		defer close(counted)
		for l := range lines {
			// restic ls prints each entry as an absolute path; any other
			// line is its header or a message.
			if l.Line != "" && l.Line[0] == '/' {
				entries++
			} else {
				lastNote = l.Line
			}
		}
	}()
	// "--" stops flag parsing, as in RestorePreview.
	runErr := m.runner.Run(ctx, "restic", []string{"ls", "--", snapshotID}, m.resticEnv(pwFile), lines)
	close(lines)
	<-counted

	if runErr != nil {
		runErr = timeoutError(ctx, runErr, "restic ls")
		if lastNote != "" {
			return entries, fmt.Errorf("verify snapshot %s: %w (restic: %s)", shortSnapshotID(snapshotID), runErr, lastNote)
		}
		return entries, fmt.Errorf("verify snapshot %s: %w", shortSnapshotID(snapshotID), runErr)
	}
	return entries, nil
}

// retentionTimeout bounds a plain `restic forget`, which only rewrites
// snapshot metadata. pruneTimeout bounds `restic prune` and `forget --prune`,
// which repack data and are legitimately long on a large repository; it
// matches verifyTimeout, the bound already chosen for reading pack data. Both
// run while Capstan's global backup lock is held, so a hung one would
// otherwise block every later backup, sync, restore and check until the server
// restarts (agent-os-j6ob, safe-defaults rule 1).
const (
	retentionTimeout = 10 * time.Minute
	pruneTimeout     = 4 * time.Hour
)

// opDeadline returns timeout, or testDeadlineCap when a test set a shorter
// one.
func (m *ResticManager) opDeadline(timeout time.Duration) time.Duration {
	if m.testDeadlineCap > 0 && m.testDeadlineCap < timeout {
		return m.testDeadlineCap
	}
	return timeout
}

// ApplyRetention runs `restic forget` with the configured keep-* flags and
// --prune when AutoPrune is set. It streams output to out.
func (m *ResticManager) ApplyRetention(ctx context.Context, tag string, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	// Grouped by path only, with no host filter (agent-os-wdlf). restic
	// groups forget by host and path by default, and the host used to be the
	// container id, which changes on every redeploy: each deploy's snapshots
	// became a group of their own that a `--hostname <current>` filter never
	// reached again. Not grouped by tags either: every snapshot carries a
	// date tag (Backup), so grouping by tags makes one group per day and
	// keeps every snapshot (OBSERVED, restic 0.18.0 dry-run). The tag filter
	// keeps the policy to this stack's snapshots.
	args := []string{"forget", "--verbose", "--group-by", "paths"}
	if tag != "" {
		args = append(args, "--tag", tag)
	}

	hasRetention := false
	if m.cfg.KeepDaily > 0 {
		args = append(args, "--keep-daily", strconv.Itoa(m.cfg.KeepDaily))
		hasRetention = true
	}
	if m.cfg.KeepWeekly > 0 {
		args = append(args, "--keep-weekly", strconv.Itoa(m.cfg.KeepWeekly))
		hasRetention = true
	}
	if m.cfg.KeepMonthly > 0 {
		args = append(args, "--keep-monthly", strconv.Itoa(m.cfg.KeepMonthly))
		hasRetention = true
	}
	if m.cfg.KeepYearly > 0 {
		args = append(args, "--keep-yearly", strconv.Itoa(m.cfg.KeepYearly))
		hasRetention = true
	}

	if !hasRetention {
		m.logger.Warn("ApplyRetention: no keep-* settings configured, skipping forget")
		return nil
	}

	timeout := retentionTimeout
	if m.cfg.AutoPrune {
		args = append(args, "--prune")
		timeout = pruneTimeout
	}

	// The deadline covers the attempt, the stale-lock unlock and the retry.
	ctx, cancel := withCommandDeadline(ctx, m.opDeadline(timeout))
	defer cancel()
	return timeoutError(ctx, m.runLocking(ctx, args, m.resticEnv(pwFile), out), "restic forget")
}

// Prune runs `restic prune` (optionally with --dry-run). It streams output to out.
func (m *ResticManager) Prune(ctx context.Context, dryRun bool, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"prune", "--verbose"}
	if dryRun {
		args = append(args, "--dry-run")
	}

	ctx, cancel := withCommandDeadline(ctx, m.opDeadline(pruneTimeout))
	defer cancel()
	return timeoutError(ctx, m.runLocking(ctx, args, m.resticEnv(pwFile), out), "restic prune")
}

// resticSnapshot is the JSON shape returned by `restic snapshots --json`.
// It mirrors the engine's Snapshot struct adapted to Capstan field names.
type resticSnapshot struct {
	ID      string   `json:"id"`
	ShortID string   `json:"short_id"`
	Time    string   `json:"time"`
	Host    string   `json:"hostname"`
	Tags    []string `json:"tags"`
	Paths   []string `json:"paths"`
	// agent-os-kezb: restic >= 0.17 attaches a summary to every snapshot it
	// writes, and `restic snapshots --json` already returns it in the bytes
	// parsed below — so the snapshot size costs no extra restic invocation.
	// Snapshots written by older restic carry no summary at all; the zero value
	// then leaves BackupSnapshot.SizeBytes at 0, which `omitempty` drops.
	Summary *resticSnapshotSummary `json:"summary"`
}

// resticSnapshotSummary is the subset of restic's per-snapshot summary we read.
// TotalBytesProcessed is the logical size of what the snapshot holds, which is
// what a column headed "Size" means. DataAdded — the deduplicated delta this run
// added to the repository — answers a different question and is not read here.
type resticSnapshotSummary struct {
	TotalBytesProcessed int64 `json:"total_bytes_processed"`
}

// ListSnapshots returns snapshots filtered by tag (empty = all), capped at
// limit (0 = unlimited). It parses the JSON output of `restic snapshots --json`.
func (m *ResticManager) ListSnapshots(ctx context.Context, tag string, limit int) ([]models.BackupSnapshot, error) {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"snapshots", "--json"}
	if tag != "" {
		args = append(args, "--tag", tag)
	}
	if limit > 0 {
		args = append(args, "--latest", strconv.Itoa(limit))
	}

	raw, err := m.runner.Output(ctx, "restic", args, m.resticEnv(pwFile))
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}

	// restic may return "null" on an empty repo.
	raw = bytes.TrimSpace(raw)
	if string(raw) == "null" || len(raw) == 0 {
		return nil, nil
	}

	var snaps []resticSnapshot
	if err := json.Unmarshal(raw, &snaps); err != nil {
		return nil, fmt.Errorf("parse snapshots JSON: %w", err)
	}

	result := make([]models.BackupSnapshot, len(snaps))
	for i, s := range snaps {
		result[i] = models.BackupSnapshot{
			ID:       s.ID,
			ShortID:  s.ShortID,
			Time:     s.Time,
			Hostname: s.Host,
			Tags:     emptyIfNil(s.Tags),
			Paths:    emptyIfNil(s.Paths),
		}
		if s.Summary != nil {
			result[i].SizeBytes = s.Summary.TotalBytesProcessed
		}
	}
	return result, nil
}

// emptyIfNil keeps a key restic omitted (it drops "tags" for every untagged
// snapshot) on the wire as [] rather than null (agent-os-e5pr).
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// RestorePreview returns the file listing of a snapshot via `restic ls`.
func (m *ResticManager) RestorePreview(ctx context.Context, snapshotID string, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// "--" stops flag parsing so a snapshot ID beginning with "-" cannot be
	// interpreted as a restic flag (M5; the handler also validates the format).
	return m.runner.Run(ctx, "restic", []string{"ls", "--", snapshotID}, m.resticEnv(pwFile), out)
}

// resticStatsOutput is the JSON shape returned by `restic stats --json`.
// Only the fields Capstan uses are mapped; extras are silently discarded.
type resticStatsOutput struct {
	TotalSize uint64 `json:"total_size"`
}

// Stats runs `restic stats --mode raw-data --json` and returns the total
// on-disk size of the repository in bytes. It uses the Output helper (no
// streaming) and applies a 60-second timeout matching ListSnapshots.
func (m *ResticManager) Stats(ctx context.Context) (int64, error) {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return 0, err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"stats", "--mode", "raw-data", "--json"}
	raw, err := m.runner.Output(ctx, "restic", args, m.resticEnv(pwFile))
	if err != nil {
		return 0, fmt.Errorf("restic stats: %w", err)
	}

	var out resticStatsOutput
	if jsonErr := json.Unmarshal(bytes.TrimSpace(raw), &out); jsonErr != nil {
		return 0, fmt.Errorf("parse restic stats JSON: %w", jsonErr)
	}
	//nolint:gosec // TotalSize overflowing int64 needs a backup past ~9.2 exabytes, not a realistic size for this class of tool
	return int64(out.TotalSize), nil
}

// Restore restores the given snapshot to targetPath via `restic restore --target`.
// It always passes --delete, so targetPath ends up matching the snapshot: files
// created after it are removed rather than surviving next to the restored ones
// (agent-os-z91e.8). excludes are paths relative to targetPath that restic must
// leave untouched, normally restoreProtectedPaths' result: without them,
// --delete would also remove the live content of mounts and cache directories
// the backup never covered.
func (m *ResticManager) Restore(ctx context.Context, snapshotID, sourcePath, targetPath string, excludes []string, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	// Backups store the stack's absolute path, so a plain `restore <id> --target X`
	// recreates that absolute tree *under* X (X/home/.../stack/...). Use restic's
	// `<snapshotID>:<subfolder>` form to strip the stored source prefix so the
	// snapshot contents land directly in targetPath.
	ref := snapshotID
	if sourcePath != "" {
		ref = snapshotID + ":" + sourcePath
	}
	args := []string{"restore", ref, "--target", targetPath, "--verbose", "--delete"}
	for _, rel := range excludes {
		args = append(args, "--exclude", restoreExcludePattern(rel))
	}
	return m.runner.Run(ctx, "restic", args, m.resticEnv(pwFile), out)
}

// cacheDirTagSignature is the header a CACHEDIR.TAG file must start with for
// restic's --exclude-caches to skip the directory holding it.
const cacheDirTagSignature = "Signature: 8a477f597d28d172789f06886806bc55"

// restoreProtectedPaths lists, relative to stackDir, the paths under it that
// Backup() leaves out of a snapshot of stackDir: entries on another filesystem
// than stackDir (--one-file-system) and directories holding a valid CACHEDIR.TAG
// (--exclude-caches). No snapshot holds their content, so `restore --delete`
// would delete it; Restore passes them to restic as excludes instead.
//
// It reads the tree as it is now. A directory that was a mount or a cache at
// backup time but is not one now is not listed, and loses its current content
// to --delete. A stackDir that is itself a symlink or a cache directory is
// refused: the backup never covered it.
func restoreProtectedPaths(stackDir string) ([]string, error) {
	return scanRestoreProtected(stackDir, statDevice)
}

// statDevice returns the device id of a FileInfo from os.Stat/os.Lstat.
func statDevice(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // Dev is uint64 on linux but int32 on darwin
}

// scanRestoreProtected is restoreProtectedPaths with the device lookup injected,
// so a test can stand in for a mount without root.
func scanRestoreProtected(stackDir string, deviceOf func(fs.FileInfo) (uint64, bool)) ([]string, error) {
	stackInfo, err := os.Stat(stackDir)
	if errors.Is(err, fs.ErrNotExist) {
		// Nothing on disk, so nothing --delete could remove.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat stack directory: %w", err)
	}
	// Backup() is given stackDir as its only root, so its device is the one
	// --one-file-system kept.
	stackDev, ok := deviceOf(stackInfo)
	if !ok {
		return nil, fmt.Errorf("read device id of %s", stackDir)
	}

	prefix := stackDir + string(filepath.Separator)
	var protected []string
	err = filepath.WalkDir(stackDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == stackDir && errors.Is(walkErr, fs.ErrNotExist) {
				return nil // restic creates the target; nothing to protect
			}
			return walkErr
		}
		isRoot := path == stackDir
		if isRoot && d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("restore target %s is a symbolic link", stackDir)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		dev, ok := deviceOf(info)
		if !ok {
			return fmt.Errorf("read device id of %s", path)
		}
		// The root cannot differ from stackDev: it is the directory stackDev was
		// read from, so only entries below it can sit on another filesystem.
		if dev != stackDev {
			protected = append(protected, strings.TrimPrefix(path, prefix))
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		tagged, err := hasCacheDirTag(path)
		if err != nil {
			return err
		}
		if !tagged {
			return nil
		}
		if isRoot {
			return fmt.Errorf("restore target %s is a cache directory (CACHEDIR.TAG), which the backup never covered", stackDir)
		}
		protected = append(protected, strings.TrimPrefix(path, prefix))
		return fs.SkipDir
	})
	if err != nil {
		return nil, fmt.Errorf("scan restore target for content the backup never covered: %w", err)
	}
	return protected, nil
}

// hasCacheDirTag reports whether dir holds a CACHEDIR.TAG that starts with
// cacheDirTagSignature, the test restic's --exclude-caches applies.
func hasCacheDirTag(dir string) (bool, error) {
	f, err := os.Open(filepath.Join(dir, "CACHEDIR.TAG")) //nolint:gosec // G304: dir is a directory restoreProtectedPaths is walking under the stack's restore target, already confined by runRestore
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, len(cacheDirTagSignature))
	if _, err := io.ReadFull(f, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil // too short to carry the signature
		}
		return false, err
	}
	return string(buf) == cacheDirTagSignature, nil
}

// restoreExcludePattern turns a path relative to the restore target into a
// restic pattern matching exactly that path: the leading "/" anchors it to the
// restore root (an absolute stack path matches nothing under the
// `<snapshot>:<subfolder>` form), and glob metacharacters are escaped so a
// directory named "we[i]rd*" protects itself, not "weird" (both measured with
// restic 0.19.1, agent-os-z91e.8).
func restoreExcludePattern(rel string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range filepath.ToSlash(rel) {
		switch r {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
