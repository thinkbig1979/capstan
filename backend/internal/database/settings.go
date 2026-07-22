package database

import (
	"fmt"
)

// sensitiveSettingKeys is the set of settings keys whose values are encrypted
// at rest via TokenEncryptor. Extend this set when adding new secret settings.
var sensitiveSettingKeys = map[string]bool{
	"git_https_token": true,
	"restic_password": true,
}

func (d *DB) GetSetting(key string) (string, error) {
	var value string
	query := `SELECT value FROM settings WHERE key = ?`
	err := d.db.QueryRow(query, key).Scan(&value)
	if err != nil {
		return "", err
	}
	if d.encryptor != nil && sensitiveSettingKeys[key] && value != "" {
		decrypted, err := d.encryptor.Decrypt(value)
		if err != nil {
			return "", err
		}
		return decrypted, nil
	}
	return value, nil
}

func (d *DB) SetSetting(key, value string) error {
	if sensitiveSettingKeys[key] && value != "" {
		// Fail closed: never persist a secret in plaintext. If no encryptor is
		// configured (no STORAGE_KEY/JWT_SECRET), refuse rather than silently
		// storing cleartext (L1).
		if d.encryptor == nil {
			return fmt.Errorf("cannot store sensitive setting %q without an encryption key (set STORAGE_KEY or JWT_SECRET)", key)
		}
		encrypted, err := d.encryptor.Encrypt(value)
		if err != nil {
			return fmt.Errorf("failed to encrypt setting: %w", err)
		}
		value = encrypted
	}
	query := `INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`
	_, err := d.db.Exec(query, key, value)
	return err
}
