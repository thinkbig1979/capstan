package database

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

func (d *DB) LogAction(log models.ActionLog) error {
	// action_log is a denormalized, append-only audit record: user_id and
	// stack_id are plain columns with no foreign keys (see migration v9), so
	// that deleting a user or a stack never erases the history of what they
	// did. Sentinel actor labels like "anonymous" or "system" are legitimate
	// values here and are stored verbatim. stack_id is still normalized to
	// NULL for actions with no associated stack, so it stays meaningful
	// ("no stack" vs. an empty string) even without a constraint enforcing it.
	var stackID interface{}
	if log.StackID != "" {
		stackID = log.StackID
	}
	query := `INSERT INTO action_log (id, user_id, stack_id, action, detail, request_id, created_at)
	          VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err := d.db.Exec(query, log.ID, log.UserID, stackID, log.Action, log.Detail, log.RequestID, log.CreatedAt)
	return err
}

func (d *DB) GetActionsByStack(stackID string, limit int) ([]models.ActionLog, error) {
	query := `SELECT id, user_id, stack_id, action, detail, request_id, created_at
	          FROM action_log WHERE stack_id = ? ORDER BY created_at DESC LIMIT ?`
	rows, err := d.db.Query(query, stackID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		var action models.ActionLog
		var stackID, requestID sql.NullString
		err := rows.Scan(&action.ID, &action.UserID, &stackID, &action.Action, &action.Detail, &requestID, &action.CreatedAt)
		if err != nil {
			return nil, err
		}
		action.StackID = stackID.String
		action.RequestID = requestID.String
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading actions for stack: %w", err)
	}
	return actions, nil
}

func (d *DB) GetRecentActions(limit int) ([]models.ActionLog, error) {
	query := `SELECT id, user_id, stack_id, action, detail, request_id, created_at
	          FROM action_log ORDER BY created_at DESC LIMIT ?`
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		var action models.ActionLog
		var stackID, requestID sql.NullString
		err := rows.Scan(&action.ID, &action.UserID, &stackID, &action.Action, &action.Detail, &requestID, &action.CreatedAt)
		if err != nil {
			return nil, err
		}
		action.StackID = stackID.String
		action.RequestID = requestID.String
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading recent actions: %w", err)
	}
	return actions, nil
}

// deleteOldActionLogsStmt is a named constant for the same reason as its two
// siblings in retention.go: the floor guard's negative control runs this exact
// statement unguarded, and must not drift away from what production issues.
const deleteOldActionLogsStmt = `DELETE FROM action_log WHERE created_at < datetime('now', '-' || ? || ' days')`

// DeleteOldActionLogs removes action_log rows older than retentionDays.
//
// A retention below MinRetentionDays is refused rather than clamped; see
// errBelowRetentionFloor (retention.go) for why, and for what retentionDays = 0
// does to this statement.
func (d *DB) DeleteOldActionLogs(retentionDays int) error {
	if err := errBelowRetentionFloor(retentionDays); err != nil {
		return err
	}
	_, err := d.db.Exec(deleteOldActionLogsStmt, retentionDays)
	return err
}

// ActionLogFilter narrows an audit-log query. Empty fields are ignored.
type ActionLogFilter struct {
	Action   string // exact action match
	Search   string // substring match on detail or action
	DateFrom string // inclusive lower bound, "YYYY-MM-DD" (compared on UTC date)
	DateTo   string // inclusive upper bound, "YYYY-MM-DD"
}

func (d *DB) ListActionLogsPaginated(limit, offset int) ([]models.ActionLog, int, error) {
	return d.ListActionLogsFiltered(limit, offset, ActionLogFilter{})
}

// ListActionLogsFiltered returns a page of audit-log entries matching the filter,
// along with the total count of matching rows (not just the returned page).
func (d *DB) ListActionLogsFiltered(limit, offset int, f ActionLogFilter) ([]models.ActionLog, int, error) {
	var where []string
	var args []interface{}
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if f.Search != "" {
		where = append(where, "(detail LIKE ? OR action LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like)
	}
	// Compare on the leading date portion ("YYYY-MM-DD") of the stored timestamp;
	// this is independent of the driver's time format and SQLite's date() parsing.
	if f.DateFrom != "" {
		where = append(where, "substr(created_at, 1, 10) >= ?")
		args = append(args, f.DateFrom)
	}
	if f.DateTo != "" {
		where = append(where, "substr(created_at, 1, 10) <= ?")
		args = append(args, f.DateTo)
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM action_log`+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT id, user_id, stack_id, action, detail, request_id, created_at
	          FROM action_log` + whereClause + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		var action models.ActionLog
		var stackID, requestID sql.NullString
		err := rows.Scan(&action.ID, &action.UserID, &stackID, &action.Action, &action.Detail, &requestID, &action.CreatedAt)
		if err != nil {
			return nil, 0, err
		}
		action.StackID = stackID.String
		action.RequestID = requestID.String
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("reading action log page: %w", err)
	}
	return actions, total, nil
}

// DistinctActionLogActions returns the unique action names present in the audit
// log, ordered alphabetically, for populating the filter dropdown.
func (d *DB) DistinctActionLogActions() ([]string, error) {
	rows, err := d.db.Query(`SELECT DISTINCT action FROM action_log ORDER BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]string, 0)
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		actions = append(actions, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading distinct action log actions: %w", err)
	}
	return actions, nil
}
