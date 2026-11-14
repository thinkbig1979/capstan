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

// Review C4: a new or changed value is written as KEY=value, so it must read
// back as that one variable. `"hello" world=1` gave compose a second
// variable; `'it` left a quote open, and compose rejected all of global.env.
func TestGlobalEnvColon_ChangedValueQuoteShapeRefused(t *testing.T) {
	for _, bad := range []string{`"hello" world=1`, `'it`, `"open`} {
		t.Run(bad, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, "A=x\n")
			for _, vars := range [][]map[string]string{
				{{"key": "A", "value": bad}},                               // changed
				{{"key": "A", "value": "x"}, {"key": "NEW", "value": bad}}, // new
			} {
				w := putGlobalVars(t, router, vars)
				require.Equal(t, http.StatusBadRequest, w.Code, "value %q: %s", bad, w.Body.String())
				assert.Equal(t, "A=x\n", readGlobalEnvTestFile(t, p), "nothing written")
			}
		})
	}
	// The other side: well-formed quoting is accepted.
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "A=x\n")
	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": `"hello world" # c`}, {"key": "B", "value": "it's"}})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "A=\"hello world\" # c\nB=it's\n", readGlobalEnvTestFile(t, p))
}

// Review C3: duplicate keys match in order, so an unchanged save keeps them
// where they are (TestEnvCorpus fixture "duplicate-key" covers the unchanged
// save); an edit of the second A changes the second A.
func TestGlobalEnvColon_DuplicateKeysMatchInOrder(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "A=1\nA=2\n")
	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "1"}, {"key": "A", "value": "9"}})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "A=1\nA=9\n", readGlobalEnvTestFile(t, p))
}

// Review C2 (server half): a change to a multi-line value is refused, the
// page's one-line field having dropped its line breaks; an unchanged save
// and a delete still work.
func TestGlobalEnvColon_MultilineValueChangeRefused(t *testing.T) {
	const content = "CERT=\"-----BEGIN\nabc\n-----END\"\nB=1\n"
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, content)

	w := putGlobalVars(t, router, []map[string]string{{"key": "CERT", "value": `"-----BEGINabc-----END"X`}, {"key": "B", "value": "1"}})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "multi-line")
	assert.Equal(t, content, readGlobalEnvTestFile(t, p), "nothing written")

	vars, _ := getGlobalVars(t, router, "/unlocked")
	require.Equal(t, http.StatusNoContent, putGlobalVars(t, router, vars).Code, "an unchanged save still works")
	assert.Equal(t, content, readGlobalEnvTestFile(t, p))

	require.Equal(t, http.StatusNoContent, putGlobalVars(t, router, []map[string]string{{"key": "B", "value": "1"}}).Code, "a delete still works")
	assert.Equal(t, "B=1\n", readGlobalEnvTestFile(t, p))
}

// Review F1: the parser keys "export A=1" as "export A" (the key the page
// shows and sends back) while compose's dotenv reads it as A, so the C4
// re-read must look the value up under compose's key. Every export row is
// edited; the 1c017b1 behaviour (before C4) is the expected output.
func TestGlobalEnvColon_ExportRowsEditable(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "export A=1\nexport B: 2\nexport C\n")
	vars, _ := getGlobalVars(t, router, "/unlocked")
	require.Len(t, vars, 3)
	for _, v := range vars {
		v["value"] = "x"
	}
	w := putGlobalVars(t, router, vars)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "export A=x\nB=x\nC=x\n", readGlobalEnvTestFile(t, p))

	// The quote-shape refusal still holds for an export row.
	w = putGlobalVars(t, router, []map[string]string{{"key": "export A", "value": `"open`}, {"key": "B", "value": "x"}, {"key": "C", "value": "x"}})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "export A=x\nB=x\nC=x\n", readGlobalEnvTestFile(t, p), "nothing written")
}
