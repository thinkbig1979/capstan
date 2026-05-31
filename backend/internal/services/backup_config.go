package services

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// BackupConfig holds the effective backup configuration resolved from DB
// settings (highest precedence) over environment-variable fallbacks over
// hard-coded defaults. resolveBackupConfig reads from the DB at call time so
// that GUI changes take effect without a server restart.
type BackupConfig struct {
	// ResticRepository is the local path to the restic repository.
	// Default: {DATA_DIR}/restic-repo
	ResticRepository string

	// ResticPassword is the decrypted restic repository password.
	// It is never logged. Store via SetSetting("restic_password", …) which
	// encrypts it at rest; decrypt is handled transparently by GetSetting.
	ResticPassword string

	// Retention policy passed to `restic forget`.
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int

	// AutoPrune controls whether `--prune` is appended to the forget command.
	AutoPrune bool

	// ScheduleInterval is the backup scheduler tick in minutes. 0 = disabled.
	ScheduleInterval int

	// SyncAfter causes an rclone sync to run after each local backup.
	SyncAfter bool

	// Rclone settings for cloud sync (Stage 2 / DR).
	RcloneRemote    string
	RclonePath      string
	RcloneTransfers int

	// BackupHostname is passed as `--hostname` to restic backup/forget.
	// Default: system hostname.
	BackupHostname string
}

// resolveBackupConfig builds a BackupConfig by merging DB settings over env
// fallbacks over hard-coded defaults. The precedence order matches GetGitSettings
// in handlers/settings.go: DB value wins, env var is the fallback, then default.
//
// restic_password is decrypted transparently by db.GetSetting (the DB layer
// uses TokenEncryptor for sensitive keys). The plaintext password is held only
// in the returned struct and must not be logged.
func resolveBackupConfig(db *database.DB, cfg *config.Config) BackupConfig {
	var bc BackupConfig

	// --- restic_repository ---
	repo, _ := db.GetSetting("restic_repository")
	if repo == "" {
		repo = cfg.ResticRepository
	}
	if repo == "" {
		repo = filepath.Join(cfg.DataDir, "restic-repo")
	}
	bc.ResticRepository = repo

	// --- restic_password (decrypted by GetSetting) ---
	pwd, _ := db.GetSetting("restic_password")
	if pwd == "" {
		pwd = cfg.ResticPassword // from RESTIC_PASSWORD env; never logged
	}
	bc.ResticPassword = pwd

	// --- backup_keep_daily (default 7) ---
	bc.KeepDaily = resolveIntSetting(db, "backup_keep_daily", cfg.BackupKeepDaily, 7)

	// --- backup_keep_weekly (default 4) ---
	bc.KeepWeekly = resolveIntSetting(db, "backup_keep_weekly", cfg.BackupKeepWeekly, 4)

	// --- backup_keep_monthly (default 6) ---
	bc.KeepMonthly = resolveIntSetting(db, "backup_keep_monthly", cfg.BackupKeepMonthly, 6)

	// --- backup_keep_yearly (default 0) ---
	bc.KeepYearly = resolveIntSetting(db, "backup_keep_yearly", cfg.BackupKeepYearly, 0)

	// --- backup_auto_prune (default true) ---
	bc.AutoPrune = resolveBoolSetting(db, "backup_auto_prune", cfg.BackupAutoPrune, true)

	// --- backup_schedule_interval (default 0 = disabled) ---
	bc.ScheduleInterval = resolveIntSetting(db, "backup_schedule_interval", cfg.BackupScheduleInterval, 0)

	// --- backup_sync_after (default false) ---
	bc.SyncAfter = resolveBoolSetting(db, "backup_sync_after", cfg.BackupSyncAfter, false)

	// --- rclone_remote ---
	rcloneRemote, _ := db.GetSetting("rclone_remote")
	if rcloneRemote == "" {
		rcloneRemote = cfg.RcloneRemote
	}
	bc.RcloneRemote = rcloneRemote

	// --- rclone_path ---
	rclonePath, _ := db.GetSetting("rclone_path")
	if rclonePath == "" {
		rclonePath = cfg.RclonePath
	}
	bc.RclonePath = rclonePath

	// --- rclone_transfers (default 4) ---
	bc.RcloneTransfers = resolveIntSetting(db, "rclone_transfers", cfg.RcloneTransfers, 4)

	// --- backup_hostname (default: system hostname) ---
	hostname, _ := db.GetSetting("backup_hostname")
	if hostname == "" {
		hostname = cfg.BackupHostname
	}
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	bc.BackupHostname = hostname

	return bc
}

// ResolveBackupConfig is the exported variant of resolveBackupConfig for use
// by the BackupHandler. It builds the effective BackupConfig from DB settings,
// using an empty Config struct as the env-var fallback layer (DB values win).
func ResolveBackupConfig(db *database.DB) BackupConfig {
	return resolveBackupConfig(db, &config.Config{})
}

// ResolveBackupConfigWithCfg is like ResolveBackupConfig but accepts a
// config.Config so env-var fallbacks are applied. Use this when the caller
// has access to the live Config (e.g. from BackupService.Config()).
func ResolveBackupConfigWithCfg(db *database.DB, cfg *config.Config) BackupConfig {
	return resolveBackupConfig(db, cfg)
}

// settingSourceKind classifies where the effective value for a setting came from.
type settingSourceKind string

const (
	// settingSourceDB means the value was explicitly stored in the database.
	settingSourceDB settingSourceKind = "db"
	// settingSourceEnv means the DB had no value but the environment supplied one.
	settingSourceEnv settingSourceKind = "env"
	// settingSourceDefault means neither DB nor env provided a value.
	settingSourceDefault settingSourceKind = "default"
)

// RepoSettingSources returns the source classifications for restic_repository
// and restic_password given the raw DB values and the live config.
// It determines sources without exposing the password value.
//
// repoSource is "db" / "env" / "default".
// pwSource  is "db" / "env" / "default".
// hasPassword is true when any source provides a non-empty password.
func RepoSettingSources(db *database.DB, cfg *config.Config) (repoSource, pwSource settingSourceKind, hasPassword bool) {
	dbRepo, _ := db.GetSetting("restic_repository")
	switch {
	case dbRepo != "":
		repoSource = settingSourceDB
	case cfg.ResticRepository != "":
		repoSource = settingSourceEnv
	default:
		repoSource = settingSourceDefault
	}

	dbPw, _ := db.GetSetting("restic_password")
	switch {
	case dbPw != "":
		pwSource = settingSourceDB
		hasPassword = true
	case cfg.ResticPassword != "":
		pwSource = settingSourceEnv
		hasPassword = true
	default:
		pwSource = settingSourceDefault
		hasPassword = false
	}

	return repoSource, pwSource, hasPassword
}

// resolveIntSetting reads a DB setting, falls back to envVal string, then to
// defaultVal. It silently ignores parse errors and uses the default.
func resolveIntSetting(db *database.DB, key, envVal string, defaultVal int) int {
	dbVal, _ := db.GetSetting(key)
	if dbVal != "" {
		if v, err := strconv.Atoi(dbVal); err == nil {
			return v
		}
	}
	if envVal != "" {
		if v, err := strconv.Atoi(envVal); err == nil {
			return v
		}
	}
	return defaultVal
}

// resolveBoolSetting reads a DB setting, falls back to envVal string, then to
// defaultVal. "true" (case-insensitive) is the only truthy string value.
func resolveBoolSetting(db *database.DB, key, envVal string, defaultVal bool) bool {
	dbVal, _ := db.GetSetting(key)
	if dbVal != "" {
		return dbVal == "true"
	}
	if envVal != "" {
		return envVal == "true"
	}
	return defaultVal
}
