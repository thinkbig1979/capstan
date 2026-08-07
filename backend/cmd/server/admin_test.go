package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// The offline reset is the only way back into an instance whose single admin
// password has been lost (agent-os-8pa): /auth/setup 409s once an account
// exists, and there is no reset endpoint. These tests assert on the stored
// state — the bcrypt hash actually verifying against the new password, the
// session rows actually being gone — rather than on the command's exit code,
// because an exit code is exactly what a broken reset would still get right.

const seededPassword = "OldPassw0rd!"

// seedInstance creates a data dir holding a migrated capstan.db with one
// account and two live sessions, and returns the dir and the user's ID.
func seedInstance(t *testing.T, username string) (string, string) {
	t.Helper()

	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	defer db.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte(seededPassword), bcrypt.DefaultCost)
	require.NoError(t, err)

	userID := uuid.New().String()
	now := time.Now().UTC()
	require.NoError(t, db.CreateUser(models.User{
		ID:        userID,
		Username:  username,
		Password:  string(hash),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	// Two sessions, so the revocation assertion cannot pass by deleting only
	// the most recent row.
	for i := 0; i < 2; i++ {
		require.NoError(t, db.CreateSession(models.Session{
			ID:        uuid.New().String(),
			UserID:    userID,
			ExpiresAt: now.Add(24 * time.Hour),
			CreatedAt: now,
		}))
	}

	return dataDir, userID
}

// storedHash reads the password hash straight back out of the database, so the
// assertions never go through the same code path that wrote it.
func storedHash(t *testing.T, dataDir, username string) string {
	t.Helper()

	db, err := database.New(dataDir)
	require.NoError(t, err)
	defer db.Close()

	user, err := db.GetUserByUsername(username)
	require.NoError(t, err)
	return user.Password
}

func sessionCount(t *testing.T, dataDir, userID string) int {
	t.Helper()

	db, err := database.New(dataDir)
	require.NoError(t, err)
	defer db.Close()

	// GetSession is per-ID, so count through the exported helper the server
	// itself uses for expiry sweeps: any session still present is one the
	// reset failed to revoke.
	n, err := db.CountSessionsForUser(userID)
	require.NoError(t, err)
	return n
}

func runAdmin(dataDir, stdin string, args ...string) (int, string) {
	var out bytes.Buffer
	full := append([]string{"reset-password", "--data-dir", dataDir}, args...)
	code := runAdminCommand(full, strings.NewReader(stdin), &out, &out)
	return code, out.String()
}

func TestAdminResetPassword_SetsNewPasswordAndRevokesSessions(t *testing.T) {
	dataDir, userID := seedInstance(t, "admin")
	before := storedHash(t, dataDir, "admin")
	require.Equal(t, 2, sessionCount(t, dataDir, userID), "precondition: two live sessions")

	code, output := runAdmin(dataDir, "BrandNewPassw0rd!\n")
	require.Equal(t, 0, code, "reset should succeed; output: %s", output)

	after := storedHash(t, dataDir, "admin")
	assert.NotEqual(t, before, after, "the stored hash must change")
	assert.NoError(t,
		bcrypt.CompareHashAndPassword([]byte(after), []byte("BrandNewPassw0rd!")),
		"the new password must verify against the stored hash")
	assert.Error(t,
		bcrypt.CompareHashAndPassword([]byte(after), []byte(seededPassword)),
		"the old password must no longer verify")

	assert.Equal(t, 0, sessionCount(t, dataDir, userID),
		"every session must be revoked: a reset that leaves an existing cookie valid "+
			"defeats the purpose of resetting because someone else may have access")
}

// TestAdminResetPassword_RejectsWeakPassword is the must-fail control. Without
// it the implementation could 'pass' the test above by accepting anything at
// all, which would make the recovery path weaker than the signup path it
// restores access to.
func TestAdminResetPassword_RejectsWeakPassword(t *testing.T) {
	dataDir, userID := seedInstance(t, "admin")
	before := storedHash(t, dataDir, "admin")

	code, output := runAdmin(dataDir, "short\n")
	require.NotEqual(t, 0, code, "a password failing the shared rules must be refused")
	assert.Contains(t, strings.ToLower(output), "password")

	assert.Equal(t, before, storedHash(t, dataDir, "admin"),
		"a refused reset must leave the stored hash untouched")
	assert.Equal(t, 2, sessionCount(t, dataDir, userID),
		"a refused reset must not revoke sessions either")
}

func TestAdminResetPassword_ResolvesSoleAccountWithoutUsername(t *testing.T) {
	dataDir, _ := seedInstance(t, "someone-else")

	code, output := runAdmin(dataDir, "BrandNewPassw0rd!\n")
	require.Equal(t, 0, code,
		"with exactly one account the username should not be required; output: %s", output)

	assert.NoError(t, bcrypt.CompareHashAndPassword(
		[]byte(storedHash(t, dataDir, "someone-else")), []byte("BrandNewPassw0rd!")))
}

func TestAdminResetPassword_UnknownUsernameIsRefused(t *testing.T) {
	dataDir, _ := seedInstance(t, "admin")
	before := storedHash(t, dataDir, "admin")

	code, output := runAdmin(dataDir, "BrandNewPassw0rd!\n", "--username", "nobody")
	require.NotEqual(t, 0, code)
	assert.Contains(t, strings.ToLower(output), "nobody")
	assert.Equal(t, before, storedHash(t, dataDir, "admin"),
		"naming a user that does not exist must not touch the account that does")
}

// TestAdminResetPassword_MissingDatabaseIsRefused pins that a mistyped
// --data-dir reports the mistake instead of silently creating an empty
// database there and then reporting "no accounts", which would read as data
// loss to an operator already having a bad day.
func TestAdminResetPassword_MissingDatabaseIsRefused(t *testing.T) {
	empty := t.TempDir()

	code, output := runAdmin(empty, "BrandNewPassw0rd!\n")
	require.NotEqual(t, 0, code)
	assert.Contains(t, output, "capstan.db")

	_, err := os.Stat(filepath.Join(empty, "capstan.db"))
	assert.True(t, os.IsNotExist(err),
		"the command must not create a database at a path that had none")
}

func TestAdminResetPassword_EmptyStdinIsRefused(t *testing.T) {
	dataDir, _ := seedInstance(t, "admin")
	before := storedHash(t, dataDir, "admin")

	code, output := runAdmin(dataDir, "\n")
	require.NotEqual(t, 0, code, "an empty password must never be accepted")
	assert.Equal(t, before, storedHash(t, dataDir, "admin"))
	assert.NotEmpty(t, output)
}

func TestAdminCommand_UnknownSubcommandIsRefused(t *testing.T) {
	var out bytes.Buffer
	code := runAdminCommand([]string{"delete-everything"}, strings.NewReader(""), &out, &out)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, out.String(), "reset-password", "usage should name the commands that do exist")
}
