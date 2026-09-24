package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
	t.Helper()
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()
	dir, hash = newRepoWithTwoFileCommit(t, stacksDir)

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	r = gin.New()
	handler.RegisterRoutes(r.Group("/api/git"))
	return r, dir, hash
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
