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

// Container policies are keyed on the container NAME (agent-os-ygyv). They
// used to be keyed on the Docker container ID, which every update and every
// compose redeploy replaces, so a policy matched nothing after its container's
// first update. A row written before the change still holds an ID, and the
// methods below are what services.SchedulerService uses to re-key such rows:
// it needs Docker to learn a live container's name, which a SQL migration
// cannot reach.

// legacyContainerPolicyWhere selects container rows whose target is a full
// Docker container ID: exactly 64 lowercase hex characters.
const legacyContainerPolicyWhere = `target_type = 'container' AND length(target_id) = 64 AND target_id NOT GLOB '*[^0-9a-f]*'`

// LegacyContainerPolicies returns the container policies still keyed on a
// container ID.
func (d *DB) LegacyContainerPolicies() ([]models.AutoUpdatePolicy, error) {
	rows, err := d.db.Query(`SELECT ` + autoUpdatePolicyColumns + ` FROM auto_update_policies WHERE ` + legacyContainerPolicyWhere + ` ORDER BY created_at, id`)
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
		return nil, fmt.Errorf("reading container-ID auto-update policies: %w", err)
	}
	return policies, nil
}

// LastKnownContainerName returns the name update_history last recorded for a
// container ID, or "" when it has none. It names a container that no longer
// exists: an update replaced it, which is how its policy stopped matching.
func (d *DB) LastKnownContainerName(containerID string) (string, error) {
	var name string
	err := d.db.QueryRow(`SELECT container_name FROM update_history
		WHERE container_id = ? AND container_name != ''
		ORDER BY started_at DESC LIMIT 1`, containerID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return name, err
}

// RekeyOutcome is what RekeyContainerPolicy did.
type RekeyOutcome string

const (
	// RekeyMoved: the row now has the name as its target.
	RekeyMoved RekeyOutcome = "moved"
	// RekeyDropped: a policy for that name already existed and was kept; the
	// ID-keyed row was deleted. The name's row is the newer one: it was saved
	// through a screen that already keyed on the name, or re-keyed from the
	// container that is live now.
	RekeyDropped RekeyOutcome = "dropped"
	// RekeyGone: the row changed or went away meanwhile; nothing was written.
	RekeyGone RekeyOutcome = "gone"
)

// RekeyContainerPolicy moves one ID-keyed container policy (by row id, still
// holding fromTargetID) to the container name. One transaction, so a policy
// for the name saved meanwhile cannot meet a concurrent move on the UNIQUE
// constraint.
func (d *DB) RekeyContainerPolicy(id, fromTargetID, name string) (RekeyOutcome, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }() //nolint:errcheck // After Commit this is sql.ErrTxDone by design; on an earlier error that error is what is returned.

	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM auto_update_policies WHERE target_type = 'container' AND target_id = ?`, name).Scan(&existing); err != nil {
		return "", err
	}

	outcome := RekeyMoved
	query := `UPDATE auto_update_policies SET target_id = ? WHERE id = ? AND target_type = 'container' AND target_id = ?`
	args := []any{name, id, fromTargetID}
	if existing > 0 {
		outcome = RekeyDropped
		query = `DELETE FROM auto_update_policies WHERE id = ? AND target_type = 'container' AND target_id = ?`
		args = args[1:]
	}
	res, err := tx.Exec(query, args...)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return RekeyGone, nil
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return outcome, nil
}
