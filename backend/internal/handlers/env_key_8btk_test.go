package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-8btk: a key compose reads differently was written as typed:
// "A=B" became A, "A:B" became A, "#A" a comment, "A B" a file compose
// rejects, and "export FOO" the variable FOO. Each is refused on save, with
// the index and the key named.

var keys8btk = []string{"A=B", "A:B", "#A", "A B", "export FOO"}

func TestEnvKey_8btk_StackTableRefusesKeysComposeReadsDifferently(t *testing.T) {
	const content = "OK=1\n"
	for _, key := range keys8btk {
		t.Run(key, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, content)
			entries := getEntriesAsJSON(t, router, stackID, token)
			entries = append(entries, map[string]any{"key": key, "value": "v"})
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert8btkNamesKey(t, w.Body.String(), 1, key)
			assert.Equal(t, content, readEnvQuoteFile(t, envPath))
		})
	}
}

func TestEnvKey_8btk_ComposeEnvRefusesKeysComposeReadsDifferently(t *testing.T) {
	const content = "OK=1\n"
	for _, key := range keys8btk {
		t.Run(key, func(t *testing.T) {
			s, envPath := composeEnvFixture(t, content)
			w := s.put(t, composeEnvEntriesBody(t, []map[string]any{
				{"key": "OK", "value": "1", "line": 1},
				{"key": key, "value": "v"},
			}))
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert8btkNamesKey(t, w.Body.String(), 1, key)
			assert.Equal(t, content, readEnvQuoteFile(t, envPath))
		})
	}
}

// The global page refused these already (globalValueReadsAsOne); "export
// FOO" there is the variable FOO written with its prefix (agent-os-ale2,
// TestGlobalEnvAle2_TypedExportKey), so it is not in this list.
func TestEnvKey_8btk_GlobalEnvRefusesKeysComposeReadsDifferently(t *testing.T) {
	const content = "OK=1\n"
	for _, key := range keys8btk[:4] {
		t.Run(key, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			p := writeGlobalEnvFile(t, dataDir, content)
			w := putGlobalVars(t, router, []map[string]string{{"key": "OK", "value": "1"}, {"key": key, "value": "v"}})
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert8btkNamesKey(t, w.Body.String(), 1, key)
			assert.Equal(t, content, readGlobalEnvTestFile(t, p))
		})
	}
}

// Every key in the repo's real .env fixtures is still accepted: an unedited
// table save of each one answers 200 and keeps the file byte for byte.
func TestEnvKey_8btk_FixtureKeysStillAccepted(t *testing.T) {
	var files []string
	for _, pattern := range []string{"../../testdata/env-files/*.env", "../../testdata/stacks/*/.env*"} {
		m, err := filepath.Glob(pattern)
		require.NoError(t, err)
		files = append(files, m...)
	}
	require.GreaterOrEqual(t, len(files), 6, "the fixture corpus was found")
	for _, f := range files {
		t.Run(filepath.Base(filepath.Dir(f))+"/"+filepath.Base(f), func(t *testing.T) {
			b, err := os.ReadFile(f) //nolint:gosec // test fixture path from a fixed glob
			require.NoError(t, err)
			router, stackID, token, envPath := envQuoteFixture(t, "")
			require.NoError(t, os.WriteFile(envPath, b, 0o600)) //nolint:gosec // test: envPath is under the test's own t.TempDir(); b is a tracked fixture's text
			entries := getEntriesAsJSON(t, router, stackID, token)
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, string(b), readEnvQuoteFile(t, envPath))
		})
	}
}

func assert8btkNamesKey(t *testing.T, body string, index int, key string) {
	t.Helper()
	assert.Contains(t, body, fmt.Sprintf("index %d", index))
	// The key as %q writes it, then as JSON escapes that (\" only, here).
	q := strconv.Quote(strconv.Quote(key))
	assert.Contains(t, body, q[1:len(q)-1])
	// The refusal is about the key, and says where such a line can be written.
	assert.Contains(t, body, "key")
	assert.NotContains(t, body, "value compose would read differently")
	if strings.Contains(key, " ") {
		assert.Contains(t, body, "space in its key")
	} else {
		assert.Contains(t, body, "Raw view")
	}
}
