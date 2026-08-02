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
func TestUpsertDirectory_ScanDoesNotClobberCredentials(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: "/stacks/app", Name: "app", ScannedAt: dirTestTime,
	}))
	require.NoError(t, db.UpdateDirectoryCredentials("/stacks/app", "https", "", "git-user", "s3cr3t-token"))

	// Simulate a rescan: a bare Directory with only the scan-owned fields set,
	// exactly as scanner.ScanDirectoryWithRoot builds it.
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: "/stacks/app", Name: "app", IsGitRepo: true, GitRemote: "origin", GitBranch: "main", ScannedAt: dirTestTime,
	}))

	cred, err := db.GetDirectoryCredentials("/stacks/app")
	require.NoError(t, err)
	assert.Equal(t, "https", cred.GitAuthType)
	assert.Equal(t, "git-user", cred.GitHTTPSUser)
	assert.Equal(t, "s3cr3t-token", cred.GitHTTPSToken)

	// The rescan's own fields must still have landed.
	dir, err := db.GetDirectory("/stacks/app")
	require.NoError(t, err)
	assert.True(t, dir.IsGitRepo)
	assert.Equal(t, "origin", dir.GitRemote)
	assert.Equal(t, "main", dir.GitBranch)
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
