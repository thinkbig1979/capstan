package database

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SetSettings must store a sensitive key exactly as SetSetting does:
// ciphertext at rest, plaintext through GetSetting (safe-defaults rule 13). A
// non-sensitive key in the same batch stays plaintext.
func TestSetSettingsEncryptsSensitiveKeysAtRest(t *testing.T) {
	t.Parallel()

	const password = "u0nd-batch-restic-passphrase"
	db, err := NewWithMigrationsAndEncryptor(":memory:", newTestEncryptor(t, "test-aes-gcm-key-32chars-padding"))
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.SetSettings([]SettingValue{
		{Key: "restic_password", Value: password},
		{Key: "rclone_path", Value: "backups/u0nd"},
	}))

	raw := rawSettingValue(t, db, "restic_password")
	assert.NotEqual(t, password, raw, "SetSettings stored the password in plaintext")
	assert.True(t, strings.HasPrefix(raw, SealedV2Prefix), "raw value must be a v2 ciphertext, got %q", raw)
	got, err := db.GetSetting("restic_password")
	require.NoError(t, err)
	assert.Equal(t, password, got)

	assert.Equal(t, "backups/u0nd", rawSettingValue(t, db, "rclone_path"),
		"a non-sensitive key must be stored as given")
}

// A sensitive value that cannot be encrypted refuses the whole batch before
// anything is written: the key ahead of it in the batch is unchanged.
func TestSetSettingsRefusesTheWholeBatchWithoutAnEncryptor(t *testing.T) {
	t.Parallel()

	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, db.SetSetting("backup_keep_daily", "7"))

	err = db.SetSettings([]SettingValue{
		{Key: "backup_keep_daily", Value: "9"},
		{Key: "restic_password", Value: "never-stored"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"restic_password"`, "the error must name the key that failed")
	assert.Equal(t, "7", rawSettingValue(t, db, "backup_keep_daily"))
}

// A storage fault on a later key rolls back the earlier ones. Control: the
// same batch with no fault writes both, so the fault arm cannot pass on a
// setter that writes nothing.
func TestSetSettingsRollsBackOnAStorageFault(t *testing.T) {
	t.Parallel()

	for _, faulted := range []bool{false, true} {
		db, err := NewWithMigrations(":memory:")
		require.NoError(t, err)
		require.NoError(t, db.SetSetting("backup_keep_daily", "7"))
		require.NoError(t, db.SetSetting("backup_hostname", "before"))
		if faulted {
			_, err = db.db.Exec(`CREATE TRIGGER u0nd_fail_hostname BEFORE INSERT ON settings
				WHEN NEW.key = 'backup_hostname'
				BEGIN SELECT RAISE(ABORT, 'u0nd injected storage fault'); END`)
			require.NoError(t, err)
		}

		err = db.SetSettings([]SettingValue{
			{Key: "backup_keep_daily", Value: "9"},
			{Key: "backup_hostname", Value: "after"},
		})
		if faulted {
			require.Error(t, err)
			assert.Contains(t, err.Error(), `"backup_hostname"`)
			assert.Equal(t, "7", rawSettingValue(t, db, "backup_keep_daily"), "the earlier key was not rolled back")
			assert.Equal(t, "before", rawSettingValue(t, db, "backup_hostname"))
		} else {
			require.NoError(t, err)
			assert.Equal(t, "9", rawSettingValue(t, db, "backup_keep_daily"))
			assert.Equal(t, "after", rawSettingValue(t, db, "backup_hostname"))
		}
		require.NoError(t, db.Close())
	}
}
