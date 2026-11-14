package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-jb5g: a line the stack table shows read-only (bare KEY, KEY: value,
// a value followed by a second statement, a multi-line value) came to the
// global page as a plain {key, value}, so the page offered it for editing, and
// the save rewrote it as KEY=value: for a bare KEY that turned "inherit from
// the environment" into "set to this".

const jb5gFixture = "  FOO\nBAR: 1\nNOTES=\"a\" OTHER=b\nCERT=\"x\ny\"\nA=1\n"

func TestGlobalEnvJb5g_GetMarksReadOnlyRows(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	writeGlobalEnvFile(t, dataDir, jb5gFixture)

	for _, path := range []string{"/unlocked", "/locked"} {
		rows := getGlobalVarRows(t, router, path)
		require.Len(t, rows, 5, path)
		for i, key := range []string{"FOO", "BAR", "NOTES", "CERT"} {
			assert.Equal(t, key, rows[i]["key"], path)
			assert.Equal(t, true, rows[i]["readOnly"], "%s: %s is read-only", path, key)
		}
		assert.Equal(t, "A", rows[4]["key"], path)
		assert.NotContains(t, rows[4], "readOnly", "%s: a KEY=value row is editable", path)
	}
}

func TestGlobalEnvJb5g_EditOfReadOnlyRowRefused(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"bare KEY", "FOO", "x"},
		{"KEY: value", "BAR", "2"},
		{"second statement", "NOTES", `"a"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, jb5gFixture)

			vars, _ := getGlobalVars(t, router, "/unlocked")
			for _, v := range vars {
				if v["key"] == tc.key {
					v["value"] = tc.value
				}
			}
			w := putGlobalVars(t, router, vars)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "read-only")
			assert.Equal(t, jb5gFixture, readGlobalEnvTestFile(t, p), "nothing written")
		})
	}
}

// The read-only rows still save unchanged, and still delete.
func TestGlobalEnvJb5g_ReadOnlyRowsKeepAndDelete(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, jb5gFixture)

	vars, _ := getGlobalVars(t, router, "/unlocked")
	vars[4]["value"] = "2"
	require.Equal(t, http.StatusNoContent, putGlobalVars(t, router, vars).Code)
	assert.Equal(t, "  FOO\nBAR: 1\nNOTES=\"a\" OTHER=b\nCERT=\"x\ny\"\nA=2\n", readGlobalEnvTestFile(t, p))

	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "2"}})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "A=2\n", readGlobalEnvTestFile(t, p))
}
