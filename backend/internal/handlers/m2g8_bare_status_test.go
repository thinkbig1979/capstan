package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestGitGetStatus_BareRepoBody pins agent-os-m2g8 at the wire.
//
// A bare repository answered 500 because `git status` needs a work tree. It now
// answers 200 with its branch and commit and `isBare: true`, and the work-tree
// fields are absent rather than zero: `dirty: false` would claim a clean tree
// that does not exist. The non-bare row is the control: it must keep every
// work-tree key and carry `isBare: false`, so the frontend union can narrow on it.
func TestGitGetStatus_BareRepoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
		return strings.TrimSpace(string(out))
	}
	git(stacksDir, "init", "-q", "--bare", "bare")
	git(stacksDir, "init", "-q", "work")
	work := filepath.Join(stacksDir, "work")
	git(work, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	git(work, "push", "-q", filepath.Join(stacksDir, "bare"), "HEAD")
	head := git(work, "rev-parse", "HEAD")

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	get := func(dir string) map[string]interface{} {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/git?dir="+dir, nil))
		require.Equal(t, http.StatusOK, w.Code, "dir=%s body=%s", dir, w.Body.String())
		var body map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body
	}

	workTreeKeys := []string{"dirty", "dirtyCount", "ahead", "behind", "trackingBranch"}

	bare := get("bare")
	require.Equal(t, true, bare["isBare"], "body=%v", bare)
	require.Equal(t, true, bare["isRepo"], "body=%v", bare)
	require.Equal(t, true, bare["hasCommits"], "body=%v", bare)
	require.Equal(t, head, bare["commit"], "body=%v", bare)
	require.NotEmpty(t, bare["branch"], "body=%v", bare)
	for _, k := range workTreeKeys {
		require.NotContains(t, bare, k, "a bare repository has no work tree; %q must be absent, body=%v", k, bare)
	}

	repo := get("work")
	require.Equal(t, false, repo["isBare"], "body=%v", repo)
	for _, k := range workTreeKeys {
		require.Contains(t, repo, k, "a work-tree repository keeps %q, body=%v", k, repo)
	}
}
