package services

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// A directory credential that cannot be DECRYPTED used to be indistinguishable
// from "this directory has no credential of its own" (agent-os-2au): both took
// the `err != nil` path at git_credentials.go:59 and fell through to the global
// credential. The effect was not "no credential" but "a different credential,
// silently" — the exact failure mode the directory-credential feature exists to
// prevent (see the doc comment on httpsCredentials).
//
// It requires a MIXED key state to reach: STORAGE_KEY rotated with the global
// credential re-saved under the new key and the directory one left behind. When
// BOTH are undecryptable the global read fails too and the result is an empty
// token, which is benign.
//
// These tests never assert on a token by printing it; they compare against the
// fixture constants and against emptiness.

const (
	decryptTestDirToken    = "dir-pat-2au-do-not-log"
	decryptTestDirUser     = "dir-user"
	decryptTestGlobalToken = "global-pat-2au-do-not-log"
	decryptTestGlobalUser  = "global-user"

	decryptTestKeyOne = "storage-key-one-0123456789abcdef"
	decryptTestKeyTwo = "storage-key-two-fedcba9876543210"
)

// gitServiceWithUndecryptableDirectoryCredential builds the mixed-key state:
// the directory credential is written under key one, the database is reopened
// under key two, and the global credential is written under key two. Only the
// directory credential is therefore undecryptable, which is what lets a test
// tell "fell back to the global one" apart from "found nothing anywhere".
func gitServiceWithUndecryptableDirectoryCredential(t *testing.T, dirPath, authType string) *GitService {
	t.Helper()
	dataDir := t.TempDir()

	db1, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault(decryptTestKeyOne, ""))
	if err != nil {
		t.Fatalf("open database under key one: %v", err)
	}
	if err := db1.UpsertDirectory(models.Directory{
		Path: dirPath, Name: filepath.Base(dirPath), RootDir: filepath.Dir(dirPath), ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed directory row: %v", err)
	}
	if err := db1.UpdateDirectoryCredentials(dirPath, authType, "", decryptTestDirUser, decryptTestDirToken); err != nil {
		t.Fatalf("store directory credentials: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close database under key one: %v", err)
	}

	db2, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault(decryptTestKeyTwo, ""))
	if err != nil {
		t.Fatalf("reopen database under key two: %v", err)
	}
	t.Cleanup(func() { db2.Close() })
	if err := db2.SetSetting("git_https_user", decryptTestGlobalUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := db2.SetSetting("git_https_token", decryptTestGlobalToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}

	// Guard the fixture itself: without a genuine decrypt failure the tests below
	// would pass for the wrong reason.
	if _, err := db2.GetDirectoryCredentials(dirPath); err == nil {
		t.Fatal("fixture is not discriminating: the directory credential still decrypts under the rotated key")
	}

	return NewGitService(&config.Config{}, db2)
}

// TestHTTPSCredentials_DirectoryDecryptFailure_DoesNotUseGlobalCredential is the
// regression test for agent-os-2au. A directory whose own credential cannot be
// decrypted must not authenticate as somebody else.
func TestHTTPSCredentials_DirectoryDecryptFailure_DoesNotUseGlobalCredential(t *testing.T) {
	const dirPath = "/stacks/rotated-key"
	svc := gitServiceWithUndecryptableDirectoryCredential(t, dirPath, "https")

	user, token := svc.httpsCredentials(dirPath)

	if token == decryptTestGlobalToken {
		t.Error("silent substitution: a directory configured for its own https credential resolved to the GLOBAL token")
	}
	if token != "" {
		t.Error("expected no token when the directory's own credential cannot be decrypted")
	}
	if user != "" {
		t.Errorf("expected no user when the directory's own credential cannot be decrypted, got %q", user)
	}
}

// TestHTTPSCredentials_DirectoryDecryptFailure_LogsError gives the failure an
// operator-visible trail: without it, a rotated STORAGE_KEY presents only as a
// generic auth error from git, with nothing naming the affected directory.
func TestHTTPSCredentials_DirectoryDecryptFailure_LogsError(t *testing.T) {
	const dirPath = "/stacks/rotated-key-logging"
	svc := gitServiceWithUndecryptableDirectoryCredential(t, dirPath, "https")
	buf := captureSlog(t)

	svc.httpsCredentials(dirPath)

	out := buf.String()
	if !strings.Contains(out, dirPath) {
		t.Errorf("log does not name the affected directory: %s", out)
	}
	if !strings.Contains(out, "level=ERROR") {
		t.Errorf("expected an ERROR-level log for an unreadable credential, got: %s", out)
	}
	for _, secret := range []string{decryptTestDirToken, decryptTestGlobalToken} {
		if strings.Contains(out, secret) {
			t.Error("log leaked a token value")
		}
	}
}

// TestHTTPSCredentials_DirectoryDecryptFailure_SSHGetsNoHTTPSToken covers the
// contract violation hidden inside the same bug: discarding the credential row
// on a decrypt error also discarded its GitAuthType, so an "ssh" directory —
// which httpsCredentials promises never falls through to an HTTPS credential —
// received the global HTTPS token.
func TestHTTPSCredentials_DirectoryDecryptFailure_SSHGetsNoHTTPSToken(t *testing.T) {
	const dirPath = "/stacks/rotated-key-ssh"
	svc := gitServiceWithUndecryptableDirectoryCredential(t, dirPath, "ssh")

	user, token := svc.httpsCredentials(dirPath)

	if user != "" || token != "" {
		t.Errorf("an ssh-authType directory received an HTTPS credential (user=%q, token set=%v)", user, token != "")
	}
}

// TestHTTPSCredentials_DecryptFailure_PositiveControl_MatchingKeyWins is the
// control the regression test needs: "no global token returned" is equally
// satisfied by breaking directory credentials outright, so the same fixture
// shape with a MATCHING key must still resolve to the directory's own token.
func TestHTTPSCredentials_DecryptFailure_PositiveControl_MatchingKeyWins(t *testing.T) {
	const dirPath = "/stacks/same-key"
	dataDir := t.TempDir()

	db, err := database.NewWithMigrationsAndEncryptor(dataDir, NewTokenEncryptorOrDefault(decryptTestKeyOne, ""))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.UpsertDirectory(models.Directory{
		Path: dirPath, Name: filepath.Base(dirPath), RootDir: filepath.Dir(dirPath), ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed directory row: %v", err)
	}
	if err := db.UpdateDirectoryCredentials(dirPath, "https", "", decryptTestDirUser, decryptTestDirToken); err != nil {
		t.Fatalf("store directory credentials: %v", err)
	}
	if err := db.SetSetting("git_https_user", decryptTestGlobalUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := db.SetSetting("git_https_token", decryptTestGlobalToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}

	user, token := NewGitService(&config.Config{}, db).httpsCredentials(dirPath)

	if token != decryptTestDirToken {
		t.Error("the directory's own credential must win when it decrypts")
	}
	if user != decryptTestDirUser {
		t.Errorf("user = %q, want the directory's own user", user)
	}
}

// TestHTTPSCredentials_NoDirectoryRow_StillFallsBackToGlobal pins the behaviour
// the fix must NOT regress: a directory with no row at all is a legitimate
// sql.ErrNoRows and still inherits the global credential.
func TestHTTPSCredentials_NoDirectoryRow_StillFallsBackToGlobal(t *testing.T) {
	db := newTestDBWithEncryptor(t)
	if err := db.SetSetting("git_https_user", decryptTestGlobalUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := db.SetSetting("git_https_token", decryptTestGlobalToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}

	user, token := NewGitService(&config.Config{}, db).httpsCredentials("/stacks/never-registered")

	if token != decryptTestGlobalToken {
		t.Error("a directory with no stored row must still inherit the global token")
	}
	if user != decryptTestGlobalUser {
		t.Errorf("user = %q, want the global user", user)
	}
}
