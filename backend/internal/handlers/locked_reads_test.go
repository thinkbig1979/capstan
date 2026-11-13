package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-w6wx (Edwin, 2026-11-13): a session that has not unlocked env
// values must not see inline compose secrets or a secret line in any file's
// diff. newGitWriteEnv's router has no EnvUnlock middleware, so it is LOCKED
// (envUnlocked fails closed); unlockedRouterW6wx is the same handlers behind
// an unlock. Every masking test runs both, on the same fixture.

const inlineSecretW6wx = "W6WX-INLINE-SECRET-value"

const secretComposeW6wx = "services:\n  db:\n    image: postgres:16\n    environment: { POSTGRES_PASSWORD: " + inlineSecretW6wx + " }\n"

func unlockedRouterW6wx(t *testing.T, e *gitWriteEnv) *gin.Engine {
	t.Helper()
	cfg := &config.Config{StacksDir: e.root}
	gitSvc := services.NewGitService(cfg, e.db)
	gitSvc.SetOperationLock(e.lock)
	compose := NewComposeHandler(services.NewLinterService(), e.db, cfg)
	compose.SetOperationLock(e.lock)
	r := gin.New()
	r.Use(unlockedSiou)
	NewGitHandler(gitSvc, nil, e.db, cfg).RegisterRoutes(r.Group("/api/git"))
	compose.RegisterRoutes(r.Group("/api/stacks"))
	return r
}

func getW6wx(t *testing.T, r *gin.Engine, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code, w.Body.String()
}

func TestLockedReads_ComposeGetMasksInlineSecret(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), secretComposeW6wx)

	code, body := getW6wx(t, e.router, "/api/stacks/s-app/compose")
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, inlineSecretW6wx, "locked")
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Contains(t, resp["content"], "POSTGRES_PASSWORD: ***")
	assert.Contains(t, resp["content"], "image: postgres:16", "the rest of the file is shown")
	assert.Equal(t, true, resp["locked"])
	assert.Equal(t, float64(1), resp["maskedValues"])

	code, body = getW6wx(t, unlockedRouterW6wx(t, e), "/api/stacks/s-app/compose")
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, inlineSecretW6wx, "unlocked")
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	assert.Equal(t, false, resp["locked"])
	assert.Equal(t, float64(0), resp["maskedValues"])
}

func TestLockedReads_ChangeDiffMasksInlineSecret(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), secretComposeW6wx)
	const path = "/api/git/changes/diff?stackId=s-app&path=compose.yaml"

	code, body := getW6wx(t, e.router, path)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, inlineSecretW6wx, "locked")
	assert.Contains(t, body, "POSTGRES_PASSWORD: ***")

	code, body = getW6wx(t, unlockedRouterW6wx(t, e), path)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, inlineSecretW6wx, "unlocked")
}

func TestLockedReads_HistoryDiffMasksInlineSecret(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), secretComposeW6wx)
	k81pGit(t, e.top, "commit", "-q", "-am", "add secret")
	path := "/api/git/diff/" + e.head(t) + "?stackId=s-app"

	code, body := getW6wx(t, e.router, path)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, inlineSecretW6wx, "locked")
	assert.Contains(t, body, "POSTGRES_PASSWORD: ***")

	code, body = getW6wx(t, unlockedRouterW6wx(t, e), path)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, inlineSecretW6wx, "unlocked")
}

// The refuted half of the bead: env files are hidden from every session.
func TestLockedReads_EnvDiffHiddenEitherWay(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, ".env"), "DB_PASSWORD=W6WX-ENV-SECRET\n")
	const path = "/api/git/changes/diff?stackId=s-app&path=.env"

	for name, r := range map[string]*gin.Engine{"locked": e.router, "unlocked": unlockedRouterW6wx(t, e)} {
		code, body := getW6wx(t, r, path)
		require.Equal(t, http.StatusOK, code, body)
		assert.Contains(t, body, `"hidden":true`, name)
		assert.NotContains(t, body, "W6WX-ENV-SECRET", name)
	}
}

// Edwin: mask the diffs of EVERY file while locked, not only compose files.
func TestLockedReads_NonComposeFileDiffMasked(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "x"), "x\nexport API_TOKEN=W6WX-SCRIPT-SECRET\n")
	const path = "/api/git/changes/diff?stackId=s-app&path=x"

	code, body := getW6wx(t, e.router, path)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, "W6WX-SCRIPT-SECRET", "locked")
	assert.Contains(t, body, "API_TOKEN=***")

	code, body = getW6wx(t, unlockedRouterW6wx(t, e), path)
	require.Equal(t, http.StatusOK, code, body)
	assert.Contains(t, body, "W6WX-SCRIPT-SECRET", "unlocked")
}

// Ruling 2: git's own hunk for a change deep inside a block scalar starts
// inside the value, with its key out of view. Masking each side whole still
// sees the key.
func TestLockedReads_HunkStartingInsideBlockScalarIsMasked(t *testing.T) {
	e := newGitWriteEnv(t, false)
	var block []string
	for i := 0; i < 14; i++ {
		block = append(block, "        KEYLINE-W6WX-"+strings.Repeat("a", i+1))
	}
	before := "services:\n  db:\n    image: postgres:16\n    environment:\n      PRIVATE_KEY: |\n" + strings.Join(block, "\n") + "\n"
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), before)
	k81pGit(t, e.top, "commit", "-q", "-am", "add key")
	block[11] = "        KEYLINE-W6WX-CHANGED"
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), "services:\n  db:\n    image: postgres:16\n    environment:\n      PRIVATE_KEY: |\n"+strings.Join(block, "\n")+"\n")
	const path = "/api/git/changes/diff?stackId=s-app&path=compose.yaml"

	code, body := getW6wx(t, unlockedRouterW6wx(t, e), path)
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, "KEYLINE-W6WX-CHANGED", "unlocked shows the change")
	require.NotContains(t, body, "PRIVATE_KEY", "precondition: git's own hunk does not reach the key")

	code, body = getW6wx(t, e.router, path)
	require.Equal(t, http.StatusOK, code, body)
	assert.NotContains(t, body, "KEYLINE-W6WX", "locked: no line of the value")
	assert.Contains(t, body, "***")
}

func lockedPutW6wx(t *testing.T, r *gin.Engine, path string, body any) (int, string) {
	t.Helper()
	w := doJSONSiou(t, r, http.MethodPut, path, body)
	return w.Code, w.Body.String()
}

// The request content is not even YAML: a 403 that runs before the lint
// answers 403, a gate after it would answer 422.
func TestLockedReads_ComposePutRefusedWhenFileHasMaskedValues(t *testing.T) {
	e := newGitWriteEnv(t, false)
	composePath := filepath.Join(e.top, "compose.yaml")
	k81pWrite(t, composePath, secretComposeW6wx)

	code, body := lockedPutW6wx(t, e.router, "/api/stacks/s-app/compose", map[string]string{"content": "services: [::"})
	require.Equal(t, http.StatusForbidden, code, body)
	assert.Contains(t, body, "Re-enter your password")
	got, err := os.ReadFile(composePath)
	require.NoError(t, err)
	assert.Equal(t, secretComposeW6wx, string(got), "file unchanged")

	code, body = lockedPutW6wx(t, e.router, "/api/stacks/s-app/compose-env", map[string]string{"composeContent": "services: {}\n"})
	require.Equal(t, http.StatusForbidden, code, body)

	// The other side: unlocked, the same save goes through.
	code, body = lockedPutW6wx(t, unlockedRouterW6wx(t, e), "/api/stacks/s-app/compose", map[string]string{"content": "services: {}\n"})
	require.Equal(t, http.StatusOK, code, body)
}

func TestLockedReads_ComposePutAllowedWithoutSecrets(t *testing.T) {
	e := newGitWriteEnv(t, false)
	k81pWrite(t, filepath.Join(e.top, "compose.yaml"), "services:\n  db:\n    image: postgres:16\n    environment:\n      POSTGRES_PASSWORD: ${DB_PASSWORD}\n")

	code, body := lockedPutW6wx(t, e.router, "/api/stacks/s-app/compose", map[string]string{"content": "services: {}\n"})
	require.Equal(t, http.StatusOK, code, body)
}

// 4hxa interaction (ruling 4): a link the write would refuse answers that
// refusal, checked first.
func TestLockedReads_ComposePutDanglingLinkIs409_W6wx(t *testing.T) {
	e := newGitWriteEnv(t, false)
	composePath := filepath.Join(e.top, "compose.yaml")
	require.NoError(t, os.Remove(composePath))
	require.NoError(t, os.Symlink("missing.yaml", composePath))

	code, body := lockedPutW6wx(t, e.router, "/api/stacks/s-app/compose", map[string]string{"content": "services: {}\n"})
	require.Equal(t, http.StatusConflict, code, body)
	assert.Contains(t, body, "fix or remove the link")
}
