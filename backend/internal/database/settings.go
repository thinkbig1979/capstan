package database

import (
	"errors"
	"fmt"
)

// sensitiveSettingKeys is the set of settings keys whose values are encrypted
// at rest via TokenEncryptor. Extend this set when adding new secret settings,
// including any setting that can EMBED a credential (a URL with userinfo, a
// connection string), not only the ones that are a credential.
var sensitiveSettingKeys = map[string]bool{
	"git_https_token":   true,
	"restic_password":   true,
	"restic_repository": true,
	"rclone_remote":     true,
}

// plaintextTolerantSettingKeys are the sensitive keys that were stored in
// clear before agent-os-n4ca.7 and that stay writable with no encryption key.
//
// DESIGN CHOICE (agent-os-n4ca.7, approved at checkpoint): with no STORAGE_KEY
// or JWT_SECRET, these two are stored in clear rather than refused. Refusing
// would make a keyless install unable to configure where backups go, which it
// can do today; the password and git tokens keep refusing, as they always
// have. It is the one place a sensitive setting is written unencrypted, it
// happens only on ErrEncryptionUnavailable, and ReencryptSecrets encrypts the
// value at the first boot that has a key.
//
// Read side: an unprefixed value under one of these keys is plaintext, never
// a v1 ciphertext, because these keys had no v1 era. So an old ciphertext
// planted here is returned as an opaque string and never decrypted.
var plaintextTolerantSettingKeys = map[string]bool{
	"restic_repository": true,
	"rclone_remote":     true,
}

// IsSensitiveSetting reports whether key is encrypted at rest. Readers that
// must not let a decrypt error's text reach a log (services/backup_config.go)
// branch on it rather than keeping their own copy of the list.
func IsSensitiveSetting(key string) bool {
	return sensitiveSettingKeys[key]
}

// settingAAD binds a settings ciphertext to its key: the restic_password
// ciphertext copied into the git_https_token row does not decrypt there.
func settingAAD(key string) string {
	return "settings:" + key
}

func (d *DB) GetSetting(key string) (string, error) {
	var value string
	query := `SELECT value FROM settings WHERE key = ?`
	err := d.db.QueryRow(query, key).Scan(&value)
	if err != nil {
		return "", notFound(err, "setting", key)
	}
	if d.encryptor != nil && sensitiveSettingKeys[key] && value != "" {
		if plaintextTolerantSettingKeys[key] && !IsSealedV2(value) {
			return value, nil
		}
		decrypted, err := d.encryptor.Decrypt(value, settingAAD(key))
		if err != nil {
			return "", err
		}
		return decrypted, nil
	}
	return value, nil
}

func (d *DB) SetSetting(key, value string) error {
	stored, err := d.encodeSetting(key, value)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(upsertSettingQuery, key, stored)
	return err
}

// SettingValue is one key/value pair for SetSettings.
type SettingValue struct {
	Key   string
	Value string
}

// SetSettings writes every value in one transaction, so a form that saves
// several settings saves all of them or none (agent-os-u0nd). Every value is
// encoded first, exactly as SetSetting encodes it, so a sensitive value that
// cannot be encrypted refuses the whole save before anything is written. The
// error names the key that failed and wraps its cause.
func (d *DB) SetSettings(values []SettingValue) error {
	stored := make([]string, len(values))
	for i, v := range values {
		enc, err := d.encodeSetting(v.Key, v.Value)
		if err != nil {
			return fmt.Errorf("setting %q: %w", v.Key, err)
		}
		stored[i] = enc
	}

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin settings transaction: %w", err)
	}
	for i, v := range values {
		if _, err := tx.Exec(upsertSettingQuery, v.Key, stored[i]); err != nil {
			_ = tx.Rollback() //nolint:errcheck // The exec error above is already being returned and the tx is abandoned either way.
			return fmt.Errorf("setting %q: %w", v.Key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings transaction: %w", err)
	}
	return nil
}

const upsertSettingQuery = `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`

// encodeSetting returns the value as it is stored: encrypted for a sensitive
// key, unchanged otherwise. It is the one encryption path for every setter.
func (d *DB) encodeSetting(key, value string) (string, error) {
	if sensitiveSettingKeys[key] && value != "" {
		// Fail closed: never persist a secret in plaintext. If no encryptor is
		// configured (no STORAGE_KEY/JWT_SECRET), refuse rather than silently
		// storing cleartext (L1).
		if d.encryptor == nil {
			return "", fmt.Errorf("cannot store sensitive setting %q without an encryption key (set STORAGE_KEY or JWT_SECRET)", key)
		}
		encrypted, err := d.encryptor.Encrypt(value, settingAAD(key))
		switch {
		case err == nil:
			return encrypted, nil
		case plaintextTolerantSettingKeys[key] && errors.Is(err, ErrEncryptionUnavailable):
			// Stored in clear; see plaintextTolerantSettingKeys. A value that
			// itself begins with the v2 prefix cannot be: it would be read
			// back as a ciphertext.
			if IsSealedV2(value) {
				return "", fmt.Errorf("setting %q cannot begin with %q", key, SealedV2Prefix)
			}
		default:
			return "", fmt.Errorf("failed to encrypt setting: %w", err)
		}
	}
	return value, nil
}
