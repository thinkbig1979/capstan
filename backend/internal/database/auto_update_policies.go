package database

import (
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) GetAutoUpdatePolicies() ([]models.AutoUpdatePolicy, error) {
	query := `SELECT id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at
	          FROM auto_update_policies ORDER BY target_type, target_id`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.AutoUpdatePolicy
	for rows.Next() {
		var p models.AutoUpdatePolicy
		err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled,
			&p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, nil
}

func (d *DB) GetAutoUpdatePolicy(targetType, targetID string) (*models.AutoUpdatePolicy, error) {
	var p models.AutoUpdatePolicy
	query := `SELECT id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at
	          FROM auto_update_policies WHERE target_type = ? AND target_id = ?`
	err := d.db.QueryRow(query, targetType, targetID).Scan(&p.ID, &p.TargetType, &p.TargetID,
		&p.Enabled, &p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
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

func (d *DB) DeleteAutoUpdatePolicy(targetType, targetID string) error {
	_, err := d.db.Exec("DELETE FROM auto_update_policies WHERE target_type = ? AND target_id = ?", targetType, targetID)
	return err
}

func (d *DB) GetEnabledAutoUpdatePolicies() ([]models.AutoUpdatePolicy, error) {
	query := `SELECT id, target_type, target_id, enabled, consecutive_failures, paused, created_at, updated_at
	          FROM auto_update_policies WHERE enabled = TRUE AND paused = FALSE`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []models.AutoUpdatePolicy
	for rows.Next() {
		var p models.AutoUpdatePolicy
		err := rows.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Enabled,
			&p.ConsecutiveFailures, &p.Paused, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, nil
}
