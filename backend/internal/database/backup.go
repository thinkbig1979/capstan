package database

import (
	"fmt"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// --- Backup Policies ---

func (d *DB) GetBackupPolicies() ([]models.BackupPolicy, error) {
	query := `SELECT id, target_type, target_id, enabled, stop_policy, created_at, updated_at
	          FROM backup_policies ORDER BY target_type, target_id`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.BackupPolicy
	for rows.Next() {
		var p models.BackupPolicy
		err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled, &p.StopPolicy, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading backup policies: %w", err)
	}
	return policies, nil
}

func (d *DB) GetBackupPolicy(targetID string) (*models.BackupPolicy, error) {
	var p models.BackupPolicy
	query := `SELECT id, target_type, target_id, enabled, stop_policy, created_at, updated_at
	          FROM backup_policies WHERE target_id = ?`
	err := d.db.QueryRow(query, targetID).Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled, &p.StopPolicy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *DB) GetEnabledBackupPolicies() ([]models.BackupPolicy, error) {
	query := `SELECT id, target_type, target_id, enabled, stop_policy, created_at, updated_at
	          FROM backup_policies WHERE enabled = TRUE`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.BackupPolicy
	for rows.Next() {
		var p models.BackupPolicy
		err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled, &p.StopPolicy, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading enabled backup policies: %w", err)
	}
	return policies, nil
}

func (d *DB) UpsertBackupPolicy(p *models.BackupPolicy) error {
	query := `INSERT INTO backup_policies (id, target_type, target_id, enabled, stop_policy, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(target_type, target_id) DO UPDATE SET
	              id          = excluded.id,
	              enabled     = excluded.enabled,
	              stop_policy = excluded.stop_policy,
	              updated_at  = excluded.updated_at`
	_, err := d.db.Exec(query, p.ID, p.TargetType, p.TargetID, p.Enabled, p.StopPolicy, p.CreatedAt, p.UpdatedAt)
	return err
}

func (d *DB) DeleteBackupPolicy(targetID string) error {
	_, err := d.db.Exec("DELETE FROM backup_policies WHERE target_id = ?", targetID)
	return err
}

// --- Backup Runs ---

func (d *DB) CreateBackupRun(r *models.BackupRun) error {
	query := `INSERT INTO backup_runs (id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, r.ID, r.Kind, r.Trigger, r.Status, r.StartedAt, r.FinishedAt,
		r.StacksTotal, r.StacksOK, r.StacksFailed, r.BytesAdded, r.ErrorMessage)
	return err
}

func (d *DB) UpdateBackupRun(r *models.BackupRun) error {
	query := `UPDATE backup_runs SET status = ?, finished_at = ?, stacks_total = ?, stacks_ok = ?, stacks_failed = ?, bytes_added = ?, error_message = ?
	          WHERE id = ?`
	_, err := d.db.Exec(query, r.Status, r.FinishedAt, r.StacksTotal, r.StacksOK, r.StacksFailed, r.BytesAdded, r.ErrorMessage, r.ID)
	return err
}

func (d *DB) GetBackupRuns(limit int) ([]models.BackupRun, error) {
	query := `SELECT id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message
	          FROM backup_runs ORDER BY started_at DESC LIMIT ?`
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []models.BackupRun
	for rows.Next() {
		var r models.BackupRun
		err := rows.Scan(&r.ID, &r.Kind, &r.Trigger, &r.Status, &r.StartedAt, &r.FinishedAt,
			&r.StacksTotal, &r.StacksOK, &r.StacksFailed, &r.BytesAdded, &r.ErrorMessage)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading backup runs: %w", err)
	}
	return runs, nil
}

// GetBackupRunByID fetches a single BackupRun by its primary key.
// Returns sql.ErrNoRows (wrapped) when the row does not exist.
func (d *DB) GetBackupRunByID(id string) (*models.BackupRun, error) {
	query := `SELECT id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message
	          FROM backup_runs WHERE id = ?`
	var r models.BackupRun
	err := d.db.QueryRow(query, id).Scan(
		&r.ID, &r.Kind, &r.Trigger, &r.Status, &r.StartedAt, &r.FinishedAt,
		&r.StacksTotal, &r.StacksOK, &r.StacksFailed, &r.BytesAdded, &r.ErrorMessage,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// interruptedRunErrorMessage explains, to an operator reading history, why a
// run ended without ever reaching a normal outcome.
const interruptedRunErrorMessage = "process stopped before this run completed"

// SweepInterruptedBackupRuns terminates any backup_runs row still at
// status='running'. Nothing can legitimately be running in a freshly started
// process, so every such row is left over from either a crash (the process
// died mid-run) or a restore (the row was captured mid-flight by the snapshot
// that seeded this database) — both need the same fix: a terminal status and
// a non-null finished_at, so history stops showing a backup as perpetually
// in progress.
//
// 'interrupted' (migration 12) is used rather than reusing 'failed': the run
// never reported a real outcome and may well have succeeded on the original
// instance before a restore captured it mid-flight, so labelling it "failed"
// would actively mislead an operator reading the dashboard right after
// recovering from an outage — a different, louder wrong answer than the
// "perpetually running" bug this fixes, in exactly the scenario this exists
// for (agent-os-pid review).
//
// The single UPDATE is naturally idempotent — once a row's status leaves
// 'running' it no longer matches the WHERE clause, so calling this again
// (e.g. on every startup) never re-touches an already-terminal row.
func (d *DB) SweepInterruptedBackupRuns() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := d.db.Exec(
		`UPDATE backup_runs SET status = 'interrupted', finished_at = ?, error_message = ?
		 WHERE status = 'running'`,
		now, interruptedRunErrorMessage,
	)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// --- Backup Run Items ---

func (d *DB) AddBackupRunItem(item *models.BackupRunItem) error {
	query := `INSERT INTO backup_run_items (id, run_id, stack_id, status, snapshot_id, stop_applied, duration_ms, error_message)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, item.ID, item.RunID, item.StackID, item.Status, item.SnapshotID,
		item.StopApplied, item.DurationMs, item.ErrorMessage)
	return err
}

func (d *DB) GetBackupRunItems(runID string) ([]models.BackupRunItem, error) {
	query := `SELECT id, run_id, stack_id, status, COALESCE(snapshot_id, ''), stop_applied, COALESCE(duration_ms, 0), COALESCE(error_message, '')
	          FROM backup_run_items WHERE run_id = ? ORDER BY id`
	rows, err := d.db.Query(query, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.BackupRunItem
	for rows.Next() {
		var item models.BackupRunItem
		err := rows.Scan(&item.ID, &item.RunID, &item.StackID, &item.Status, &item.SnapshotID,
			&item.StopApplied, &item.DurationMs, &item.ErrorMessage)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading backup run items: %w", err)
	}
	return items, nil
}

func (d *DB) GetLatestRunItemForStack(stackID string) (*models.BackupRunItem, error) {
	var item models.BackupRunItem
	query := `SELECT bri.id, bri.run_id, bri.stack_id, bri.status, COALESCE(bri.snapshot_id, ''), bri.stop_applied, COALESCE(bri.duration_ms, 0), COALESCE(bri.error_message, '')
	          FROM backup_run_items bri
	          JOIN backup_runs br ON br.id = bri.run_id
	          WHERE bri.stack_id = ?
	          ORDER BY br.started_at DESC
	          LIMIT 1`
	err := d.db.QueryRow(query, stackID).Scan(&item.ID, &item.RunID, &item.StackID, &item.Status,
		&item.SnapshotID, &item.StopApplied, &item.DurationMs, &item.ErrorMessage)
	if err != nil {
		return nil, err
	}
	return &item, nil
}
