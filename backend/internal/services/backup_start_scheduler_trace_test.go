package services

import (
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// StartScheduler refuses to arm when the backup settings cannot be read. That
// refusal used to leave only a log line, so the backup history showed nothing
// and scheduled backups silently never ran (agent-os-awfh). It now writes one
// 'failed' backup_runs row. rotatedKeyDB is the fault because it fails the
// read while leaving the database writable, which is the case where the row
// can actually land; intactKeyDB is the same fixture with the fault removed.

func startSchedulerOn(t *testing.T, db *database.DB) (*BackupService, *fakeScheduler) {
	t.Helper()
	if err := db.SetSetting("backup_schedule_interval", "60"); err != nil {
		t.Fatalf("seed backup_schedule_interval: %v", err)
	}
	svc, _, _, _, sched := dbFaultSvc(t, db, &config.Config{DataDir: t.TempDir()})
	svc.StartScheduler()
	return svc, sched
}

func TestStartScheduler_UnreadableSettingsLeaveAFailedHistoryRow(t *testing.T) {
	db := rotatedKeyDB(t, dbFaultConfiguredRepo)
	svc, sched := startSchedulerOn(t, db)

	sched.mu.Lock()
	started := sched.started
	sched.mu.Unlock()
	if started || svc.SchedulerRunning() {
		t.Fatal("StartScheduler armed the scheduler from unreadable settings")
	}

	runs, err := db.GetBackupRuns(10)
	if err != nil {
		t.Fatalf("GetBackupRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("an unarmed scheduler left %d backup_runs row(s), want exactly 1: %+v", len(runs), runs)
	}
	r := runs[0]
	if r.Status != "failed" || r.Trigger != TriggerScheduled || r.Kind != "backup" {
		t.Fatalf("row = status %q trigger %q kind %q, want failed/scheduled/backup", r.Status, r.Trigger, r.Kind)
	}
	if !strings.Contains(r.ErrorMessage, "scheduled backups were not started") {
		t.Fatalf("row message does not name the consequence: %q", r.ErrorMessage)
	}
	if r.FinishedAt == nil {
		t.Fatal("row has no finished_at, so it reads as still running")
	}
	assertNoPlaintextLeakString(t, r.ErrorMessage)
}

// Control: the same fixture without the fault arms the scheduler and writes
// no row, so the row above is caused by the refusal and not by arming.
func TestStartScheduler_ReadableSettingsWriteNoHistoryRow(t *testing.T) {
	db := intactKeyDB(t, dbFaultConfiguredRepo)
	svc, sched := startSchedulerOn(t, db)

	sched.mu.Lock()
	started := sched.started
	sched.mu.Unlock()
	if !started || !svc.SchedulerRunning() {
		t.Fatal("control did not arm: the fixture is not the readable case it claims to be")
	}
	runs, err := db.GetBackupRuns(10)
	if err != nil {
		t.Fatalf("GetBackupRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("an armed scheduler left %d backup_runs row(s), want 0: %+v", len(runs), runs)
	}
}

func assertNoPlaintextLeakString(t *testing.T, s string) {
	t.Helper()
	if strings.Contains(s, dbFaultTestPassword) {
		t.Fatal("the history row carries the restic password in clear")
	}
}
