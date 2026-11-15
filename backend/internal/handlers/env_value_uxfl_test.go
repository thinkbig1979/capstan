package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-uxfl: an edited or new unquoted value was written raw, so compose
// read a different value ("a # b" as "a", 'abc as a broken file, 'x' Y as x,
// ab$cd as ab), and a validation refusal answered 500, logged as a server
// fault. Every check reads the saved file with compose's own parser.

var uxflValues = []string{"a # b", "'abc", "'x' Y", "ab$cd"}

// composeReadsFile parses an env file's text as compose does, with no
// variable defined, so an interpolated value reads differently from its text.
func composeReadsFile(t *testing.T, content string) map[string]string {
	t.Helper()
	vars, err := dotenv.UnmarshalWithLookup(content, func(string) (string, bool) { return "", false })
	require.NoError(t, err, "compose must be able to read the saved file:\n%s", content)
	return vars
}

func TestEnvValueUxfl_EditedValueReadsBackAsTyped(t *testing.T) {
	for _, v := range uxflValues {
		t.Run(v, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, "K=old\n")
			entries := getEntriesAsJSON(t, router, stackID, token)
			entries[0]["value"] = v
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, v, composeReadsFile(t, readEnvQuoteFile(t, envPath))["K"])
			// The table shows the value as typed after the save too.
			assert.Equal(t, v, entryByKey(t, getEntriesAsJSON(t, router, stackID, token), "K")["value"])
		})
	}
}

func TestEnvValueUxfl_NewValueReadsBackAsTyped(t *testing.T) {
	for _, v := range uxflValues {
		t.Run(v, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, "K=old\n")
			entries := getEntriesAsJSON(t, router, stackID, token)
			entries = append(entries, map[string]any{"key": "N", "value": v})
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			vars := composeReadsFile(t, readEnvQuoteFile(t, envPath))
			assert.Equal(t, v, vars["N"])
			assert.Equal(t, "old", vars["K"])
		})
	}
}

// An unchanged row is the file's own text, kept byte for byte even where
// compose interpolates it: the save must not change what an untouched line
// means.
func TestEnvValueUxfl_UnchangedRawDollarLineKept(t *testing.T) {
	const content = "K=ab$cd\nM=1\n"
	router, stackID, token, envPath := envQuoteFixture(t, content)
	entries := getEntriesAsJSON(t, router, stackID, token)
	entries[1]["value"] = "2"
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "K=ab$cd\nM=2\n", readEnvQuoteFile(t, envPath))
}

// A value no quoting can keep (both quote kinds, and compose would read it
// differently unquoted) is refused with a 400 naming the key, never the value.
func TestEnvValueUxfl_UnquotableValueRefused400(t *testing.T) {
	const content = "K=old\n"
	const v = `'a" # uxflsecret`
	router, stackID, token, envPath := envQuoteFixture(t, content)
	entries := getEntriesAsJSON(t, router, stackID, token)
	entries[0]["value"] = v
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `\"K\"`)
	assert.Contains(t, w.Body.String(), "Raw view")
	assert.NotContains(t, w.Body.String(), "uxflsecret")
	assert.Equal(t, content, readEnvQuoteFile(t, envPath))
}

// A validation refusal is a client error: 400 VALIDATION_ERROR, on both
// routes that save env rows.
func TestEnvValueUxfl_ValidationRefusalIs400(t *testing.T) {
	t.Run("PUT env", func(t *testing.T) {
		router, stackID, token, envPath := envQuoteFixture(t, "K=old\n")
		w := putEntries(t, router, stackID, token, []map[string]any{{"key": "", "value": "x"}})
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), models.ErrValidation)
		assert.Equal(t, "K=old\n", readEnvQuoteFile(t, envPath))
	})
	t.Run("PUT compose-env", func(t *testing.T) {
		s, envPath := composeEnvFixture(t, "K=old\n")
		w := s.put(t, composeEnvEntriesBody(t, []map[string]any{{"key": "", "value": "x"}}))
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), models.ErrValidation)
		assert.Equal(t, "K=old\n", readEnvQuoteFile(t, envPath))
	})
}

// composeEnvFixture is newNoEnvStack with an env file recorded, for the
// compose-env route's entries path.
func composeEnvFixture(t *testing.T, envContent string) (noEnvStack, string) {
	t.Helper()
	s := newNoEnvStack(t, "compose.yaml")
	envPath := filepath.Join(s.dir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte(envContent), 0o600))
	stack, err := s.db.GetStack(s.id)
	require.NoError(t, err)
	stack.EnvFile = ".env"
	require.NoError(t, s.db.UpsertStack(*stack))
	return s, envPath
}

func composeEnvEntriesBody(t *testing.T, entries []map[string]any) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"composeContent": noEnvOriginalCompose, "envEntries": entries})
	require.NoError(t, err)
	return string(b)
}
