package database

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// autoUpdatePolicyColumns is the one column list every full-row
// auto_update_policies reader selects, in scanAutoUpdatePolicy's target
// order. Three readers used to carry their own copies, which agreed only by
// convention (agent-os-rh7m, as agent-os-13xd did for backup_runs).
const autoUpdatePolicyColumns = `id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at`

// scanAutoUpdatePolicy reads one row selected with autoUpdatePolicyColumns,
// from *sql.Row or *sql.Rows.
func scanAutoUpdatePolicy(row interface{ Scan(dest ...any) error }) (models.AutoUpdatePolicy, error) {
	var p models.AutoUpdatePolicy
	err := row.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled,
		&p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (d *DB) GetAutoUpdatePolicies() ([]models.AutoUpdatePolicy, error) {
	query := `SELECT ` + autoUpdatePolicyColumns + ` FROM auto_update_policies ORDER BY target_type, target_id`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.AutoUpdatePolicy
	for rows.Next() {
		p, err := scanAutoUpdatePolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading auto-update policies: %w", err)
	}
	return policies, nil
}

func (d *DB) GetAutoUpdatePolicy(targetType, targetID string) (*models.AutoUpdatePolicy, error) {
	query := `SELECT ` + autoUpdatePolicyColumns + ` FROM auto_update_policies WHERE target_type = ? AND target_id = ?`
	p, err := scanAutoUpdatePolicy(d.db.QueryRow(query, targetType, targetID))
	if err != nil {
		return nil, notFound(err, "auto-update policy", targetID)
	}
	return &p, nil
}

func (d *DB) UpsertAutoUpdatePolicy(policy *models.AutoUpdatePolicy) error {
	query := `INSERT OR REPLACE INTO auto_update_policies (id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, policy.ID, policy.TargetType, policy.TargetID,
		policy.Enabled, policy.ConsecutiveFailures, policy.Paused,
		policy.CreatedAt, policy.UpdatedAt)
	return err
}

// The two writers below are the auto-update pass's only writes to a policy.
// The pass reads its policies once, at its start, and an operator can turn a
// policy off or delete it while the pass runs. Writing the pass's whole row
// back with UpsertAutoUpdatePolicy (INSERT OR REPLACE) turned such a policy
// back on, or re-created it (agent-os-71sp). So the pass writes only its own
// columns, and only to the row it read while that row is still enabled and not
// paused:
//   - keyed on id, not on the target: a policy deleted and created again gets
//     a new id (handlers/updates.go upsertAutoUpdatePolicy), and must not
//     inherit the stale pass's counters;
//   - a disabled, paused or missing row matches nothing, so the write is a
//     no-op and the operator's change stands;
//   - the failure count is incremented in SQL, from the stored value, never
//     from the copy the pass read.
const autoUpdatePolicyPassGuard = `WHERE id = ? AND enabled = TRUE AND paused = FALSE`

// ResetAutoUpdatePolicyFailures zeroes a policy's failure count after a pass
// in which its updates succeeded. It reports whether a row was written.
func (d *DB) ResetAutoUpdatePolicyFailures(id, updatedAt string) (bool, error) {
	res, err := d.db.Exec(`UPDATE auto_update_policies SET consecutive_failures = 0, updated_at = ? `+autoUpdatePolicyPassGuard,
		updatedAt, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// RecordAutoUpdatePolicyFailure counts one failed pass against a policy and
// pauses it when that makes pauseAt consecutive failures. written reports
// whether a row was written; paused reports whether this write paused it.
func (d *DB) RecordAutoUpdatePolicyFailure(id, updatedAt string, pauseAt int) (written, paused bool, err error) {
	err = d.db.QueryRow(`UPDATE auto_update_policies
		SET consecutive_failures = consecutive_failures + 1,
		    paused = (consecutive_failures + 1 >= ?),
		    updated_at = ?
		`+autoUpdatePolicyPassGuard+`
		RETURNING paused`, pauseAt, updatedAt, id).Scan(&paused)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, paused, nil
}

func (d *DB) DeleteAutoUpdatePolicy(targetType, targetID string) error {
	_, err := d.db.Exec("DELETE FROM auto_update_policies WHERE target_type = ? AND target_id = ?", targetType, targetID)
	return err
}

func (d *DB) GetEnabledAutoUpdatePolicies() ([]models.AutoUpdatePolicy, error) {
	query := `SELECT ` + autoUpdatePolicyColumns + ` FROM auto_update_policies WHERE enabled = TRUE AND paused = FALSE`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.AutoUpdatePolicy
	for rows.Next() {
		p, err := scanAutoUpdatePolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading enabled auto-update policies: %w", err)
	}
	return policies, nil
}
