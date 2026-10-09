package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

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
	// created_at is ORDERed and compared as text: see storedInstant.
	_, err := d.db.Exec(query, log.ID, log.UserID, stackID, log.Action, log.Detail, log.RequestID, storedInstant(log.CreatedAt))
	return err
}

// actionLogColumns is the one column list every full-row action_log reader
// selects, in scanActionLog's target order. Three readers used to carry their
// own copies, which agreed only by convention (agent-os-rh7m, as
// agent-os-13xd did for backup_runs).
//
// detail is nullable, and a NULL scanned into a plain string fails rows.Scan
// and loses the whole list (agent-os-d1c7). No Capstan writer stores a NULL,
// so the COALESCE guards databases edited or written outside Capstan.
const actionLogColumns = `id, user_id, stack_id, action, COALESCE(detail, ''), request_id, created_at`

// scanActionLog reads one row selected with actionLogColumns, from *sql.Row
// or *sql.Rows. stack_id and request_id are nullable and read back as "".
func scanActionLog(row interface{ Scan(dest ...any) error }) (models.ActionLog, error) {
	var action models.ActionLog
	var stackID, requestID sql.NullString
	err := row.Scan(&action.ID, &action.UserID, &stackID, &action.Action, &action.Detail, &requestID, &action.CreatedAt)
	action.StackID = stackID.String
	action.RequestID = requestID.String
	return action, err
}

func (d *DB) GetActionsByStack(stackID string, limit int) ([]models.ActionLog, error) {
	query := `SELECT ` + actionLogColumns + ` FROM action_log WHERE stack_id = ? ORDER BY created_at DESC LIMIT ?`
	rows, err := d.db.Query(query, stackID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		action, err := scanActionLog(rows)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading actions for stack: %w", err)
	}
	return actions, nil
}

func (d *DB) GetRecentActions(limit int) ([]models.ActionLog, error) {
	query := `SELECT ` + actionLogColumns + ` FROM action_log ORDER BY created_at DESC LIMIT ?`
	rows, err := d.db.Query(query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		action, err := scanActionLog(rows)
		if err != nil {
			return nil, err
		}
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
//
// Unlike its siblings it takes a cutoff computed in Go, not a SQL clock: a
// datetime('now', ...) cutoff is space-separated and would be decided at the
// separator against the stored 'T'. The cutoff is bound in the same spelling
// LogAction writes (storedInstant). Before agent-os-6exk LogAction bound a raw
// time.Time, which the driver stored as t.String() in the value's own zone, so
// a row and a cutoff spelled in different zones compared wrong (agent-os-h8qa
// fixed the SQL-clock half of that).
const deleteOldActionLogsStmt = `DELETE FROM action_log WHERE created_at < ?`

// actionLogCutoff is the bound value before which action_log rows are pruned.
// It is a function, not inline, so the floor guard's negative control binds
// exactly what production binds.
func actionLogCutoff(retentionDays int) string {
	return storedInstant(time.Now().AddDate(0, 0, -retentionDays))
}

// DeleteOldActionLogs removes action_log rows older than retentionDays.
//
// A retention below MinRetentionDays is refused rather than clamped; see
// errBelowRetentionFloor (retention.go) for why, and for what retentionDays = 0
// does to this statement.
func (d *DB) DeleteOldActionLogs(retentionDays int) error {
	if err := errBelowRetentionFloor(retentionDays); err != nil {
		return err
	}
	_, err := d.db.Exec(deleteOldActionLogsStmt, actionLogCutoff(retentionDays))
	return err
}

// ActionLogFilter narrows an audit-log query. Empty fields are ignored.
type ActionLogFilter struct {
	Action   string // exact action match
	Search   string // substring match on detail or action
	DateFrom string // inclusive lower bound, "YYYY-MM-DD", a date in the server's local zone (time.Local)
	DateTo   string // inclusive upper bound, "YYYY-MM-DD", same zone
}

// localDayStart is the instant local midnight of a "YYYY-MM-DD" date begins,
// offset days later, in the spelling created_at is stored in. created_at is
// UTC (storedInstant), so the server-local date the filter is documented to
// match becomes a range of instants rather than a prefix of the text.
// time.Date normalises across a DST change, so a 23- or 25-hour day is right.
func localDayStart(date string, offsetDays int) (string, error) {
	d, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return "", fmt.Errorf("invalid date %q: %w", date, err)
	}
	return storedInstant(time.Date(d.Year(), d.Month(), d.Day()+offsetDays, 0, 0, 0, 0, time.Local)), nil
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
	// [local midnight of DateFrom, local midnight of the day after DateTo).
	if f.DateFrom != "" {
		from, err := localDayStart(f.DateFrom, 0)
		if err != nil {
			return nil, 0, err
		}
		where = append(where, "created_at >= ?")
		args = append(args, from)
	}
	if f.DateTo != "" {
		until, err := localDayStart(f.DateTo, 1)
		if err != nil {
			return nil, 0, err
		}
		where = append(where, "created_at < ?")
		args = append(args, until)
	}
	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM action_log`+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT ` + actionLogColumns + ` FROM action_log` + whereClause + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	queryArgs := append(append([]interface{}{}, args...), limit, offset)
	rows, err := d.db.Query(query, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	actions := make([]models.ActionLog, 0)
	for rows.Next() {
		action, err := scanActionLog(rows)
		if err != nil {
			return nil, 0, err
		}
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
