package handlers

import (
	"bytes"
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
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-siou: the compose linter loaded files with no interpolation
// environment, so a `${VAR:?msg}` reference failed the load and became an
// error-level lint result, which every save gate answers with a 422. These
// tests drive the save and lint routes over a stack whose .env sets the
// variable. The save routes run UNLOCKED (orch ruling): the w6wx masked-value
// gate is not what they are about.

const requiredVarComposeSiou = "services:\n  db:\n    image: postgres:16\n    restart: unless-stopped\n    environment:\n      POSTGRES_PASSWORD: ${DB_PASS:?DB_PASS is required}\n"

// unlockedSiou marks the request as carrying a valid env-unlock token, as
// middleware.EnvUnlock does.
func unlockedSiou(c *gin.Context) {
	c.Set(middleware.CtxEnvUnlocked, true)
	c.Next()
}

// composeStackSiou writes a stack with the given compose and .env content
// (no .env when envContent is empty) and returns the handler, the stack id and
// the stack directory.
func composeStackSiou(t *testing.T, compose, envContent string) (*ComposeHandler, string, string) {
	t.Helper()
	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(compose), 0o644))
	envFile := ""
	if envContent != "" {
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, ".env"), []byte(envContent), 0o600))
		envFile = ".env"
	}
	createTestDirectory(t, db, stackDir)

	id := filepath.Base(tempDir) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          id,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     envFile,
		ProjectName: "stack1-default",
	}))

	cfg := &config.Config{StacksDir: tempDir, DataDir: t.TempDir()}
	return NewComposeHandler(services.NewLinterService(), db, cfg), id, stackDir
}

func doJSONSiou(t *testing.T, router *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(mustMarshal(t, body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestComposePut_RequiredVarInDotEnvSaves_siou(t *testing.T) {
	h, id, stackDir := composeStackSiou(t, "services:\n  db:\n    image: postgres:16\n", "DB_PASS=hunter2\n")
	router := gin.New()
	router.PUT("/stacks/:id/compose", unlockedSiou, h.Put)

	w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": requiredVarComposeSiou})

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	got, err := os.ReadFile( //nolint:gosec // G304: the test reads back the fixture file it wrote
		filepath.Join(stackDir, "compose.yaml"))
	require.NoError(t, err)
	assert.Equal(t, requiredVarComposeSiou, string(got))
}

func TestComposeLint_RequiredVarInDotEnvIsValid_siou(t *testing.T) {
	h, id, _ := composeStackSiou(t, requiredVarComposeSiou, "DB_PASS=hunter2\n")
	router := gin.New()
	router.POST("/stacks/:id/compose/lint", unlockedSiou, h.Lint)

	w := doJSONSiou(t, router, http.MethodPost, "/stacks/"+id+"/compose/lint", map[string]string{"content": requiredVarComposeSiou})

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp LintResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Valid, "lint results: %+v", resp.LintResults)
	for _, r := range resp.LintResults {
		assert.NotEqual(t, "required-variable", r.Rule, "DB_PASS is set in .env: %+v", r)
	}
}

func TestComposePutComposeAndEnv_RequiredVarInRequestEnv_siou(t *testing.T) {
	// No .env on disk: the variable is set only by the env half of this save.
	h, id, stackDir := composeStackSiou(t, "services:\n  db:\n    image: postgres:16\n", "")
	router := gin.New()
	router.PUT("/stacks/:id/compose-env", unlockedSiou, h.PutComposeAndEnv)

	w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose-env", map[string]string{
		"composeContent": requiredVarComposeSiou,
		"envRaw":         "DB_PASS=fromrequest\n",
	})

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "required-variable", "the request's own env sets DB_PASS")
	got, err := os.ReadFile( //nolint:gosec // G304: the test reads back the fixture file it wrote
		filepath.Join(stackDir, "compose.yaml"))
	require.NoError(t, err)
	assert.Equal(t, requiredVarComposeSiou, string(got))
}

func TestStacksCreate_RequiredVarInEnvContent_siou(t *testing.T) {
	_, router := hgtbFixture(t, nil, "newstack")

	w := doJSONSiou(t, router, http.MethodPost, "/stacks", map[string]any{
		"name":           "newstack",
		"composeContent": requiredVarComposeSiou,
		"envContent":     "DB_PASS=hunter2\n",
		"deploy":         false,
	})

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "required-variable", "envContent sets DB_PASS")
}

// The other side: with the variable set nowhere the save still goes through,
// with a warning that names it, instead of an unsavable 422.
func TestComposePut_RequiredVarUnsetSavesWithWarning_siou(t *testing.T) {
	h, id, _ := composeStackSiou(t, "services:\n  db:\n    image: postgres:16\n", "OTHER=1\n")
	router := gin.New()
	router.PUT("/stacks/:id/compose", unlockedSiou, h.Put)

	w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": requiredVarComposeSiou})

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp ComposeSaveResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	var found bool
	for _, r := range resp.LintResults {
		if r.Rule == "required-variable" {
			found = true
			assert.Equal(t, "warning", r.Level)
			assert.Contains(t, r.Message, "DB_PASS")
		}
	}
	assert.True(t, found, "want a required-variable warning, got %+v", resp.LintResults)
}

// Linting with the real env makes compose-go quote env values in its errors.
// A LOCKED session must not read a secret back through the lint route. Since
// sec D1 the secret is never interpolated (a placeholder stands in), so a
// secret in a typed field lints clean. Two-sided: a NON-secret variable is
// still interpolated as itself and quoted, and a secret the content spells
// out is starred by the redaction layer.
func TestComposeLint_LockedSessionNeverSeesEnvValue_siou(t *testing.T) {
	const secret = "hunter2SECRETsiou"
	h, id, _ := composeStackSiou(t, "services:\n  db:\n    image: postgres:16\n", "DB_PASSWORD="+secret+"\nPLAIN=visibleXYZ\n")
	router := gin.New()
	router.POST("/stacks/:id/compose/lint", h.Lint) // no unlock: locked

	lint := func(compose string) (LintResponse, string) {
		w := doJSONSiou(t, router, http.MethodPost, "/stacks/"+id+"/compose/lint", map[string]string{"content": compose})
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp LintResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.False(t, strings.Contains(w.Body.String(), secret), "body leaks the env value: %s", w.Body.String())
		return resp, w.Body.String()
	}

	// The real value does not decode as cpus, so the file is invalid (R2-D1),
	// and the one error says so without quoting the real run.
	resp, body := lint("services:\n  db:\n    image: postgres:16\n    restart: unless-stopped\n    cpus: ${DB_PASSWORD}\n")
	assert.False(t, resp.Valid, "%+v", resp.LintResults)
	assert.Contains(t, body, "does not load with the stack's environment values", body)

	resp, body = lint("services:\n  db:\n    image: postgres:16\n    restart: unless-stopped\n    cpus: ${PLAIN}\n")
	assert.False(t, resp.Valid, "a non-secret variable is interpolated as itself: %+v", resp.LintResults)
	assert.Contains(t, body, "visibleXYZ", "and its value is quoted: %s", body)

	resp, body = lint("services:\n  db:\n    image: postgres:16\n    restart: unless-stopped\n    cpus: " + secret + "\n")
	assert.False(t, resp.Valid, "%+v", resp.LintResults)
	assert.Contains(t, body, "***", "a secret the content spells out is starred: %s", body)
}

// Review cor C1: Put's env wiring had no test that could fail. A missing
// variable is a warning, so the 200 above holds with no env at all; the
// warning's ABSENCE is what shows the stack's .env was used. REGION is not a
// sensitive name, so it is interpolated as itself.
func TestComposePut_LintsWithStackEnv_siou(t *testing.T) {
	compose := "services:\n  web:\n    image: nginx:1.27\n    restart: always\n    environment:\n      REGION: ${REGION:?set REGION}\n"
	h, id, _ := composeStackSiou(t, "services:\n  web:\n    image: nginx:1.27\n", "REGION=eu-west\n")
	router := gin.New()
	router.PUT("/stacks/:id/compose", unlockedSiou, h.Put)

	w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": compose})

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.NotContains(t, w.Body.String(), "required-variable", "REGION is set in the stack's .env: %s", w.Body.String())
}
