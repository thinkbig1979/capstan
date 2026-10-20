package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

// TestGitPull_UnresolvableStackFolderIsNamedOnTheWire is agent-os-l4h6: the
// pull answer names the stack whose folder blocked the lock, and its folder.
func TestGitPull_UnresolvableStackFolderIsNamedOnTheWire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	good, file := filepath.Join(root, "good"), filepath.Join(root, "afile")
	require.NoError(t, os.Mkdir(good, 0o750))
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	bad := filepath.Join(file, "stack") // a file where its parent folder should be

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for id, dir := range map[string]string{"s-good": good, "s-bad": bad} {
		require.NoError(t, db.UpsertDirectory(models.Directory{Path: dir, Name: id, RootDir: root, ScannedAt: time.Now()}))
		require.NoError(t, db.UpsertStack(models.Stack{ID: id, ProjectName: id[2:], Directory: dir, ComposeFile: "compose.yml", Status: "stopped"}))
	}
	cfg := &config.Config{StacksDir: root}
	svc := services.NewGitService(cfg, db)
	svc.SetOperationLock(services.NewOperationLock())
	router := gin.New()
	NewGitHandler(svc, nil, db, cfg).RegisterRoutes(router.Group("/api/git"))
	// The repository top is read from git, so the good folder must be one.
	//nolint:gosec // test helper, explicit argv, not a shell string
	out, err := exec.Command("git", "-C", good, "init", "-q", "-b", "main").CombinedOutput()
	require.NoError(t, err, "%s", out)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/git/pull?stackId=s-good", nil))

	require.Equal(t, http.StatusInternalServerError, w.Code, "body %s", w.Body.String())
	var body struct {
		Reason  string         `json:"reason"`
		Details map[string]any `json:"details"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Contains(t, body.Reason, "stack bad")
	assert.Contains(t, body.Reason, bad)
	assert.Equal(t, "s-bad", body.Details["blockingStackId"])
	assert.Equal(t, bad, body.Details["blockingDirectory"])
}
