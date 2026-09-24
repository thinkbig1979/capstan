package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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

// newGitArgvRouter serves the git routes over a real two-commit repository
// (newRepoWithTwoFileCommit) and returns its ?dir= value and HEAD hash.
func newGitArgvRouter(t *testing.T) (r *gin.Engine, dir, hash string) {
	r, _, dir, hash = newGitArgvRouterIn(t)
	return r, dir, hash
}

func newGitArgvRouterIn(t *testing.T) (r *gin.Engine, stacksDir, dir, hash string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	stacksDir = t.TempDir()
	dir, hash = newRepoWithTwoFileCommit(t, stacksDir)

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	r = gin.New()
	handler.RegisterRoutes(r.Group("/api/git"))
	return r, stacksDir, dir, hash
}

func gitArgvGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

// TestGitGetLog_FileParam pins agent-os-tyl6: ?file= follows "--" in the git
// argv, so it is never a flag, but git refuses a path outside the repository
// with exit 128 and that answered 500. Those values are now a 400.
func TestGitGetLog_FileParam(t *testing.T) {
	r, dir, _ := newGitArgvRouter(t)
	logURL := func(file string) string {
		return "/api/git/log?" + url.Values{"dir": {dir}, "file": {file}}.Encode()
	}

	rejected := map[string]string{
		"parent":         "../x",
		"absolute":       "/etc/passwd",
		"escapes via ..": "alpha.yml/../../x",
		"bare ..":        "..",
		"glob magic":     ":(glob)../*",
		"exclude magic":  ":!../x",
		"NUL":            "alpha.yml\x00x",
		"over PATH_MAX":  strings.Repeat("a", maxLogFileLen+1),
	}
	for name, file := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			w := gitArgvGet(r, logURL(file))
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%.300s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrValidation, body["code"])
			assert.Equal(t, "Invalid file path", body["message"])
		})
	}

	accepted := map[string]string{
		// A flag-shaped value is a pathspec after "--": no such file, no commits.
		"flag-shaped":      "--help",
		"at PATH_MAX":      strings.Repeat("a", maxLogFileLen),
		"inner .. in repo": "sub/../alpha.yml",
		"tracked file":     "alpha.yml",
	}
	for name, file := range accepted {
		t.Run("accepts "+name, func(t *testing.T) {
			w := gitArgvGet(r, logURL(file))
			require.Equal(t, http.StatusOK, w.Code, "body=%.300s", w.Body.String())
		})
	}

	t.Run("tracked file lists the commit that touched it", func(t *testing.T) {
		w := gitArgvGet(r, logURL("alpha.yml"))
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		assert.Equal(t, float64(1), decodeBody(t, w)["total"])
	})
}

// TestGitGetDiff_OverLongHash pins agent-os-tyl6: a hex hash longer than any
// object id answered 500 (git "unknown revision", or execve E2BIG past
// ~128 KiB). It is now refused before git runs.
func TestGitGetDiff_OverLongHash(t *testing.T) {
	r, dir, hash := newGitArgvRouter(t)
	diffURL := func(h string) string { return "/api/git/diff/" + h + "?dir=" + dir }

	for name, h := range map[string]string{
		"65 hex":     strings.Repeat("a", 65),
		"200000 hex": strings.Repeat("a", 200000),
	} {
		t.Run("rejects "+name, func(t *testing.T) {
			w := gitArgvGet(r, diffURL(h))
			require.Equal(t, http.StatusBadRequest, w.Code, "body=%.300s", w.Body.String())
			assert.Equal(t, "Invalid commit hash format", decodeBody(t, w)["message"])
		})
	}

	t.Run("accepts the real 40-char hash", func(t *testing.T) {
		w := gitArgvGet(r, diffURL(hash))
		require.Equal(t, http.StatusOK, w.Code, "body=%.300s", w.Body.String())
	})
}

func gitArgvRun(t *testing.T, dir string, args ...string) string {
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
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

// TestGitGetDiff_UnknownCommitIsNotFound pins agent-os-tyl6: a well-formed
// hash naming no commit answered 500, because `git log` exits 128 for it just
// as for a fault. It is now 404 NOT_FOUND, decided by `git rev-parse --verify
// --quiet`, and a real fault still answers 500.
func TestGitGetDiff_UnknownCommitIsNotFound(t *testing.T) {
	r, stacksDir, dir, hash := newGitArgvRouterIn(t)
	work := filepath.Join(stacksDir, dir)
	diffURL := func(h string) string { return "/api/git/diff/" + h + "?dir=" + dir }

	// A tracked FILE named like a hash: `git log -1 deadbeef` reads it as a
	// path when no commit matches.
	require.NoError(t, os.WriteFile(filepath.Join(work, "deadbeef"), []byte("x\n"), 0o600))
	gitArgvRun(t, work, "add", "deadbeef")
	gitArgvRun(t, work, "commit", "-m", "file named like a hash")

	blob := gitArgvRun(t, work, "rev-parse", "HEAD:alpha.yml")

	for name, h := range map[string]string{
		"unknown full sha1":             strings.Repeat("d", 40),
		"unknown short":                 "0000000",
		"unknown, names a tracked file": "deadbeef",
		"a blob, not a commit":          blob,
	} {
		t.Run(name+" -> 404", func(t *testing.T) {
			w := gitArgvGet(r, diffURL(h))
			require.Equal(t, http.StatusNotFound, w.Code, "body=%s", w.Body.String())
			body := decodeBody(t, w)
			assert.Equal(t, models.ErrNotFound, body["code"])
			assert.Equal(t, "Commit not found", body["message"])
		})
	}

	t.Run("known commit -> 200", func(t *testing.T) {
		w := gitArgvGet(r, diffURL(hash))
		require.Equal(t, http.StatusOK, w.Code, "body=%.300s", w.Body.String())
	})

	t.Run("a commit whose tree is unreadable stays 500", func(t *testing.T) {
		// The commit object exists, so rev-parse --verify succeeds; its tree
		// object is deleted, so `git show` fails. That is a fault, not a 404.
		tree := gitArgvRun(t, work, "rev-parse", hash+"^{tree}")
		require.NoError(t, os.Remove(filepath.Join(work, ".git", "objects", tree[:2], tree[2:])))

		w := gitArgvGet(r, diffURL(hash))
		require.Equal(t, http.StatusInternalServerError, w.Code, "body=%s", w.Body.String())
	})
}
