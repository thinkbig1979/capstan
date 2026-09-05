package services

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

const (
	// ScheduleModeInterval fires every ScheduleInterval minutes, anchored to
	// process start. This is the historical behaviour and the default.
	ScheduleModeInterval = "interval"
	// ScheduleModeScheduled fires at a fixed wall-clock time on chosen days.
	ScheduleModeScheduled = "scheduled"

	// DefaultScheduleTime is the fire time used when none is configured.
	DefaultScheduleTime = "02:00"
	// DefaultScheduleDays is every weekday, in Go's 0=Sunday numbering.
	DefaultScheduleDays = "0,1,2,3,4,5,6"
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
	// Only consulted in "interval" mode; see ScheduleMode.
	ScheduleInterval int

	// ScheduleMode selects how the backup scheduler decides when to fire:
	// ScheduleModeInterval (a plain ticker anchored to process start) or
	// ScheduleModeScheduled (a fixed wall-clock time on chosen weekdays).
	// Any unrecognised stored value is treated as interval mode, which is the
	// behaviour every existing install already has.
	ScheduleMode string

	// ScheduleTime is the wall-clock fire time in "HH:MM" 24-hour form, used
	// only in scheduled mode. Default "02:00".
	ScheduleTime string

	// ScheduleDays is the comma-separated Go weekday list (0=Sunday) the
	// schedule fires on, used only in scheduled mode. Default: every day.
	ScheduleDays string

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

// resticPasswordSettingKey is the one key this file reads that the DB layer
// encrypts (sensitiveSettingKeys, database/settings.go:9-12).
const resticPasswordSettingKey = "restic_password"

// ErrResticPasswordUnreadable is the fixed cause reported when the stored
// restic password cannot be read.
//
// It deliberately wraps NOTHING. restic_password is decrypted inside
// db.GetSetting, so the error it returns for that key can be crypto output, and
// handlers/directories.go:281-282 refuses to log such an error in as many
// words: it "can wrap crypto output, and logging it risks writing ciphertext or
// derived key material to disk for no operator benefit". Callers log the
// resolver's error as the cause, so this sentinel is what makes a plain
// `"cause", err` at the log site safe.
//
// SCOPE: only restic_password is encrypted here. restic_repository and the rest
// are stored in clear, so a STORAGE_KEY rotation produces this fault for the
// PASSWORD key alone; an unreadable repository key means a closed, locked or
// otherwise broken database.
var ErrResticPasswordUnreadable = errors.New(
	"the stored restic password could not be read or decrypted (STORAGE_KEY may have been rotated)")

// readSetting reads one DB setting and separates "no such row" from "this
// database could not answer".
//
// db.GetSetting returns the bare Scan error (database/settings.go:14-19), so an
// absent row arrives as sql.ErrNoRows. Mapping that — and only that — to
// ("", nil) keeps the existing env/default fallback chain byte-for-byte, while
// every other failure becomes an error the caller must handle. Before
// agent-os-l42o both were discarded, which made a database fault and an
// unconfigured setting the same event.
func readSetting(db *database.DB, key string) (string, error) {
	v, err := db.GetSetting(key)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case key == resticPasswordSettingKey:
		return "", ErrResticPasswordUnreadable
	default:
		// Setting KEYS are not secret and naming the one that failed is the
		// only thing that makes the refusal actionable. The VALUE never
		// reaches the error: a driver error does not echo the row it failed
		// to read, and the one key whose error could carry key material is
		// handled by the branch above.
		return "", fmt.Errorf("read backup setting %q: %w", key, err)
	}
}

// resolveBackupConfig builds a BackupConfig by merging DB settings over env
// fallbacks over hard-coded defaults. The precedence order matches GetGitSettings
// in handlers/settings.go: DB value wins, env var is the fallback, then default.
//
// It returns an error on the FIRST unreadable setting and stops. The hazard is
// not any single default but a PARTIAL config — real values mixed with defaults
// — handed to restic as if it were whole: the repository decides where bytes
// are written, the password what they are encrypted with, the retention keys
// reach `restic forget --prune` (backup_restic.go:442-464), the rclone keys
// decide where a sync goes and where a DR restore is read FROM, and the
// hostname decides which snapshots a forget policy covers, because restic
// groups forget by host. No key here is log-and-default (agent-os-l42o).
//
// restic_password is decrypted transparently by db.GetSetting (the DB layer
// uses TokenEncryptor for sensitive keys). The plaintext password is held only
// in the returned struct and must not be logged; see ErrResticPasswordUnreadable
// for why the failure path carries a fixed sentinel instead of the real error.
func resolveBackupConfig(db *database.DB, cfg *config.Config) (BackupConfig, error) {
	var bc BackupConfig

	// --- restic_repository ---
	// Read first, and unencrypted: a closed or locked database refuses here and
	// never reaches the password read below, so ErrResticPasswordUnreadable
	// fires in practice only on a genuine decrypt failure.
	repo, err := readSetting(db, "restic_repository")
	if err != nil {
		return BackupConfig{}, err
	}
	if repo == "" {
		repo = cfg.ResticRepository
	}
	if repo == "" {
		repo = filepath.Join(cfg.DataDir, "restic-repo")
	}
	bc.ResticRepository = repo

	// --- restic_password (decrypted by GetSetting) ---
	pwd, err := readSetting(db, resticPasswordSettingKey)
	if err != nil {
		return BackupConfig{}, err
	}
	if pwd == "" {
		pwd = cfg.ResticPassword // from RESTIC_PASSWORD env; never logged
	}
	bc.ResticPassword = pwd

	// --- backup_keep_daily (default 7) ---
	if bc.KeepDaily, err = resolveIntSetting(db, "backup_keep_daily", cfg.BackupKeepDaily, 7); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_keep_weekly (default 4) ---
	if bc.KeepWeekly, err = resolveIntSetting(db, "backup_keep_weekly", cfg.BackupKeepWeekly, 4); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_keep_monthly (default 6) ---
	if bc.KeepMonthly, err = resolveIntSetting(db, "backup_keep_monthly", cfg.BackupKeepMonthly, 6); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_keep_yearly (default 0) ---
	if bc.KeepYearly, err = resolveIntSetting(db, "backup_keep_yearly", cfg.BackupKeepYearly, 0); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_auto_prune (default true) ---
	if bc.AutoPrune, err = resolveBoolSetting(db, "backup_auto_prune", cfg.BackupAutoPrune, true); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_schedule_interval (default 0 = disabled) ---
	if bc.ScheduleInterval, err = resolveIntSetting(db, "backup_schedule_interval", cfg.BackupScheduleInterval, 0); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_schedule_mode / _time / _days ---
	//
	// Deliberately NOT seeded by any migration: resolveStringSetting (like
	// resolveIntSetting) returns a non-empty DB value BEFORE consulting the env
	// fallback, so seeding a row would make the matching BACKUP_SCHEDULE_* env
	// var permanently dead on every install.
	if bc.ScheduleMode, err = resolveStringSetting(db, "backup_schedule_mode", cfg.BackupScheduleMode, ScheduleModeInterval); err != nil {
		return BackupConfig{}, err
	}
	if bc.ScheduleTime, err = resolveStringSetting(db, "backup_schedule_time", cfg.BackupScheduleTime, DefaultScheduleTime); err != nil {
		return BackupConfig{}, err
	}
	if bc.ScheduleDays, err = resolveStringSetting(db, "backup_schedule_days", cfg.BackupScheduleDays, DefaultScheduleDays); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_sync_after (default false) ---
	if bc.SyncAfter, err = resolveBoolSetting(db, "backup_sync_after", cfg.BackupSyncAfter, false); err != nil {
		return BackupConfig{}, err
	}

	// --- rclone_remote ---
	rcloneRemote, err := readSetting(db, "rclone_remote")
	if err != nil {
		return BackupConfig{}, err
	}
	if rcloneRemote == "" {
		rcloneRemote = cfg.RcloneRemote
	}
	bc.RcloneRemote = rcloneRemote

	// --- rclone_path ---
	rclonePath, err := readSetting(db, "rclone_path")
	if err != nil {
		return BackupConfig{}, err
	}
	if rclonePath == "" {
		rclonePath = cfg.RclonePath
	}
	bc.RclonePath = rclonePath

	// --- rclone_transfers (default 4) ---
	if bc.RcloneTransfers, err = resolveIntSetting(db, "rclone_transfers", cfg.RcloneTransfers, 4); err != nil {
		return BackupConfig{}, err
	}

	// --- backup_hostname (default: system hostname) ---
	hostname, err := readSetting(db, "backup_hostname")
	if err != nil {
		return BackupConfig{}, err
	}
	if hostname == "" {
		hostname = cfg.BackupHostname
	}
	if hostname == "" {
		// os.Hostname's error stays discarded: it is not a database fault, and
		// the empty string it leaves behind means "let restic pick the host",
		// which is the historical behaviour.
		hostname, _ = os.Hostname()
	}
	bc.BackupHostname = hostname

	return bc, nil
}

// ResolveBackupConfig is the exported variant of resolveBackupConfig for use
// by the BackupHandler. It builds the effective BackupConfig from DB settings,
// using an empty Config struct as the env-var fallback layer (DB values win).
// REMOVED (agent-os-9au): ResolveBackupConfig(db *database.DB).
//
// It called resolveBackupConfig(db, &config.Config{}) — an EMPTY config. With
// cfg.DataDir == "" the default repository became filepath.Join("", "restic-repo"),
// i.e. the RELATIVE path "restic-repo", resolved against the server's working
// directory (/app). Four handlers used it, so repo init, cloud test, snapshot
// listing and snapshot preview all pointed at /app/restic-repo while every
// other path correctly used <DataDir>/restic-repo. Init reported success and
// backups then failed with "repository does not exist".
//
// Deliberately not replaced with a fixed version: any resolver that does not
// take the live config can reintroduce the same defect silently. Callers go
// through BackupService.ResolveConfig / NewResticManager / NewRcloneManager,
// which cannot be constructed without a config.Config.

// ResolveBackupConfigWithCfg is like ResolveBackupConfig but accepts a
// config.Config so env-var fallbacks are applied. Use this when the caller
// has access to the live Config (e.g. from BackupService.Config()).
func ResolveBackupConfigWithCfg(db *database.DB, cfg *config.Config) (BackupConfig, error) {
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
//
// It returns an error rather than reporting "default" for a setting it could
// not read: this is the operator's only view of what is configured, and
// answering an unreadable database with "nothing is configured" is the same
// silent substitution resolveBackupConfig used to make (agent-os-l42o).
func RepoSettingSources(db *database.DB, cfg *config.Config) (repoSource, pwSource settingSourceKind, hasPassword bool, err error) {
	dbRepo, err := readSetting(db, "restic_repository")
	if err != nil {
		return "", "", false, err
	}
	switch {
	case dbRepo != "":
		repoSource = settingSourceDB
	case cfg.ResticRepository != "":
		repoSource = settingSourceEnv
	default:
		repoSource = settingSourceDefault
	}

	dbPw, err := readSetting(db, resticPasswordSettingKey)
	if err != nil {
		return "", "", false, err
	}
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

	return repoSource, pwSource, hasPassword, nil
}

// resolveIntSetting reads a DB setting, falls back to envVal string, then to
// defaultVal. It silently ignores PARSE errors and uses the default — a stored
// value that is not a number is a malformed setting, not a database fault — but
// an unreadable database is returned as an error.
func resolveIntSetting(db *database.DB, key, envVal string, defaultVal int) (int, error) {
	dbVal, err := readSetting(db, key)
	if err != nil {
		return 0, err
	}
	if dbVal != "" {
		if v, atoiErr := strconv.Atoi(dbVal); atoiErr == nil {
			return v, nil
		}
	}
	if envVal != "" {
		if v, atoiErr := strconv.Atoi(envVal); atoiErr == nil {
			return v, nil
		}
	}
	return defaultVal, nil
}

// resolveStringSetting reads a DB setting, falls back to envVal, then to
// defaultVal. Mirrors resolveIntSetting/resolveBoolSetting: a non-empty DB
// value always wins, so no caller should seed one for a key that also has an
// env fallback.
func resolveStringSetting(db *database.DB, key, envVal, defaultVal string) (string, error) {
	dbVal, err := readSetting(db, key)
	if err != nil {
		return "", err
	}
	if dbVal != "" {
		return dbVal, nil
	}
	if envVal != "" {
		return envVal, nil
	}
	return defaultVal, nil
}

// resolveBoolSetting reads a DB setting, falls back to envVal string, then to
// defaultVal. "true" (case-insensitive) is the only truthy string value.
func resolveBoolSetting(db *database.DB, key, envVal string, defaultVal bool) (bool, error) {
	dbVal, err := readSetting(db, key)
	if err != nil {
		return false, err
	}
	if dbVal != "" {
		return dbVal == "true", nil
	}
	if envVal != "" {
		return envVal == "true", nil
	}
	return defaultVal, nil
}
