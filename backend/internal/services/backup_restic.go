package services

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
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

// execRunner is the real commandRunner backed by os/exec.
type execRunner struct{}

// Run starts the process in its own process group (Setpgid) so that a context
// cancellation can kill the entire process tree, not just the parent PID.
func (r *execRunner) Run(ctx context.Context, name string, args []string, env []string, out chan<- StreamLine) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), env...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	// Kill the whole process group on context cancellation.
	pgid := cmd.Process.Pid
	go func() {
		<-ctx.Done()
		// Negative PID signals the process group.
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}()

	scanDone := make(chan struct{}, 2)
	scan := func(r *bufio.Scanner) {
		for r.Scan() {
			line := r.Text()
			if line != "" {
				out <- StreamLine{Type: "data", Line: line}
			}
		}
		scanDone <- struct{}{}
	}
	go scan(bufio.NewScanner(stdout))
	go scan(bufio.NewScanner(stderr))
	<-scanDone
	<-scanDone

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s exited: %w", name, err)
	}
	return nil
}

// Output runs the process and captures stdout only.
func (r *execRunner) Output(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), env...)

	pgid := -1
	go func() {
		<-ctx.Done()
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}()

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// ResticManager wraps restic operations for a single BackupConfig.
// It writes the repository password to a 0600 temp file and passes it via
// RESTIC_PASSWORD_FILE; the raw password is never included in argv or logs.
type ResticManager struct {
	cfg    BackupConfig
	runner commandRunner
	logger *slog.Logger
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

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()

	if err := m.runner.Run(ctx, "restic", []string{"snapshots", "--quiet"}, m.resticEnv(pwFile), out); err != nil {
		return fmt.Errorf("cannot access restic repository: %w", err)
	}
	close(out)
	return nil
}

// EnsureRepository initialises the repository if it has not been initialised
// yet. It first calls CheckRepository; if that fails it runs `restic init`.
func (m *ResticManager) EnsureRepository(ctx context.Context) error {
	if err := m.CheckRepository(ctx); err == nil {
		return nil // already initialised
	}

	m.logger.Info("Initialising restic repository", "path", m.cfg.ResticRepository)

	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()

	if err := m.runner.Run(ctx, "restic", []string{"init"}, m.resticEnv(pwFile), out); err != nil {
		return fmt.Errorf("restic init failed: %w", err)
	}
	close(out)
	return nil
}

// Backup backs up stackDir with the required Capstan tags plus any additional
// tags provided by the caller. It streams output to out.
func (m *ResticManager) Backup(ctx context.Context, stackDir string, tags []string, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	dateTag := time.Now().Format("2006-01-02")

	args := []string{
		"backup",
		"--verbose",
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
		args = append(args, "--hostname", m.cfg.BackupHostname)
	}

	args = append(args, stackDir)

	return m.runner.Run(ctx, "restic", args, m.resticEnv(pwFile), out)
}

// Verify confirms the most recent snapshot for tag is readable by running
// `restic ls` on it. For a deeper check pass tag and rely on restic check.
func (m *ResticManager) Verify(ctx context.Context, tag string, out chan<- StreamLine) error {
	snapshots, err := m.ListSnapshots(ctx, tag, 1)
	if err != nil {
		return fmt.Errorf("verify: list snapshots: %w", err)
	}
	if len(snapshots) == 0 {
		return fmt.Errorf("verify: no snapshots found for tag %q", tag)
	}

	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	args := []string{"ls", snapshots[0].ShortID}
	return m.runner.Run(ctx, "restic", args, m.resticEnv(pwFile), out)
}

// ApplyRetention runs `restic forget` with the configured keep-* flags and
// --prune when AutoPrune is set. It streams output to out.
func (m *ResticManager) ApplyRetention(ctx context.Context, tag string, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{"forget", "--verbose"}
	if tag != "" {
		args = append(args, "--tag", tag)
	}
	if m.cfg.BackupHostname != "" {
		args = append(args, "--hostname", m.cfg.BackupHostname)
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

	if m.cfg.AutoPrune {
		args = append(args, "--prune")
	}

	return m.runner.Run(ctx, "restic", args, m.resticEnv(pwFile), out)
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

	return m.runner.Run(ctx, "restic", args, m.resticEnv(pwFile), out)
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
			Tags:     s.Tags,
			Paths:    s.Paths,
		}
	}
	return result, nil
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

	return m.runner.Run(ctx, "restic", []string{"ls", snapshotID}, m.resticEnv(pwFile), out)
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
	return int64(out.TotalSize), nil
}

// Restore restores the given snapshot to targetPath via `restic restore --target`.
func (m *ResticManager) Restore(ctx context.Context, snapshotID, sourcePath, targetPath string, out chan<- StreamLine) error {
	pwFile, cleanup, err := m.withPasswordFile()
	if err != nil {
		return err
	}
	defer cleanup()

	// Backups store the stack's absolute path, so a plain `restore <id> --target X`
	// recreates that absolute tree *under* X (X/home/.../stack/...). Use restic's
	// `<snapshotID>:<subfolder>` form to strip the stored source prefix so the
	// snapshot contents land directly in targetPath (true in-place restore).
	ref := snapshotID
	if sourcePath != "" {
		ref = snapshotID + ":" + sourcePath
	}
	args := []string{"restore", ref, "--target", targetPath, "--verbose"}
	return m.runner.Run(ctx, "restic", args, m.resticEnv(pwFile), out)
}
