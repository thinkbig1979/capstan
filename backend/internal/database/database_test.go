package database

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// newTestDB creates an in-memory DB with all migrations applied.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// --- Backup Policy tests ---

func TestGetBackupPolicies_Empty(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

func TestUpsertBackupPolicy_Insert(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p := &models.BackupPolicy{
		ID:         "bp-001",
		TargetType: "stack",
		TargetID:   "stacks~myapp",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(p))

	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, p.ID, policies[0].ID)
	assert.Equal(t, p.TargetID, policies[0].TargetID)
	assert.True(t, policies[0].Enabled)
	assert.Equal(t, "stop", policies[0].StopPolicy)
}

func TestUpsertBackupPolicy_OverwriteOnConflict(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	original := &models.BackupPolicy{
		ID:         "bp-001",
		TargetType: "stack",
		TargetID:   "stacks~myapp",
		Enabled:    false,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(original))

	// Upsert with same target_type+target_id but different values.
	updated := &models.BackupPolicy{
		ID:         "bp-001-updated",
		TargetType: "stack",
		TargetID:   "stacks~myapp",
		Enabled:    true,
		StopPolicy: "hot",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T01:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(updated))

	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1, "upsert must not create a duplicate row")
	assert.True(t, policies[0].Enabled, "enabled must be updated to true")
	assert.Equal(t, "hot", policies[0].StopPolicy, "stop_policy must be updated")
}

func TestGetBackupPolicy_ByTargetID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p := &models.BackupPolicy{
		ID:         "bp-002",
		TargetType: "stack",
		TargetID:   "stacks~web",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(p))

	got, err := db.GetBackupPolicy("stacks~web")
	require.NoError(t, err)
	assert.Equal(t, "bp-002", got.ID)

	_, err = db.GetBackupPolicy("stacks~nonexistent")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestGetEnabledBackupPolicies(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	enabled := &models.BackupPolicy{
		ID:         "bp-en",
		TargetType: "stack",
		TargetID:   "stacks~enabled",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	disabled := &models.BackupPolicy{
		ID:         "bp-dis",
		TargetType: "stack",
		TargetID:   "stacks~disabled",
		Enabled:    false,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(enabled))
	require.NoError(t, db.UpsertBackupPolicy(disabled))

	policies, err := db.GetEnabledBackupPolicies()
	require.NoError(t, err)
	require.Len(t, policies, 1)
	assert.Equal(t, "bp-en", policies[0].ID)
}

func TestDeleteBackupPolicy(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	p := &models.BackupPolicy{
		ID:         "bp-del",
		TargetType: "stack",
		TargetID:   "stacks~todelete",
		Enabled:    true,
		StopPolicy: "stop",
		CreatedAt:  "2026-05-30T00:00:00Z",
		UpdatedAt:  "2026-05-30T00:00:00Z",
	}
	require.NoError(t, db.UpsertBackupPolicy(p))

	require.NoError(t, db.DeleteBackupPolicy("stacks~todelete"))

	policies, err := db.GetBackupPolicies()
	require.NoError(t, err)
	assert.Empty(t, policies)
}

// --- Backup Run tests ---

func TestCreateBackupRun_AndGetBackupRuns(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:           "run-001",
		Kind:         "backup",
		Trigger:      "manual",
		Status:       "running",
		StartedAt:    "2026-05-30T10:00:00Z",
		StacksTotal:  3,
		StacksOK:     0,
		StacksFailed: 0,
	}
	require.NoError(t, db.CreateBackupRun(run))

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "run-001", runs[0].ID)
	assert.Equal(t, "backup", runs[0].Kind)
	assert.Equal(t, "manual", runs[0].Trigger)
	assert.Equal(t, "running", runs[0].Status)
	assert.Equal(t, 3, runs[0].StacksTotal)
}

func TestUpdateBackupRun(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:        "run-upd",
		Kind:      "sync",
		Trigger:   "scheduled",
		Status:    "running",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	finishedAt := "2026-05-30T10:05:00Z"
	run.Status = "success"
	run.FinishedAt = &finishedAt
	run.StacksTotal = 2
	run.StacksOK = 2
	run.StacksFailed = 0
	require.NoError(t, db.UpdateBackupRun(run))

	runs, err := db.GetBackupRuns(10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "success", runs[0].Status)
	assert.Equal(t, 2, runs[0].StacksOK)
	require.NotNil(t, runs[0].FinishedAt)
	assert.Equal(t, "2026-05-30T10:05:00Z", *runs[0].FinishedAt)
}

func TestGetBackupRuns_Limit(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	for _, id := range []string{"run-a", "run-b", "run-c"} {
		r := &models.BackupRun{
			ID:        id,
			Kind:      "backup",
			Trigger:   "manual",
			Status:    "success",
			StartedAt: "2026-05-30T10:00:00Z",
		}
		require.NoError(t, db.CreateBackupRun(r))
	}

	runs, err := db.GetBackupRuns(2)
	require.NoError(t, err)
	assert.Len(t, runs, 2, "limit parameter must be respected")
}

// --- Backup Run Item tests ---

func TestAddBackupRunItem_AndGetBackupRunItems(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:        "run-items",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	item := &models.BackupRunItem{
		ID:          "item-001",
		RunID:       "run-items",
		StackID:     "stacks~myapp",
		Status:      "success",
		SnapshotID:  "abc12345",
		StopApplied: true,
		DurationMs:  4200,
	}
	require.NoError(t, db.AddBackupRunItem(item))

	items, err := db.GetBackupRunItems("run-items")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "item-001", items[0].ID)
	assert.Equal(t, "stacks~myapp", items[0].StackID)
	assert.Equal(t, "abc12345", items[0].SnapshotID)
	assert.True(t, items[0].StopApplied)
	assert.Equal(t, int64(4200), items[0].DurationMs)
}

func TestGetBackupRunItems_EmptyForUnknownRun(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	items, err := db.GetBackupRunItems("nonexistent-run")
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetLatestRunItemForStack(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run1 := &models.BackupRun{
		ID:        "run-first",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-05-30T09:00:00Z",
	}
	run2 := &models.BackupRun{
		ID:        "run-second",
		Kind:      "backup",
		Trigger:   "scheduled",
		Status:    "success",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run1))
	require.NoError(t, db.CreateBackupRun(run2))

	item1 := &models.BackupRunItem{
		ID:         "item-first",
		RunID:      "run-first",
		StackID:    "stacks~myapp",
		Status:     "success",
		SnapshotID: "snap-old",
		DurationMs: 1000,
	}
	item2 := &models.BackupRunItem{
		ID:         "item-second",
		RunID:      "run-second",
		StackID:    "stacks~myapp",
		Status:     "success",
		SnapshotID: "snap-new",
		DurationMs: 800,
	}
	require.NoError(t, db.AddBackupRunItem(item1))
	require.NoError(t, db.AddBackupRunItem(item2))

	got, err := db.GetLatestRunItemForStack("stacks~myapp")
	require.NoError(t, err)
	assert.Equal(t, "item-second", got.ID, "must return the item from the most recent run")
	assert.Equal(t, "snap-new", got.SnapshotID)
}

func TestGetLatestRunItemForStack_NotFound(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	_, err := db.GetLatestRunItemForStack("stacks~nonexistent")
	assert.ErrorIs(t, err, sql.ErrNoRows)
}

func TestFKCascade_DeleteRunRemovesItems(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	run := &models.BackupRun{
		ID:        "run-cascade",
		Kind:      "backup",
		Trigger:   "manual",
		Status:    "success",
		StartedAt: "2026-05-30T10:00:00Z",
	}
	require.NoError(t, db.CreateBackupRun(run))

	for i, sid := range []string{"stacks~a", "stacks~b"} {
		item := &models.BackupRunItem{
			ID:      fmt.Sprintf("item-casc-%d", i),
			RunID:   "run-cascade",
			StackID: sid,
			Status:  "success",
		}
		require.NoError(t, db.AddBackupRunItem(item))
	}

	items, err := db.GetBackupRunItems("run-cascade")
	require.NoError(t, err)
	assert.Len(t, items, 2)

	_, err = db.db.Exec("DELETE FROM backup_runs WHERE id = ?", "run-cascade")
	require.NoError(t, err)

	items, err = db.GetBackupRunItems("run-cascade")
	require.NoError(t, err)
	assert.Empty(t, items, "deleting a run must cascade-delete its items")
}
