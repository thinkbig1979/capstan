package handlers

import (
	"net/http"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-qdnb: compose-go's dotenv parser reads a bare KEY on the last line
// with no line end as key "" with value "KEY", so KEY is not defined; a bare
// "export" line is never a variable either (its line end is part of the
// prefix). The table keyed both. Named N43b so that bead's filter runs them.

// composeDefines is the keys compose defines for content, with every
// inherited name present in the environment.
func composeDefines(t *testing.T, content string) map[string]bool {
	t.Helper()
	vars, err := dotenv.UnmarshalWithLookup(content, func(string) (string, bool) { return "inherited", true })
	require.NoError(t, err)
	out := map[string]bool{}
	for k := range vars {
		if k != "" {
			out[k] = true
		}
	}
	return out
}

func qdnbTableKeys(t *testing.T, content string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, e := range (&EnvHandler{}).parseEnvFile(content) {
		if e.Key != "" {
			out[e.Key] = true
		}
	}
	return out
}

func TestEnvComposeLines_N43b_Qdnb_TableKeysMatchCompose(t *testing.T) {
	for _, content := range []string{
		"A=1\nFOO",          // bare last line, no line end: not defined
		"A=1\nFOO  ",        // trailing space, still no line end
		"A=1\nFOO\n",        // with a line end: defined, inherited
		"FOO",               // the whole file
		"export\nB=1\n",     // a bare export line
		"A=1\nexport",       // ... as the unterminated last line
		"A=1\nexport FOO",   // export prefix, no line end: compose reads FOO
		"A=1\nexport FOO\n", // and with one
		"A=1\nFOO: x",       // a colon line is a variable either way
		"A=1\nDB_PASSWORD",  // the masking case below
	} {
		t.Run(content, func(t *testing.T) {
			assert.Equal(t, composeDefines(t, content), qdnbTableKeys(t, content))
		})
	}
}

func TestEnvComposeLines_N43b_Qdnb_UneditedSaveByteIdentical(t *testing.T) {
	for _, content := range []string{"A=1\nFOO", "A=1\nexport", "export\nB=1\n"} {
		t.Run(content, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, content)
			w := putEntries(t, router, stackID, token, getEntriesAsJSON(t, router, stackID, token))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, content, readEnvQuoteFile(t, envPath))
		})
	}
}

// Unkeyed, the line is still hidden while locked if it looked sensitive:
// the row only gets hidden, never shown (fail closed).
func TestEnvComposeLines_N43b_Qdnb_LockedViewStillHidesIt(t *testing.T) {
	const content = "A=1\nDB_PASSWORD"
	router, stackID, token, _ := envQuoteFixture(t, content)
	require.Contains(t, getEnvBody(t, router, stackID, token), "DB_PASSWORD")
	assert.NotContains(t, getEnvBody(t, router, stackID, ""), "DB_PASSWORD")
}

func TestEnvComposeLines_N43b_Qdnb_GlobalPageShowsNoRow(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "A=1\nFOO")
	vars, _ := getGlobalVars(t, router, "/unlocked")
	require.Len(t, vars, 1)
	assert.Equal(t, "A", vars[0]["key"])
	w := putGlobalVars(t, router, vars)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "A=1\nFOO", readGlobalEnvTestFile(t, p))
}

// A row written after the unterminated bare last line would give it a line
// end, and compose would then define FOO: the save must not change what
// compose defines, so it is refused and the file left as it is.
func TestEnvComposeLines_N43b_Qdnb_RowAfterBareLastLineRefused(t *testing.T) {
	const content = "A=1\nFOO"
	t.Run("stack", func(t *testing.T) {
		router, stackID, token, envPath := envQuoteFixture(t, content)
		entries := append(getEntriesAsJSON(t, router, stackID, token), map[string]any{"key": "N", "value": "1"})
		w := putEntries(t, router, stackID, token, entries)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "Raw view")
		assert.Equal(t, content, readEnvQuoteFile(t, envPath))
	})
	t.Run("global", func(t *testing.T) {
		dataDir, router := globalEnvTestRouter(t)
		p := writeGlobalEnvFile(t, dataDir, content)
		w := putGlobalVars(t, router, []map[string]string{{"key": "A", "value": "1"}, {"key": "N", "value": "1"}})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Equal(t, content, readGlobalEnvTestFile(t, p))
	})
	// Positive side: with a line end the same saves succeed.
	t.Run("stack with a line end", func(t *testing.T) {
		router, stackID, token, envPath := envQuoteFixture(t, content+"\n")
		entries := append(getEntriesAsJSON(t, router, stackID, token), map[string]any{"key": "N", "value": "1"})
		w := putEntries(t, router, stackID, token, entries)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "A=1\nFOO\nN=1\n", readEnvQuoteFile(t, envPath))
	})
}
