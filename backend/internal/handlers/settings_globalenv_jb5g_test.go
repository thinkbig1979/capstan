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

// Review F1: rows of one key paired by position, so deleting A=1 above a bare
// A paired the kept A with A=1, wrote it as "A=" and dropped the bare A:
// compose then read A as empty instead of inherited. A read-only line only
// pairs with a request row equal to it; editable rows still pair in order.
func TestGlobalEnvJb5g_DuplicateKeyKeepsReadOnlyLine(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		req        []map[string]string
		want       string
	}{
		{"delete A=1, keep bare A", "A=1\nA\n", []map[string]string{{"key": "A", "value": ""}}, "A\n"},
		{"delete A=1, keep export A", "A=1\nexport A\n", []map[string]string{{"key": "A", "value": ""}}, "export A\n"},
		{"delete A=1, keep A: v", "A=1\nA: v\n", []map[string]string{{"key": "A", "value": "v"}}, "A: v\n"},
		{"delete multi-line copy, keep A=2", "A=\"x\ny\"\nA=2\n", []map[string]string{{"key": "A", "value": "2"}}, "A=2\n"},
		{"bare A first", "A\nA=1\n", []map[string]string{{"key": "A", "value": ""}}, "A\n"},
		{"edit A=1 beside bare A", "A=1\nA\n", []map[string]string{{"key": "A", "value": "2"}, {"key": "A", "value": ""}}, "A=2\nA\n"},
		{"editable duplicates stay in order", "A=1\nA=2\n", []map[string]string{{"key": "A", "value": "2"}, {"key": "A", "value": "3"}}, "A=2\nA=3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, tc.file)
			w := putGlobalVars(t, router, tc.req)
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			assert.Equal(t, tc.want, readGlobalEnvTestFile(t, p))
		})
	}
}

// The edit of a lone read-only line is still refused.
func TestGlobalEnvJb5g_DuplicateKeyReadOnlyEditRefused(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "A=1\nA\n")
	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "1"}, {"key": "A", "value": "x"}})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "read-only")
	assert.Equal(t, "A=1\nA\n", readGlobalEnvTestFile(t, p), "nothing written")
}
