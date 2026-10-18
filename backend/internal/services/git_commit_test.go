package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// Tests for agent-os-k81p.2: the user-initiated commit and push. Every
// fixture is a real repository with a local bare remote.

type commitFix struct {
	svc     *GitService
	db      *database.DB
	lock    *OperationLock
	dataDir string
	top     string // the work tree's top level
	dir     string // the stack folder under test
	sibling string // a second stack folder in the same repository ("" unless shared)
	bare    string
}

// newCommitFix builds a work clone of a bare remote with an upstream set, one
// registered stack folder (the top level, or app/ when shared, with other/ as
// a second stack folder) whose push switch is on, and the author settings.
func newCommitFix(t *testing.T, shared bool) *commitFix {
	t.Helper()
	// The service's git children inherit the environment: no host config.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	base := t.TempDir()
	f := &commitFix{bare: filepath.Join(base, "remote.git"), top: filepath.Join(base, "work")}
	runGit(t, base, "init", "-q", "--bare", "-b", "main", f.bare)
	runGit(t, base, "init", "-q", "-b", "main", f.top)
	files := map[string]string{"compose.yaml": "services: {}\n", "x": "x\n", ".env": "A=1\n", "decoy.txt": "d\n"}
	f.dir = f.top
	if shared {
		f.dir = filepath.Join(f.top, "app")
		f.sibling = filepath.Join(f.top, "other")
		files = map[string]string{
			"app/compose.yaml": "services: {}\n", "app/a.txt": "a\n",
			"other/compose.yaml": "services: {}\n",
		}
	}
	for name, body := range files {
		writeFile(t, filepath.Join(f.top, name), body)
	}
	runGit(t, f.top, "add", "-A")
	runGit(t, f.top, "commit", "-q", "-m", "seed")
	runGit(t, f.top, "remote", "add", "origin", f.bare)
	runGit(t, f.top, "push", "-q", "-u", "origin", "main")

	f.dataDir = t.TempDir()
	db, err := database.NewWithMigrationsAndEncryptor(f.dataDir, NewTokenEncryptorOrDefault("", "test-secret-key-for-unit-tests-32c"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	f.db = db
	f.registerStack(t, f.dir, "s-app", "app")
	if shared {
		f.registerStack(t, f.sibling, "s-other", "other")
	}
	require.NoError(t, db.SetSetting(models.SettingGitAuthorName, "Capstan User"))
	require.NoError(t, db.SetSetting(models.SettingGitAuthorEmail, "user@capstan.invalid"))

	f.svc = NewGitService(&config.Config{}, db)
	f.lock = NewOperationLock()
	f.svc.SetOperationLock(f.lock)
	return f
}

func (f *commitFix) registerStack(t *testing.T, dir, id, name string) {
	t.Helper()
	require.NoError(t, f.db.UpsertDirectory(models.Directory{Path: dir, Name: filepath.Base(dir)}))
	require.NoError(t, f.db.SetDirectoryPushEnabled(dir, true))
	require.NoError(t, f.db.UpsertStack(models.Stack{ID: id, Directory: dir, ComposeFile: "compose.yaml", EnvFile: ".env", ProjectName: name}))
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func (f *commitFix) head(t *testing.T) string {
	t.Helper()
	return runGit(t, f.top, "rev-parse", "HEAD")
}

func (f *commitFix) remoteMain(t *testing.T) string {
	t.Helper()
	return runGit(t, f.bare, "rev-parse", "refs/heads/main")
}

// committedFiles lists the paths HEAD's commit touched.
func (f *commitFix) committedFiles(t *testing.T) []string {
	t.Helper()
	out := runGit(t, f.top, "-c", "core.quotePath=false", "show", "--name-only", "--format=", "HEAD")
	return strings.Fields(out)
}

// requireRefusal asserts a classified failure with this status and code.
func requireRefusal(t *testing.T, ar truth.ActionResult, status int, code string) {
	t.Helper()
	require.Equal(t, truth.OutcomeFailed, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	var appErr *models.AppError
	require.True(t, errors.As(ar.Err, &appErr), "err %v is not an AppError (reason %q)", ar.Err, ar.Reason)
	assert.Equal(t, status, appErr.Status, "message %q", appErr.Message)
	assert.Equal(t, code, appErr.Code, "message %q", appErr.Message)
}

func changePaths(r *models.GitChangesResult) []string {
	var p []string
	for _, c := range r.Changes {
		p = append(p, c.Path)
	}
	return p
}

func TestGetChanges_TrackedOnlyEnvFlagged(t *testing.T) {
	f := newCommitFix(t, false)
	// A stack env file whose name matches no .env pattern: flagged by the
	// stack's EnvFile, compared relative to the folder.
	require.NoError(t, f.db.UpsertStack(models.Stack{ID: "s-app", Directory: f.dir, ComposeFile: "compose.yaml", EnvFile: "config/vars", ProjectName: "app"}))
	writeFile(t, filepath.Join(f.top, "config/vars"), "B=1\n")
	runGit(t, f.top, "add", "config/vars")
	runGit(t, f.top, "commit", "-q", "-m", "vars")
	runGit(t, f.top, "push", "-q")

	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	writeFile(t, filepath.Join(f.top, ".env"), "A=2\n")
	writeFile(t, filepath.Join(f.top, "config/vars"), "B=2\n")
	writeFile(t, filepath.Join(f.top, "untracked.txt"), "u\n")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	byPath := map[string]models.GitChange{}
	for _, c := range got.Changes {
		byPath[c.Path] = c
	}
	assert.ElementsMatch(t, []string{"compose.yaml", ".env", "config/vars"}, changePaths(got), "untracked files must not be listed")
	assert.Equal(t, ".M", byPath["compose.yaml"].Status)
	assert.False(t, byPath["compose.yaml"].IsEnv)
	assert.True(t, byPath[".env"].IsEnv, ".env is an env file")
	assert.True(t, byPath["config/vars"].IsEnv, "the stack's EnvFile is an env file")
	assert.Equal(t, "main", got.Branch)
	assert.False(t, got.Detached)
	assert.True(t, got.HasUpstream)
	assert.True(t, got.PushEnabled)
	assert.True(t, got.IdentityConfigured)

	// No upstream: hasUpstream false, even though trackingBranch falls back.
	runGit(t, f.top, "branch", "--unset-upstream")
	got, err = f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	assert.False(t, got.HasUpstream)
}

// TestGetChangesFolderOnly: one repository, two stack folders. Only this
// folder's files are listed, with repository-root-relative paths, and the
// first record is a worktree-only change (the state a save leaves), whose
// leading status character is where a trimming parse would cut.
func TestGetChangesFolderOnly(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.top, "app/compose.yaml"), "services: {a: {}}\n")
	writeFile(t, filepath.Join(f.top, "other/compose.yaml"), "services: {b: {}}\n")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	require.Len(t, got.Changes, 1, "changes %+v", got.Changes)
	assert.Equal(t, models.GitChange{Path: "app/compose.yaml", Status: ".M"}, got.Changes[0])

	t.Run("rename", func(t *testing.T) {
		runGit(t, f.top, "mv", "app/a.txt", "app/b.txt")
		got, err := f.svc.GetChanges(f.dir)
		require.NoError(t, err)
		assert.Contains(t, got.Changes, models.GitChange{Path: "app/b.txt", OrigPath: "app/a.txt", Status: "R."})
		assert.NotContains(t, changePaths(got), "other/compose.yaml")
	})
}

func TestCommitIdentityMissing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		prep  func(f *commitFix)
		field string
	}{
		{"name unset", func(f *commitFix) {
			_, err := koy9Raw(t, f.dataDir).Exec("DELETE FROM settings WHERE key = ?", models.SettingGitAuthorName)
			require.NoError(t, err)
		}, "name"},
		{"email blank", func(f *commitFix) {
			require.NoError(t, f.db.SetSetting(models.SettingGitAuthorEmail, "   "))
		}, "email"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCommitFix(t, false)
			tc.prep(f)
			writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
			before := f.head(t)

			ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

			requireRefusal(t, ar, http.StatusBadRequest, models.ErrGitIdentityMissing)
			assert.Nil(t, w)
			assert.Equal(t, before, f.head(t), "a commit was written")
		})
	}
}

// TestCommitIdentityReadFault: an author setting that cannot be read refuses
// with a 500 naming that read, never commits under a blank or default author.
func TestCommitIdentityReadFault(t *testing.T) {
	f := newCommitFix(t, false)
	rltuNullSetting(t, f.dataDir, models.SettingGitAuthorEmail)
	_, readErr := f.db.GetSetting(models.SettingGitAuthorEmail)
	require.Error(t, readErr, "fixture: the email setting must fail to read")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)

	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

	require.Equal(t, truth.OutcomeFailed, ar.Outcome)
	assert.Equal(t, "could not read the commit author settings", ar.Reason)
	require.Error(t, ar.Err)
	assert.Contains(t, ar.Err.Error(), models.SettingGitAuthorEmail)
	var appErr *models.AppError
	assert.False(t, errors.As(ar.Err, &appErr), "a read fault must stay a 500, not a classified 4xx")
	assert.Nil(t, w)
	assert.Equal(t, before, f.head(t))

	// GET /changes answers the same fault as an error, never as "not configured".
	_, err := f.svc.GetChanges(f.dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), models.SettingGitAuthorEmail)
}

// TestCommitOnlyTicked: a decoy staged before the request stays staged and out
// of the commit, and an unticked modified file stays modified.
func TestCommitOnlyTicked(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	writeFile(t, filepath.Join(f.top, "x"), "x2\n")
	writeFile(t, filepath.Join(f.top, "decoy.txt"), "d2\n")
	runGit(t, f.top, "add", "decoy.txt")

	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "only compose", Files: []string{"compose.yaml"}})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	require.NotNil(t, w)
	assert.Equal(t, f.head(t), w.Commit)
	assert.Equal(t, []string{"compose.yaml"}, f.committedFiles(t))
	assert.Equal(t, "decoy.txt", runGit(t, f.top, "diff", "--cached", "--name-only"), "the pre-staged decoy must stay staged")
	assert.Equal(t, "x", runGit(t, f.top, "diff", "--name-only"), "the unticked file must stay modified")
	assert.Equal(t, "only compose", runGit(t, f.top, "log", "-1", "--format=%s"))
	assert.Equal(t, "Capstan User <user@capstan.invalid>", runGit(t, f.top, "log", "-1", "--format=%an <%ae>"))
	assert.Equal(t, "Capstan User <user@capstan.invalid>", runGit(t, f.top, "log", "-1", "--format=%cn <%ce>"))
	assert.False(t, w.PushRan)
}

// TestCommitHooksDisabled: a failing pre-commit hook in the repository does not
// run, and neither does a commit-msg hook.
func TestCommitHooksDisabled(t *testing.T) {
	f := newCommitFix(t, false)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\ntouch '" + marker + "'\nexit 1\n"
	for _, name := range []string{"pre-commit", "commit-msg"} {
		require.NoError(t, os.WriteFile(filepath.Join(f.top, ".git/hooks", name), []byte(hook), 0o755)) //nolint:gosec // test hook
	}
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "a repository hook ran (stat err %v)", err)
}

func TestCommitPushBareRemote(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Contains(t, ar.Reason, "including ones made outside Capstan")
	require.NotNil(t, w)
	assert.True(t, w.PushRan)
	assert.Equal(t, "origin", w.Remote)
	assert.Equal(t, "refs/heads/main", w.Branch)
	assert.Equal(t, f.head(t), f.remoteMain(t), "the commit did not reach the remote")
	assert.Equal(t, w.Commit, f.remoteMain(t))

	// Pushing again has nothing to send.
	ar, _ = f.svc.PushVerified(f.dir)
	assert.Equal(t, truth.OutcomeNoChange, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
}

// advanceRemote pushes a commit to the bare remote from another clone.
func advanceRemote(t *testing.T, f *commitFix) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, filepath.Dir(other), "clone", "-q", f.bare, other)
	writeFile(t, filepath.Join(other, "remote-only.txt"), "r\n")
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-q", "-m", "remote")
	runGit(t, other, "push", "-q", "origin", "main")
	return runGit(t, other, "rev-parse", "HEAD")
}

// TestPushRemoteAhead: both rejection shapes classify GIT_REMOTE_AHEAD and the
// local commit is kept, one ahead.
func TestPushRemoteAhead(t *testing.T) {
	t.Run("fetch first, commit and push", func(t *testing.T) {
		f := newCommitFix(t, false)
		remote := advanceRemote(t, f)
		writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

		ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})

		require.Equal(t, truth.OutcomePartial, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
		assert.Equal(t, models.ErrGitRemoteAhead, ar.Details["pushCode"])
		require.NotNil(t, w)
		assert.Equal(t, f.head(t), ar.Details["commit"])
		assert.True(t, w.PushRan)
		assert.Equal(t, remote, f.remoteMain(t), "the remote moved")
		assert.Equal(t, "1", runGit(t, f.top, "rev-list", "--count", "@{upstream}..HEAD"), "the local commit must be kept, one ahead")
	})
	t.Run("non-fast-forward, push", func(t *testing.T) {
		f := newCommitFix(t, false)
		remote := advanceRemote(t, f)
		writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
		runGit(t, f.top, "commit", "-q", "-am", "local")
		runGit(t, f.top, "fetch", "-q")
		local := f.head(t)

		ar, w := f.svc.PushVerified(f.dir)

		requireRefusal(t, ar, http.StatusConflict, models.ErrGitRemoteAhead)
		require.NotNil(t, w)
		assert.True(t, w.PushRan)
		assert.Equal(t, remote, f.remoteMain(t))
		assert.Equal(t, local, f.head(t))
		status, err := f.svc.GetStatus(f.dir)
		require.NoError(t, err)
		assert.Equal(t, 1, status.Ahead)
	})
}

func TestPushNoUpstream(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")
	before := f.remoteMain(t)

	ar, w := f.svc.PushVerified(f.dir)

	requireRefusal(t, ar, http.StatusConflict, models.ErrGitNoUpstream)
	assert.Nil(t, w)
	assert.Equal(t, before, f.remoteMain(t))
	assert.Equal(t, "", runGit(t, f.top, "config", "--default", "", "--get", "branch.feature.remote"), "an upstream was set")
	assert.NotContains(t, runGit(t, f.bare, "branch", "--list"), "feature", "a branch was created on the remote")
}

func TestPushDetached(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "checkout", "-q", "--detach")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)

	ar, w := f.svc.PushVerified(f.dir)
	requireRefusal(t, ar, http.StatusConflict, models.ErrGitDetachedHead)
	assert.Nil(t, w)

	// A local commit on a detached HEAD is refused too.
	ar, w = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})
	requireRefusal(t, ar, http.StatusConflict, models.ErrGitDetachedHead)
	assert.Nil(t, w)
	assert.Equal(t, before, f.head(t))
}

func TestCommitRefusesMergeInProgress(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, ".git/MERGE_HEAD"), f.head(t)+"\n")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

	requireRefusal(t, ar, http.StatusConflict, models.ErrGitOperationInProgress)
	assert.Equal(t, before, f.head(t))
}

// debugSlog captures slog at debug level, so the zero-exit stderr lines
// gitCommandWithCreds logs are searched too.
func debugSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestCommitPushRedact: a remote URL carrying the token as userinfo pushes,
// and neither the result, its error nor the log holds the token or the URL.
func TestCommitPushRedact(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	local, _ := authenticatedHTTPRepo(t)
	// authenticatedHTTPRepo's backend refuses receive-pack unless enabled.
	runGit(t, filepath.Join(filepath.Dir(local), "srv", "repo.git"), "config", "http.receivepack", "true")
	plainURL := runGit(t, local, "remote", "get-url", "origin")
	tokenURL := strings.Replace(plainURL, "http://", "http://"+testGitUser+":"+testGitToken+"@", 1)
	runGit(t, local, "remote", "set-url", "origin", tokenURL)
	runGit(t, local, "fetch", "-q", "origin")
	runGit(t, local, "branch", "-q", "--set-upstream-to=origin/main", "main")

	db := newTestDB(t)
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: local, Name: "local"}))
	require.NoError(t, db.SetDirectoryPushEnabled(local, true))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s1", Directory: local, ComposeFile: "docker-compose.yml", ProjectName: "web"}))
	require.NoError(t, db.SetSetting(models.SettingGitAuthorName, "Capstan User"))
	require.NoError(t, db.SetSetting(models.SettingGitAuthorEmail, "user@capstan.invalid"))
	svc := NewGitService(&config.Config{}, db)
	svc.SetOperationLock(NewOperationLock())
	logs := debugSlog(t)

	noSecret := func(t *testing.T, ar truth.ActionResult) {
		t.Helper()
		body, err := json.Marshal(ar)
		require.NoError(t, err)
		errText := ""
		if ar.Err != nil {
			errText = ar.Err.Error()
		}
		for _, where := range map[string]string{"result": string(body), "error": errText, "reason": ar.Reason, "log": logs.String()} {
			assert.NotContains(t, where, testGitToken)
			assert.NotContains(t, where, tokenURL)
		}
	}

	writeFile(t, filepath.Join(local, "docker-compose.yml"), "services: {a: {}}\n")
	ar, _ := svc.CommitVerified(local, CommitRequest{Message: "m", Files: []string{"docker-compose.yml"}, Push: true})
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	// Positive arm: the push really went through the token in the URL.
	assert.Equal(t, runGit(t, local, "rev-parse", "HEAD"),
		runGit(t, filepath.Join(filepath.Dir(local), "srv", "repo.git"), "rev-parse", "refs/heads/main"))
	noSecret(t, ar)

	// Failure arm: a wrong token in the URL fails the push; still no secret.
	runGit(t, local, "remote", "set-url", "origin", strings.Replace(tokenURL, testGitToken, testGitToken+"-wrong", 1))
	writeFile(t, filepath.Join(local, "docker-compose.yml"), "services: {b: {}}\n")
	runGit(t, local, "commit", "-q", "-am", "local")
	ar, _ = svc.PushVerified(local)
	requireRefusal(t, ar, http.StatusBadGateway, models.ErrGitRemoteUnreachable)
	noSecret(t, ar)
}

// recordingGitOnPath puts a git on PATH that records every invocation and then
// runs the real one, and returns the record file.
func recordingGitOnPath(t *testing.T) string {
	t.Helper()
	realGit := runGitPath(t)
	dir := t.TempDir()
	record := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + record + "'\nexec '" + realGit + "' \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755)) //nolint:gosec // test-owned wrapper
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func runGitPath(t *testing.T) string {
	t.Helper()
	p := runGit(t, t.TempDir(), "--exec-path")
	return filepath.Join(p, "git")
}

// TestPushSSHRefused: an ssh-auth directory is refused before any git child,
// and an inherit-auth directory whose push URL is ssh-shaped is refused
// before any push.
func TestPushSSHRefused(t *testing.T) {
	t.Run("ssh auth type runs no git child", func(t *testing.T) {
		f := newCommitFix(t, false)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "SSH", "/keys/id", "", ""))
		record := recordingGitOnPath(t)

		ar, w := f.svc.PushVerified(f.dir)
		requireRefusal(t, ar, http.StatusConflict, models.ErrGitSSHUnsupported)
		assert.Nil(t, w)
		ar, _ = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})
		requireRefusal(t, ar, http.StatusConflict, models.ErrGitSSHUnsupported)
		_, err := os.Stat(record)
		assert.True(t, os.IsNotExist(err), "a git child ran for an ssh-auth directory")

		// Control: the same wrapper records an https directory's push.
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "inherit", "", "", ""))
		_, _ = f.svc.PushVerified(f.dir)
		calls, err := os.ReadFile(record) //nolint:gosec // test-owned record file under t.TempDir()
		require.NoError(t, err, "the recording wrapper is not on PATH")
		assert.Contains(t, string(calls), "push --porcelain")
	})

	for _, tc := range []struct {
		name string
		prep func(t *testing.T, f *commitFix)
		code string
	}{
		{"scp-like with a user", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		}, models.ErrGitSSHUnsupported},
		{"scp-like without a user", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "example.invalid:o/r.git")
		}, models.ErrGitSSHUnsupported},
		{"pushurl rewrite while url stays https", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "https://example.invalid/o/r.git")
			runGit(t, f.top, "config", "remote.origin.pushurl", "ssh://git@example.invalid/o/r.git")
		}, models.ErrGitSSHUnsupported},
		{"pushInsteadOf rewrite", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "https://example.invalid/o/r.git")
			runGit(t, f.top, "config", "url.git+ssh://git@example.invalid/.pushInsteadOf", "https://example.invalid/")
		}, models.ErrGitSSHUnsupported},
		{"two push URLs, local then ssh-shaped", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "config", "--add", "remote.origin.pushurl", f.bare)
			runGit(t, f.top, "config", "--add", "remote.origin.pushurl", "git@example.invalid:o/r.git")
		}, models.ErrGitPushURLUnsupported},
		{"two push URLs, both local", func(t *testing.T, f *commitFix) {
			second := filepath.Join(t.TempDir(), "second.git")
			runGit(t, filepath.Dir(second), "init", "-q", "--bare", "-b", "main", second)
			runGit(t, f.top, "config", "--add", "remote.origin.pushurl", f.bare)
			runGit(t, f.top, "config", "--add", "remote.origin.pushurl", second)
		}, models.ErrGitPushURLUnsupported},
		{"git protocol", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "git://example.invalid/o/r.git")
		}, models.ErrGitPushURLUnsupported},
		{"transport helper", func(t *testing.T, f *commitFix) {
			runGit(t, f.top, "remote", "set-url", "origin", "ext::sh -c true")
		}, models.ErrGitPushURLUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCommitFix(t, false)
			tc.prep(t, f)
			writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
			remoteBefore := f.remoteMain(t)
			// One commit ahead, so a push that wrongly ran would move the remote.
			runGit(t, f.top, "-c", "core.hooksPath=/dev/null", "commit", "-q", "--allow-empty", "-m", "ahead")
			before := f.head(t)

			ar, w := f.svc.PushVerified(f.dir)
			requireRefusal(t, ar, http.StatusConflict, tc.code)
			assert.Nil(t, w, "a push ran")
			assert.Equal(t, remoteBefore, f.remoteMain(t), "the remote moved")

			ar, w = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})
			requireRefusal(t, ar, http.StatusConflict, tc.code)
			assert.Nil(t, w)
			assert.Equal(t, before, f.head(t), "a commit was written before a predictable push refusal")
		})
	}
}

// TestGitCommitLockSharedRepo: a lock held on the sibling folder's stack blocks
// a commit in this folder of the same repository.
func TestGitCommitLockSharedRepo(t *testing.T) {
	f := newCommitFix(t, true)
	writeFile(t, filepath.Join(f.top, "app/compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)
	token, err := f.lock.Acquire("s-other", OpKindBackup)
	require.NoError(t, err)

	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/compose.yaml"}})
	requireRefusal(t, ar, http.StatusConflict, models.ErrOperationInProgress)
	assert.Contains(t, ar.Err.Error(), "stack other: backup in progress since")
	assert.Nil(t, w)
	assert.Equal(t, before, f.head(t))

	f.lock.Release("s-other", token)
	ar, _ = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/compose.yaml"}})
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	for _, id := range []string{"s-app", "s-other"} {
		tok, err := f.lock.Acquire(id, OpKindStart)
		require.NoError(t, err, "the commit left %s locked", id)
		f.lock.Release(id, tok)
	}
}

// TestCommitLiteralPathspec: a tracked file named ":!x" is that file, not
// pathspec magic that sweeps in every other change.
func TestCommitLiteralPathspec(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, ":!x"), "magic\n")
	runGit(t, f.top, "add", "--", ":(literal):!x")
	runGit(t, f.top, "commit", "-q", "-m", "magic name")
	runGit(t, f.top, "push", "-q")
	writeFile(t, filepath.Join(f.top, ":!x"), "magic2\n")
	writeFile(t, filepath.Join(f.top, "x"), "x2\n")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{":!x"}})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, []string{":!x"}, f.committedFiles(t))
	assert.ElementsMatch(t, []string{"compose.yaml", "x"}, strings.Fields(runGit(t, f.top, "diff", "--name-only")))
}

// TestCommitTrackedSymlinkOutsideFolder: a tracked symlink in the folder that
// points outside it is listed, and committing it commits the link only.
func TestCommitTrackedSymlinkOutsideFolder(t *testing.T) {
	f := newCommitFix(t, false)
	outside := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(outside, "a"), filepath.Join(f.top, "link")))
	runGit(t, f.top, "add", "link")
	runGit(t, f.top, "commit", "-q", "-m", "link")
	require.NoError(t, os.Remove(filepath.Join(f.top, "link")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "b"), filepath.Join(f.top, "link")))
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	assert.Contains(t, changePaths(got), "link")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"link"}})
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, []string{"link"}, f.committedFiles(t))
	assert.True(t, strings.HasPrefix(runGit(t, f.top, "ls-tree", "HEAD", "link"), "120000 "), "link was not committed as a symlink")
}

func TestPushHooksDisabled(t *testing.T) {
	f := newCommitFix(t, false)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\ntouch '" + marker + "'\nexit 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(f.top, ".git/hooks/pre-push"), []byte(hook), 0o755)) //nolint:gosec // test hook
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "-c", "core.hooksPath=/dev/null", "commit", "-q", "-am", "local")

	ar, _ := f.svc.PushVerified(f.dir)

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, f.head(t), f.remoteMain(t))
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "the pre-push hook ran (stat err %v)", err)
}

// TestPushIgnoresRepoReceivePack: for a local remote git runs the
// receive-pack program, and repository config must not choose it.
func TestPushIgnoresRepoReceivePack(t *testing.T) {
	f := newCommitFix(t, false)
	marker := filepath.Join(t.TempDir(), "receivepack-ran")
	script := filepath.Join(t.TempDir(), "rp")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexec git-receive-pack \"$@\"\n"), 0o755)) //nolint:gosec // test script
	runGit(t, f.top, "config", "remote.origin.receivepack", script)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")

	// Control: a plain push honours the repository's receivepack.
	runGit(t, f.top, "push", "-q", "--dry-run", "origin", "main")
	_, err := os.Stat(marker)
	require.NoError(t, err, "control: the configured receivepack did not run")
	require.NoError(t, os.Remove(marker))

	ar, _ := f.svc.PushVerified(f.dir)
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, f.head(t), f.remoteMain(t))
	_, err = os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "the repository's receivepack ran (stat err %v)", err)
}

// corruptPushSwitch makes the push switch of dir unreadable while every other
// column, and so the credential read, still answers.
func corruptPushSwitch(t *testing.T, f *commitFix, dir string) {
	t.Helper()
	_, err := koy9Raw(t, f.dataDir).Exec("UPDATE directories SET git_push_enabled = 'garbage' WHERE path = ?", dir)
	require.NoError(t, err)
	_, readErr := f.db.GetDirectory(dir)
	require.Error(t, readErr, "fixture: the switch must fail to read")
	_, credErr := f.db.GetDirectoryCredentials(dir)
	require.NoError(t, credErr, "fixture: the credential read must still work")
}

func TestPushSwitchReadFault(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")
	before := f.remoteMain(t)
	corruptPushSwitch(t, f, f.dir)

	ar, w := f.svc.PushVerified(f.dir)

	require.Equal(t, truth.OutcomeFailed, ar.Outcome)
	assert.Equal(t, "could not read the push switch", ar.Reason)
	var appErr *models.AppError
	assert.False(t, errors.As(ar.Err, &appErr), "a read fault must stay a 500, err %v", ar.Err)
	assert.Nil(t, w)
	assert.Equal(t, before, f.remoteMain(t), "a push ran on an unreadable switch")

	_, err := f.svc.GetChanges(f.dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "push switch")
}

func TestCommitSwitchReadFault(t *testing.T) {
	f := newCommitFix(t, false)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)
	corruptPushSwitch(t, f, f.dir)

	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

	require.Equal(t, truth.OutcomeFailed, ar.Outcome)
	assert.Equal(t, "could not read the push switch", ar.Reason)
	assert.Nil(t, w)
	assert.Equal(t, before, f.head(t), "a commit ran on an unreadable switch")
}

// TestPushRemoteDot: an upstream in this same repository moves a local branch
// and reports success; it is refused and nothing moves.
func TestPushRemoteDot(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "branch", "base")
	runGit(t, f.top, "config", "branch.main.remote", ".")
	runGit(t, f.top, "config", "branch.main.merge", "refs/heads/base")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")
	base := runGit(t, f.top, "rev-parse", "refs/heads/base")

	ar, w := f.svc.PushVerified(f.dir)

	requireRefusal(t, ar, http.StatusConflict, models.ErrGitNoUpstream)
	// The remote "." check itself, not the get-url fallback that also 409s.
	assert.Contains(t, ar.Err.Error(), "tracks a local branch")
	assert.Nil(t, w)
	assert.Equal(t, base, runGit(t, f.top, "rev-parse", "refs/heads/base"), "a local branch moved")
}

func TestPushDisabledBeatsSSH(t *testing.T) {
	f := newCommitFix(t, false)
	require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "ssh", "/keys/id", "", ""))
	require.NoError(t, f.db.SetDirectoryPushEnabled(f.dir, false))

	ar, _ := f.svc.PushVerified(f.dir)
	requireRefusal(t, ar, http.StatusForbidden, models.ErrGitPushDisabled)
	ar, _ = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})
	requireRefusal(t, ar, http.StatusForbidden, models.ErrGitPushDisabled)
}

// TestCommitPushPreconditions: commit with push on a branch with no upstream
// is refused before the commit, so nothing is half done.
func TestCommitPushPreconditions(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "checkout", "-q", "-b", "feature")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)

	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})

	requireRefusal(t, ar, http.StatusConflict, models.ErrGitNoUpstream)
	assert.Nil(t, w)
	assert.Equal(t, before, f.head(t), "the commit ran before a predictable push refusal")
	assert.Equal(t, "compose.yaml", runGit(t, f.top, "diff", "--name-only"))
}

func TestPushVerified_NilLockRefuses(t *testing.T) {
	f := newCommitFix(t, false)
	f.svc.SetOperationLock(nil)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	before := f.head(t)

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})
	require.Equal(t, truth.OutcomeFailed, ar.Outcome)
	assert.Equal(t, http.StatusInternalServerError, ar.HTTPStatus())
	ar, _ = f.svc.PushVerified(f.dir)
	require.Equal(t, truth.OutcomeFailed, ar.Outcome)
	assert.Equal(t, before, f.head(t))
}

func TestParsePorcelainV2(t *testing.T) {
	out := "1 .M N... 100644 100644 100644 abc abc a dir/file with spaces.yaml\x00" +
		"2 R. N... 100644 100644 100644 abc abc R100 new name\x00old name\x00" +
		"u UU N... 100644 100644 100644 100644 a b c conflicted\x00" +
		"? untracked\x00"
	got, err := parsePorcelainV2(out)
	require.NoError(t, err)
	assert.Equal(t, []porcelainEntry{
		{xy: ".M", path: "a dir/file with spaces.yaml"},
		{xy: "R.", path: "new name", origPath: "old name"},
		{xy: "UU", path: "conflicted", unmerged: true},
	}, got)

	_, err = parsePorcelainV2("2 R. N... 100644 100644 100644 abc abc R100 new\x00")
	assert.Error(t, err, "a rename with no original path")
	_, err = parsePorcelainV2(" M v1 record\x00")
	assert.Error(t, err, "a v1 record")
}

func TestPushURLRefusal(t *testing.T) {
	for u, want := range map[string]string{
		"https://h/r.git":       "",
		"http://u:t@h/r.git":    "",
		"file:///srv/r.git":     "",
		"/srv/r.git":            "",
		"../r.git":              "",
		"/srv/a:b/r.git":        "",
		"ssh://git@h/r.git":     models.ErrGitSSHUnsupported,
		"git+ssh://h/r.git":     models.ErrGitSSHUnsupported,
		"ssh+git://h/r.git":     models.ErrGitSSHUnsupported,
		"git@h:r.git":           models.ErrGitSSHUnsupported,
		"h:r.git":               models.ErrGitSSHUnsupported,
		"git://h/r.git":         models.ErrGitPushURLUnsupported,
		"ext::sh -c x":          models.ErrGitPushURLUnsupported,
		"fd::3":                 models.ErrGitPushURLUnsupported,
		"":                      models.ErrGitPushURLUnsupported,
		"ftp://h/r.git":         models.ErrGitPushURLUnsupported,
		"HTTPS://H/R.git":       "",
		"SSH://git@h/r.git":     models.ErrGitSSHUnsupported,
		"https://h/path::x.git": "",
	} {
		got := pushURLRefusal("origin", u)
		code := ""
		if got != nil {
			code = got.Code
		}
		if got != nil && u != "" {
			assert.NotContains(t, got.Message, u, "the refusal must not echo the URL")
		}
		assert.Equal(t, want, code, "url %q", u)
	}
}

// gitTry runs git like runGit but returns the error instead of failing, for
// controls that must show plain git doing what the service refuses to.
func gitTry(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	//nolint:gosec // test helper, explicit argv
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.invalid")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestPushMultiValuedUpstream: @{upstream} resolves the FIRST
// branch.<b>.merge and `config --get` the LAST, so two values would push HEAD
// to a branch the upstream does not name. Refused, nothing moves.
func TestPushMultiValuedUpstream(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"two merge values", "branch.main.merge", "refs/heads/production"},
		{"two remote values", "branch.main.remote", "origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCommitFix(t, false)
			runGit(t, f.top, "push", "-q", "origin", "main:production")
			runGit(t, f.top, "config", "--add", tc.key, tc.value)
			writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
			runGit(t, f.top, "commit", "-q", "-am", "local")
			main, production := f.remoteMain(t), runGit(t, f.bare, "rev-parse", "refs/heads/production")

			ar, w := f.svc.PushVerified(f.dir)

			requireRefusal(t, ar, http.StatusConflict, models.ErrGitNoUpstream)
			assert.Contains(t, ar.Err.Error(), "more than one upstream")
			assert.Nil(t, w)
			assert.Equal(t, main, f.remoteMain(t))
			assert.Equal(t, production, runGit(t, f.bare, "rev-parse", "refs/heads/production"), "the push moved another remote branch")
		})
	}
}

// TestPushFailureLogsCause: a failed push logs git's own text (the only
// place an auth failure and a network failure differ), redacted.
func TestPushFailureLogsCause(t *testing.T) {
	f := newCommitFix(t, false)
	require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", testGitUser, testGitToken))
	runGit(t, f.top, "remote", "set-url", "origin", "http://"+testGitUser+":"+testGitToken+"@127.0.0.1:9/r.git")
	logs := debugSlog(t)

	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")
	ar, _ := f.svc.PushVerified(f.dir)
	requireRefusal(t, ar, http.StatusBadGateway, models.ErrGitRemoteUnreachable)
	assert.Contains(t, logs.String(), "git push failed")
	assert.Contains(t, logs.String(), "Failed to connect", "git's cause is not in the log")

	// The commit-kept path logs it too.
	logs.Reset()
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {b: {}}\n")
	ar, _ = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})
	require.Equal(t, truth.OutcomePartial, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Contains(t, logs.String(), "Failed to connect")
	assert.NotContains(t, logs.String(), testGitToken)
}

func TestCommitIgnoresRepoGPGSign(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "config", "commit.gpgsign", "true")
	runGit(t, f.top, "config", "gpg.program", "false")
	writeFile(t, filepath.Join(f.top, "x"), "x2\n")
	_, err := gitTry(t, f.top, "commit", "-q", "-m", "control", "--", "x")
	require.Error(t, err, "control: the repository's signing config must make a plain commit fail")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.NotContains(t, runGit(t, f.top, "cat-file", "commit", "HEAD"), "gpgsig")
}

// TestCommitIgnoresRepoFSMonitor: the repository's fsmonitor program runs
// under plain git and not in any child of a commit request.
func TestCommitIgnoresRepoFSMonitor(t *testing.T) {
	f := newCommitFix(t, false)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	script := filepath.Join(t.TempDir(), "fsmonitor")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0o755)) //nolint:gosec // test script
	runGit(t, f.top, "config", "core.fsmonitor", script)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	_, err := gitTry(t, f.top, "status", "--porcelain")
	require.NoError(t, err, "control: plain git status")
	_, err = os.Stat(marker)
	require.NoError(t, err, "control: plain git status did not run the fsmonitor program")
	require.NoError(t, os.Remove(marker))

	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	_, err = os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "the fsmonitor program ran during a commit request (stat err %v)", err)
}

func TestPushDoesNotFollowTags(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "config", "push.followTags", "true")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")
	runGit(t, f.top, "tag", "-a", "v1", "-m", "v1")
	control := runGit(t, f.top, "push", "--dry-run", "--porcelain", "origin", "HEAD:refs/heads/main")
	require.Contains(t, control, "refs/tags/v1", "control: plain push would send the tag")

	ar, _ := f.svc.PushVerified(f.dir)

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, f.head(t), f.remoteMain(t))
	assert.Empty(t, runGit(t, f.bare, "tag", "--list"), "a tag was pushed")
}

// TestPushIgnoresRepoRecurseSubmodules: push.recurseSubmodules=check in the
// repository refuses a push whose submodule commit is on no remote; the
// service's push is not the repository's to change.
func TestPushIgnoresRepoRecurseSubmodules(t *testing.T) {
	f := newCommitFix(t, false)
	base := t.TempDir()
	subBare := filepath.Join(base, "sub.git")
	subSeed := filepath.Join(base, "subseed")
	runGit(t, base, "init", "-q", "--bare", "-b", "main", subBare)
	runGit(t, base, "init", "-q", "-b", "main", subSeed)
	writeFile(t, filepath.Join(subSeed, "s"), "s\n")
	runGit(t, subSeed, "add", "-A")
	runGit(t, subSeed, "commit", "-q", "-m", "sub seed")
	runGit(t, subSeed, "push", "-q", subBare, "main")
	runGit(t, f.top, "-c", "protocol.file.allow=always", "submodule", "-q", "add", subBare, "sub")
	runGit(t, f.top, "commit", "-q", "-m", "add sub")
	runGit(t, f.top, "push", "-q")
	writeFile(t, filepath.Join(f.top, "sub/s"), "s2\n")
	runGit(t, filepath.Join(f.top, "sub"), "commit", "-q", "-am", "unpushed sub commit")
	runGit(t, f.top, "add", "sub")
	runGit(t, f.top, "commit", "-q", "-m", "bump sub")
	runGit(t, f.top, "config", "push.recurseSubmodules", "check")
	_, err := gitTry(t, f.top, "push", "--dry-run", "origin", "HEAD:refs/heads/main")
	require.Error(t, err, "control: the repository's recurseSubmodules=check must refuse a plain push")

	ar, _ := f.svc.PushVerified(f.dir)

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, f.head(t), f.remoteMain(t))
}

// TestPushBranchNamedLikeATag: a tag named like the branch makes
// `symbolic-ref --short` answer "heads/main", which names no branch config.
func TestPushBranchNamedLikeATag(t *testing.T) {
	f := newCommitFix(t, false)
	runGit(t, f.top, "tag", "main")
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")

	got, err := f.svc.GetChanges(f.dir)
	require.NoError(t, err)
	assert.Equal(t, "main", got.Branch)
	ar, _ := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"compose.yaml"}, Push: true})

	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.Equal(t, f.head(t), f.remoteMain(t))
}

// TestPushRefusesURLNamedRemote: a remote whose NAME is a URL with userinfo
// (hand-written config) is refused before the name reaches a reason, the
// details, the log or the action log.
func TestPushRefusesURLNamedRemote(t *testing.T) {
	f := newCommitFix(t, false)
	const secret = "k81p-url-name-SECRET"
	name := "http://u:" + secret + "@127.0.0.1:9/r.git"
	runGit(t, f.top, "config", "remote."+name+".url", name)
	runGit(t, f.top, "config", "remote."+name+".fetch", "+refs/heads/*:refs/remotes/x/*")
	runGit(t, f.top, "update-ref", "refs/remotes/x/main", "HEAD")
	runGit(t, f.top, "config", "branch.main.remote", name)
	writeFile(t, filepath.Join(f.top, "compose.yaml"), "services: {a: {}}\n")
	runGit(t, f.top, "commit", "-q", "-am", "local")
	_, upErr := gitTry(t, f.top, "rev-parse", "--abbrev-ref", "@{upstream}")
	require.NoError(t, upErr, "fixture: the URL-named remote must resolve as the upstream")
	logs := debugSlog(t)

	ar, w := f.svc.PushVerified(f.dir)

	requireRefusal(t, ar, http.StatusConflict, models.ErrGitPushURLUnsupported)
	assert.Nil(t, w, "a push ran, so an action-log row would name the remote")
	body, err := json.Marshal(ar)
	require.NoError(t, err)
	for _, where := range []string{string(body), ar.Err.Error(), logs.String()} {
		assert.NotContains(t, where, secret)
	}
}

// TestCommitFilesCountsCommittedPaths: the files count is what was committed:
// a rename's original path counts, a duplicated name does not.
func TestCommitFilesCountsCommittedPaths(t *testing.T) {
	f := newCommitFix(t, true)
	runGit(t, f.top, "mv", "app/a.txt", "app/b.txt")
	ar, w := f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/b.txt"}})
	require.Equal(t, truth.OutcomeSuccess, ar.Outcome, "reason %q err %v", ar.Reason, ar.Err)
	assert.ElementsMatch(t, []string{"app/a.txt", "app/b.txt"},
		strings.Fields(runGit(t, f.top, "show", "--no-renames", "--name-only", "--format=", "HEAD")))
	assert.Equal(t, 2, w.Files)
	assert.Equal(t, 2, ar.Details["files"])

	writeFile(t, filepath.Join(f.top, "app/compose.yaml"), "services: {a: {}}\n")
	_, w = f.svc.CommitVerified(f.dir, CommitRequest{Message: "m", Files: []string{"app/compose.yaml", "app/compose.yaml"}})
	require.NotNil(t, w)
	assert.Equal(t, 1, w.Files)
}
