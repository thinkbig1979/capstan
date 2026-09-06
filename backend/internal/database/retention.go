package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// Retention bounds, shared by every history table so one operator-facing
// concept ("how long is history kept") behaves identically everywhere.
const (
	// DefaultRetentionDays applies when the setting is unset or unparseable.
	DefaultRetentionDays = 90
	// MinRetentionDays is the floor. A prune is destructive and irreversible,
	// so an absurdly low setting must not wipe the history an operator is in
	// the middle of reading. Matches the action_log floor.
	MinRetentionDays = 7
)

// Settings keys holding per-table retention. action_log keeps its original key
// so existing deployments' configured value is not silently reset.
const (
	SettingLogRetentionDays           = "max_log_retention_days"
	SettingUpdateHistoryRetentionDays = "max_update_history_retention_days"
	SettingBackupHistoryRetentionDays = "max_backup_history_retention_days"
)

// RetentionDays parses an already-read retention VALUE, applying the default
// when it is empty or unparseable and clamping to the floor.
//
// Note the name collision: this package-level function and the (*DB) method
// below are different functions sharing one name, and only the method touches
// the database. Read the receiver before citing either.
//
// This half never returns an error, and that is still correct: it is handed a
// string, so "unparseable" is a value judgement about a row that WAS read, not
// a failure to read it. The method below is where the two were previously
// conflated — it answered an unreadable database with this same default
// (agent-os-r1kc).
func RetentionDays(value string) int {
	days := DefaultRetentionDays
	trimmed := strings.TrimSpace(value)
	if trimmed != "" {
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			slog.Warn("Unparseable retention setting, using the default",
				"value", value, "default_days", DefaultRetentionDays)
			return DefaultRetentionDays
		}
		days = parsed
	}
	if days < MinRetentionDays {
		return MinRetentionDays
	}
	return days
}

// RetentionDays resolves the retention for a settings key, separating "no such
// row" from "this database could not answer".
//
// An ABSENT row keeps DefaultRetentionDays: that is the fresh-install case, and
// it is the only case the pre-agent-os-r1kc form was right about. ANY other
// error is returned, because the value goes straight into three irreversible
// DELETEs (PruneHistory) and onto the settings page (handlers.GetLogRetention),
// and a fault answered with a confident 90 silently truncates every retention an
// operator deliberately raised — the fallback can only ever be less conservative
// than the configured value, never more.
//
// The comment this replaces defended the swallow on the grounds that absence is
// ordinary. It is not, after startup: migrations.go:182, :394 and :395 seed all
// three keys with INSERT OR IGNORE, so on a started instance the row exists and
// err != nil means an I/O error, corruption, or a closed or locked database.
//
// This is the discrimination handlers/settings_read.go:26 settingOrFault already
// makes for the handler layer, expressed here because these callers are in this
// package. The error is safe to log verbatim: GetSetting only decrypts keys in
// sensitiveSettingKeys (settings.go:9-12, :21), which holds git_https_token and
// restic_password and none of the three retention keys, so no crypto output can
// reach this error.
//
// The int returned beside a non-nil error is the Go zero, deliberately not
// DefaultRetentionDays. Both in-tree callers refuse before issuing a DELETE, so
// nothing reaches a deleter with it; and unlike the old shape there is now an
// error for check-getter-errors.sh to see if a future caller discards one.
func (d *DB) RetentionDays(key string) (int, error) {
	value, err := d.GetSetting(key)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultRetentionDays, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read retention setting %q: %w", key, err)
	}
	return RetentionDays(value), nil
}

// errBelowRetentionFloor refuses a retention below MinRetentionDays at the
// statements that actually issue the DELETE, rather than only on the routes to
// them (RetentionDays, retention.go:43; handlers/settings.go:488).
//
// It REFUSES rather than clamping. Clamping to the floor would still delete, and
// a value arriving here below the floor is not an operator preference — the
// preference is clamped at the parser, where a deliberately low setting is
// honoured as far as the floor allows. Below the floor at THIS boundary means a
// caller computed the value some other way and got it wrong, and answering a
// caller's bug with a smaller irreversible deletion buries it. Refusing costs
// only unbounded growth, which is recoverable; the deletion is not. This is the
// same asymmetry the (*DB).RetentionDays comment argues (retention.go:65-70),
// and the same choice as agent-os-r1kc and agent-os-6wbu.
//
// retentionDays = 0 is the destructive input: `datetime('now', '-' || 0 ||
// ' days')` is NOW, so the predicate becomes "older than this instant" and every
// eligible row goes, including one written seconds ago. OBSERVED, not inferred —
// TestRetentionFloor_UnguardedSQLWipesTable runs the unguarded statements and
// shows the tables emptied.
//
// Negative values are refused too, though they are not destructive: `'-' || -1`
// concatenates to "--1 days", which is not a valid SQLite modifier, so datetime()
// returns NULL, `col < NULL` is NULL, and nothing matches. OBSERVED on the same
// fixture. That makes a negative a prune that silently does nothing, which is
// still out of contract and still worth surfacing.
func errBelowRetentionFloor(retentionDays int) error {
	if retentionDays >= MinRetentionDays {
		return nil
	}
	return fmt.Errorf("refusing to prune at a retention of %d days: below the %d-day floor",
		retentionDays, MinRetentionDays)
}

// These two prune statements are named constants so the guard's negative control
// executes the SAME SQL these functions run; the third lives beside its function
// as deleteOldActionLogsStmt (audit.go). Inlined literals would let a production
// statement change while the control kept proving something about the old one.
const (
	deleteOldUpdateHistoryStmt = `DELETE FROM update_history
	          WHERE completed_at IS NOT NULL
	            AND completed_at < datetime('now', '-' || ? || ' days')`

	deleteOldBackupRunsStmt = `DELETE FROM backup_runs WHERE started_at < datetime('now', '-' || ? || ' days')`
)

// DeleteOldUpdateHistory removes update_history rows completed longer ago than
// retentionDays, returning how many went.
//
// The rows are matched on completed_at, the same column the manual "clear
// history older than" endpoint uses, so a run still in flight (completed_at
// unset) is never pruned out from under itself.
func (d *DB) DeleteOldUpdateHistory(retentionDays int) (int, error) {
	if err := errBelowRetentionFloor(retentionDays); err != nil {
		return 0, err
	}
	result, err := d.db.Exec(deleteOldUpdateHistoryStmt, retentionDays)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// DeleteOldBackupRuns removes backup_runs started longer ago than
// retentionDays. backup_run_items rows go with their parent via
// ON DELETE CASCADE, which is only effective because foreign_keys enforcement
// is set pool-wide on the DSN (agent-os-94t); the cascade is covered by a test
// rather than assumed.
func (d *DB) DeleteOldBackupRuns(retentionDays int) (int, error) {
	if err := errBelowRetentionFloor(retentionDays); err != nil {
		return 0, err
	}
	result, err := d.db.Exec(deleteOldBackupRunsStmt, retentionDays)
	if err != nil {
		return 0, err
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// RetentionResult reports what one cleanup pass removed.
type RetentionResult struct {
	UpdateHistory int
	BackupRuns    int
}

// PruneHistory runs every retention policy once. Each table is pruned
// independently: one failing must not skip the others, since the whole point is
// bounding growth on a long-lived instance.
//
// A table whose retention could not be RESOLVED is refused rather than pruned at
// the default, and the refusal happens before the DELETE is issued rather than
// merely before the loop (agent-os-obgr's rule, agent-os-r1kc's site). Absence
// still resolves to the default, so the fresh-install pass is unchanged.
func (d *DB) PruneHistory() RetentionResult {
	var result RetentionResult

	logDays, logErr := d.RetentionDays(SettingLogRetentionDays)
	if logErr != nil {
		slog.Error("Refusing to prune action logs: the retention setting could not be read",
			"setting", SettingLogRetentionDays, "error", logErr)
	} else if err := d.DeleteOldActionLogs(logDays); err != nil {
		slog.Error("Failed to delete old action logs", "error", err, "retention_days", logDays)
	}

	updateDays, updateErr := d.RetentionDays(SettingUpdateHistoryRetentionDays)
	if updateErr != nil {
		slog.Error("Refusing to prune update history: the retention setting could not be read",
			"setting", SettingUpdateHistoryRetentionDays, "error", updateErr)
	} else if n, err := d.DeleteOldUpdateHistory(updateDays); err != nil {
		slog.Error("Failed to delete old update history", "error", err, "retention_days", updateDays)
	} else {
		result.UpdateHistory = n
	}

	backupDays, backupErr := d.RetentionDays(SettingBackupHistoryRetentionDays)
	if backupErr != nil {
		slog.Error("Refusing to prune backup history: the retention setting could not be read",
			"setting", SettingBackupHistoryRetentionDays, "error", backupErr)
	} else if n, err := d.DeleteOldBackupRuns(backupDays); err != nil {
		slog.Error("Failed to delete old backup runs", "error", err, "retention_days", backupDays)
	} else {
		result.BackupRuns = n
	}

	slog.Info("History retention pass complete",
		"log_retention_days", retentionForLog(logDays, logErr),
		"update_history_retention_days", retentionForLog(updateDays, updateErr),
		"backup_history_retention_days", retentionForLog(backupDays, backupErr),
		"update_history_deleted", result.UpdateHistory,
		"backup_runs_deleted", result.BackupRuns,
	)

	return result
}

// retentionForLog keeps the summary line from reporting a retention that was
// never resolved. On a refusal the value is the Go zero, and "0" there would read
// as "pruned at zero days" — a worse lie than the 90 this bead removed, since it
// describes a deletion that did not happen at all.
func retentionForLog(days int, err error) any {
	if err != nil {
		return "unreadable"
	}
	return days
}
