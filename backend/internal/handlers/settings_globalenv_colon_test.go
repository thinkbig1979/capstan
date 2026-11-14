package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-rd1l: the global env page had its own parser, which dropped every
// line with no "=" and split the rest at the first "=". compose reads a bare
// KEY (inherited) and "KEY: value" as variables, so GET hid FOO and BAR and
// keyed "API_TOKEN: a=b" as "API_TOKEN: a", and UpdateGlobalEnv, which rebuilt
// the file from the rows it received, deleted FOO and BAR on an unchanged save.

// globalEnvTestRouter serves the global env routes with GET both unlocked and
// locked, and PUT unlocked.
func globalEnvTestRouter(t *testing.T) (dataDir string, router *gin.Engine) {
	t.Helper()
	h, _ := newTestSettingsHandler(t)
	router = gin.New()
	router.GET("/unlocked", envUnlockedMiddleware(), h.GetGlobalEnv)
	router.GET("/locked", h.GetGlobalEnv)
	router.PUT("/global", envUnlockedMiddleware(), h.UpdateGlobalEnv)
	return h.cfg.DataDir, router
}

func writeGlobalEnvFile(t *testing.T, dataDir, content string) string {
	t.Helper()
	p := filepath.Join(dataDir, "global.env")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func readGlobalEnvTestFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // test helper: a file under the test's own t.TempDir()
	require.NoError(t, err)
	return string(b)
}

func getGlobalVars(t *testing.T, router http.Handler, path string) ([]map[string]string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Vars []map[string]string `json:"vars"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Vars, w.Body.String()
}

func putGlobalVars(t *testing.T, router http.Handler, vars []map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"vars": vars})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/global", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

const rd1lFixture = "FOO\nBAR: 1\nAPI_TOKEN: a=rd1lcolonsecret\n"

func TestGlobalEnvColon_KeysMatchCompose(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	writeGlobalEnvFile(t, dataDir, rd1lFixture)

	vars, _ := getGlobalVars(t, router, "/unlocked")
	assert.Equal(t, []map[string]string{
		{"key": "FOO", "value": ""},
		{"key": "BAR", "value": "1"},
		{"key": "API_TOKEN", "value": "a=rd1lcolonsecret"},
	}, vars)
}

func TestGlobalEnvColon_LockedMasksColonValue(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	writeGlobalEnvFile(t, dataDir, rd1lFixture)

	_, unlocked := getGlobalVars(t, router, "/unlocked")
	require.Contains(t, unlocked, "rd1lcolonsecret", "positive side: the fixture carries the secret")

	vars, locked := getGlobalVars(t, router, "/locked")
	assert.NotContains(t, locked, "rd1lcolonsecret")
	assert.Equal(t, []map[string]string{
		{"key": "FOO", "value": ""},
		{"key": "BAR", "value": "1"},
		{"key": "API_TOKEN", "value": ""},
	}, vars)
}

func TestGlobalEnvColon_UnchangedSaveIsByteIdentical(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	content := rd1lFixture + "PLAIN=x\n"
	p := writeGlobalEnvFile(t, dataDir, content)

	vars, _ := getGlobalVars(t, router, "/unlocked")
	w := putGlobalVars(t, router, vars)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, content, readGlobalEnvTestFile(t, p))
}

// The merge: an edited row is rewritten in place as KEY=value, a removed row
// is dropped, a new row is appended, and everything else stays verbatim.
func TestGlobalEnvColon_MergeEditDeleteAppend(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "  FOO\nBAR: 1\nGONE=1\nAPI_TOKEN: a=b\n")

	w := putGlobalVars(t, router, []map[string]string{
		{"key": "FOO", "value": ""},
		{"key": "BAR", "value": "2"},
		{"key": "API_TOKEN", "value": "a=b"},
		{"key": "NEW", "value": "n"},
	})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "  FOO\nBAR=2\nAPI_TOKEN: a=b\nNEW=n\n", readGlobalEnvTestFile(t, p))
}

// A changed or new value is still validated; an unchanged one is not.
func TestGlobalEnvColon_ChangedValueWithNewlineRefused(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, rd1lFixture)

	w := putGlobalVars(t, router, []map[string]string{
		{"key": "FOO", "value": ""},
		{"key": "BAR", "value": "1\nEVIL=1"},
		{"key": "API_TOKEN", "value": "a=rd1lcolonsecret"},
	})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "index 1")
	assert.Equal(t, rd1lFixture, readGlobalEnvTestFile(t, p), "nothing written")
}

// Pin: a read failure of the current file refuses the save (the base refused
// it too, at the write). The merge needs the file; an empty stand-in would
// write one missing every comment.
func TestGlobalEnvColon_UnreadableFileRefusesSave(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	require.NoError(t, os.Mkdir(filepath.Join(dataDir, "global.env"), 0o755))

	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "1"}})
	assert.GreaterOrEqual(t, w.Code, 400, "refused: %s", w.Body.String())
}
