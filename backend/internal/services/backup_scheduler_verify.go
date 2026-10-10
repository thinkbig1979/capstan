package services

import (
	"context"
	"errors"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// SettingBackupVerifyWeekly is the settings key for the weekly scheduled
// repository check (agent-os-ffaj). An absent row means DefaultVerifyWeekly,
// so no migration seeds it.
const SettingBackupVerifyWeekly = "backup_verify_weekly"

// DefaultVerifyWeekly is on: a repository that has silently lost its data is
// the failure this check exists to surface, and an opt-in check is one most
// installs never run.
const DefaultVerifyWeekly = true

// scheduledVerifyEvery is how long a finished check (success or failed) keeps
// the scheduled one from running again.
const scheduledVerifyEvery = 7 * 24 * time.Hour

// errVerifyInProgress is the reason a scheduled backup is skipped when it
// falls due while this scheduler's own weekly check is still running.
var errVerifyInProgress = errors.New("the weekly repository check was still running")

// VerifyLauncher starts a repository verification run and returns a channel
// that is closed when the run has finished. *BackupRunnerRegistry implements
// it; it is an interface here because the registry is built by the backup
// handler, after the scheduler.
type VerifyLauncher interface {
	LaunchVerify(subset, trigger string) (runID string, done <-chan struct{}, err error)
}

// VerifyWeeklyEnabled reports the weekly-check setting. It has no environment
// variable, so the stored row or the default decides.
func VerifyWeeklyEnabled(db *database.DB) (bool, error) {
	return resolveBoolSetting(db, SettingBackupVerifyWeekly, "", DefaultVerifyWeekly)
}

// SetVerifier wires the launcher the weekly repository check runs through.
// Without it (nil) scheduled cycles run backups only.
func (s *BackupSchedulerService) SetVerifier(v VerifyLauncher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifier = v
}

// maybeVerify runs the weekly repository check at the end of a scheduled
// backup cycle when it is on and due, and waits for it to finish.
//
// Waiting is the point: the cycle keeps the single-flight flag for the whole
// check, so no scheduled backup starts while `restic check` holds the
// repository's exclusive lock (a backup started then exits 11; OBSERVED with
// restic 0.18.0 and 0.19.1). A backup that falls due meanwhile is skipped with
// a history row (see beginCycle). The wait ends early when the scheduler stops;
// the run itself belongs to the registry, which drains it at shutdown, and its
// own deadline (verifyTimeout) bounds how long it can hold the lock.
//
// Due means no check ENDED in success or failed in the last
// scheduledVerifyEvery. An interrupted or skipped check does not count, so the
// next cycle tries again (HasFinishedVerifySince). There is no catch-up beyond
// that: weeks missed while the process was down produce one check, at the
// first cycle after they pass.
func (s *BackupSchedulerService) maybeVerify(ctx context.Context) {
	s.mu.Lock()
	v := s.verifier
	s.mu.Unlock()
	if v == nil {
		return
	}

	on, err := VerifyWeeklyEnabled(s.db)
	if err != nil {
		s.logger.Error("Weekly repository check not run: its setting could not be read", "error", err)
		return
	}
	if !on {
		return
	}
	recent, err := s.db.HasFinishedVerifySince(time.Now().Add(-scheduledVerifyEvery))
	if err != nil {
		s.logger.Error("Weekly repository check not run: the last check could not be read", "error", err)
		return
	}
	if recent {
		return
	}

	s.mu.Lock()
	s.verifying = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.verifying = false
		s.mu.Unlock()
	}()

	runID, done, err := v.LaunchVerify("", TriggerScheduled)
	if err != nil {
		s.logger.Warn("Weekly repository check could not start", "error", err)
		return
	}
	s.logger.Info("Weekly repository check started", "run_id", runID)
	select {
	case <-done:
	case <-ctx.Done():
	}
}
