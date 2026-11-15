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
		assert.Equal(t, s.original, s.composeOnDisk(t), "compose half written on a refused save")
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
	b, err := json.Marshal(map[string]any{"composeContent": changedComposeF4, "envEntries": entries})
	require.NoError(t, err)
	return string(b)
}

// changedComposeF4 differs from the compose file on disk, so a refused save
// that wrote the compose half anyway shows (review F4).
const changedComposeF4 = "services:\n  web:\n    image: nginx\n"

func (s noEnvStack) composeOnDisk(t *testing.T) string {
	t.Helper()
	return readEnvQuoteFile(t, filepath.Join(s.dir, "compose.yaml"))
}

// Review F4: every env refusal on PUT compose-env leaves both files as they
// were, whether validateEnvEntries refuses before the lock or
// serializeEnvFileOver refuses under it. The accepted control writes both.
func TestComposeEnvUxflF4_RefusalLeavesComposeUnchanged(t *testing.T) {
	cases := []struct {
		name, env string
		entries   []map[string]any
	}{
		{"empty key", "K=old\n", []map[string]any{{"key": "", "value": "x"}}},
		{"key compose reads differently", "K=old\n", []map[string]any{{"key": "A B", "value": "x"}}},
		{"value no quoting keeps", "K=old\n", []map[string]any{{"key": "K", "value": `'a" # b`, "line": 1}}},
		{"row after a bare unterminated last line", "A=1\nFOO", []map[string]any{
			{"key": "A", "value": "1", "line": 1},
			{"key": "", "value": "FOO", "line": 2, "comment": true},
			{"key": "N", "value": "v"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, envPath := composeEnvFixture(t, c.env)
			w := s.put(t, composeEnvEntriesBody(t, c.entries))
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Equal(t, s.original, s.composeOnDisk(t), "compose half written on a refused save")
			assert.Equal(t, c.env, readEnvQuoteFile(t, envPath))
		})
	}
	t.Run("control: accepted save writes both", func(t *testing.T) {
		s, envPath := composeEnvFixture(t, "K=old\n")
		w := s.put(t, composeEnvEntriesBody(t, []map[string]any{{"key": "K", "value": "new", "line": 1}}))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, changedComposeF4, s.composeOnDisk(t))
		assert.Equal(t, "K=new\n", readEnvQuoteFile(t, envPath))
	})
}
