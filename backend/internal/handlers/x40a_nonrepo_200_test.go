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

// x40aRepoOnMain initialises a repository at dir with one commit on branch
// `main`, and asserts via git itself that it reached that state. The branch
// name is pinned with --initial-branch rather than inherited from the host's
// init.defaultBranch, because arm 2 below asserts the branch VALUE and a host
// configured for `master` would otherwise fail this test for a reason that has
// nothing to do with the code under test.
func x40aRepoOnMain(t *testing.T, dir string) {
	t.Helper()

	run := func(args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
		return string(out)
	}

	run("init", "--initial-branch=main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o600))
	run("add", "base.txt")
	run("commit", "-m", "base")

	require.Equal(t, "main\n", run("rev-parse", "--abbrev-ref", "HEAD"),
		"fixture precondition: the repo must be on branch main, else arm 2 asserts nothing about the handler")
}

// TestGitGetStatus_NonRepoAnswers200 pins agent-os-x40a.
//
// "This directory is not a git repository" is a normal, expected answer about a
// resource, and GET /api/v1/git modelled it as an ERROR. Every symptom followed
// from that one decision: the browser console showed a red failed request on
// every non-git stack, and the endpoint is asked of EVERY stack the frontend
// renders, so on a host with no git-backed stack every one of those answers was
// a failure for something nobody did wrong.
//
// The fix lives at the HTTP boundary, not in the service. services/git.go's
// gitFailure still mints models.ErrGitNotRepo, and /git/log, /git/diff and the
// file-log path still answer 404 with it — that is a different surface
// (GitHistory on the Activity tab) and services/git_notrepo_test.go's two
// REGRESSION GUARD rows still pin it. Those rows drive GetLog, GetDiff and
// GetLogForFile and do not include GetStatus, so they are untouchable by a
// handler-level fix by construction rather than by care.
//
// # Why the three arms are one table and not three tests
//
// Arm 1 alone is satisfiable by a handler that answers 200 for every failure,
// and every gate would read green. Recorded RED before the fix:
//
//	--- FAIL: TestGitGetStatus_NonRepoAnswers200/arm1_genuine_non-repo_answers_200
//	    Error: Not equal: expected: 200 / actual: 404
//	    Messages: a genuinely non-git directory is a NORMAL answer about a
//	    resource, not a client error;
//	    body={"code":"GIT_NOT_REPO","message":"Not a git repository"}
//
// Arm 2's `branch == "main"` half is a pure GUARD: it passed before the fix,
// visible in that same pre-fix run's own failure message, which printed
// `"branch":"main"` inside a 200 body for a directory the scanner calls
// isGitRepo=false. Per testing-standards §19 that half has told us nothing on
// its own. Its `isRepo == true` half is new behaviour and did fail first.
//
// Arm 3 passed before AND after — a guard by that measure — and is still the
// arm that cannot be dropped, because the mutants below show it is the ONLY
// one that discriminates the fix's central decision. A stack whose directory
// is GONE and a stack that is simply not a repository are OPPOSITE conditions:
// the first is a broken deployment (an unmounted /stacks volume produces it for
// every stack at once), the second is normal configuration. Collapsing them
// hides the exact incident models.ErrStackDirMissing exists to surface, and
// hides it SILENTLY — a 200 produces no console error and no WARN, because
// STACK_DIR_MISSING is deliberately absent from respond.go's routineErrorCodes.
// That distinction did not exist before agent-os-n2df (2f1f735).
//
// # What each arm actually kills, MEASURED with go test -overlay
//
//	mutant                                        arm1  arm2  arm3
//	code-keyed -> `appErr.Status == 404`          PASS  PASS  FAIL
//	repo payload drops `"isRepo": true`           PASS  FAIL  PASS
//	no fix at all (pre-fix HEAD)                  FAIL  FAIL  PASS
//
// So the three are not redundant: each mutant is killed by exactly one arm.
//
// The discriminator is the error CODE, never the status and never the route.
// TestGitGetStatus_UnknownStackID404sQuietly (ua4y_7lg1_cause_test.go) is a
// 404 on this same handler and route that must stay a 404, and it does — but
// it is NOT a discriminator for the code-vs-status choice and must not be
// mistaken for one: MEASURED, it passes under the status-keyed mutant above,
// because resolvePathFromStack fails at git.go:124-127 and returns before
// h.git.GetStatus is ever called, so that request never reaches the branch.
// Arm 3 is what covers that choice.
func TestGitGetStatus_NonRepoAnswers200(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()

	// Arm 1: a genuine non-repo, with no parent repository above it.
	plain := filepath.Join(stacksDir, "plain")
	require.NoError(t, os.MkdirAll(plain, 0o750))

	// Arm 2: a stack nested inside a parent repo. gitCmd sets cmd.Dir without
	// GIT_CEILING_DIRECTORIES, so git walks UP and serves this directory from
	// the parent's .git — which is why no frontend gate on stack.isGitRepo can
	// fix this bug: resolveGitState stats the stack's OWN directory and reports
	// false here, while the endpoint returns a real branch.
	parent := filepath.Join(stacksDir, "parent")
	require.NoError(t, os.MkdirAll(parent, 0o750))
	x40aRepoOnMain(t, parent)
	nested := filepath.Join(parent, "stacks", "app")
	require.NoError(t, os.MkdirAll(nested, 0o750))

	// Arm 3: a directory that does not exist. pathutil.resolveExisting
	// re-appends trailing components that are not there yet, so this passes
	// containment and reaches gitFailure rather than being turned away as a
	// traversal attempt.
	gone := filepath.Join(stacksDir, "gone")

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

	t.Run("arm1 genuine non-repo answers 200", func(t *testing.T) {
		w := get(plain)
		require.Equal(t, http.StatusOK, w.Code,
			"a genuinely non-git directory is a NORMAL answer about a resource, not a client error; body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		require.Equal(t, false, body["isRepo"],
			"the discriminator must be present and false, not merely absent; body=%s", w.Body.String())
		assert.NotContains(t, body, "branch",
			"the repo-only fields must be ABSENT, not zero-valued: a branch of \"\" is indistinguishable from a failed read and invites callers to use it; body=%s", w.Body.String())
		assert.NotContains(t, body, "code",
			"a 200 must not carry an error envelope; body=%s", w.Body.String())
	})

	t.Run("arm2 GUARD nested in a parent repo still serves its real branch", func(t *testing.T) {
		w := get(nested)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		assert.Equal(t, true, body["isRepo"],
			"the repo answer must carry isRepo:true; the frontend narrows on it and renders nothing without it; body=%s", w.Body.String())
		assert.Equal(t, "main", body["branch"],
			"REGRESSION GUARD: gating this on stack.isGitRepo would have hidden a WORKING git panel from every monorepo layout; body=%s", w.Body.String())
	})

	t.Run("arm3 GUARD a missing directory is still an error", func(t *testing.T) {
		w := get(gone)
		require.Equal(t, http.StatusNotFound, w.Code,
			"a stack whose directory is GONE is a broken deployment, not a healthy non-git stack; answering 200 here hides an unmounted volume silently; body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

		assert.Equal(t, models.ErrStackDirMissing, body["code"],
			"the code must stay STACK_DIR_MISSING, which is deliberately NOT in routineErrorCodes so it still logs at WARN; body=%s", w.Body.String())
	})
}
