package services

import (
	"bytes"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/cgi" //nolint:gosec // hosts git-http-backend as a local, non-network-exposed httptest server for the credential regression harness (agent-os-qqw) below — the Httpoxy CVE this rule flags needs an actual exposed proxy, not a local test double
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
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
	//nolint:gosec // test helper, explicit argv, not a shell string
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

// realLocalRepo creates a real, minimal git repository on disk: no remote, one
// commit. getStatusCLI's nine internal `git` invocations need an actual repo to
// reach past the first `rev-parse` — a non-existent repo bails out after one
// call — so the credential-amplification tests (agent-os-9ha) need this rather
// than a bare path string.
func realLocalRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "initial")
	return dir
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

// gitServiceWithDirectoryCredential returns a GitService whose database holds
// a directory-scoped git credential for dirPath (encrypted at rest, exactly
// as UpdateDirectoryCredentials stores it), plus a DIFFERENT, invalid global
// settings token. A test using this must fail unless the directory-scoped
// credential — not merely "some" credential — is the one gitCmd used.
func gitServiceWithDirectoryCredential(t *testing.T, dirPath, authType, user, token string) *GitService {
	t.Helper()
	db := newTestDBWithEncryptor(t)
	if err := db.UpsertDirectory(models.Directory{Path: dirPath, Name: filepath.Base(dirPath)}); err != nil {
		t.Fatalf("seed directory row: %v", err)
	}
	if err := db.UpdateDirectoryCredentials(dirPath, authType, "", user, token); err != nil {
		t.Fatalf("store directory credentials: %v", err)
	}
	if err := db.SetSetting("git_https_user", "wrong-global-user"); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := db.SetSetting("git_https_token", "wrong-global-token"); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}
	return NewGitService(&config.Config{}, db)
}

// TestPull_UsesDirectoryHTTPSToken is the regression test for agent-os-qll's
// first defect (no read path): a per-directory credential saved via
// UpdateDirectoryCredentials must be the one gitCmd actually uses for that
// directory, in preference to whatever is stored globally.
func TestPull_UsesDirectoryHTTPSToken(t *testing.T) {
	local, advance := authenticatedHTTPRepo(t)
	want := advance()

	svc := gitServiceWithDirectoryCredential(t, local, "https", testGitUser, testGitToken)

	result, err := svc.Pull(local)
	if err != nil {
		t.Fatalf("Pull with a directory-scoped HTTPS token failed: %v", err)
	}
	if result.CurrentCommit != want {
		t.Errorf("HEAD after pull = %q, want the remote's new commit %q", result.CurrentCommit, want)
	}

	// Same leak check as TestPull_TokenNeverPersistsToGitConfig, for the
	// directory-scoped path.
	//nolint:gosec // local is a test fixture clone under t.TempDir()
	cfg, err := os.ReadFile(filepath.Join(local, ".git", "config"))
	if err != nil {
		t.Fatalf("read .git/config: %v", err)
	}
	if strings.Contains(string(cfg), testGitToken) {
		t.Errorf(".git/config contains the token:\n%s", cfg)
	}
}

// TestPull_DirectoryHTTPSAuthType_EmptyTokenDoesNotFallBackToGlobal covers the
// failure mode the fix must not reintroduce: a directory explicitly configured
// for its own HTTPS credential (authType "https") but with an empty stored
// token must NOT silently use the global settings token instead. That would
// reproduce agent-os-qll's whole pattern — the feature reports a specific
// credential is configured for this directory while actually doing something
// else — just shifted from "wipe on scan" to "wrong credential on pull".
func TestPull_DirectoryHTTPSAuthType_EmptyTokenDoesNotFallBackToGlobal(t *testing.T) {
	local, advance := authenticatedHTTPRepo(t)
	advance()

	// Global settings hold the CORRECT token; the directory row claims authType
	// "https" but never got one stored (empty string).
	svc := gitServiceWithDirectoryCredential(t, local, "https", testGitUser, "")
	if err := svc.db.SetSetting("git_https_token", testGitToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}
	if err := svc.db.SetSetting("git_https_user", testGitUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}

	if _, err := svc.Pull(local); err == nil {
		t.Fatal("expected the pull to fail: authType=https with no stored token must not fall back to the global one")
	}
}

// TestPull_DirectoryInheritAuthType_UsesGlobalToken covers the other half of
// the acceptance criteria: a directory left at authType "inherit" (or with no
// credential row at all) must still use the global settings token, exactly
// like agent-os-qqw's original fix.
func TestPull_DirectoryInheritAuthType_UsesGlobalToken(t *testing.T) {
	local, advance := authenticatedHTTPRepo(t)
	want := advance()

	db := newTestDBWithEncryptor(t)
	if err := db.UpsertDirectory(models.Directory{Path: local, Name: filepath.Base(local)}); err != nil {
		t.Fatalf("seed directory row: %v", err)
	}
	if err := db.UpdateDirectoryCredentials(local, "inherit", "", "", ""); err != nil {
		t.Fatalf("store directory credentials: %v", err)
	}
	if err := db.SetSetting("git_https_user", testGitUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := db.SetSetting("git_https_token", testGitToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}
	svc := NewGitService(&config.Config{}, db)

	result, err := svc.Pull(local)
	if err != nil {
		t.Fatalf("Pull with authType=inherit and a stored global token failed: %v", err)
	}
	if result.CurrentCommit != want {
		t.Errorf("HEAD after pull = %q, want the remote's new commit %q", result.CurrentCommit, want)
	}
}

// TestHTTPSCredentials_SSHAuthType_NoHTTPSCredentialApplies asserts that a
// directory configured for SSH auth never has the global (or any) HTTPS
// credential applied to it — SSH key auth is a separate, currently unwired
// path, and falling back to HTTPS here would silently use the wrong
// credential for that directory.
func TestHTTPSCredentials_SSHAuthType_NoHTTPSCredentialApplies(t *testing.T) {
	dirPath := "/stacks/ssh-repo"
	svc := gitServiceWithDirectoryCredential(t, dirPath, "ssh", "", "")
	if err := svc.db.SetSetting("git_https_user", testGitUser); err != nil {
		t.Fatalf("store git_https_user: %v", err)
	}
	if err := svc.db.SetSetting("git_https_token", testGitToken); err != nil {
		t.Fatalf("store git_https_token: %v", err)
	}

	user, token := svc.httpsCredentials(dirPath)
	if user != "" || token != "" {
		t.Errorf("got user=%q token=%q, want no HTTPS credential for an ssh-authType directory", user, token)
	}
}

// captureSlog redirects the process-wide slog default to a buffer for the
// duration of the test and restores the previous default on cleanup.
//
// The stdlib log package must be restored explicitly, and its writer and flags
// read BEFORE the swap: slog.SetDefault also does log.SetOutput(handlerWriter{})
// and log.SetFlags(0), and slog.SetDefault(prev) undoes neither, so restoring
// slog alone leaks the redirect and every later stdlib-log write in this test
// binary lands in a dead buffer (agent-os-ac0o). TestCaptureSlog_RestoresStdlibLog
// below is the ratchet that keeps this from being simplified back to one line.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevSlog := slog.Default()
	prevWriter, prevFlags := log.Writer(), log.Flags()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevWriter)
		log.SetFlags(prevFlags)
	})
	return &buf
}

// TestHTTPSCredentials_DirectoryHTTPSEmptyToken_LogsWarning gives the silent
// failure covered by TestPull_DirectoryHTTPSAuthType_EmptyTokenDoesNotFallBackToGlobal
// an operator-visible trail. Without this, "authType=https configured but no
// token saved" fails a pull with nothing beyond a generic auth error — exactly
// the kind of invisible failure agent-os-qll is about, just moved one step
// over from "wiped on scan" to "never explained on pull".
func TestHTTPSCredentials_DirectoryHTTPSEmptyToken_LogsWarning(t *testing.T) {
	dirPath := "/stacks/half-configured"
	svc := gitServiceWithDirectoryCredential(t, dirPath, "https", testGitUser, "")
	buf := captureSlog(t)

	user, token := svc.httpsCredentials(dirPath)
	if token != "" {
		t.Fatalf("expected an empty token, got %q", token)
	}
	_ = user

	out := buf.String()
	if !strings.Contains(out, dirPath) {
		t.Errorf("warning does not name the affected directory: %s", out)
	}
	if !strings.Contains(out, "no stored credential") {
		t.Errorf("warning does not explain the missing token: %s", out)
	}
	if strings.Contains(out, testGitToken) || strings.Contains(out, "wrong-global-token") {
		t.Errorf("warning leaked a token value: %s", out)
	}
}

// TestHTTPSCredentials_DirectoryHTTPSWithToken_NoWarning is the negative case:
// a directory that IS fully configured must not log anything, so the warning
// above stays a meaningful signal instead of routine noise on every pull.
func TestHTTPSCredentials_DirectoryHTTPSWithToken_NoWarning(t *testing.T) {
	dirPath := "/stacks/fully-configured"
	svc := gitServiceWithDirectoryCredential(t, dirPath, "https", testGitUser, testGitToken)
	buf := captureSlog(t)

	svc.httpsCredentials(dirPath)

	if buf.Len() != 0 {
		t.Errorf("expected no warning for a fully-configured directory credential, got: %s", buf.String())
	}
}

// TestGitCmd_DirectoryToken_TravelsInEnvNotArgv pins the same argv-safety
// invariant as TestGitCmd_TokenTravelsInEnvNotArgv onto the directory-scoped
// credential path.
func TestGitCmd_DirectoryToken_TravelsInEnvNotArgv(t *testing.T) {
	dirPath := "/stacks/https-repo"
	svc := gitServiceWithDirectoryCredential(t, dirPath, "https", testGitUser, testGitToken)

	cmd, token := svc.gitCmd(dirPath, "pull", "--ff-only")

	if token != testGitToken {
		t.Errorf("gitCmd returned token %q, want the directory-scoped one", token)
	}
	for i, arg := range cmd.Args {
		if strings.Contains(arg, testGitToken) {
			t.Errorf("argv[%d] contains the token: %q", i, arg)
		}
	}

	var sawToken bool
	for _, kv := range cmd.Env {
		if kv == credentialEnvToken+"="+testGitToken {
			sawToken = true
		}
	}
	if !sawToken {
		t.Error("directory-scoped credential missing from the child environment")
	}
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

	//nolint:gosec // local is a test fixture clone under t.TempDir()
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

// TestGitCmdWithCreds_StripsCapstanSecrets is the git sibling of
// TestLogs_DoesNotLeakCapstanSecrets (exec_env_test.go): gitCmdWithCreds must
// not forward Capstan's own secrets (JWT_SECRET, STORAGE_KEY, ...) into the
// git child's environment, while still carrying GIT_TERMINAL_PROMPT=0 and,
// when a token is configured, the credential helper's env pair. This is
// hygiene consistent with stripCapstanSecrets' other call sites
// (exec_env.go, backup_restic.go), not a response to a confirmed exploit.
func TestGitCmdWithCreds_StripsCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-jwt-secret")

	svc := gitServiceWithStoredToken(t)
	cmd, _ := svc.gitCmd(t.TempDir(), "status", "--porcelain")

	var sawSecret, sawPrompt, sawUser, sawToken bool
	for _, kv := range cmd.Env {
		switch {
		case strings.HasPrefix(kv, "JWT_SECRET="):
			sawSecret = true
		case kv == "GIT_TERMINAL_PROMPT=0":
			sawPrompt = true
		case kv == credentialEnvUser+"="+testGitUser:
			sawUser = true
		case kv == credentialEnvToken+"="+testGitToken:
			sawToken = true
		}
	}
	if sawSecret {
		t.Errorf("git child environment carries JWT_SECRET: %v", cmd.Env)
	}
	if !sawPrompt {
		t.Errorf("git child environment missing GIT_TERMINAL_PROMPT=0: %v", cmd.Env)
	}
	if !sawUser || !sawToken {
		t.Errorf("credential missing from the child environment (user=%v token=%v)", sawUser, sawToken)
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

// TestGetStatusCLI_DirectoryHTTPSEmptyToken_LogsWarnOnce is the WARN sibling of
// TestGetStatusCLI_UnreadableDirectoryCredential_LogsErrorOnce (git_credentials_decrypt_test.go):
// a directory configured for https auth with no stored token warns at
// git_credentials.go:103, on the SAME nine-internal-call getStatusCLI path
// (git.go:134,141,146,147,148,149,158,168,176), and amplified identically to
// nine before the fix (agent-os-9ha) because every one of those nine calls
// re-resolved credentials independently through httpsCredentials.
func TestGetStatusCLI_DirectoryHTTPSEmptyToken_LogsWarnOnce(t *testing.T) {
	dirPath := realLocalRepo(t)
	svc := gitServiceWithDirectoryCredential(t, dirPath, "https", testGitUser, "")
	buf := captureSlog(t)

	if _, err := svc.getStatusCLI(dirPath); err != nil {
		t.Fatalf("getStatusCLI: %v", err)
	}

	got := strings.Count(buf.String(), "no stored credential")
	if got != 1 {
		t.Errorf("got %d WARN lines for the empty stored https token, want exactly 1\nlog:\n%s", got, buf.String())
	}
}

// TestGetStatusCLI_ResolvedTokenReachesEveryInvocation is the auth-still-works
// counterpart to the log-count tests above. Those only assert on how many
// times a message was logged; none of them would notice a bug that threads an
// unresolved (empty) credential through the converted getStatusCLI path while
// still logging correctly — that would break private-remote auth silently,
// which is worse than the log noise agent-os-9ha set out to fix.
//
// A wrapper script stands in for `git` on PATH: it appends the credential
// environment variables it received to a shared log file on every invocation,
// then execs the real git binary so getStatusCLI still completes normally.
// gitCmdWithCreds sets CAPSTAN_GIT_USERNAME/CAPSTAN_GIT_PASSWORD in the child
// environment on every invocation once a token is configured (regardless of
// whether that particular git subcommand needs to contact a remote — see
// gitCmd's doc comment), so this observes the resolved credential landing on
// each of getStatusCLI's nine internal git processes directly, rather than
// inferring it from a log line.
func TestGetStatusCLI_ResolvedTokenReachesEveryInvocation(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}

	dirPath := realLocalRepo(t)
	svc := gitServiceWithDirectoryCredential(t, dirPath, "https", testGitUser, testGitToken)

	wrapperDir := t.TempDir()
	logPath := filepath.Join(wrapperDir, "env.log")
	wrapperScript := "#!/bin/sh\n" +
		"printf 'user=%s token=%s\\n' \"$" + credentialEnvUser + "\" \"$" + credentialEnvToken + "\" >> \"" + logPath + "\"\n" +
		"exec \"" + realGit + "\" \"$@\"\n"
	wrapperPath := filepath.Join(wrapperDir, "git")
	//nolint:gosec // test-owned wrapper script under t.TempDir(), not user input
	if err := os.WriteFile(wrapperPath, []byte(wrapperScript), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}

	t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if _, err := svc.getStatusCLI(dirPath); err != nil {
		t.Fatalf("getStatusCLI: %v", err)
	}

	//nolint:gosec // logPath is built a few lines above from filepath.Join(wrapperDir, "env.log"), where wrapperDir is this test's own t.TempDir() — never attacker- or environment-influenced, so the "variable path" this rule warns about is a path this test constructed itself, not external input
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read wrapper log (wrapper never ran — the real git was invoked directly instead of via PATH): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != 9 {
		t.Fatalf("wrapper observed %d git invocations, want 9 (one per getStatusCLI internal call): %v", len(lines), lines)
	}
	want := fmt.Sprintf("user=%s token=%s", testGitUser, testGitToken)
	for i, line := range lines {
		if line != want {
			t.Errorf("invocation %d: git child process env = %q, want %q — the resolved credential did not reach this invocation", i, line, want)
		}
	}
}

// TestHTTPSCredentials_SettingsBeatEnvironment records the precedence: an
// operator who stores a token through Settings expects it to win over whatever
// GIT_HTTPS_TOKEN the container was started with.
func TestHTTPSCredentials_SettingsBeatEnvironment(t *testing.T) {
	svc := gitServiceWithStoredToken(t)
	svc.config = &config.Config{GitHTTPSUser: "env-user", GitHTTPSToken: "env-token"}

	// No directory row exists for this path, so resolution falls straight
	// through to global settings/environment.
	user, token := svc.httpsCredentials("/no/such/directory")
	if user != testGitUser || token != testGitToken {
		t.Errorf("got %q/%q, want the stored settings values", user, token)
	}

	// With nothing stored, the environment is the fallback rather than nothing.
	bare := NewGitService(&config.Config{GitHTTPSUser: "env-user", GitHTTPSToken: "env-token"}, newTestDBWithEncryptor(t))
	user, token = bare.httpsCredentials("/no/such/directory")
	if user != "env-user" || token != "env-token" {
		t.Errorf("got %q/%q, want the environment values", user, token)
	}
}

// The global-token analogue of this precedence test — an undecryptable
// git_https_token must not fall through to GIT_HTTPS_TOKEN — lives in
// git_credentials_decrypt_test.go as
// TestHTTPSCredentials_GlobalDecryptFailure_FailsClosed, alongside the
// existing gitServiceWithUndecryptableGlobalCredential fixture that already
// does the same reopen-under-a-different-key setup this would otherwise
// duplicate (agent-os-oyj).

// TestCaptureSlog_RestoresStdlibLog guards the helper above, not the
// production code. slog.SetDefault silently re-points the stdlib log package
// (log.SetOutput + log.SetFlags(0)), and slog.SetDefault(prev) does not undo
// either, because prev's handler is slog's internal defaultHandler and the
// restore path skips the re-pointing branch. So a cleanup that restores only
// slog leaks the redirect for the rest of this test binary: every later
// stdlib-log write lands in a dead buffer instead of stderr.
//
// That is not theoretical in this package. The git-http-backend harness above
// runs an httptest server, and an http.Server with a nil ErrorLog reports
// through the stdlib log package — so a leaked redirect swallows exactly the
// "superfluous WriteHeader" warnings, hijack complaints and serving-goroutine
// panic traces that harness would otherwise surface. Nothing fails when that
// happens; the diagnostics simply stop arriving. This is what notices.
//
// The check is safe to run serially alongside this package's 221 t.Parallel
// tests: all nine captureSlog call sites are in serial tests (OBSERVED), and
// Go releases the parallel barrier only after every serial test has returned,
// so no parallel test can be mutating the stdlib log writer during the two
// comparisons below.
func TestCaptureSlog_RestoresStdlibLog(t *testing.T) {
	writerBefore, flagsBefore := log.Writer(), log.Flags()

	t.Run("capture", func(t *testing.T) { _ = captureSlog(t) })

	if log.Writer() != writerBefore {
		t.Errorf("stdlib log writer not restored: the capture leaked its redirect, so later log output in this binary is swallowed")
	}
	if log.Flags() != flagsBefore {
		t.Errorf("stdlib log flags not restored: got %d, want %d", log.Flags(), flagsBefore)
	}
}
