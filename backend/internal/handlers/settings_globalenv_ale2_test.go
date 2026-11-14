package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-ale2: a global env save wrote changed and added rows with "\n" in a
// CRLF file, and a key typed as "export FOO" was refused with a message about
// quotes.

func TestGlobalEnvAle2_CRLFKeptOnChangedAndAddedRows(t *testing.T) {
	for _, tc := range []struct{ name, file, want string }{
		{"final newline", "# c\r\nA=1\r\nB=2\r\n", "# c\r\nA=9\r\nB=2\r\nC=3\r\n"},
		{"no final newline, last line kept", "A=1\r\nB=2", "A=9\r\nB=2\r\nC=3\r\n"},
		{"no final newline, last line changed", "B=2\r\nA=1", "B=2\r\nA=9\r\nC=3\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, tc.file)

			vars, _ := getGlobalVars(t, router, "/unlocked")
			for _, v := range vars {
				if v["key"] == "A" {
					v["value"] = "9"
				}
			}
			vars = append(vars, map[string]string{"key": "C", "value": "3"})
			w := putGlobalVars(t, router, vars)
			require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
			assert.Equal(t, tc.want, readGlobalEnvTestFile(t, p))
		})
	}
}

// An LF file stays LF (the control for the CRLF arm).
func TestGlobalEnvAle2_LFFileStaysLF(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "A=1\nB=2")
	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "9"}, {"key": "B", "value": "2"}, {"key": "C", "value": "3"}})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "A=9\nB=2\nC=3\n", readGlobalEnvTestFile(t, p))
}

// A key typed as "export FOO" is the variable FOO to compose, written with its
// export prefix, as the stack table writes it.
func TestGlobalEnvAle2_TypedExportKey(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "A=1\n")

	w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "1"}, {"key": "export FOO", "value": "1"}})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "A=1\nexport FOO=1\n", readGlobalEnvTestFile(t, p))

	vars, _ := getGlobalVars(t, router, "/unlocked")
	assert.Equal(t, []map[string]string{{"key": "A", "value": "1"}, {"key": "FOO", "value": "1"}}, vars)

	// A bad value under a typed export key is still refused, by the quote check.
	w = putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "1"}, {"key": "FOO", "value": "1"}, {"key": "export BAR", "value": `"open`}})
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "A=1\nexport FOO=1\n", readGlobalEnvTestFile(t, p), "nothing written")
}
