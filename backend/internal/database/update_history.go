package database

import (
	"database/sql"
	"strings"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) GetUpdateHistory(filters models.UpdateHistoryFilters) ([]models.UpdateHistoryEntry, int, error) {
	var whereClauses []string
	var args []interface{}

	if filters.Status != "" {
		whereClauses = append(whereClauses, "status = ?")
		args = append(args, filters.Status)
	}
	if filters.Trigger != "" {
		whereClauses = append(whereClauses, "trigger = ?")
		args = append(args, filters.Trigger)
	}
	if filters.ContainerID != "" {
		whereClauses = append(whereClauses, "container_id = ?")
		args = append(args, filters.ContainerID)
	}
	if filters.StackID != "" {
		whereClauses = append(whereClauses, "stack_id = ?")
		args = append(args, filters.StackID)
	}
	if filters.From != nil {
		whereClauses = append(whereClauses, "started_at >= ?")
		args = append(args, filters.From.Format(time.RFC3339))
	}
	if filters.To != nil {
		whereClauses = append(whereClauses, "started_at <= ?")
		args = append(args, filters.To.Format(time.RFC3339))
	}

	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	var total int
	countQuery := "SELECT COUNT(*) FROM update_history " + whereClause
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
	offset := (page - 1) * limit

	query := `SELECT id, container_id, container_name, COALESCE(stack_id, ''), COALESCE(stack_name, ''),
	          image, old_digest, new_digest, old_image_ref, new_image_ref,
	          status, trigger, started_at, completed_at, duration_ms, error_message
	          FROM update_history ` + whereClause + ` ORDER BY started_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(args, limit, offset)

	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []models.UpdateHistoryEntry
	for rows.Next() {
		var e models.UpdateHistoryEntry
		var stackID, stackName sql.NullString
		var oldDigest, newDigest, oldImageRef, newImageRef sql.NullString
		var completedAt sql.NullString
		var durationMs sql.NullInt64
		var errorMsg sql.NullString

		err := rows.Scan(&e.ID, &e.ContainerID, &e.ContainerName, &stackID, &stackName,
			&e.Image, &oldDigest, &newDigest, &oldImageRef, &newImageRef,
			&e.Status, &e.Trigger, &e.StartedAt, &completedAt, &durationMs, &errorMsg)
		if err != nil {
			return nil, 0, err
		}

		if stackID.Valid && stackID.String != "" {
			e.StackID = &stackID.String
		}
		if stackName.Valid && stackName.String != "" {
			e.StackName = &stackName.String
		}
		if oldDigest.Valid {
			e.OldDigest = &oldDigest.String
		}
		if newDigest.Valid {
			e.NewDigest = &newDigest.String
		}
		if oldImageRef.Valid {
			e.OldImageRef = &oldImageRef.String
		}
		if newImageRef.Valid {
			e.NewImageRef = &newImageRef.String
		}
		if completedAt.Valid {
			e.CompletedAt = &completedAt.String
		}
		if durationMs.Valid {
			e.DurationMs = &durationMs.Int64
		}
		if errorMsg.Valid {
			e.ErrorMessage = &errorMsg.String
		}

		entries = append(entries, e)
	}
	return entries, total, nil
}

func (d *DB) InsertUpdateHistory(entry *models.UpdateHistoryEntry) error {
	query := `INSERT INTO update_history (id, container_id, container_name, stack_id, stack_name,
	          image, old_digest, new_digest, old_image_ref, new_image_ref,
	          status, trigger, started_at, completed_at, duration_ms, error_message)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var stackID, stackName interface{}
	if entry.StackID != nil {
		stackID = *entry.StackID
	}
	if entry.StackName != nil {
		stackName = *entry.StackName
	}

	_, err := d.db.Exec(query, entry.ID, entry.ContainerID, entry.ContainerName,
		stackID, stackName, entry.Image,
		entry.OldDigest, entry.NewDigest, entry.OldImageRef, entry.NewImageRef,
		entry.Status, entry.Trigger, entry.StartedAt,
		entry.CompletedAt, entry.DurationMs, entry.ErrorMessage)
	return err
}

func (d *DB) UpdateUpdateHistory(id string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	allowedColumns := map[string]bool{
		"status":        true,
		"completed_at":  true,
		"duration_ms":   true,
		"error_message": true,
		"new_digest":    true,
		"new_image_ref": true,
	}

	var setClauses []string
	var args []interface{}
	for key, val := range updates {
		if !allowedColumns[key] {
			continue
		}
		setClauses = append(setClauses, key+" = ?")
		args = append(args, val)
	}

	if len(setClauses) == 0 {
		return nil
	}

	args = append(args, id)

	//nolint:gosec // setClauses is built only from keys present in the allowedColumns allowlist above (6 fixed names); values are bound parameters via args, never concatenated
	query := "UPDATE update_history SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	_, err := d.db.Exec(query, args...)
	return err
}

func (d *DB) DeleteUpdateHistoryOlderThan(before time.Time) (int, error) {
	result, err := d.db.Exec("DELETE FROM update_history WHERE completed_at < ?", before.Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

func (d *DB) GetUpdateStats() (enabledContainers int, last7Days int, last30Days int, err error) {
	err = d.db.QueryRow("SELECT COUNT(*) FROM auto_update_policies WHERE enabled = TRUE").Scan(&enabledContainers)
	if err != nil {
		return
	}

	err = d.db.QueryRow("SELECT COUNT(*) FROM update_history WHERE status = 'success' AND started_at >= datetime('now', '-7 days')").Scan(&last7Days)
	if err != nil {
		return
	}

	err = d.db.QueryRow("SELECT COUNT(*) FROM update_history WHERE status = 'success' AND started_at >= datetime('now', '-30 days')").Scan(&last30Days)
	if err != nil {
		return
	}

	return
}
