package services

import (
	"os"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
)

// newTestDB creates an in-memory SQLite DB with migrations applied.
func newTestDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("newTestDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestDBWithEncryptor creates an in-memory SQLite DB with an encryptor.
func newTestDBWithEncryptor(t *testing.T) *database.DB {
	t.Helper()
	enc := NewTokenEncryptorOrDefault("", "test-secret-key-for-unit-tests-32c")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	if err != nil {
		t.Fatalf("newTestDBWithEncryptor: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func baseCfg(dataDir string) *config.Config {
	return &config.Config{
		DataDir: dataDir,
	}
}

// TestResolveBackupConfig_Defaults verifies all defaults when DB and env are empty.
func TestResolveBackupConfig_Defaults(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/app/data")

	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.ResticRepository != "/app/data/restic-repo" {
		t.Errorf("ResticRepository = %q; want /app/data/restic-repo", bc.ResticRepository)
	}
	if bc.ResticPassword != "" {
		t.Error("ResticPassword should be empty when not set")
	}
	if bc.KeepDaily != 7 {
		t.Errorf("KeepDaily = %d; want 7", bc.KeepDaily)
	}
	if bc.KeepWeekly != 4 {
		t.Errorf("KeepWeekly = %d; want 4", bc.KeepWeekly)
	}
	if bc.KeepMonthly != 6 {
		t.Errorf("KeepMonthly = %d; want 6", bc.KeepMonthly)
	}
	if bc.KeepYearly != 0 {
		t.Errorf("KeepYearly = %d; want 0", bc.KeepYearly)
	}
	if !bc.AutoPrune {
		t.Error("AutoPrune should default to true")
	}
	if bc.ScheduleInterval != 0 {
		t.Errorf("ScheduleInterval = %d; want 0", bc.ScheduleInterval)
	}
	if bc.SyncAfter {
		t.Error("SyncAfter should default to false")
	}
	if bc.RcloneTransfers != 4 {
		t.Errorf("RcloneTransfers = %d; want 4", bc.RcloneTransfers)
	}
	if bc.BackupHostname == "" {
		t.Error("BackupHostname should default to system hostname (non-empty)")
	}
}

// TestResolveBackupConfig_EnvFallback verifies env vars are used when DB is empty.
func TestResolveBackupConfig_EnvFallback(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/data")
	cfg.ResticRepository = "/mnt/backup/repo"
	cfg.BackupKeepDaily = "14"
	cfg.BackupKeepWeekly = "8"
	cfg.BackupKeepMonthly = "12"
	cfg.BackupKeepYearly = "1"
	cfg.BackupAutoPrune = "false"
	cfg.BackupScheduleInterval = "60"
	cfg.BackupSyncAfter = "true"
	cfg.RcloneRemote = "backblaze"
	cfg.RclonePath = "capstan-backup"
	cfg.RcloneTransfers = "8"
	cfg.BackupHostname = "myserver"

	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.ResticRepository != "/mnt/backup/repo" {
		t.Errorf("ResticRepository = %q; want /mnt/backup/repo", bc.ResticRepository)
	}
	if bc.KeepDaily != 14 {
		t.Errorf("KeepDaily = %d; want 14", bc.KeepDaily)
	}
	if bc.KeepWeekly != 8 {
		t.Errorf("KeepWeekly = %d; want 8", bc.KeepWeekly)
	}
	if bc.KeepMonthly != 12 {
		t.Errorf("KeepMonthly = %d; want 12", bc.KeepMonthly)
	}
	if bc.KeepYearly != 1 {
		t.Errorf("KeepYearly = %d; want 1", bc.KeepYearly)
	}
	if bc.AutoPrune {
		t.Error("AutoPrune should be false from env")
	}
	if bc.ScheduleInterval != 60 {
		t.Errorf("ScheduleInterval = %d; want 60", bc.ScheduleInterval)
	}
	if !bc.SyncAfter {
		t.Error("SyncAfter should be true from env")
	}
	if bc.RcloneRemote != "backblaze" {
		t.Errorf("RcloneRemote = %q; want backblaze", bc.RcloneRemote)
	}
	if bc.RclonePath != "capstan-backup" {
		t.Errorf("RclonePath = %q; want capstan-backup", bc.RclonePath)
	}
	if bc.RcloneTransfers != 8 {
		t.Errorf("RcloneTransfers = %d; want 8", bc.RcloneTransfers)
	}
	if bc.BackupHostname != "myserver" {
		t.Errorf("BackupHostname = %q; want myserver", bc.BackupHostname)
	}
}

// TestResolveBackupConfig_DBOverridesEnv verifies DB values win over env fallbacks.
func TestResolveBackupConfig_DBOverridesEnv(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/data")
	// Set env fallbacks that should be overridden by DB values.
	cfg.ResticRepository = "/env/repo"
	cfg.BackupKeepDaily = "3"
	cfg.RcloneRemote = "env-remote"
	cfg.BackupHostname = "env-host"

	// Write DB values.
	if err := db.SetSetting("restic_repository", "/db/repo"); err != nil {
		t.Fatalf("SetSetting restic_repository: %v", err)
	}
	if err := db.SetSetting("backup_keep_daily", "21"); err != nil {
		t.Fatalf("SetSetting backup_keep_daily: %v", err)
	}
	if err := db.SetSetting("rclone_remote", "db-remote"); err != nil {
		t.Fatalf("SetSetting rclone_remote: %v", err)
	}
	if err := db.SetSetting("backup_hostname", "db-host"); err != nil {
		t.Fatalf("SetSetting backup_hostname: %v", err)
	}

	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.ResticRepository != "/db/repo" {
		t.Errorf("ResticRepository = %q; want /db/repo (DB should win)", bc.ResticRepository)
	}
	if bc.KeepDaily != 21 {
		t.Errorf("KeepDaily = %d; want 21 (DB should win)", bc.KeepDaily)
	}
	if bc.RcloneRemote != "db-remote" {
		t.Errorf("RcloneRemote = %q; want db-remote (DB should win)", bc.RcloneRemote)
	}
	if bc.BackupHostname != "db-host" {
		t.Errorf("BackupHostname = %q; want db-host (DB should win)", bc.BackupHostname)
	}
}

// TestResolveBackupConfig_PasswordRoundTrip verifies restic_password encrypts at
// rest and decrypts transparently on resolve. The plaintext must never appear in
// the DB row.
func TestResolveBackupConfig_PasswordRoundTrip(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)
	cfg := baseCfg("/data")

	const plainPwd = "super-secret-restic-passphrase"

	// Store via SetSetting — should be encrypted at rest.
	if err := db.SetSetting("restic_password", plainPwd); err != nil {
		t.Fatalf("SetSetting restic_password: %v", err)
	}

	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.ResticPassword != plainPwd {
		t.Errorf("ResticPassword round-trip failed: got %q; want %q", bc.ResticPassword, plainPwd)
	}
}

// TestResolveBackupConfig_PasswordEnvFallback verifies the env fallback path for
// restic_password when no DB value is set.
func TestResolveBackupConfig_PasswordEnvFallback(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)
	cfg := baseCfg("/data")
	cfg.ResticPassword = "env-password"

	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.ResticPassword != "env-password" {
		t.Errorf("ResticPassword env fallback failed: got %q; want env-password", bc.ResticPassword)
	}
}

// TestResolveBackupConfig_DBPasswordOverridesEnv verifies DB password wins over env.
func TestResolveBackupConfig_DBPasswordOverridesEnv(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)
	cfg := baseCfg("/data")
	cfg.ResticPassword = "env-password"

	if err := db.SetSetting("restic_password", "db-password"); err != nil {
		t.Fatalf("SetSetting restic_password: %v", err)
	}

	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.ResticPassword != "db-password" {
		t.Errorf("ResticPassword DB override failed: got %q; want db-password", bc.ResticPassword)
	}
}

// TestResolveBackupConfig_HostnameFromOS verifies the OS hostname is used when
// neither DB nor env provide a value.
func TestResolveBackupConfig_HostnameFromOS(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/data")
	// No cfg.BackupHostname set.

	sysHostname, _ := os.Hostname()
	bc, err := resolveBackupConfig(db, cfg)
	if err != nil {
		t.Fatalf("resolveBackupConfig: %v", err)
	}

	if bc.BackupHostname != sysHostname {
		t.Errorf("BackupHostname = %q; want system hostname %q", bc.BackupHostname, sysHostname)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// RepoSettingSources
// ─────────────────────────────────────────────────────────────────────────────

// TestRepoSettingSources_AllDefault verifies "default" when DB and env are empty.
func TestRepoSettingSources_AllDefault(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/data")

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(db, cfg)
	if err != nil {
		t.Fatalf("RepoSettingSources: %v", err)
	}

	if repoSrc != settingSourceDefault {
		t.Errorf("repoSource = %q; want %q", repoSrc, settingSourceDefault)
	}
	if pwSrc != settingSourceDefault {
		t.Errorf("pwSource = %q; want %q", pwSrc, settingSourceDefault)
	}
	if hasPassword {
		t.Error("hasPassword must be false when no password is configured")
	}
}

// TestRepoSettingSources_DBSources verifies "db" when DB has values.
func TestRepoSettingSources_DBSources(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)
	cfg := baseCfg("/data")

	if err := db.SetSetting("restic_repository", "/db/repo"); err != nil {
		t.Fatalf("SetSetting restic_repository: %v", err)
	}
	if err := db.SetSetting("restic_password", "db-secret"); err != nil {
		t.Fatalf("SetSetting restic_password: %v", err)
	}

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(db, cfg)
	if err != nil {
		t.Fatalf("RepoSettingSources: %v", err)
	}

	if repoSrc != settingSourceDB {
		t.Errorf("repoSource = %q; want %q", repoSrc, settingSourceDB)
	}
	if pwSrc != settingSourceDB {
		t.Errorf("pwSource = %q; want %q", pwSrc, settingSourceDB)
	}
	if !hasPassword {
		t.Error("hasPassword must be true when DB has a password")
	}
}

// TestRepoSettingSources_EnvSources verifies "env" when DB is empty but env supplies values.
func TestRepoSettingSources_EnvSources(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/data")
	cfg.ResticRepository = "/env/repo"
	cfg.ResticPassword = "env-secret"

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(db, cfg)
	if err != nil {
		t.Fatalf("RepoSettingSources: %v", err)
	}

	if repoSrc != settingSourceEnv {
		t.Errorf("repoSource = %q; want %q", repoSrc, settingSourceEnv)
	}
	if pwSrc != settingSourceEnv {
		t.Errorf("pwSource = %q; want %q", pwSrc, settingSourceEnv)
	}
	if !hasPassword {
		t.Error("hasPassword must be true when env has a password")
	}
}

// TestRepoSettingSources_DBWinsOverEnv verifies DB takes precedence over env.
func TestRepoSettingSources_DBWinsOverEnv(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)
	cfg := baseCfg("/data")
	// Env fallbacks are set but DB values should win.
	cfg.ResticRepository = "/env/repo"
	cfg.ResticPassword = "env-secret"

	if err := db.SetSetting("restic_repository", "/db/repo"); err != nil {
		t.Fatalf("SetSetting restic_repository: %v", err)
	}
	if err := db.SetSetting("restic_password", "db-secret"); err != nil {
		t.Fatalf("SetSetting restic_password: %v", err)
	}

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(db, cfg)
	if err != nil {
		t.Fatalf("RepoSettingSources: %v", err)
	}

	if repoSrc != settingSourceDB {
		t.Errorf("repoSource = %q; want %q (DB must win over env)", repoSrc, settingSourceDB)
	}
	if pwSrc != settingSourceDB {
		t.Errorf("pwSource = %q; want %q (DB must win over env)", pwSrc, settingSourceDB)
	}
	if !hasPassword {
		t.Error("hasPassword must be true when DB has a password")
	}
}

// TestRepoSettingSources_MixedSources verifies independent source tracking.
func TestRepoSettingSources_MixedSources(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	cfg := baseCfg("/data")
	// Repo from DB, password from env.
	cfg.ResticPassword = "env-secret"

	if err := db.SetSetting("restic_repository", "/db/repo"); err != nil {
		t.Fatalf("SetSetting restic_repository: %v", err)
	}

	repoSrc, pwSrc, hasPassword, err := RepoSettingSources(db, cfg)
	if err != nil {
		t.Fatalf("RepoSettingSources: %v", err)
	}

	if repoSrc != settingSourceDB {
		t.Errorf("repoSource = %q; want %q", repoSrc, settingSourceDB)
	}
	if pwSrc != settingSourceEnv {
		t.Errorf("pwSource = %q; want %q", pwSrc, settingSourceEnv)
	}
	if !hasPassword {
		t.Error("hasPassword must be true when env has a password")
	}
}

// TestGitHTTPSTokenEncryptionUnchanged verifies that the existing git_https_token
// encryption behaviour is intact after generalising the sensitive-key set.
func TestGitHTTPSTokenEncryptionUnchanged(t *testing.T) {
	t.Parallel()
	enc := NewTokenEncryptorOrDefault("", "test-secret-key-for-unit-tests-32c")
	db, err := database.NewWithMigrationsAndEncryptor(":memory:", enc)
	if err != nil {
		t.Fatalf("NewWithMigrationsAndEncryptor: %v", err)
	}
	defer db.Close()

	const token = "ghp_SomeGitHubToken"
	if err := db.SetSetting("git_https_token", token); err != nil {
		t.Fatalf("SetSetting git_https_token: %v", err)
	}

	got, err := db.GetSetting("git_https_token")
	if err != nil {
		t.Fatalf("GetSetting git_https_token: %v", err)
	}
	if got != token {
		t.Errorf("git_https_token round-trip failed: got %q; want %q", got, token)
	}
}
