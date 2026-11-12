package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-qt88: resolveBoolSetting's docblock says "true" is truthy
// case-insensitively, but it compared with == "true", so TRUE/True read as
// false. Every caller family reads through it: backup config, the weekly
// verify switch, and the docker-cleanup switches (that arm is in
// docker_cleanup_bool_qt88_test.go). ---

func TestQt88_BackupBoolSettingsAreCaseInsensitive(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	require.NoError(t, db.SetSetting("backup_sync_after", "True"))
	cfg := baseCfg(t.TempDir())
	cfg.BackupAutoPrune = "TRUE"

	bc, err := resolveBackupConfig(db, cfg)
	require.NoError(t, err)
	assert.True(t, bc.AutoPrune, "BACKUP_AUTO_PRUNE=TRUE (env)")
	assert.True(t, bc.SyncAfter, "backup_sync_after=True (DB row)")
}

// Control on the same instrument: "false" and a value that is not a
// spelling of true still read false, from either source.
func TestQt88_BackupBoolSettingsFalseAndUnparseableReadFalse(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"false", "FALSE", "yes", "1", "on", "truee"} {
		db := newBackupTestDB(t)
		require.NoError(t, db.SetSetting("backup_sync_after", v))
		cfg := baseCfg(t.TempDir())
		cfg.BackupAutoPrune = v

		bc, err := resolveBackupConfig(db, cfg)
		require.NoError(t, err)
		assert.False(t, bc.AutoPrune, "BACKUP_AUTO_PRUNE=%q (env)", v)
		assert.False(t, bc.SyncAfter, "backup_sync_after=%q (DB row)", v)
	}
}

func TestQt88_WeeklyVerifySettingIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{"TRUE": true, "True": true, "true": true, "FALSE": false, "off": false} {
		db := newBackupTestDB(t)
		require.NoError(t, db.SetSetting(SettingBackupVerifyWeekly, v))
		got, err := VerifyWeeklyEnabled(db)
		require.NoError(t, err)
		assert.Equal(t, want, got, "%s=%q", SettingBackupVerifyWeekly, v)
	}
}
