package services

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// agent-os-mwgg: the credential helper prints the stored user and token in
// git's line protocol (username=..\npassword=..\n). A value holding a line
// break added protocol lines, such as host=, to what git read. Every producer
// (the two settings routes, the database written directly, the
// GIT_HTTPS_USER/GIT_HTTPS_TOKEN config) ends here, so the guard here is the
// one that covers them all: the helper refuses, names the field, and the
// value never reaches the git child's environment.

const mwggEvilLine = "host=evil.invalid"

// credentialFill runs `git credential fill` through gitCmdWithCreds, the way
// git itself would ask the installed helper, and returns stdout, stderr and
// whether git exited zero.
func credentialFillMwgg(t *testing.T, svc *GitService, c gitCreds) (stdout, stderr string, ok bool) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Keep the developer's own git config (URL-scoped helpers) out of it.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	dir := t.TempDir()
	cmd, _ := svc.gitCmdWithCreds(context.Background(), dir, c, "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=example.com\n\n")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err == nil
}

func assertRefusedMwgg(t *testing.T, stdout, stderr string, ok bool, field, secret string) {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if line == mwggEvilLine {
			t.Errorf("an injected protocol line reached git: stdout = %q", stdout)
		}
	}
	if ok {
		t.Errorf("git credential fill succeeded with a control character in the %s; stdout = %q", field, stdout)
	}
	if !strings.Contains(stderr, "HTTPS "+field) {
		t.Errorf("stderr does not name the field %q: %q", field, stderr)
	}
	if strings.Contains(stderr, secret) || strings.Contains(stdout, secret) {
		t.Errorf("the refused value was echoed: stdout %q stderr %q", stdout, stderr)
	}
}

func TestGitCredential_Mwgg_TokenWithNewlineFromDBNeverAddsALine(t *testing.T) {
	db := newTestDBWithEncryptor(t)
	if err := db.SetSetting("git_https_user", "mwgguser"); err != nil {
		t.Fatal(err)
	}
	// Written straight into the database, bypassing both handlers.
	if err := db.SetSetting("git_https_token", "mwggtok\n"+mwggEvilLine); err != nil {
		t.Fatal(err)
	}
	svc := NewGitService(&config.Config{}, db)
	c := svc.gitCredentials(t.TempDir())
	stdout, stderr, ok := credentialFillMwgg(t, svc, c)
	assertRefusedMwgg(t, stdout, stderr, ok, "token", "mwggtok")
}

func TestGitCredential_Mwgg_UserWithCRFromDBRefused(t *testing.T) {
	db := newTestDBWithEncryptor(t)
	if err := db.SetSetting("git_https_user", "mwgguser\r\n"+mwggEvilLine); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("git_https_token", "mwggtok"); err != nil {
		t.Fatal(err)
	}
	svc := NewGitService(&config.Config{}, db)
	c := svc.gitCredentials(t.TempDir())
	stdout, stderr, ok := credentialFillMwgg(t, svc, c)
	assertRefusedMwgg(t, stdout, stderr, ok, "username", "mwgguser")
}

// The third producer: GIT_HTTPS_TOKEN / GIT_HTTPS_USER from the environment,
// read into config. There is no API to refuse it at, only this guard.
func TestGitCredential_Mwgg_ConfigTokenWithNewlineRefused(t *testing.T) {
	svc := NewGitService(&config.Config{GitHTTPSUser: "mwgguser", GitHTTPSToken: "mwggtok\n" + mwggEvilLine}, newTestDBWithEncryptor(t))
	c := svc.gitCredentials(t.TempDir())
	if c.token == "" {
		t.Fatal("fixture: the config token did not resolve")
	}
	stdout, stderr, ok := credentialFillMwgg(t, svc, c)
	assertRefusedMwgg(t, stdout, stderr, ok, "token", "mwggtok")
}

// A NUL cannot be passed in an environment variable at all: before the guard
// it made exec fail for every git child, local reads included.
func TestGitCredential_Mwgg_NULNeverReachesTheEnvironment(t *testing.T) {
	svc := NewGitService(&config.Config{}, newTestDBWithEncryptor(t))
	cmd, _ := svc.gitCmdWithCreds(context.Background(), t.TempDir(), gitCreds{user: "u", token: "mwgg\x00tok"}, "status")
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, credentialEnvToken+"=") || strings.HasPrefix(kv, credentialEnvUser+"=") {
			t.Errorf("a refused credential still reached the child environment: %q", kv)
		}
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "credential.helper="+gitCredentialHelper) {
		t.Errorf("the printing helper was installed for a refused credential: %v", cmd.Args)
	}
}

// Positive control on the same instrument: a clean credential is still
// answered, exactly two lines from the helper.
func TestGitCredential_Mwgg_CleanCredentialStillAnswered(t *testing.T) {
	svc := NewGitService(&config.Config{}, newTestDBWithEncryptor(t))
	stdout, stderr, ok := credentialFillMwgg(t, svc, gitCreds{user: "mwgguser", token: "mwggtok"})
	if !ok {
		t.Fatalf("clean credential refused: stderr %q", stderr)
	}
	if !strings.Contains(stdout, "username=mwgguser\n") || !strings.Contains(stdout, "password=mwggtok\n") {
		t.Errorf("clean credential not answered: %q", stdout)
	}
}
