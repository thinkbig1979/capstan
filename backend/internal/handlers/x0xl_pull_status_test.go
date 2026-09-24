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
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestGitPull_FailureStatusReachesTheWire pins agent-os-x0xl.
//
// PullVerified wraps every pullCLI error in truth.Failed, and a failed
// ActionResult renders as 500. So the classified answers pullCLI already built
// (400 GIT_DIRTY, 409 GIT_BARE_REPO, and the 409/502 from pullFailure) all
// reached the client as a 500 "git pull failed". The last row is the control:
// a failure that is NOT an AppError must stay 500, so a handler that turned
// every failure into a 4xx fails here.
func TestGitPull_FailureStatusReachesTheWire(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		//nolint:gosec // test helper, explicit argv, not a shell string
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
	}
	git(stacksDir, "init", "-q", "--bare", "bare")
	git(stacksDir, "init", "-q", "dirty")
	dirty := filepath.Join(stacksDir, "dirty")
	git(dirty, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "seed")
	git(dirty, "push", "-q", filepath.Join(stacksDir, "bare"), "HEAD")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "untracked.txt"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(stacksDir, "notrepo"), 0o750))

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	cases := []struct {
		dir        string
		wantStatus int
		wantCode   string
		wantReason string
	}{
		{"dirty", http.StatusBadRequest, models.ErrGitDirty, "Working directory has uncommitted changes"},
		{"bare", http.StatusConflict, models.ErrGitBareRepo, "A bare repository has no work tree to pull into"},
		// Control: the bare probe cannot run in a non-repository, which is a
		// plain wrapped error, not a classified one.
		{"notrepo", http.StatusInternalServerError, "", "git pull failed"},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/git/pull?dir="+tc.dir, nil))
			require.Equal(t, tc.wantStatus, w.Code, "body=%s", w.Body.String())

			var body struct {
				Outcome string         `json:"outcome"`
				Reason  string         `json:"reason"`
				Details map[string]any `json:"details"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, "failed", body.Outcome, "body=%s", w.Body.String())
			require.Equal(t, tc.wantReason, body.Reason, "body=%s", w.Body.String())
			if tc.wantCode == "" {
				require.NotContains(t, body.Details, "code", "body=%s", w.Body.String())
			} else {
				require.Equal(t, tc.wantCode, body.Details["code"], "body=%s", w.Body.String())
			}
		})
	}
}
