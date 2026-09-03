package services

import (
	"context"
	"fmt"
	"log/slog"
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

// RestoreRepo copies the remote rclone repository (remote:path) to localPath
// using `rclone copy`, not `rclone sync`. This is the Stage 3 DR restore:
// fetch the restic repository from cloud storage so that `restic restore`
// can be run against it locally.
//
// This direction deliberately does NOT mirror-with-delete like Sync does.
// The destination here is the live local restic repository, which on a
// healthy install is the only copy of every snapshot; `sync` would delete
// any local-only snapshot absent from the remote (e.g. taken after the last
// upload, or the remote path is reachable but empty). OBSERVED 2026-09-03
// (real restic 0.18.0 + real rclone, four arms, production flag set, local
// repo ahead of remote by one snapshot): `sync` silently destroyed the
// local-only snapshot and `restic check` still PASSED afterwards, so nothing
// downstream could detect the loss. `copy` merges the remote in instead,
// preserves local-only snapshots, still fully restores a totally-lost local
// repo, and stays valid against a remote that was since forget+pruned.
// Sync (local -> remote, upload direction) intentionally keeps `sync`: there,
// the local repo is authoritative and mirror-with-delete is how retention
// (forgotten/pruned snapshots) propagates offsite. Do not "fix" this
// asymmetry back to a single shared verb; the two directions differ because
// which side is authoritative differs.
// It retries with the same backoff as Sync.
func (m *RcloneManager) RestoreRepo(ctx context.Context, remote, path, localPath string, retries int, out chan<- StreamLine) error {
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

	source := fmt.Sprintf("%s:%s", remote, path)
	m.logger.Info("Starting rclone restore", "source", source, "destination", localPath)

	// copy, not sync: see the doc comment above for why this direction must
	// not mirror-with-delete onto the live local restic repo.
	args := append([]string{"copy"}, syncOptions(transfers)...)
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
