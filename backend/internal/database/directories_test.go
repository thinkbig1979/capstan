package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

var dirTestTime = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// TestUpsertDirectory_ScanDoesNotClobberCredentials is the regression test for
// agent-os-qll: UpsertDirectory used to be INSERT OR REPLACE over all eleven
// columns, so a scan (which never populates the credential fields) deleted
// and rewrote the row, wiping any credential UpdateDirectoryCredentials had
// saved. A directory-only upsert (what a rescan performs) must leave a
// previously-saved credential in place.
//
// This is the test that holds the invariant, not the ON CONFLICT column list
// in UpsertDirectory: that list is a set of names a future column addition can
// silently forget to omit, reintroducing this exact bug in a new column. This
// test asserts the invariant directly and is mutation-tested against a
// reverted INSERT OR REPLACE (see the commit message / PR description for the
// verbatim failure), so a regression here fails a test, not just a review.
//
// All four credential columns are exercised (auth type, SSH key path, HTTPS
// user, HTTPS token) so a future column that the UPDATE SET list forgets is
// caught here too.
//
// It uses an encryptor-backed DB (agent-os-dgj). It previously used newTestDB,
// whose nil encryptor stored the token as cleartext and read it straight back,
// so the round trip below never exercised encryption at all — the assertion
// held for the wrong reason. With a real encryptor the token is written as
// ciphertext and decrypted on read, so "the rescan preserved the credential"
// now means the stored ciphertext survived, which is the claim being made.
func TestUpsertDirectory_ScanDoesNotClobberCredentials(t *testing.T) {
	t.Parallel()
	db := newTestDBWithEncryptor(t)

	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: "/stacks/app", Name: "app", ScannedAt: dirTestTime,
	}))
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/app", "https", "/home/op/.ssh/app_key", "git-user", "s3cr3t-token"))

	// Simulate a rescan: a bare Directory with only the scan-owned fields set
	// (all credential fields at their zero value), exactly as
	// scanner.ScanDirectoryWithRoot builds it.
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: "/stacks/app", Name: "app", IsGitRepo: true, GitRemote: "origin", GitBranch: "main", ScannedAt: dirTestTime,
	}))

	cred, err := db.GetDirectoryCredentials("/stacks/app")
	require.NoError(t, err)
	assert.Equal(t, "https", cred.GitAuthType)
	assert.Equal(t, "/home/op/.ssh/app_key", cred.GitSSHKeyPath)
	assert.Equal(t, "git-user", cred.GitHTTPSUser)
	assert.Equal(t, "s3cr3t-token", cred.GitHTTPSToken)

	// The rescan's own fields must still have landed.
	dir, err := db.GetDirectory("/stacks/app")
	require.NoError(t, err)
	assert.True(t, dir.IsGitRepo)
	assert.Equal(t, "origin", dir.GitRemote)
	assert.Equal(t, "main", dir.GitBranch)
}

// TestUpdateDirectoryCredentials_NoEncryptorNeverStoresPlaintext is the
// regression test for agent-os-dgj: UpdateDirectoryCredentials guarded its
// encryption step on `d.encryptor != nil`, so a DB built by New/NewWithMigrations
// — which passed a literal nil — read that nil as "skip encryption" and wrote the
// HTTPS token to disk in cleartext. SetSetting reads the same condition as "fail
// closed" (settings.go: "never persist a secret in plaintext"), and that
// asymmetry was the bug.
//
// The test drives NewWithMigrations rather than passing an encryptor explicitly,
// because the invariant being held is a property of the CONSTRUCTOR, not of this
// one call site: no constructor may leave the encryptor field literal-nil, so no
// future writer of a sensitive column can inherit the plaintext fallback.
//
// It reads the raw column deliberately. GetDirectoryCredentials decrypts only
// when an encryptor is present, so with a nil encryptor it round-trips plaintext
// unchanged and reports success — asserting through that accessor would pass
// against the very defect this test exists to catch.
func TestUpdateDirectoryCredentials_NoEncryptorNeverStoresPlaintext(t *testing.T) {
	t.Parallel()
	const plaintext = "ghp_PROBE_PLAINTEXT_TOKEN_dgj"

	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/stacks/app", Name: "app", ScannedAt: dirTestTime}))

	// assert (not require) so a regression reports BOTH the missing refusal and
	// the resulting plaintext at rest, rather than halting at the first.
	err = db.UpdateDirectoryCredentials("/stacks/app", "https", "", "git-user", plaintext)
	assert.Error(t, err, "storing an HTTPS token with no encryption key configured must fail closed, not silently store cleartext")

	var raw string
	require.NoError(t, db.db.QueryRow(`SELECT git_https_token FROM directories WHERE path = ?`, "/stacks/app").Scan(&raw))
	assert.NotEqual(t, plaintext, raw, "git_https_token must never contain the cleartext token")
	assert.Empty(t, raw, "a refused credential write must leave the token column untouched")
}

// TestUpdateDirectoryCredentials_NoEncryptorAllowsNonSecretFields pins the
// blast radius of the fail-closed behaviour above: only the token write is
// refused. Setting the auth type, SSH key path and HTTPS user carries no secret
// and must keep working without an encryption key, so SSH-authenticated
// directories are unaffected by the fix.
func TestUpdateDirectoryCredentials_NoEncryptorAllowsNonSecretFields(t *testing.T) {
	t.Parallel()

	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/stacks/app", Name: "app", ScannedAt: dirTestTime}))
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/app", "ssh", "/home/op/.ssh/app_key", "git-user", ""))

	cred, err := db.GetDirectoryCredentials("/stacks/app")
	require.NoError(t, err)
	assert.Equal(t, "ssh", cred.GitAuthType)
	assert.Equal(t, "/home/op/.ssh/app_key", cred.GitSSHKeyPath)
	assert.Equal(t, "git-user", cred.GitHTTPSUser)
	assert.Empty(t, cred.GitHTTPSToken)
}

// TestGetDirectoryCredentials_DecryptsToken covers the second defect in
// agent-os-qll: the column was encrypted on write but no reader ever decrypted
// it back. GetDirectoryCredentials must return the plaintext, not ciphertext.
func TestGetDirectoryCredentials_DecryptsToken(t *testing.T) {
	t.Parallel()
	const plaintext = "ghp_directoryScopedToken"
	enc := newTestEncryptor(t, "test-aes-gcm-key-32chars-padding")
	db, err := NewWithMigrationsAndEncryptor(":memory:", enc)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/stacks/app", Name: "app", ScannedAt: dirTestTime}))
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/app", "https", "", "git-user", plaintext))

	// The raw column must be ciphertext, not the plaintext token.
	var raw string
	require.NoError(t, db.db.QueryRow(`SELECT git_https_token FROM directories WHERE path = ?`, "/stacks/app").Scan(&raw))
	assert.NotEqual(t, plaintext, raw, "raw DB value must be ciphertext, not the plaintext token")

	cred, err := db.GetDirectoryCredentials("/stacks/app")
	require.NoError(t, err)
	assert.Equal(t, plaintext, cred.GitHTTPSToken, "GetDirectoryCredentials must return the decrypted token")

	// ListDirectories/GetDirectory must still never leak it, decrypted or not.
	listed, err := db.ListDirectories()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Empty(t, listed[0].GitHTTPSToken)
	assert.True(t, listed[0].HasHTTPSToken)

	got, err := db.GetDirectory("/stacks/app")
	require.NoError(t, err)
	assert.Empty(t, got.GitHTTPSToken)
	assert.True(t, got.HasHTTPSToken)
}
