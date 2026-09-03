package services

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// RcloneManager wraps rclone operations for cloud sync and restore.
// It uses the same commandRunner seam as ResticManager so that both
// real exec calls and fake test runners are interchangeable.
type RcloneManager struct {
	cfg    BackupConfig
	runner commandRunner
	logger *slog.Logger
}

// NewRcloneManager creates an RcloneManager using the real exec-based runner.
func NewRcloneManager(cfg BackupConfig, logger *slog.Logger) *RcloneManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &RcloneManager{
		cfg:    cfg,
		runner: &execRunner{},
		logger: logger.With("component", "rclone-manager"),
	}
}

// NewRcloneManagerForTest creates an RcloneManager with an injected runner.
// It is intended for use in external test packages (e.g. integrationtest)
// where the unexported newRcloneManagerWithRunner constructor is inaccessible.
func NewRcloneManagerForTest(cfg BackupConfig, runner CommandRunner, logger *slog.Logger) *RcloneManager {
	return newRcloneManagerWithRunner(cfg, runner, logger)
}

// newRcloneManagerWithRunner is used in tests to inject a fake runner.
func newRcloneManagerWithRunner(cfg BackupConfig, runner commandRunner, logger *slog.Logger) *RcloneManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &RcloneManager{
		cfg:    cfg,
		runner: runner,
		logger: logger.With("component", "rclone-manager"),
	}
}

// TestConnectivity checks that the configured rclone remote is reachable by
// running `rclone lsd <remote>: --max-depth 1`. Returns nil on success.
func (m *RcloneManager) TestConnectivity(ctx context.Context, remote string) error {
	if remote == "" {
		remote = m.cfg.RcloneRemote
	}
	if remote == "" {
		return fmt.Errorf("rclone remote is not configured")
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"lsd", remote + ":", "--max-depth", "1"}

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()

	if err := m.runner.Run(ctx, "rclone", args, nil, out); err != nil {
		return fmt.Errorf("cannot connect to remote %q: %w", remote, err)
	}
	close(out)
	return nil
}

// syncOptions returns the common rclone flags used by Sync and RestoreRepo.
func syncOptions(transfers int) []string {
	t := transfers
	if t <= 0 {
		t = 4
	}
	return []string{
		"--progress",
		"--links",
		"--transfers", fmt.Sprintf("%d", t),
		"--retries", "3",
		"--low-level-retries", "10",
		"--stats", "30s",
		"--stats-one-line",
		"--verbose",
	}
}

// Sync copies the local restic repository at repoPath to the rclone destination
// remote:path. It retries up to retries times with linear backoff (30s * attempt).
// If retries <= 0 the engine default of 3 is used. Output is streamed to out.
func (m *RcloneManager) Sync(ctx context.Context, repoPath, remote, path string, transfers, retries int, out chan<- StreamLine) error {
	if remote == "" {
		remote = m.cfg.RcloneRemote
	}
	if path == "" {
		path = m.cfg.RclonePath
	}
	if transfers <= 0 {
		transfers = m.cfg.RcloneTransfers
	}
	if retries < 1 {
		retries = 3
	}

	destination := fmt.Sprintf("%s:%s", remote, path)
	m.logger.Info("Starting rclone sync", "source", repoPath, "destination", destination, "transfers", transfers)

	args := append([]string{"sync"}, syncOptions(transfers)...)
	args = append(args, repoPath, destination)

	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		m.logger.Info("Sync attempt", "attempt", attempt, "of", retries)
		if err := m.runner.Run(ctx, "rclone", args, nil, out); err != nil {
			lastErr = err
			m.logger.Warn("Sync attempt failed", "attempt", attempt, "error", err)
			if attempt < retries {
				wait := time.Duration(attempt*30) * time.Second
				m.logger.Info("Waiting before retry", "wait", wait)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
			}
			continue
		}
		m.logger.Info("Sync completed successfully")
		return nil
	}

	return fmt.Errorf("sync failed after %d attempts: %w", retries, lastErr)
}

// probeRestoreSource confirms remote:path is a genuine restic repository
// before RestoreRepo is allowed to run rclone sync against it, by positively
// asserting the presence of the `config` object every restic repository
// writes at its root (written once by `restic init`, never deleted). This is
// deliberately NOT an emptiness check: `rclone lsf <remote>:<path>/config` is
// a single-object listing, so it costs O(1) regardless of repository size
// (an emptiness check via e.g. `rclone size` would have to walk the whole
// tree), and it also rejects "right bucket, wrong prefix" -- a path that
// exists and holds unrelated data still has no config object at the exact
// expected location, whereas an emptiness check on that path would see
// something there and pass it.
//
// What this guard claims, precisely -- the distinction matters for what is
// safe to build on top of it:
//   - IT CLAIMS: "this source is not obviously empty or absent, so deleting
//     the local repo to mirror it is not obviously destroying data for
//     nothing." Existence of a config object is sufficient for this claim,
//     and is all this guard asserts.
//   - IT DOES NOT CLAIM: "this restore will produce a working repository."
//     A truncated/interrupted upload, or a `config` object left over from an
//     unrelated repository at the same path, both have a config object
//     present and pass this probe, yet neither restores anything usable.
//     Confirming restorability would mean decrypting with the repository
//     password, turning a near-free listing into a heavy, credentialed
//     operation -- deliberately out of scope here. Do not extend this
//     guard's pass to imply that stronger guarantee.
//
// OBSERVED 2026-09-03 (local backend, rclone v1.60.1-DEV): this exits 0 and
// prints "config" when the object is present, and exits 3 ("directory not
// found") for every negative case tried -- an empty-but-existing directory,
// a directory holding unrelated files at a wrong prefix, and a nonexistent
// path entirely.
//
// OBSERVED 2026-09-03 (object-store emulation: `rclone serve s3` on the
// pinned rclone v1.74.4 binary, checksum-verified against the Dockerfile's
// RCLONE_SHA256_AMD64, driven with the exact syncOptions() flag set): a
// wrong prefix inside a valid, reachable bucket ALSO exits 0 and deletes the
// local repo -- a prefix with no keys is a successful empty listing on an
// object store, not an error, so there is no directory-not-found signal to
// catch there. A nonexistent bucket instead exits 3 and refuses untouched,
// and a correct prefix restores correctly, confirming the instrument
// discriminates rather than trivially passing or failing every case. This is
// measured on rclone's own S3 emulation (marked Experimental upstream), not
// against real AWS/B2/GCS -- no credentials were available to confirm there
// -- but it demonstrates protocol-level object-store behaviour (an empty
// prefix is an empty listing, not an error), not something specific to the
// emulator.
//
// A missing/unreachable source may therefore surface as different non-zero
// exits depending on backend (rclone's own taxonomy reserves 3 for
// "directory not found" and 5 for temporary/connection errors); this method
// does not branch on the specific code. ANY non-nil error means "not
// confirmed as a restic repository", and the caller must refuse rather than
// proceed. Fail closed.
func (m *RcloneManager) probeRestoreSource(ctx context.Context, remote, path string) error {
	configPath := strings.TrimSuffix(path, "/")
	if configPath == "" {
		configPath = "config"
	} else {
		configPath += "/config"
	}
	target := fmt.Sprintf("%s:%s", remote, configPath)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"lsf", target}

	out := make(chan StreamLine, 32)
	go func() {
		for range out {
		}
	}()

	if err := m.runner.Run(ctx, "rclone", args, nil, out); err != nil {
		return fmt.Errorf("refusing DR restore: %s does not look like a restic repository (no config object found): %w", target, err)
	}
	close(out)
	return nil
}

// RestoreRepo copies the remote rclone repository (remote:path) to localPath
// via `rclone sync`. This is the Stage 3 DR restore: fetch the restic
// repository from cloud storage so that `restic restore` can be run against
// it locally.
//
// rclone sync deliberately stays the download-direction verb (a `copy`-based
// fix was tried and reverted -- see agent-os-h0my's history): `copy` cannot
// remove files, so it cannot reconcile a stale local `config` against an
// incoming different-key-lineage repository (restic then refuses to open the
// merged repo at all), and it silently resurrects snapshots a real `restic
// forget --prune` had already removed from the remote, with `restic check`
// printing green over the resurrection. sync's own destructive potential is
// instead bounded by two independent, fail-closed guards run before it:
//
//  1. probeRestoreSource positively confirms the source is not obviously
//     empty or absent (a config object at the exact path) before sync is
//     allowed to run at all. This catches an empty-but-existing source and a
//     right-bucket-wrong-prefix source -- both OBSERVED to make sync exit 0
//     while deleting every local snapshot; see probeRestoreSource's doc
//     comment for the measurements. It does NOT confirm the source restores
//     to a working repository -- see the same comment for why that would be
//     a different, heavier guarantee this guard deliberately doesn't make.
//  2. --backup-dir (added by the caller, RunDRRestore in backup.go) moves
//     any local-only pack/index files sync would otherwise delete into a
//     sibling directory instead, covering the probe's blind spot: a source
//     that IS a valid repository but is incomplete relative to the local one.
//
// RcloneManager.Sync (the upload direction, local -> remote) intentionally
// keeps sync with no such caller-supplied backup-dir: there the local repo is
// authoritative, and mirror-with-delete is how retention (forgotten/pruned
// snapshots) propagates offsite. Do not "fix" this asymmetry back to a
// single shared verb; the two directions differ because which side is
// authoritative differs. (BackupService.runSyncInternal guards that
// direction its own way -- see its doc comment.)
//
// It retries with the same backoff as Sync.
func (m *RcloneManager) RestoreRepo(ctx context.Context, remote, path, localPath, backupDir string, retries int, out chan<- StreamLine) error {
	if remote == "" {
		remote = m.cfg.RcloneRemote
	}
	if path == "" {
		path = m.cfg.RclonePath
	}
	if retries < 1 {
		retries = 3
	}

	transfers := m.cfg.RcloneTransfers
	if transfers <= 0 {
		transfers = 4
	}

	if err := m.probeRestoreSource(ctx, remote, path); err != nil {
		m.logger.Warn("Refusing DR restore: source failed repository probe", "error", err)
		return err
	}

	source := fmt.Sprintf("%s:%s", remote, path)
	m.logger.Info("Starting rclone restore", "source", source, "destination", localPath)

	args := append([]string{"sync"}, syncOptions(transfers)...)
	if backupDir != "" {
		args = append(args, "--backup-dir", backupDir)
	}
	args = append(args, source, localPath)

	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		m.logger.Info("Restore attempt", "attempt", attempt, "of", retries)
		if err := m.runner.Run(ctx, "rclone", args, nil, out); err != nil {
			lastErr = err
			m.logger.Warn("Restore attempt failed", "attempt", attempt, "error", err)
			if attempt < retries {
				wait := time.Duration(attempt*30) * time.Second
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
			}
			continue
		}
		m.logger.Info("RestoreRepo completed successfully")
		return nil
	}

	return fmt.Errorf("restore repo failed after %d attempts: %w", retries, lastErr)
}
