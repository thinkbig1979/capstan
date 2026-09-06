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
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// newRepoWithTwoFileCommit builds a real repo whose HEAD commit touches exactly
// two files, and asserts via git itself that it reached that state before
// returning. The fixture asserts its own precondition rather than trusting the
// code under test: a commit that silently landed with one file (or none) would
// make a "the key is present" check pass while proving nothing about the value.
func newRepoWithTwoFileCommit(t *testing.T, stacksDir string) (dir, hash string) {
	t.Helper()

	run := func(d string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, d, out)
		return string(out)
	}

	const name = "repo"
	work := filepath.Join(stacksDir, name)
	require.NoError(t, os.MkdirAll(work, 0o750))
	run(work, "init", "--initial-branch=main")

	// A first commit, so the commit under test is a normal diff-tree case and
	// not the root-commit special case.
	require.NoError(t, os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0o600))
	run(work, "add", "base.txt")
	run(work, "commit", "-m", "base")

	require.NoError(t, os.WriteFile(filepath.Join(work, "alpha.yml"), []byte("a: 1\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(work, "beta.yml"), []byte("b: 2\n"), 0o600))
	run(work, "add", "alpha.yml", "beta.yml")
	run(work, "commit", "-m", "two files")

	hash = run(work, "rev-parse", "HEAD")
	hash = hash[:len(hash)-1] // strip trailing newline

	// Precondition asserted via git, not via the handler under test.
	names := run(work, "diff-tree", "--no-commit-id", "--name-only", "-r", hash)
	require.Equal(t, "alpha.yml\nbeta.yml\n", names,
		"fixture precondition: HEAD must touch exactly alpha.yml and beta.yml, else this test proves nothing")

	return name, hash
}

// TestGitGetDiff_ResponseCarriesFiles pins agent-os-2613.
//
// models.DiffResult.Files is tagged `json:"files"` and is assigned by
// services/git.go from `git diff-tree --no-commit-id --name-only -r <hash>`,
// which GetDiff pays for on EVERY request. But GetDiff hand-builds a two-key
// gin.H rather than marshalling the struct, so the changed-file list was
// computed and thrown away. The key has never existed:
//
//	git log --oneline -S'"files"' -- backend/internal/handlers/git.go
//	-> empty, at every commit up to and including 19082bb
//
// This asserts the VALUE, not merely the key's presence. A handler that emitted
// the key while dropping the value (or emitting an empty list) would satisfy a
// presence-only check and reinstate the same defect one layer down.
func TestGitGetDiff_ResponseCarriesFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()
	dir, hash := newRepoWithTwoFileCommit(t, stacksDir)

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	req := httptest.NewRequest(http.MethodGet, "/api/git/diff/"+hash+"?dir="+dir, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.Contains(t, body, "files",
		"GET /api/git/diff/:hash must emit files; the gin.H literal in handlers/git.go omits it, so the `git diff-tree --name-only` this endpoint already runs never reaches a caller. body=%s", w.Body.String())
	require.Equal(t, []interface{}{"alpha.yml", "beta.yml"}, body["files"],
		"files must carry the changed-file list diff-tree resolved, not an empty placeholder. body=%s", w.Body.String())

	// Two-sided control: the neighbouring keys this change did not touch must
	// still be emitted, so a regression that replaced the literal wholesale
	// cannot pass by adding only the field under test.
	for _, k := range []string{"commit", "diff"} {
		require.Contains(t, body, k, "pre-existing response key %q must survive", k)
	}
}
