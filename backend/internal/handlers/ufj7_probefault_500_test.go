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
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// ufj7DirtyRepo initialises a repository at dir with one committed, then
// modified, TRACKED file, so `git status --porcelain` has exactly one line.
// --initial-branch pins the name rather than inheriting the host's
// init.defaultBranch, for the same reason x40aRepoOnMain does.
func ufj7DirtyRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
	}
	run("init", "-q", "--initial-branch", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o600))
	run("add", "f.txt")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "seed")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\nmodified\n"), 0o600))
}

// TestGitGetStatus_ProbeFaultAnswers500 pins agent-os-ufj7 AT THE CALLER.
//
// The services-package test for this bead asserts that getStatusCLI returns an
// error. That is the premise, not the conclusion. The bead's acceptance
// criterion says the distinction must be visible AT THE CALLER, and the caller
// is this handler — where the gap between "the service errored" and "the client
// sees an error" is NOT empty. GitHandler.GetStatus runs an errors.As switch on
// models.ErrGitNotRepo and models.ErrGitNoCommits that turns two error codes
// back into a 200, deliberately (agent-os-x40a, agent-os-4a4a).
//
// A bare fmt.Errorf misses both cases today, so the wire behaviour is right —
// but right by the error's TYPE, which nothing was asserting. If either probe
// error is later refined into a typed *models.AppError to give the operator
// something better than "Internal server error", it enters that switch's reach
// and a services-package test stays green through the change. This file is what
// makes that a red test rather than a silent regression to a false 200.
//
// Both faults are CONTENT faults, not permission faults: the server runs as
// root in the container, where a chmod-based fixture reads "cannot reproduce".
func TestGitGetStatus_ProbeFaultAnswers500(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()

	healthy := filepath.Join(stacksDir, "healthy")
	require.NoError(t, os.MkdirAll(healthy, 0o750))
	ufj7DirtyRepo(t, healthy)

	// Arm 2's fault: the index truncated below its header. ANY four bytes do
	// this — git's length check runs before its signature check — so the "DIRC"
	// magic is not the invariant, the LENGTH is.
	badIndex := filepath.Join(stacksDir, "badindex")
	require.NoError(t, os.MkdirAll(badIndex, 0o750))
	ufj7DirtyRepo(t, badIndex)
	require.NoError(t, os.WriteFile(filepath.Join(badIndex, ".git", "index"), []byte("DIRC"), 0o600))

	// Arm 3's fault: a remote-tracking ref pointing at a valid object that is
	// NOT a commit. rev-parse --verify resolves it, so trackingBranch IS set and
	// the rev-list block IS entered, while only rev-list fails.
	badRef := filepath.Join(stacksDir, "badref")
	require.NoError(t, os.MkdirAll(badRef, 0o750))
	ufj7DirtyRepo(t, badRef)
	//nolint:gosec // test helper, explicit argv, not a shell string
	blob, err := exec.Command("git", "-C", badRef, "hash-object", "-w", "f.txt").Output()
	require.NoError(t, err)
	refDir := filepath.Join(badRef, ".git", "refs", "remotes", "origin")
	require.NoError(t, os.MkdirAll(refDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(refDir, "main"), blob, 0o600))

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

	// ARM 1 — the CONTROL, and it must fire. A healthy dirty repository answers
	// 200 with dirty:true. Without it, arms 2 and 3's 500s are satisfied by a
	// handler that 500s on every request, which would be worse than the bug.
	t.Run("arm1 CONTROL healthy dirty repo still answers 200 with dirty true", func(t *testing.T) {
		w := get(healthy)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, true, body["dirty"], "body=%s", w.Body.String())
		assert.Equal(t, float64(1), body["dirtyCount"], "body=%s", w.Body.String())
	})

	// ARM 2 — the status probe. Pre-fix this answered 200 with dirty:false.
	t.Run("arm2 status probe fault answers 500, never a 200 saying clean", func(t *testing.T) {
		w := get(badIndex)
		require.Equal(t, http.StatusInternalServerError, w.Code,
			"a status-probe fault must reach the client as an error; a 200 here is indistinguishable from a clean worktree; body=%s", w.Body.String())

		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.NotContains(t, body, "dirty",
			"the 500 body must not carry a dirty field at all: false would be a wrong value and true would be invented; body=%s", w.Body.String())
	})

	// ARM 3 — the ahead/behind probe, the second site fixed in this same diff.
	// Pre-fix this answered 200 with ahead:0 behind:0, which GitStatus.tsx gates
	// on `{ahead > 0 && ...}` and therefore draws as "up to date".
	t.Run("arm3 rev-list fault answers 500, never a 200 saying up to date", func(t *testing.T) {
		w := get(badRef)
		require.Equal(t, http.StatusInternalServerError, w.Code,
			"a rev-list fault must reach the client as an error; a 200 with ahead=0/behind=0 renders as up-to-date; body=%s", w.Body.String())
	})
}
