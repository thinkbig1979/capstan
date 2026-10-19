package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// TestGitGetDiff_HidesEnvFilesOnTheWire pins agent-os-2omy at the route: a
// root commit that adds an env file next to ordinary files answers without the
// env file's contents and names it in hiddenFiles. The service decides what is
// hidden; this proves the handler carries the decision, which a hand-built
// response map dropped once already (agent-os-2613).
func TestGitGetDiff_HidesEnvFilesOnTheWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")

	stacksDir := t.TempDir()
	work := filepath.Join(stacksDir, "repo")
	require.NoError(t, os.MkdirAll(work, 0o750))
	run := func(args ...string) string {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "--initial-branch=main")
	for name, body := range map[string]string{
		".env":         "WIRE_SECRET=1\n",
		"compose.yaml": "services: {}\n",
		"x.txt":        "WIRE_PUBLIC\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(work, name), []byte(body), 0o600))
	}
	run("add", "-A")
	run("commit", "-q", "-m", "root")
	hash := run("rev-parse", "HEAD")

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	req := httptest.NewRequest(http.MethodGet, "/api/git/diff/"+hash+"?dir="+work, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	require.NotContains(t, w.Body.String(), "WIRE_SECRET", "an env file's contents reached the response")
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Contains(t, body["diff"], "WIRE_PUBLIC", "a file that is not an env file must still show")
	require.Equal(t, []any{".env"}, body["hiddenFiles"], "body=%s", w.Body.String())
	require.Equal(t, []any{".env", "compose.yaml", "x.txt"}, body["files"])
	require.Contains(t, body, "commit")
}
