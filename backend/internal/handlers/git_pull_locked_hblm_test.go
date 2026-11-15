package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
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

// agent-os-hblm: someone who can push to the remote commits text, a list of
// guesses say, and a locked session pulls it into the stack folder. Read
// back through a locked view (the commit diff, the compose file), the line
// equal to a stack secret comes back "***" and a wrong guess verbatim
// (OBSERVED with a probe of this fixture on 193b08c). With ?redeploy=true the
// pull also runs the remote's compose. So a pull needs an unlock, like the
// locked compose and env writes of agent-os-ly6i.

type hblmFixture struct {
	router         func(unlocked bool) *gin.Engine
	work, upstream string
	git            func(dir string, args ...string) string
}

func newHblmFixture(t *testing.T) hblmFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	const secret = "Tr0ub4dor-hblm-secret-42"
	root := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
		return strings.TrimSpace(string(out))
	}
	write := func(p, s string) {
		t.Helper()
		//nolint:forbidigo // test fixture outside any stack lock
		require.NoError(t, os.WriteFile(p, []byte(s), 0o600))
	}
	upstream := filepath.Join(root, "upstream.git")
	git(root, "init", "-q", "--bare", "-b", "main", upstream)
	seed := filepath.Join(root, "seed")
	git(root, "init", "-q", "-b", "main", seed)
	write(filepath.Join(seed, ".gitignore"), ".env\n")
	write(filepath.Join(seed, "compose.yml"), "services:\n  app:\n    image: nginx\n")
	git(seed, "add", ".")
	git(seed, "commit", "-q", "-m", "one")
	git(seed, "push", "-q", upstream, "main")
	work := filepath.Join(root, "work")
	git(root, "clone", "-q", upstream, work)
	write(filepath.Join(work, ".env"), "DB_PASSWORD="+secret+"\n")
	write(filepath.Join(seed, "notes.txt"), "guess A: "+secret+"\nguess B: Tr0ub4dor-hblm-wrong-guess\n")
	git(seed, "add", ".")
	git(seed, "commit", "-q", "-m", "guesses")
	git(seed, "push", "-q", upstream, "main")

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: work, Name: "work", RootDir: work, ScannedAt: time.Now()}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-work", ProjectName: "work", Directory: work, ComposeFile: "compose.yml", EnvFile: ".env", Status: "stopped"}))
	cfg := &config.Config{StacksDir: root}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	return hblmFixture{
		router: func(unlocked bool) *gin.Engine {
			r := gin.New()
			if unlocked {
				r.Use(envUnlockedMiddleware())
			}
			handler.RegisterRoutes(r.Group("/api/git"))
			return r
		},
		work: work, upstream: upstream, git: git,
	}
}

func TestGitPull_LockedRefused_hblm(t *testing.T) {
	f := newHblmFixture(t)
	before := f.git(f.work, "rev-parse", "HEAD")
	for _, query := range []string{"stackId=s-work", "stackId=s-work&redeploy=true"} {
		w := httptest.NewRecorder()
		f.router(false).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/git/pull?"+query, nil))
		assert.Equal(t, http.StatusForbidden, w.Code, "%s: body %s", query, w.Body.String())
		assert.Equal(t, models.ErrEnvLocked, envLockedCodeH71i(t, w), query)
		assert.Contains(t, w.Body.String(), "Unlock env values to pull from the remote: "+lockedPullReason, query)
		assert.Equal(t, before, f.git(f.work, "rev-parse", "HEAD"), "%s: a refused pull must not move HEAD", query)
	}
}

func TestGitPull_UnlockedStillPulls_hblm(t *testing.T) {
	f := newHblmFixture(t)
	w := httptest.NewRecorder()
	f.router(true).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/git/pull?stackId=s-work", nil))
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	assert.Equal(t, f.git(f.upstream, "rev-parse", "main"), f.git(f.work, "rev-parse", "HEAD"), "an unlocked pull advances HEAD")
}
