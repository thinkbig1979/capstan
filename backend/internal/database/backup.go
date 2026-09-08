package database

import (
	"fmt"
	"math"
	"strings"
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

// GetBackupRunsFiltered returns one page of backup runs plus the total number
// of runs matching the filters (ignoring the page window), for GET
// /backups/history.
//
// It mirrors GetUpdateHistory in update_history.go: clauses accumulate into
// whereClauses with every value bound as a `?` parameter, the COUNT(*) uses the
// same clause as the page query, and the page is taken newest-first. No caller
// value is ever concatenated into SQL.
//
// GetBackupRuns(limit) is deliberately left alone rather than made to delegate
// here: it returns no total and has its own callers.
func (d *DB) GetBackupRunsFiltered(filters models.BackupHistoryFilters) ([]models.BackupRun, int, error) {
	var whereClauses []string
	var args []interface{}

	if filters.Status != "" {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, filters.Status)
	}
	if filters.Kind != "" {
		whereClauses = append(whereClauses, "kind = ?")
		args = append(args, filters.Kind)
	}
	if filters.Trigger != "" {
		whereClauses = append(whereClauses, "trigger = ?")
		args = append(args, filters.Trigger)
	}
	// Same text-comparison hazard as GetUpdateHistory: normalise the
	// caller's bound to UTC before formatting, or it selects by spelling.
	if filters.From != nil {
		whereClauses = append(whereClauses, "started_at >= ?")
		args = append(args, filters.From.UTC().Format(time.RFC3339))
	}
	if filters.To != nil {
		whereClauses = append(whereClauses, "started_at <= ?")
		args = append(args, filters.To.UTC().Format(time.RFC3339))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM backup_runs " + whereClause
	if err := d.db.QueryRow(countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit := filters.Limit
	if limit <= 0 {
		limit = 25
	}
	page := filters.Page
	if page <= 0 {
		page = 1
	}

	// (page-1)*limit is arithmetic on a client-supplied page, and int wraps.
	// OBSERVED before this guard: page = MaxInt with limit = 50 produces
	// offset -100, SQLite reads a negative OFFSET as no offset at all, and the
	// call returns page ONE's rows while the caller believes it asked for a
	// page far past the end — a wrong answer served as a correct one.
	//
	// Detecting the overflow beats capping page: a page legitimately past the
	// end (?page=999999999 over a small table) already returns empty, and
	// returning empty here keeps that answer consistent instead of inventing a
	// maximum page number. total is left untouched, so the caller still learns
	// the real size of the match set.
	if page-1 > math.MaxInt/limit {
		return nil, total, nil
	}
	offset := (page - 1) * limit

	// Column order must stay identical to the Scan targets below, and to
	// GetBackupRuns' list, which this deliberately repeats rather than shares:
	// that function keeps its existing callers and is left untouched here.
	query := `SELECT id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed, bytes_added, error_message
	          FROM backup_runs ` + whereClause + ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(args, limit, offset)

	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var runs []models.BackupRun
	for rows.Next() {
		var r models.BackupRun
		err := rows.Scan(&r.ID, &r.Kind, &r.Trigger, &r.Status, &r.StartedAt, &r.FinishedAt,
			&r.StacksTotal, &r.StacksOK, &r.StacksFailed, &r.BytesAdded, &r.ErrorMessage)
		if err != nil {
			return nil, 0, err
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reading backup runs: %w", err)
	}
	return runs, total, nil
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
