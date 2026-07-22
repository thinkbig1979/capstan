package database

import (
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
