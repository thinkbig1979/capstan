package services

import (
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// The stored git HTTPS token was encrypted, persisted and reported as present,
// but never handed to any git process (agent-os-qqw). Every pull of a private
// HTTPS repository therefore died with "could not read Username".
//
// Reproducing that needs a remote that actually demands credentials, so these
// tests run git's own smart-HTTP server (git-http-backend, over CGI) behind
// Basic auth. No network leaves the machine and no real token is involved.

const (
	testGitUser  = "oauth2"
	testGitToken = "test-pat-4f8c1e9b-do-not-log"
)

// runGit executes git in dir and fails the test on a non-zero exit.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// authenticatedHTTPRepo builds a bare repository, serves it over smart HTTP
// behind Basic auth, and returns a working clone whose origin URL carries NO
// embedded credentials. advance() adds a commit to the remote so a subsequent
// pull has real work to do and cannot pass as a no-op "Already up to date".
func authenticatedHTTPRepo(t *testing.T) (local string, advance func() string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git --exec-path failed: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend not available: %v", err)
	}

	base := t.TempDir()
	root := filepath.Join(base, "srv")
	bare := filepath.Join(root, "repo.git")
	seed := filepath.Join(base, "seed")
	local = filepath.Join(base, "local")
	for _, d := range []string{root, seed} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	runGit(t, root, "init", "--bare", "-b", "main", bare)

	runGit(t, seed, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, seed, "add", "-A")
	runGit(t, seed, "commit", "-m", "initial")
	runGit(t, seed, "remote", "add", "origin", bare)
	runGit(t, seed, "push", "-u", "origin", "main")

	// Serve the bare repo over smart HTTP, demanding Basic auth on every request.
	handler := &cgi.Handler{
		Path: backend,
		Env: []string{
			"GIT_PROJECT_ROOT=" + root,
			"GIT_HTTP_EXPORT_ALL=1",
			"PATH=" + os.Getenv("PATH"),
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testGitUser || pass != testGitToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="capstan-test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	// Clone from the filesystem, then point origin at the authenticated URL. The
	// remote URL deliberately contains no credentials — supplying them is exactly
	// what is under test.
	runGit(t, base, "clone", bare, local)
	runGit(t, local, "remote", "set-url", "origin", srv.URL+"/repo.git")

	advance = func() string {
		if err := os.WriteFile(filepath.Join(seed, "post-restore-canary.env"), []byte("CANARY=1\n"), 0o644); err != nil {
			t.Fatalf("write canary: %v", err)
		}
		runGit(t, seed, "add", "-A")
		runGit(t, seed, "commit", "-m", "canary")
		runGit(t, seed, "push", "origin", "main")
		return runGit(t, seed, "rev-parse", "HEAD")
	}

	return local, advance
}

// gitServiceWithStoredToken returns a GitService whose database holds the git
// HTTPS credential exactly as the settings handler stores it: encrypted at rest
// under the git_https_token key.
func gitServiceWithStoredToken(t *testing.T) *GitService {
	t.Helper()
	db := newTestDBWithEncryptor(t)
	if err := db.SetSetting("git_https_user", testGitUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := db.SetSetting("git_https_token", testGitToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}
	return NewGitService(&config.Config{}, db)
}

// TestPull_UsesStoredHTTPSToken is the regression test for agent-os-qqw. With a
// token stored in settings and none embedded in the remote URL, a pull of a
// credential-protected repository must fast-forward.
func TestPull_UsesStoredHTTPSToken(t *testing.T) {
	local, advance := authenticatedHTTPRepo(t)
	want := advance()

	svc := gitServiceWithStoredToken(t)

	result, err := svc.Pull(local)
	if err != nil {
		t.Fatalf("Pull with a stored HTTPS token failed: %v", err)
	}
	if result.CurrentCommit != want {
		t.Errorf("HEAD after pull = %q, want the remote's new commit %q", result.CurrentCommit, want)
	}
	if result.PreviousCommit == result.CurrentCommit {
		t.Error("pull reported no change; the remote had a new commit to fetch")
	}
}

// TestPull_WithoutStoredTokenStillFails guards the test harness itself: the
// remote really does demand credentials, so a service with no token configured
// must fail. Without this, TestPull_UsesStoredHTTPSToken would also pass against
// an unauthenticated remote and prove nothing.
func TestPull_WithoutStoredTokenStillFails(t *testing.T) {
	local, advance := authenticatedHTTPRepo(t)
	advance()

	svc := NewGitService(&config.Config{}, newTestDBWithEncryptor(t))

	if _, err := svc.Pull(local); err == nil {
		t.Fatal("expected the pull to fail without credentials, but it succeeded")
	}
}

// TestPull_TokenNeverPersistsToGitConfig covers the leak the fix must not
// introduce: rewriting origin to embed the token would put it in .git/config on
// disk, inside that stack's backup snapshot, and in every log printing the
// remote.
func TestPull_TokenNeverPersistsToGitConfig(t *testing.T) {
	local, advance := authenticatedHTTPRepo(t)
	advance()

	svc := gitServiceWithStoredToken(t)
	if _, err := svc.Pull(local); err != nil {
		t.Fatalf("Pull with a stored HTTPS token failed: %v", err)
	}

	cfg, err := os.ReadFile(filepath.Join(local, ".git", "config"))
	if err != nil {
		t.Fatalf("read .git/config: %v", err)
	}
	if strings.Contains(string(cfg), testGitToken) {
		t.Errorf(".git/config contains the token:\n%s", cfg)
	}

	remote := runGit(t, local, "remote", "get-url", "origin")
	if strings.Contains(remote, testGitToken) {
		t.Errorf("origin URL contains the token: %q", remote)
	}
}

// TestGitCmd_TokenTravelsInEnvNotArgv pins down *how* the credential reaches
// git. argv is world-readable through /proc/<pid>/cmdline, so the token must
// appear only in the child's environment.
func TestGitCmd_TokenTravelsInEnvNotArgv(t *testing.T) {
	svc := gitServiceWithStoredToken(t)

	cmd, token := svc.gitCmd(t.TempDir(), "pull", "--ff-only")

	if token != testGitToken {
		t.Errorf("gitCmd returned token %q, want the stored one", token)
	}
	for i, arg := range cmd.Args {
		if strings.Contains(arg, testGitToken) {
			t.Errorf("argv[%d] contains the token: %q", i, arg)
		}
	}

	var sawUser, sawToken bool
	for _, kv := range cmd.Env {
		switch kv {
		case credentialEnvUser + "=" + testGitUser:
			sawUser = true
		case credentialEnvToken + "=" + testGitToken:
			sawToken = true
		}
	}
	if !sawUser || !sawToken {
		t.Errorf("credential missing from the child environment (user=%v token=%v)", sawUser, sawToken)
	}

	if !strings.Contains(strings.Join(cmd.Args, " "), "credential.helper="+gitCredentialHelper) {
		t.Errorf("credential helper not installed; args = %v", cmd.Args)
	}
}

// TestGitCmd_NoCredentialWhenNoneConfigured keeps the change inert for the
// common case: with nothing stored, git is invoked exactly as before.
func TestGitCmd_NoCredentialWhenNoneConfigured(t *testing.T) {
	svc := NewGitService(&config.Config{}, newTestDBWithEncryptor(t))

	cmd, token := svc.gitCmd(t.TempDir(), "status", "--porcelain")

	if token != "" {
		t.Errorf("expected no token, got %q", token)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "credential.helper") {
		t.Errorf("credential helper installed with no credential configured: %v", cmd.Args)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, credentialEnvToken+"=") {
			t.Errorf("credential env var set with no credential configured: %q", kv)
		}
	}
}

// TestGitCommand_RedactsTokenFromOutput proves the redaction is wired into the
// real error path, not just available as a helper. git's error text flows into
// AppError.Details, the action log and the API response, so a token that
// reaches git's output would be persisted.
func TestGitCommand_RedactsTokenFromOutput(t *testing.T) {
	local, _ := authenticatedHTTPRepo(t)
	svc := gitServiceWithStoredToken(t)

	// rev-parse echoes an unknown revision back verbatim, which is the cheapest
	// way to get the token into genuine git output.
	_, err := svc.gitCommand(local, "rev-parse", testGitToken)
	if err == nil {
		t.Fatal("expected rev-parse of a bogus revision to fail")
	}
	if strings.Contains(err.Error(), testGitToken) {
		t.Errorf("git error text leaks the token: %v", err)
	}
	if !strings.Contains(err.Error(), "***") {
		t.Errorf("expected the token to be replaced by a placeholder, got: %v", err)
	}
}

// TestHTTPSCredentials_SettingsBeatEnvironment records the precedence: an
// operator who stores a token through Settings expects it to win over whatever
// GIT_HTTPS_TOKEN the container was started with.
func TestHTTPSCredentials_SettingsBeatEnvironment(t *testing.T) {
	svc := gitServiceWithStoredToken(t)
	svc.config = &config.Config{GitHTTPSUser: "env-user", GitHTTPSToken: "env-token"}

	user, token := svc.httpsCredentials()
	if user != testGitUser || token != testGitToken {
		t.Errorf("got %q/%q, want the stored settings values", user, token)
	}

	// With nothing stored, the environment is the fallback rather than nothing.
	bare := NewGitService(&config.Config{GitHTTPSUser: "env-user", GitHTTPSToken: "env-token"}, newTestDBWithEncryptor(t))
	user, token = bare.httpsCredentials()
	if user != "env-user" || token != "env-token" {
		t.Errorf("got %q/%q, want the environment values", user, token)
	}
}
