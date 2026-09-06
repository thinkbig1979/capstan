package database

import (
	"fmt"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) GetCachedUpdates() ([]models.CachedUpdate, error) {
	query := `SELECT id, container_id, container_name, image, image_ref, state,
	          COALESCE(stack_id, ''), COALESCE(project_name, ''), COALESCE(service_name, ''),
	          is_compose, local_digest, remote_digest, scanned_at
	          FROM cached_updates ORDER BY container_name`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var updates []models.CachedUpdate
	for rows.Next() {
		var u models.CachedUpdate
		var stackID, projectName, serviceName string
		err := rows.Scan(&u.ID, &u.ContainerID, &u.ContainerName, &u.Image, &u.ImageRef,
			&u.State, &stackID, &projectName, &serviceName,
			&u.IsCompose, &u.LocalDigest, &u.RemoteDigest, &u.ScannedAt)
		if err != nil {
			return nil, err
		}
		if stackID != "" {
			u.StackID = stackID
		}
		if projectName != "" {
			u.ProjectName = projectName
		}
		if serviceName != "" {
			u.ServiceName = serviceName
		}
		updates = append(updates, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading cached updates: %w", err)
	}
	return updates, nil
}

func (d *DB) SetCachedUpdates(updates []models.CachedUpdate) error {
	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM cached_updates"); err != nil {
		// Rollback error is secondary to the exec error already being
		// returned; the tx is abandoned either way.
		_ = tx.Rollback()
		return fmt.Errorf("clear cached updates: %w", err)
	}

	for _, u := range updates {
		_, err := tx.Exec(`INSERT INTO cached_updates (id, container_id, container_name, image, image_ref, state,
		                  stack_id, project_name, service_name, is_compose, local_digest, remote_digest, scanned_at)
		                  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			u.ID, u.ContainerID, u.ContainerName, u.Image, u.ImageRef, u.State,
			u.StackID, u.ProjectName, u.ServiceName, u.IsCompose,
			u.LocalDigest, u.RemoteDigest, u.ScannedAt)
		if err != nil {
			// Rollback error is secondary to the exec error already being
			// returned; the tx is abandoned either way.
			_ = tx.Rollback()
			return fmt.Errorf("insert cached update: %w", err)
		}
	}

	return tx.Commit()
}

func (d *DB) ClearCachedUpdates() error {
	_, err := d.db.Exec("DELETE FROM cached_updates")
	return err
}

// DeleteCachedUpdate removes a single row from cached_updates by containerID.
// Used by the update apply paths to converge the list immediately after a
// verified update or confirmed no-change (finding #4).
func (d *DB) DeleteCachedUpdate(containerID string) error {
	_, err := d.db.Exec("DELETE FROM cached_updates WHERE container_id = ?", containerID)
	return err
}
