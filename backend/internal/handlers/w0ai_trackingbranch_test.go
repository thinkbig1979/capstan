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

// newClonedRepoWithUpstream builds a real clone whose checked-out branch has a
// configured @{upstream}, and asserts via git itself that it reached that state
// before returning. Without that assertion a fixture that silently failed to set
// an upstream would make the test below pass for the wrong reason: TrackingBranch
// would be empty, and an assertion that merely checked "the key is present" would
// still be satisfied by a null.
func newClonedRepoWithUpstream(t *testing.T, stacksDir string) string {
	t.Helper()

	run := func(dir string, args ...string) string {
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
		require.NoError(t, err, "git %v in %s: %s", args, dir, out)
		return string(out)
	}

	origin := filepath.Join(t.TempDir(), "origin.git")
	run(t.TempDir(), "init", "--bare", "--initial-branch=main", origin)

	const name = "repo"
	run(stacksDir, "-c", "init.defaultBranch=main", "clone", origin, name)
	work := filepath.Join(stacksDir, name)

	require.NoError(t, os.WriteFile(filepath.Join(work, "README"), []byte("x\n"), 0o600))
	run(work, "add", "README")
	run(work, "commit", "-m", "initial")
	run(work, "push", "-u", "origin", "main")

	// The fixture asserts its own precondition, via git rather than via the
	// code under test.
	upstream := run(work, "rev-parse", "--abbrev-ref", "@{upstream}")
	require.Equal(t, "origin/main\n", upstream,
		"fixture precondition: the clone must have a configured upstream, else this test proves nothing")

	return name
}

// TestGitGetStatus_ResponseCarriesTrackingBranch pins agent-os-w0ai.
//
// models.GitStatusResult.TrackingBranch is tagged `json:"trackingBranch"` and is
// assigned by services/git.go, but GetStatus hand-builds its gin.H rather than
// marshalling the struct, and that literal has never listed the field:
//
//	git log --oneline -S'trackingBranch' -- backend/internal/handlers/git.go
//	-> empty, at every commit up to d79a5cc
//
// so the field was write-only and agent-os-r1a's second symptom ("the API's
// trackingBranch field is always the empty string in production") described a key
// that did not exist. Seen failing first against d79a5cc: body["trackingBranch"]
// is absent, so the require.Contains below fails outright.
//
// This asserts the VALUE, not merely the key's presence. A handler that emitted
// the key while dropping the value would satisfy a presence-only check and
// reinstate the same defect one layer down.
func TestGitGetStatus_ResponseCarriesTrackingBranch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	stacksDir := t.TempDir()
	dir := newClonedRepoWithUpstream(t, stacksDir)

	db, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg := &config.Config{StacksDir: stacksDir}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	req := httptest.NewRequest(http.MethodGet, "/api/git?dir="+dir, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))

	require.Contains(t, body, "trackingBranch",
		"GET /api/git must emit trackingBranch; the gin.H literal in handlers/git.go omits it, so services/git.go's @{upstream} resolution never reaches a caller. body=%s", w.Body.String())
	require.Equal(t, "origin/main", body["trackingBranch"],
		"trackingBranch must carry the resolved upstream, not an empty placeholder. body=%s", w.Body.String())

	// Two-sided control: the neighbouring keys this change did not touch must
	// still be emitted, so a regression that replaced the literal wholesale
	// cannot pass by adding the one field under test.
	for _, k := range []string{"branch", "commit", "dirty", "dirtyCount", "ahead", "behind", "remote"} {
		require.Contains(t, body, k, "pre-existing response key %q must survive", k)
	}
}
