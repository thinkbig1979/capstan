package database

import (
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

// RetentionDays reads a retention setting, applying the default when it is
// unset or unparseable and clamping to the floor.
//
// It never returns an error: a retention lookup failing must not stop the
// cleanup pass from pruning the other tables.
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

// RetentionDays resolves the retention for a settings key.
func (d *DB) RetentionDays(key string) int {
	value, err := d.GetSetting(key)
	if err != nil {
		// A missing row is the normal case on a fresh install before the
		// migration's seed, not a fault worth failing the pass over.
		return DefaultRetentionDays
	}
	return RetentionDays(value)
}

// DeleteOldUpdateHistory removes update_history rows completed longer ago than
// retentionDays, returning how many went.
//
// The rows are matched on completed_at, the same column the manual "clear
// history older than" endpoint uses, so a run still in flight (completed_at
// unset) is never pruned out from under itself.
func (d *DB) DeleteOldUpdateHistory(retentionDays int) (int, error) {
	query := `DELETE FROM update_history
	          WHERE completed_at IS NOT NULL
	            AND completed_at < datetime('now', '-' || ? || ' days')`
	result, err := d.db.Exec(query, retentionDays)
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
	query := `DELETE FROM backup_runs WHERE started_at < datetime('now', '-' || ? || ' days')`
	result, err := d.db.Exec(query, retentionDays)
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
func (d *DB) PruneHistory() RetentionResult {
	var result RetentionResult

	logDays := d.RetentionDays(SettingLogRetentionDays)
	if err := d.DeleteOldActionLogs(logDays); err != nil {
		slog.Error("Failed to delete old action logs", "error", err, "retention_days", logDays)
	}

	updateDays := d.RetentionDays(SettingUpdateHistoryRetentionDays)
	if n, err := d.DeleteOldUpdateHistory(updateDays); err != nil {
		slog.Error("Failed to delete old update history", "error", err, "retention_days", updateDays)
	} else {
		result.UpdateHistory = n
	}

	backupDays := d.RetentionDays(SettingBackupHistoryRetentionDays)
	if n, err := d.DeleteOldBackupRuns(backupDays); err != nil {
		slog.Error("Failed to delete old backup runs", "error", err, "retention_days", backupDays)
	} else {
		result.BackupRuns = n
	}

	slog.Info("History retention pass complete",
		"log_retention_days", logDays,
		"update_history_retention_days", updateDays,
		"backup_history_retention_days", backupDays,
		"update_history_deleted", result.UpdateHistory,
		"backup_runs_deleted", result.BackupRuns,
	)

	return result
}
