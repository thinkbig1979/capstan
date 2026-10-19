package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestGitPull_RefusesDirParameter is agent-os-hmoy: a pull through ?dir= on a
// folder with no stack rows took no operation lock, so it could run beside
// another pull of the same repository. A pull names a stack, as every other
// git write does. The control is the same clone, one commit behind, pulled
// through its stack.
func TestGitPull_RefusesDirParameter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
		return strings.TrimSpace(string(out))
	}
	upstream := filepath.Join(root, "upstream.git")
	git(root, "init", "-q", "--bare", "-b", "main", upstream)
	seed := filepath.Join(root, "seed")
	git(root, "init", "-q", "-b", "main", seed)
	git(seed, "commit", "-q", "--allow-empty", "-m", "one")
	git(seed, "push", "-q", upstream, "main")
	work := filepath.Join(root, "work")
	git(root, "clone", "-q", upstream, work)
	git(seed, "commit", "-q", "--allow-empty", "-m", "two")
	git(seed, "push", "-q", upstream, "main")
	before := git(work, "rev-parse", "HEAD")
	ahead := git(upstream, "rev-parse", "main")
	require.NotEqual(t, before, ahead, "the clone must be behind for the control to mean anything")

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: work, Name: "work", RootDir: work, ScannedAt: time.Now()}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-work", ProjectName: "work", Directory: work, ComposeFile: "compose.yml", Status: "stopped"}))
	cfg := &config.Config{StacksDir: root}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))
	post := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/git/pull?"+query, nil))
		return w
	}

	for _, query := range []string{"dir=" + url.QueryEscape(work), "dir=work", ""} {
		w := post(query)
		require.Equal(t, http.StatusBadRequest, w.Code, "query %q body %s", query, w.Body.String())
		assert.Contains(t, w.Body.String(), "Pull needs a stackId", "query %q", query)
		assert.Equal(t, before, git(work, "rev-parse", "HEAD"), "query %q: a git child ran a pull", query)
	}

	w := post("stackId=s-work")
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.Equal(t, ahead, git(work, "rev-parse", "HEAD"), "the control pull through the stack did not advance HEAD")
}
