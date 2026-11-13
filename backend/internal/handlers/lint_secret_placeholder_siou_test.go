package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-siou review (sec D1): linting with the stack env made compose-go
// quote env values in its errors, and it quotes them TRANSFORMED: a size
// field cuts the value at its last digit, a cast error and a duration escape
// it with strconv.Quote. Replacing the verbatim value afterwards missed both,
// so a locked session read a secret (or an escaped copy of it) back through
// every lint route. A secret now never reaches compose-go: the linter
// interpolates a placeholder in its place. These tests drive each lint route
// on a LOCKED router and require that no message holds any 4-character
// window of a secret, raw or escaped.

const (
	// The values are odd on purpose: no 4-character window of them occurs in
	// an ordinary lint message, so a hit is a leak and never a coincidence.
	dbPasswordPh = `Zw4kV8pLq2mN`
	apiTokenPh   = `Qz"7\Vx9#Kp`
	connPh       = `mysql://u:Wq"9zLm@hx/db`
)

// The env files every route reads. Single quotes keep the values literal.
const (
	stackEnvPh  = "DB_PASSWORD=" + dbPasswordPh + "\nAPI_TOKEN='" + apiTokenPh + "'\nCONN='" + connPh + "'\n"
	globalEnvPh = "API_TOKEN='" + apiTokenPh + "'\nCONN='" + connPh + "'\n"
)

// probesPh are the reviewer's inputs: each puts a secret where compose-go
// transforms it before quoting it.
var probesPh = map[string]string{
	"mem_limit truncation":      "services:\n  db:\n    image: nginx:1.27\n    restart: always\n    mem_limit: \"${DB_PASSWORD}\"\n",
	"cpus cast, quote in value": "services:\n  db:\n    image: nginx:1.27\n    restart: always\n    cpus: ${API_TOKEN}\n",
	"stop_grace_period":         "services:\n  db:\n    image: nginx:1.27\n    restart: always\n    stop_grace_period: ${API_TOKEN}\n",
	"url with a quote":          "services:\n  db:\n    image: nginx:1.27\n    restart: always\n    cpus: ${CONN}\n",
	"pids_limit":                "services:\n  db:\n    image: nginx:1.27\n    restart: always\n    pids_limit: ${DB_PASSWORD}\n",
}

// windowsPh are the 4-character windows of each secret and of its
// strconv.Quote-escaped form.
func windowsPh() []string {
	var out []string
	for _, s := range []string{dbPasswordPh, apiTokenPh, connPh} {
		q := strconv.Quote(s)
		for _, v := range []string{s, q[1 : len(q)-1]} {
			for i := 0; i+4 <= len(v); i++ {
				out = append(out, v[i:i+4])
			}
		}
	}
	return out
}

// requireNoSecretWindowPh checks the raw body and every string in the decoded
// JSON (where a quote is no longer escaped by the JSON encoding).
func requireNoSecretWindowPh(t *testing.T, name, body string) {
	t.Helper()
	texts := []string{body}
	var decoded any
	if json.Unmarshal([]byte(body), &decoded) == nil {
		texts = append(texts, jsonStringsPh(decoded)...)
	}
	for _, w := range windowsPh() {
		for _, s := range texts {
			require.NotContains(t, s, w, "%s: the response holds a piece of a secret (%q): %s", name, w, body)
		}
	}
}

func jsonStringsPh(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, jsonStringsPh(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, jsonStringsPh(e)...)
		}
		return out
	}
	return nil
}

func TestStackComposeLint_LockedNeverSeesSecretPieces_siou(t *testing.T) {
	h, id, _ := composeStackSiou(t, "services:\n  db:\n    image: nginx:1.27\n", stackEnvPh)
	router := gin.New()
	router.POST("/stacks/:id/compose/lint", h.Lint) // no unlock: locked

	for name, compose := range probesPh {
		w := doJSONSiou(t, router, http.MethodPost, "/stacks/"+id+"/compose/lint", map[string]string{"content": compose})
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", name, w.Body.String())
		requireNoSecretWindowPh(t, name, w.Body.String())
	}
}

func TestComposePut_LockedNeverSeesSecretPieces_siou(t *testing.T) {
	// The file on disk holds references only, so the locked save is allowed
	// (nothing masked) and reaches the lint.
	h, id, _ := composeStackSiou(t, "services:\n  db:\n    image: nginx:1.27\n    environment:\n      X: ${DB_PASSWORD}\n", stackEnvPh)
	router := gin.New()
	router.PUT("/stacks/:id/compose", h.Put) // locked

	for name, compose := range probesPh {
		w := doJSONSiou(t, router, http.MethodPut, "/stacks/"+id+"/compose", map[string]string{"content": compose})
		require.NotEqual(t, http.StatusForbidden, w.Code, "%s: the save must reach the lint: %s", name, w.Body.String())
		requireNoSecretWindowPh(t, name, w.Body.String())
	}
}

// globalEnvStacksPh is a StacksHandler whose DataDir holds global.env.
func globalEnvStacksPh(t *testing.T) (*gin.Engine, string, *database.DB) {
	t.Helper()
	tempDir := t.TempDir()
	dataDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "global.env"), []byte(globalEnvPh), 0o600))
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cfg := &config.Config{StacksDir: tempDir, DataDir: dataDir}
	handler := NewStacksHandler(nil, services.NewScannerService(cfg, db),
		services.NewLinterService(), db, cfg, services.NewActionLogger(db), services.NewOperationLock())
	require.NoError(t, db.CreateUser(models.User{
		ID: "test-user-id", Username: "testuser", CreatedAt: testTime, UpdatedAt: testTime,
	}))
	router := gin.New()
	router.POST("/compose/lint", handler.Lint)
	router.POST("/stacks", authContextMiddleware("test-user-id"), handler.Create)
	return router, tempDir, db
}

func TestGlobalComposeLint_LockedNeverSeesSecretPieces_siou(t *testing.T) {
	router, _, _ := globalEnvStacksPh(t)
	for name, compose := range probesPh {
		if strings.Contains(compose, "DB_PASSWORD") {
			// global.env holds API_TOKEN and CONN only; DB_PASSWORD is the
			// create request's own env.
			continue
		}
		w := doJSONSiou(t, router, http.MethodPost, "/compose/lint", map[string]string{"compose": compose})
		require.Equal(t, http.StatusOK, w.Code, "%s: %s", name, w.Body.String())
		requireNoSecretWindowPh(t, name, w.Body.String())
	}
}

func TestStacksCreate_LockedNeverSeesSecretPieces_siou(t *testing.T) {
	router, tempDir, db := globalEnvStacksPh(t)
	i := 0
	for name, compose := range probesPh {
		i++
		stackName := "phstack" + strconv.Itoa(i)
		createTestDirectory(t, db, filepath.Join(tempDir, stackName))
		w := doJSONSiou(t, router, http.MethodPost, "/stacks", map[string]any{
			"name":           stackName,
			"composeContent": compose,
			"envContent":     "DB_PASSWORD=" + dbPasswordPh + "\n",
			"deploy":         false,
		})
		require.NotEqual(t, http.StatusInternalServerError, w.Code, "%s: %s", name, w.Body.String())
		requireNoSecretWindowPh(t, name, w.Body.String())
	}
}
