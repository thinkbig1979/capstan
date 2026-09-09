package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// a4a4InitNoCommits leaves dir in the state `git init` leaves behind: a real
// repository whose HEAD points at a branch that does not exist yet.
//
// The state is asserted from git rather than assumed, and BOTH halves are
// asserted, because either one alone is satisfied by the wrong fixture: a
// directory that is not a repository at all also fails `rev-parse --verify
// HEAD`, and would then be tested against the x40a non-repo path while looking
// like this one. `symbolic-ref HEAD` succeeding is what says "repository", and
// `rev-parse --verify HEAD` failing is what says "no commits".
func a4a4InitNoCommits(t *testing.T, dir string) {
	t.Helper()

	run := func(args ...string) ([]byte, error) {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		return cmd.CombinedOutput()
	}

	out, err := run("init", "--initial-branch=main")
	require.NoError(t, err, "git init in %s: %s", dir, out)

	out, err = run("symbolic-ref", "--quiet", "HEAD")
	require.NoError(t, err, "fixture precondition: HEAD must resolve symbolically, i.e. this must BE a repository: %s", out)

	out, err = run("rev-parse", "--verify", "HEAD")
	require.Error(t, err, "fixture precondition: HEAD must NOT resolve to a commit, else this is an ordinary repo and the test asserts nothing: %s", out)
}

// TestGitGetStatus_EmptyRepoAnswers200 pins agent-os-4a4a.
//
// A `git init`'d directory with no commits yet answered 404 GIT_NO_COMMITS, so
// it painted a red failed request in the browser console on an ordinary page
// load — the same symptom, on the same endpoint and the same page load, that
// agent-os-x40a exists to remove. x40a was scoped to models.ErrGitNotRepo by an
// explicit constraint, so this one condition survived it.
//
// # Why the answer is not simply x40a's
//
// A repository with no commits IS a repository. Answering `{isRepo: false}`
// would tell the frontend there is no git here when there is, so this needed
// its own wire shape rather than a one-line widening of x40a's condition:
//
//	git init'd, no commits  ->  200 {isRepo: true, hasCommits: false}
//	genuine non-repo        ->  200 {isRepo: false}      UNCHANGED (x40a)
//	directory is gone       ->  404 STACK_DIR_MISSING    UNCHANGED (n2df)
//
// The repo-only fields are ABSENT on the no-commits answer, never null and
// never zero-valued. That is x40a's absent-over-null precedent on this same
// endpoint: `ahead: 0` would mean both "up to date" and "no commits exist",
// which is the zero-versus-read-failure conflation x40a rejected.
//
// # Why four arms and not one
//
// Arm 1 alone is satisfied by a handler that answers 200 for every failure,
// and every gate would read green. Arm 2 is the control that forbids widening
// the success path: it is the neighbouring condition, one error code away, and
// it must still answer exactly what x40a left it answering — isRepo:false, and
// WITHOUT hasCommits, because a directory that is not a repository has no
// commit story to tell either way. Arm 3 is the control for the opposite
// direction: a stack whose directory is GONE is a broken deployment, not a
// healthy empty repo, and a 200 would hide an unmounted /stacks volume
// silently (no console error, no WARN, since STACK_DIR_MISSING is deliberately
// absent from respond.go's routineErrorCodes). Arm 4 pins the positive side of
// the new discriminator: hasCommits is emitted on BOTH repo branches, like
// isRepo, because the frontend's GitStatus type is a union narrowed on it and
// an ordinary repo answer that omitted it would render no chip at all.
//
// The discriminator is the error CODE, never the status and never the route:
// three distinct 404s reach the same line in GetStatus and only one of them
// changes.
func TestGitGetStatus_EmptyRepoAnswers200(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()

	// Arm 1: `git init` and nothing else.
	empty := filepath.Join(stacksDir, "empty")
	require.NoError(t, os.MkdirAll(empty, 0o750))
	a4a4InitNoCommits(t, empty)

	// Arm 2: a genuine non-repo, with no parent repository above it.
	plain := filepath.Join(stacksDir, "plain")
	require.NoError(t, os.MkdirAll(plain, 0o750))

	// Arm 3: a directory that does not exist.
	gone := filepath.Join(stacksDir, "gone")

	// Arm 4: an ordinary repository with a commit.
	committed := filepath.Join(stacksDir, "committed")
	require.NoError(t, os.MkdirAll(committed, 0o750))
	x40aRepoOnMain(t, committed)

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	get := func(dir string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/git?dir="+dir, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	t.Run("arm1 a repo with no commits answers 200", func(t *testing.T) {
		w := get(empty)
		require.Equal(t, http.StatusOK, w.Code,
			"`git init` with no commit yet is normal configuration and nobody's mistake, not a client error; body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		assert.Equal(t, true, body["isRepo"],
			"an empty repository IS a repository; isRepo:false here would be factually wrong; body=%s", w.Body.String())
		assert.Equal(t, false, body["hasCommits"],
			"the second discriminator must be present and false, not merely absent; body=%s", w.Body.String())

		for _, field := range []string{"branch", "commit", "commitShort", "ahead", "behind", "dirty", "remote"} {
			assert.NotContains(t, body, field,
				"repo-only field %q must be ABSENT, not zero-valued: `ahead: 0` would mean both \"up to date\" and \"no commits exist\"; body=%s", field, w.Body.String())
		}
		assert.NotContains(t, body, "code",
			"a 200 must not carry an error envelope; body=%s", w.Body.String())
	})

	t.Run("arm2 CONTROL a genuine non-repo still answers exactly as x40a left it", func(t *testing.T) {
		w := get(plain)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		assert.Equal(t, false, body["isRepo"],
			"x40a's answer must not move; body=%s", w.Body.String())
		assert.NotContains(t, body, "hasCommits",
			"a directory that is not a repository has no commit story: hasCommits must not appear here, or a caller could read it as a repo with no commits; body=%s", w.Body.String())
		assert.NotContains(t, body, "branch",
			"body=%s", w.Body.String())
	})

	t.Run("arm3 CONTROL a missing directory is still an error", func(t *testing.T) {
		w := get(gone)
		require.Equal(t, http.StatusNotFound, w.Code,
			"a stack whose directory is GONE is a broken deployment, not a healthy empty repo; body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		assert.Equal(t, models.ErrStackDirMissing, body["code"],
			"the code must stay STACK_DIR_MISSING, which is deliberately NOT routine so it still logs at WARN; body=%s", w.Body.String())
	})

	t.Run("arm4 a repo with commits carries hasCommits:true and its branch", func(t *testing.T) {
		w := get(committed)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		assert.Equal(t, true, body["isRepo"], "body=%s", w.Body.String())
		assert.Equal(t, true, body["hasCommits"],
			"hasCommits is emitted on BOTH repo branches: the frontend narrows the union on it, so an ordinary repo answer that omitted it would render no chip; body=%s", w.Body.String())
		assert.Equal(t, "main", body["branch"], "body=%s", w.Body.String())
	})
}
