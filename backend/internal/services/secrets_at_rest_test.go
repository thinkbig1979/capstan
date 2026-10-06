package services

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Tests for agent-os-n4ca.7: ciphertext bound to its column with AES-GCM
// associated data, and restic_repository / rclone_remote stored encrypted.
//
// They run the REAL TokenEncryptor against a real on-disk database, and read
// and write rows through a second raw connection, because the defects are about
// what is on disk: a ciphertext copied between rows, a plaintext repository
// URI, a pre-upgrade value. An in-memory database has no second connection to
// do that through.

// sealedV2Prefix pins the persisted format literally rather than importing the
// production constant: it is a contract with every database already written,
// so a change to it must break this test, not follow it silently.
const sealedV2Prefix = "$capstan$v2$"

// fixtureSecrets are the secrets the committed testdata was sealed with.
type cryptoFixtures struct {
	StorageSecret string `json:"storage_secret"`
	JWTSecret     string `json:"jwt_secret"`
	V1Primary     map[string]struct {
		Plaintext, Ciphertext string
	} `json:"v1_primary"`
	Legacy map[string]struct {
		Plaintext, Ciphertext string
	} `json:"legacy_jwt_sha256"`
}

func loadCryptoFixtures(t *testing.T) cryptoFixtures {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "crypto_v1_fixtures.json"))
	require.NoError(t, err)
	var f cryptoFixtures
	require.NoError(t, json.Unmarshal(b, &f))
	require.NotEmpty(t, f.V1Primary)
	require.NotEmpty(t, f.Legacy)
	return f
}

// openSecretsDB opens (and migrates) the database in dir with the given
// encryptor, and returns a second raw connection to the same file.
func openSecretsDB(t *testing.T, dir string, enc Encryptor) (*database.DB, *sql.DB) {
	t.Helper()
	db, err := database.NewWithMigrationsAndEncryptor(dir, enc)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	raw, err := sql.Open("sqlite", filepath.Join(dir, "capstan.db")+"?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { raw.Close() })
	return db, raw
}

func rawSetting(t *testing.T, raw *sql.DB, key string) string {
	t.Helper()
	var v string
	require.NoError(t, raw.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v))
	return v
}

func setRawSetting(t *testing.T, raw *sql.DB, key, value string) {
	t.Helper()
	_, err := raw.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value)
	require.NoError(t, err)
}

func rawDirToken(t *testing.T, raw *sql.DB, path string) string {
	t.Helper()
	var v string
	require.NoError(t, raw.QueryRow(`SELECT git_https_token FROM directories WHERE path = ?`, path).Scan(&v))
	return v
}

func setRawDirToken(t *testing.T, raw *sql.DB, path, value string) {
	t.Helper()
	res, err := raw.Exec(`UPDATE directories SET git_https_token = ? WHERE path = ?`, value, path)
	require.NoError(t, err)
	n, err := res.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

func addDirectory(t *testing.T, db *database.DB, path string) {
	t.Helper()
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: path, Name: filepath.Base(path), RootDir: filepath.Dir(path), ScannedAt: time.Now(),
	}))
}

func realEncryptor(t *testing.T) Encryptor {
	t.Helper()
	enc, err := NewTokenEncryptor(testStorageKey, testJWTSecret)
	require.NoError(t, err)
	return enc
}

// TestSecretsAtRest_SettingsCiphertextDoesNotMoveBetweenKeys is the swap
// attack: someone with write access to the database (a restored or tampered
// file) copies the restic_password ciphertext into the git_https_token row.
// Without associated data the copy decrypts and the password is served as the
// git token. With it, the read refuses.
func TestSecretsAtRest_SettingsCiphertextDoesNotMoveBetweenKeys(t *testing.T) {
	t.Parallel()
	db, raw := openSecretsDB(t, t.TempDir(), realEncryptor(t))

	require.NoError(t, db.SetSetting("restic_password", "the-restic-password"))
	require.NoError(t, db.SetSetting("git_https_token", "the-git-token"))

	setRawSetting(t, raw, "git_https_token", rawSetting(t, raw, "restic_password"))

	got, err := db.GetSetting("git_https_token")
	assert.Error(t, err, "a ciphertext moved to another key must not decrypt; it returned %q", got)
	assert.NotEqual(t, "the-restic-password", got)
}

// TestSecretsAtRest_DirectoryTokenDoesNotMoveBetweenRows covers the second
// encrypted column, directories.git_https_token, both row to row and from
// settings.value into it.
func TestSecretsAtRest_DirectoryTokenDoesNotMoveBetweenRows(t *testing.T) {
	t.Parallel()
	db, raw := openSecretsDB(t, t.TempDir(), realEncryptor(t))

	addDirectory(t, db, "/stacks/a")
	addDirectory(t, db, "/stacks/b")
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/a", "https", "", "user", "token-for-a"))
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/b", "https", "", "user", "token-for-b"))

	// Sanity: each row reads back its own token, so a failure below is the
	// binding and not a broken fixture.
	a, err := db.GetDirectoryCredentials("/stacks/a")
	require.NoError(t, err)
	require.Equal(t, "token-for-a", a.GitHTTPSToken)

	setRawDirToken(t, raw, "/stacks/b", rawDirToken(t, raw, "/stacks/a"))
	b, err := db.GetDirectoryCredentials("/stacks/b")
	assert.Error(t, err, "a token moved from /stacks/a to /stacks/b must not decrypt")
	if b != nil {
		assert.NotEqual(t, "token-for-a", b.GitHTTPSToken)
	}

	require.NoError(t, db.SetSetting("restic_password", "the-restic-password"))
	setRawDirToken(t, raw, "/stacks/b", rawSetting(t, raw, "restic_password"))
	b, err = db.GetDirectoryCredentials("/stacks/b")
	assert.Error(t, err, "a settings ciphertext moved into directories.git_https_token must not decrypt")
	if b != nil {
		assert.NotEqual(t, "the-restic-password", b.GitHTTPSToken)
	}
}

// TestSecretsAtRest_PrefixStrippedV2DoesNotFallBackToV1 closes the downgrade
// route around the binding: strip the version prefix off a v2 value and the
// remainder is a well-formed v1-shaped base64 string. It must still refuse,
// because the v1 arm opens with no associated data and the value was sealed
// with some.
func TestSecretsAtRest_PrefixStrippedV2DoesNotFallBackToV1(t *testing.T) {
	t.Parallel()
	db, raw := openSecretsDB(t, t.TempDir(), realEncryptor(t))

	for _, key := range []string{"restic_password", "git_https_token"} {
		require.NoError(t, db.SetSetting(key, "secret-for-"+key))
		stored := rawSetting(t, raw, key)
		require.True(t, strings.HasPrefix(stored, sealedV2Prefix), "%s must be stored as v2, got %q", key, stored)

		setRawSetting(t, raw, key, strings.TrimPrefix(stored, sealedV2Prefix))
		got, err := db.GetSetting(key)
		assert.Error(t, err, "%s: a v2 value with its prefix stripped must not decrypt via the v1 arm; got %q", key, got)
	}

	addDirectory(t, db, "/stacks/a")
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/a", "https", "", "user", "token-for-a"))
	stored := rawDirToken(t, raw, "/stacks/a")
	require.True(t, strings.HasPrefix(stored, sealedV2Prefix), "directory token must be stored as v2, got %q", stored)
	setRawDirToken(t, raw, "/stacks/a", strings.TrimPrefix(stored, sealedV2Prefix))
	_, err := db.GetDirectoryCredentials("/stacks/a")
	assert.Error(t, err, "a prefix-stripped v2 directory token must not decrypt via the v1 arm")
}

// TestSecretsAtRest_RepositoryAndRemoteStoredEncrypted: both settings can
// embed a credential (an s3 key in a URL, an rclone connection string), so the
// raw row must not hold the plaintext, and the backup config must still
// resolve to it.
func TestSecretsAtRest_RepositoryAndRemoteStoredEncrypted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, raw := openSecretsDB(t, dir, realEncryptor(t))

	const repo = "s3:https://s3.example.com/bucket/restic"
	const remote = "myremote:backups/capstan"
	require.NoError(t, db.SetSetting("restic_repository", repo))
	require.NoError(t, db.SetSetting("rclone_remote", remote))

	for key, plain := range map[string]string{"restic_repository": repo, "rclone_remote": remote} {
		stored := rawSetting(t, raw, key)
		assert.NotContains(t, stored, plain, "%s is stored in clear", key)
		assert.True(t, strings.HasPrefix(stored, sealedV2Prefix), "%s must be stored as v2, got %q", key, stored)
	}

	bc, err := resolveBackupConfig(db, baseCfg(dir))
	require.NoError(t, err)
	assert.Equal(t, repo, bc.ResticRepository)
	assert.Equal(t, remote, bc.RcloneRemote)
}

// TestSecretsAtRest_UpgradeReencryptsExistingRows: a database written by the
// previous release holds v1 primary-key and legacy SHA-256(JWT_SECRET)
// ciphertexts (committed fixtures, sealed by the 54d0f04 crypto.go) and a
// plaintext repository and remote. Opening it with this release converts every
// one to v2 bound to its column, every value still reads back, and opening it
// a second time changes nothing.
func TestSecretsAtRest_UpgradeReencryptsExistingRows(t *testing.T) {
	t.Parallel()
	f := loadCryptoFixtures(t)

	for name, set := range map[string]map[string]struct{ Plaintext, Ciphertext string }{
		"v1_primary": f.V1Primary,
		"legacy":     f.Legacy,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			// Lay down the pre-upgrade state through a database opened with
			// NO key, so nothing in this release touches the values on the
			// way in.
			pre, preRaw := openSecretsDB(t, dir, nil)
			addDirectory(t, pre, "/stacks/a")
			setRawSetting(t, preRaw, "restic_password", set["restic_password"].Ciphertext)
			setRawSetting(t, preRaw, "git_https_token", set["git_https_token"].Ciphertext)
			setRawSetting(t, preRaw, "restic_repository", "/backups/restic")
			setRawSetting(t, preRaw, "rclone_remote", "myremote:capstan")
			setRawDirToken(t, preRaw, "/stacks/a", set["directory_token"].Ciphertext)
			require.NoError(t, pre.Close())
			require.NoError(t, preRaw.Close())

			enc, err := NewTokenEncryptor(f.StorageSecret, f.JWTSecret)
			require.NoError(t, err)

			want := map[string]string{
				"restic_password":   set["restic_password"].Plaintext,
				"git_https_token":   set["git_https_token"].Plaintext,
				"restic_repository": "/backups/restic",
				"rclone_remote":     "myremote:capstan",
			}
			var firstPass map[string]string
			for pass := 1; pass <= 2; pass++ {
				db, raw := openSecretsDB(t, dir, enc)
				stored := map[string]string{}
				for key, plain := range want {
					got, err := db.GetSetting(key)
					require.NoError(t, err, "pass %d: %s", pass, key)
					assert.Equal(t, plain, got, "pass %d: %s", pass, key)
					stored[key] = rawSetting(t, raw, key)
					assert.True(t, strings.HasPrefix(stored[key], sealedV2Prefix), "pass %d: %s not re-encrypted: %q", pass, key, stored[key])
				}
				d, err := db.GetDirectoryCredentials("/stacks/a")
				require.NoError(t, err, "pass %d", pass)
				assert.Equal(t, set["directory_token"].Plaintext, d.GitHTTPSToken)
				stored["dir"] = rawDirToken(t, raw, "/stacks/a")
				assert.True(t, strings.HasPrefix(stored["dir"], sealedV2Prefix), "pass %d: directory token not re-encrypted", pass)

				if pass == 1 {
					firstPass = stored
				} else {
					// Idempotent: the second boot rewrote nothing (a fresh
					// nonce would change every value if it had).
					assert.Equal(t, firstPass, stored)
				}
				require.NoError(t, db.Close())
				require.NoError(t, raw.Close())
			}
		})
	}
}

// TestSecretsAtRest_NoKeyModeStoresAndReadsRepository: with no STORAGE_KEY or
// JWT_SECRET the repository and remote must stay writable and readable (as
// before this change) and the password must still refuse; when a key is
// configured later, the next boot encrypts what was stored in clear.
func TestSecretsAtRest_NoKeyModeStoresAndReadsRepository(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	nokey, _ := openSecretsDB(t, dir, NewTokenEncryptorOrDefault("", ""))
	require.NoError(t, nokey.SetSetting("restic_repository", "/backups/restic"))
	require.NoError(t, nokey.SetSetting("rclone_remote", "myremote:capstan"))
	assert.ErrorIs(t, nokey.SetSetting("restic_password", "pw"), ErrEncryptionUnavailable)

	bc, err := resolveBackupConfig(nokey, baseCfg(dir))
	require.NoError(t, err)
	assert.Equal(t, "/backups/restic", bc.ResticRepository)
	assert.Equal(t, "myremote:capstan", bc.RcloneRemote)
	require.NoError(t, nokey.Close())

	keyed, raw := openSecretsDB(t, dir, realEncryptor(t))
	for key, plain := range map[string]string{"restic_repository": "/backups/restic", "rclone_remote": "myremote:capstan"} {
		got, err := keyed.GetSetting(key)
		require.NoError(t, err)
		assert.Equal(t, plain, got)
		assert.True(t, strings.HasPrefix(rawSetting(t, raw, key), sealedV2Prefix), "%s stored in clear in no-key mode is encrypted once a key exists", key)
	}
}

// TestSecretsAtRest_RotatedKey: after a STORAGE_KEY change the repository is
// unreadable too, now that it is encrypted. The backup config must refuse with
// the fixed sentinel naming the key, never the decrypt error, and the boot
// pass must leave the unreadable rows exactly as they were.
func TestSecretsAtRest_RotatedKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	before, raw := openSecretsDB(t, dir, realEncryptor(t))
	require.NoError(t, before.SetSetting("restic_repository", "s3:https://s3.example.com/bucket"))
	require.NoError(t, before.SetSetting("restic_password", "the-restic-password"))
	storedRepo := rawSetting(t, raw, "restic_repository")
	storedPw := rawSetting(t, raw, "restic_password")
	require.NoError(t, before.Close())

	rotated, err := NewTokenEncryptor("a-different-storage-key", "")
	require.NoError(t, err)
	db, raw := openSecretsDB(t, dir, rotated)

	assert.Equal(t, storedRepo, rawSetting(t, raw, "restic_repository"), "the boot pass must not touch a value it cannot decrypt")
	assert.Equal(t, storedPw, rawSetting(t, raw, "restic_password"), "the boot pass must not touch a value it cannot decrypt")

	_, err = resolveBackupConfig(db, baseCfg(dir))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrBackupSettingUnreadable)
	assert.Contains(t, err.Error(), `"restic_repository"`)
	assert.NotContains(t, err.Error(), "failed to decrypt token", "the decrypt error must not reach the message")

	// Control: the same database under the original key reads both.
	require.NoError(t, db.Close())
	again, _ := openSecretsDB(t, dir, realEncryptor(t))
	bc, err := resolveBackupConfig(again, baseCfg(dir))
	require.NoError(t, err)
	assert.Equal(t, "s3:https://s3.example.com/bucket", bc.ResticRepository)
	assert.Equal(t, "the-restic-password", bc.ResticPassword)
}

// TestSecretsAtRest_NoKeyModeRefusesPrefixedPlaintext: with no key, a value
// that already begins with the v2 prefix cannot be stored in clear, because it
// would be read back as a ciphertext. Everything else stays writable.
func TestSecretsAtRest_NoKeyModeRefusesPrefixedPlaintext(t *testing.T) {
	t.Parallel()
	db, _ := openSecretsDB(t, t.TempDir(), nil)

	assert.Error(t, db.SetSetting("rclone_remote", sealedV2Prefix+"looks-sealed"))
	assert.NoError(t, db.SetSetting("rclone_remote", "remote:"+sealedV2Prefix))
}
