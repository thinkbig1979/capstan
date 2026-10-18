package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/cgi" //nolint:gosec // hosts git-http-backend as a local httptest server for the push tests below, never network-exposed
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Handler tests for agent-os-k81p.2: GET /git/changes, POST /git/commit and
// POST /git/push against real repositories with a local remote.

const (
	k81pGitUser  = "oauth2"
	k81pGitToken = "k81p-test-token-7d3e-do-not-log"
)

type gitWriteEnv struct {
	db      *database.DB
	lock    *services.OperationLock
	router  *gin.Engine
	root    string // the stacks dir
	top     string // the repository work tree
	dir     string // the stack folder ("app" under top when shared)
	remote  string // the bare remote's path
	stackID string
}

// k81pGit runs git with no host config and a fixed identity.
func k81pGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // test helper, explicit argv
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.invalid")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v in %s: %s", args, dir, out)
	return strings.TrimSpace(string(out))
}

func k81pWrite(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// newGitWriteEnv is a stacks dir holding one repository cloned from a bare
// remote, one registered stack (the repository root, or app/ with a second
// stack in other/ when shared), its push switch on, the author set, and the
// git and compose routes sharing one operation lock.
func newGitWriteEnv(t *testing.T, shared bool) *gitWriteEnv {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	gin.SetMode(gin.TestMode)

	e := &gitWriteEnv{root: t.TempDir(), stackID: "s-app"}
	e.remote = filepath.Join(t.TempDir(), "remote.git")
	e.top = filepath.Join(e.root, "repo")
	k81pGit(t, e.root, "init", "-q", "--bare", "-b", "main", e.remote)
	k81pGit(t, e.root, "init", "-q", "-b", "main", e.top)
	e.dir = e.top
	files := map[string]string{"compose.yaml": "services: {}\n", ".env": "A=1\n", "x": "x\n"}
	if shared {
		e.dir = filepath.Join(e.top, "app")
		files = map[string]string{"app/compose.yaml": "services: {}\n", "other/compose.yaml": "services: {}\n"}
	}
	for name, body := range files {
		k81pWrite(t, filepath.Join(e.top, name), body)
	}
	k81pGit(t, e.top, "add", "-A")
	k81pGit(t, e.top, "commit", "-q", "-m", "seed")
	k81pGit(t, e.top, "remote", "add", "origin", e.remote)
	k81pGit(t, e.top, "push", "-q", "-u", "origin", "main")

	db, err := database.NewWithMigrationsAndEncryptor(":memory:", services.NewTokenEncryptorOrDefault("k81p-2-handler-test-key-0123456789", ""))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	e.db = db
	register := func(dir, id, name string) {
		require.NoError(t, db.UpsertDirectory(models.Directory{Path: dir, Name: filepath.Base(dir)}))
		require.NoError(t, db.SetDirectoryPushEnabled(dir, true))
		require.NoError(t, db.UpsertStack(models.Stack{ID: id, Directory: dir, ComposeFile: "compose.yaml", EnvFile: ".env", ProjectName: name}))
	}
	register(e.dir, e.stackID, "app")
	if shared {
		register(filepath.Join(e.top, "other"), "s-other", "other")
	}
	require.NoError(t, db.SetSetting(models.SettingGitAuthorName, "Capstan User"))
	require.NoError(t, db.SetSetting(models.SettingGitAuthorEmail, "user@capstan.invalid"))

	cfg := &config.Config{StacksDir: e.root}
	e.lock = services.NewOperationLock()
	gitSvc := services.NewGitService(cfg, db)
	gitSvc.SetOperationLock(e.lock)
	compose := NewComposeHandler(services.NewLinterService(), db, cfg)
	compose.SetOperationLock(e.lock)

	e.router = gin.New()
	NewGitHandler(gitSvc, nil, db, cfg).RegisterRoutes(e.router.Group("/api/git"))
	compose.RegisterRoutes(e.router.Group("/api/stacks"))
	return e
}

func (e *gitWriteEnv) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *gitWriteEnv) commit(t *testing.T, body string) *httptest.ResponseRecorder {
	return e.do(t, http.MethodPost, "/api/git/commit", body)
}

func (e *gitWriteEnv) head(t *testing.T) string { return k81pGit(t, e.top, "rev-parse", "HEAD") }
func (e *gitWriteEnv) remoteMain(t *testing.T) string {
	return k81pGit(t, e.remote, "rev-parse", "refs/heads/main")
}

type gitResultBody struct {
	Outcome string         `json:"outcome"`
	Reason  string         `json:"reason"`
	Details map[string]any `json:"details"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
}

func decodeGitResult(t *testing.T, w *httptest.ResponseRecorder) gitResultBody {
	t.Helper()
	var b gitResultBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &b), "body %s", w.Body.String())
	return b
}

// requireGitCode asserts the status and the classified code, wherever the
// body carries it: details.code for an ActionResult, code for an AppError.
func requireGitCode(t *testing.T, w *httptest.ResponseRecorder, status int, code string) gitResultBody {
	t.Helper()
	require.Equal(t, status, w.Code, "body %s", w.Body.String())
	b := decodeGitResult(t, w)
	got := b.Code
	if got == "" && b.Details != nil {
		got, _ = b.Details["code"].(string)
	}
	assert.Equal(t, code, got, "body %s", w.Body.String())
	return b
}

func gitActionRows(t *testing.T, db *database.DB, action string) []models.ActionLog {
	t.Helper()
	all, err := db.GetRecentActions(100)
	require.NoError(t, err)
	var rows []models.ActionLog
	for _, a := range all {
		if a.Action == action {
			rows = append(rows, a)
		}
	}
	return rows
}

// TestComposeSaveNoGit: saving a compose file in a git repository writes the
// file and nothing else: HEAD and the remote ref stay where they were.
func TestComposeSaveNoGit(t *testing.T) {
	e := newGitWriteEnv(t, false)
	head, remote := e.head(t), e.remoteMain(t)

	w := e.do(t, http.MethodPut, "/api/stacks/"+e.stackID+"/compose", `{"content":"services:\n  web:\n    image: nginx\n"}`)

	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	got, err := os.ReadFile(filepath.Join(e.dir, "compose.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "services:\n  web:\n    image: nginx\n", string(got), "the save did not land")
	assert.Equal(t, head, e.head(t), "a compose save committed")
	assert.Equal(t, remote, e.remoteMain(t), "a compose save pushed")
	assert.Equal(t, "compose.yaml", k81pGit(t, e.top, "diff", "--name-only"), "the save must leave an uncommitted change")
}

// TestGitPushDisabled: with the switch off, commit (with or without push) and
// push answer 403 GIT_PUSH_DISABLED and write nothing, not even a log row.
func TestGitPushDisabled(t *testing.T) {
	e := newGitWriteEnv(t, false)
	require.NoError(t, e.db.SetDirectoryPushEnabled(e.dir, false))
	k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {a: {}}\n")
	head, remote := e.head(t), e.remoteMain(t)

	for name, w := range map[string]*httptest.ResponseRecorder{
		"commit":      e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"]}`),
		"commit+push": e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"],"push":true}`),
		"push":        e.do(t, http.MethodPost, "/api/git/push", `{"stackId":"s-app"}`),
	} {
		t.Run(name, func(t *testing.T) {
			requireGitCode(t, w, http.StatusForbidden, models.ErrGitPushDisabled)
		})
	}
	assert.Equal(t, head, e.head(t))
	assert.Equal(t, remote, e.remoteMain(t))
	assert.Empty(t, gitActionRows(t, e.db, services.ActionGitCommit))
	assert.Empty(t, gitActionRows(t, e.db, services.ActionGitPush))

	// The changes list still answers, and says the switch is off.
	w := e.do(t, http.MethodGet, "/api/git/changes?stackId=s-app", "")
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	var got models.GitChangesResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.False(t, got.PushEnabled)
	assert.Equal(t, []models.GitChange{{Path: "compose.yaml", Status: ".M"}}, got.Changes)
}

func TestCommitEnvConfirm(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.dir, ".env"), "A=2\n")
	k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {a: {}}\n")
	head := e.head(t)

	w := e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml",".env"]}`)
	requireGitCode(t, w, http.StatusBadRequest, models.ErrGitEnvConfirmRequired)
	assert.Equal(t, head, e.head(t), "an env file was committed without confirmEnv")

	w = e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml",".env"],"confirmEnv":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.ElementsMatch(t, []string{".env", "compose.yaml"}, strings.Fields(k81pGit(t, e.top, "show", "--name-only", "--format=", "HEAD")))
}

// TestCommitPathContainment: every file must be a changed tracked file of this
// stack's folder; anything else is a 400 and nothing is written.
func TestCommitPathContainment(t *testing.T) {
	e := newGitWriteEnv(t, true)
	k81pWrite(t, filepath.Join(e.top, "app/compose.yaml"), "services: {a: {}}\n")
	k81pWrite(t, filepath.Join(e.top, "other/compose.yaml"), "services: {b: {}}\n")
	k81pWrite(t, filepath.Join(e.top, "app/untracked.txt"), "u\n")
	head := e.head(t)

	for name, file := range map[string]string{
		"sibling folder's changed file": "other/compose.yaml",
		"dot-dot escape":                "app/../other/compose.yaml",
		"absolute path":                 filepath.Join(e.top, "app/compose.yaml"),
		"untracked file":                "app/untracked.txt",
		"unchanged path":                "app/nope.txt",
		"folder-relative spelling":      "compose.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"stackId": "s-app", "message": "m", "files": []string{file}})
			require.NoError(t, err)
			requireGitCode(t, e.commit(t, string(body)), http.StatusBadRequest, models.ErrValidation)
			assert.Equal(t, head, e.head(t))
		})
	}

	t.Run("a write names a stack, never a directory", func(t *testing.T) {
		requireGitCode(t, e.do(t, http.MethodPost, "/api/git/commit?dir=repo/app", `{"message":"m","files":["app/compose.yaml"]}`),
			http.StatusBadRequest, models.ErrValidation)
		requireGitCode(t, e.do(t, http.MethodGet, "/api/git/changes?dir=repo/app", ""), http.StatusBadRequest, models.ErrValidation)
		requireGitCode(t, e.do(t, http.MethodPost, "/api/git/push", `{"stackId":"missing"}`), http.StatusNotFound, models.ErrStackNotFound)
	})

	// Control: the folder's own file, spelled as the list spells it, commits.
	w := e.commit(t, `{"stackId":"s-app","message":"m","files":["app/compose.yaml"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.Equal(t, "app/compose.yaml", k81pGit(t, e.top, "show", "--name-only", "--format=", "HEAD"))
}

// TestGitCommitLock: a pull and a commit on one directory take turns.
func TestGitCommitLock(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {a: {}}\n")
	head := e.head(t)

	pullToken, err := e.lock.Acquire(e.stackID, services.OpKindGitPull)
	require.NoError(t, err)
	b := requireGitCode(t, e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"]}`),
		http.StatusConflict, models.ErrOperationInProgress)
	assert.Contains(t, b.Reason, "git pull in progress since")
	requireGitCode(t, e.do(t, http.MethodPost, "/api/git/push", `{"stackId":"s-app"}`), http.StatusConflict, models.ErrOperationInProgress)
	assert.Equal(t, head, e.head(t))
	e.lock.Release(e.stackID, pullToken)

	commitToken, err := e.lock.Acquire(e.stackID, services.OpKindGitCommit)
	require.NoError(t, err)
	b = requireGitCode(t, e.do(t, http.MethodPost, "/api/git/pull?stackId=s-app", ""), http.StatusConflict, models.ErrOperationInProgress)
	assert.Contains(t, b.Reason, "git commit in progress since")
	e.lock.Release(e.stackID, commitToken)

	// A compose save during a commit waits its turn too: one lock.
	commitToken, err = e.lock.Acquire(e.stackID, services.OpKindGitCommit)
	require.NoError(t, err)
	w := e.do(t, http.MethodPut, "/api/stacks/"+e.stackID+"/compose", `{"content":"services: {}\n"}`)
	requireGitCode(t, w, http.StatusConflict, models.ErrOperationInProgress)
	e.lock.Release(e.stackID, commitToken)

	w = e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"]}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
}

// gitHTTPRemote serves a bare repository over smart HTTP. authAll demands the
// token on every request; otherwise only a push (receive-pack) needs it, the
// public-read, auth-write shape most forges use.
func gitHTTPRemote(t *testing.T, authAll bool) (bare, url string) {
	t.Helper()
	execPath, err := exec.Command("git", "--exec-path").Output()
	if err != nil {
		t.Skipf("git --exec-path: %v", err)
	}
	backend := filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skipf("git-http-backend not available: %v", err)
	}
	root := t.TempDir()
	bare = filepath.Join(root, "repo.git")
	k81pGit(t, root, "init", "-q", "--bare", "-b", "main", bare)
	k81pGit(t, bare, "config", "http.receivepack", "true")
	handler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "PATH=" + os.Getenv("PATH")}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isPush := strings.HasSuffix(r.URL.Path, "/git-receive-pack") || r.URL.Query().Get("service") == "git-receive-pack"
		if authAll || isPush {
			user, pass, ok := r.BasicAuth()
			if !ok || user != k81pGitUser || pass != k81pGitToken {
				w.Header().Set("WWW-Authenticate", `Basic realm="k81p"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return bare, srv.URL + "/repo.git"
}

// pointAtHTTPRemote moves origin to an HTTP remote seeded with the local
// branch, and sets the upstream again.
func (e *gitWriteEnv) pointAtHTTPRemote(t *testing.T, bare, url string) {
	t.Helper()
	k81pGit(t, e.top, "push", "-q", bare, "main")
	k81pGit(t, e.top, "remote", "set-url", "origin", url)
	e.remote = bare
}

// TestPushAuthNot401: a push refused for its credentials answers 502, never
// 401, which the frontend would read as a lost Capstan session.
func TestPushAuthNot401(t *testing.T) {
	for _, tc := range []struct {
		name    string
		authAll bool
	}{
		{"auth on read and write", true},
		{"public read, auth on write", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newGitWriteEnv(t, false)
			bare, url := gitHTTPRemote(t, tc.authAll)
			e.pointAtHTTPRemote(t, bare, url)
			k81pGit(t, e.top, "fetch", "-q", bare, "main:refs/remotes/origin/main")
			if !tc.authAll {
				// The fixture really is public-read: no credential needed.
				k81pGit(t, e.top, "ls-remote", url)
			}
			k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {a: {}}\n")
			k81pGit(t, e.top, "commit", "-q", "-am", "local")
			before := e.remoteMain(t)

			require.NoError(t, e.db.UpdateDirectoryCredentials(e.dir, "https", "", k81pGitUser, k81pGitToken+"-wrong"))
			w := e.do(t, http.MethodPost, "/api/git/push", `{"stackId":"s-app"}`)
			requireGitCode(t, w, http.StatusBadGateway, models.ErrGitRemoteUnreachable)
			assert.NotContains(t, w.Body.String(), k81pGitToken)
			assert.Equal(t, before, e.remoteMain(t))

			// Control: the right token pushes through the same fixture.
			require.NoError(t, e.db.UpdateDirectoryCredentials(e.dir, "https", "", k81pGitUser, k81pGitToken))
			w = e.do(t, http.MethodPost, "/api/git/push", `{"stackId":"s-app"}`)
			require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
			assert.Equal(t, e.head(t), e.remoteMain(t))
		})
	}
}

// TestGitCommitActionLog: one row per commit and one per push that ran, and a
// row never holds the token or the raw remote URL.
func TestGitCommitActionLog(t *testing.T) {
	e := newGitWriteEnv(t, false)
	bare, url := gitHTTPRemote(t, true)
	e.pointAtHTTPRemote(t, bare, url)
	tokenURL := strings.Replace(url, "http://", "http://"+k81pGitUser+":"+k81pGitToken+"@", 1)
	k81pGit(t, e.top, "remote", "set-url", "origin", tokenURL)
	k81pGit(t, e.top, "fetch", "-q", "origin")

	rows := func() (commits, pushes []models.ActionLog) {
		return gitActionRows(t, e.db, services.ActionGitCommit), gitActionRows(t, e.db, services.ActionGitPush)
	}

	// Commit only: one commit row.
	k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {a: {}}\n")
	require.Equal(t, http.StatusOK, e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"]}`).Code)
	commits, pushes := rows()
	require.Len(t, commits, 1)
	assert.Empty(t, pushes)
	assert.Equal(t, "s-app", commits[0].StackID)
	assert.Contains(t, commits[0].Detail, e.head(t))

	// Commit and push: one more of each.
	k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {b: {}}\n")
	w := e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"],"push":true}`)
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.Equal(t, e.head(t), e.remoteMain(t), "positive arm: the push went through the token in the URL")
	commits, pushes = rows()
	assert.Len(t, commits, 2)
	require.Len(t, pushes, 1)
	assert.Contains(t, pushes[0].Detail, `"remote":"origin"`)
	assert.Contains(t, pushes[0].Detail, `"outcome":"success"`)

	// Push only, up to date: a push row, no commit row.
	require.Equal(t, http.StatusOK, e.do(t, http.MethodPost, "/api/git/push", `{"stackId":"s-app"}`).Code)
	commits, pushes = rows()
	assert.Len(t, commits, 2)
	assert.Len(t, pushes, 2)

	// Commit lands, push fails (wrong token): both rows, the push row failed.
	k81pGit(t, e.top, "remote", "set-url", "origin", strings.Replace(tokenURL, k81pGitToken, k81pGitToken+"-wrong", 1))
	k81pWrite(t, filepath.Join(e.dir, "compose.yaml"), "services: {c: {}}\n")
	w = e.commit(t, `{"stackId":"s-app","message":"m","files":["compose.yaml"],"push":true}`)
	b := decodeGitResult(t, w)
	require.Equal(t, http.StatusMultiStatus, w.Code, "body %s", w.Body.String())
	assert.Equal(t, models.ErrGitRemoteUnreachable, b.Details["pushCode"])
	assert.Equal(t, e.head(t), b.Details["commit"])
	commits, pushes = rows()
	assert.Len(t, commits, 3)
	require.Len(t, pushes, 3)
	assert.Contains(t, pushes[0].Detail, `"outcome":"failed"`, "rows %+v", pushes)

	for _, r := range append(commits, pushes...) {
		assert.NotContains(t, r.Detail, k81pGitToken)
		assert.NotContains(t, r.Detail, "http://")
	}
	assert.NotContains(t, w.Body.String(), k81pGitToken)
}
