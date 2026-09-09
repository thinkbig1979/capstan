package services

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestBackupScheduler_FailedVerifyDoesNotBlockScheduledBackups pins the design
// decision recorded in BackupService.VerifyRepositoryData (agent-os-j1jw): a
// failing repository integrity check WARNS, it does not block.
//
// This is a regression guard, not a test of existing branching -- the
// scheduler does not consult verify state at all today, and that absence IS
// the behaviour. It is pinned because the alternative design (block scheduled
// backups on a failed verify) is a plausible-sounding change that would
// convert a transient repository fault into a silent backup outage, and
// nothing else in the suite would notice it being made.
//
// The tick assertion is exact rather than "at least one", mirroring
// TestBackupScheduler_TickTriggersRunBackup: a gate that skipped the first
// cycle and ran the second would satisfy a >= 1 assertion.
func TestBackupScheduler_FailedVerifyDoesNotBlockScheduledBackups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, err := database.NewWithMigrations(":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })

		// The repository has been verified and the verification FAILED.
		require.NoError(t, db.CreateBackupRun(&models.BackupRun{
			ID:           "run-verify-failed",
			Kind:         string(RunKindVerify),
			Trigger:      TriggerManual,
			Status:       "failed",
			StartedAt:    "2026-02-01T00:00:00Z",
			ErrorMessage: "repository integrity check failed",
		}))

		runner := &fakeBackupRunner{}
		svc := NewBackupScheduler(runner, db, nil)

		const interval = 20 * time.Millisecond
		svc.Start(interval)

		time.Sleep(interval*2 + interval/2) // 50ms: past ticks at 20ms and 40ms
		assert.Equal(t, int32(2), runner.callCount.Load(),
			"scheduled backups must still run with a failed verify on record: the check warns, it does not block")

		svc.Stop()
	})
}
