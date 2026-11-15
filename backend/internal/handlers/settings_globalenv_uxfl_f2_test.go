package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-uxfl review F2: the global env page writes a changed value as the
// raw right-hand side, so an unquoted "a # b" was read by compose as a and
// ab$cd was interpolated. Such a value is now refused (fail closed); a value
// the user quoted is written as before, and an unchanged line is untouched.

func TestGlobalEnvUxflF2_UnquotedValueComposeReadsDifferentlyRefused(t *testing.T) {
	for _, v := range []string{"a # b", "ab$cd", "${HOME}"} {
		t.Run(v, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, "X=1\n")
			w := putGlobalVars(t, router, []map[string]string{{"key": "X", "value": v}})
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `\"X\"`)
			assert.Contains(t, w.Body.String(), "global.env")
			assert.Equal(t, "X=1\n", readGlobalEnvTestFile(t, p))
		})
	}
}

// Positive side on the same route: values compose reads as written save.
func TestGlobalEnvUxflF2_ValueComposeReadsAsWrittenSaves(t *testing.T) {
	cases := []struct{ typed, composeReads string }{
		{"plain", "plain"},
		{"'a # b'", "a # b"},
		{"'ab$cd'", "ab$cd"},
		{"a#b", "a#b"},
	}
	for _, c := range cases {
		t.Run(c.typed, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, "X=1\n")
			w := putGlobalVars(t, router, []map[string]string{{"key": "X", "value": c.typed}})
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			assert.Equal(t, c.composeReads, composeReadsFile(t, readGlobalEnvTestFile(t, p))["X"])
		})
	}
}

// An unchanged line compose interpolates is the file's own text: kept.
func TestGlobalEnvUxflF2_UnchangedInterpolatedLineKept(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "X=ab$cd\nY=1\n")
	w := putGlobalVars(t, router, []map[string]string{{"key": "X", "value": "ab$cd"}, {"key": "Y", "value": "2"}})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "X=ab$cd\nY=2\n", readGlobalEnvTestFile(t, p))
}
