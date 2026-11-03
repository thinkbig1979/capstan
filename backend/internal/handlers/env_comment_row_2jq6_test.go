package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-2jq6: validateEnvEntries skipped comment rows, and serializeEnvFile
// writes a comment row's value verbatim and drops its key, so a crafted table
// save could add a KEY=value line to a stack's .env. The empty-key error also
// formatted the value, which may be a secret, into a 500 that is logged at
// ERROR. These tests reach the handlers only through JSON, so they compile
// against the pre-fix tree and fail there on their assertions.

// Both sides on one instrument: each rejected row is accepted before the fix,
// and each accepted row is one the parser itself produces from a real file.
func TestValidateEnvEntries_CommentRows(t *testing.T) {
	accepted := []EnvEntry{
		{Comment: true, Value: "# a comment"},
		{Comment: true, Value: "#"},
		{Comment: true, Value: "LEGACY_FLAG"}, // the parser labels a no-"=" line a comment
		{Comment: true, Value: "KEY: v"},
		{Comment: true, Value: ""}, // a blank line
	}
	for _, e := range accepted {
		assert.NoError(t, validateEnvEntries([]EnvEntry{e}), "comment row %q", e.Value)
	}

	rejected := []EnvEntry{
		{Comment: true, Value: "# x\nEVIL=1"},
		{Comment: true, Value: "EVIL=1"},
		{Comment: true, Value: "# x\rEVIL=1"},
		{Comment: true, Value: " # padded"},
		{Comment: true, Key: "EVIL", Value: "# x"},
	}
	for _, e := range rejected {
		assert.Error(t, validateEnvEntries([]EnvEntry{e}), "comment row key %q value %q", e.Key, e.Value)
	}
}

const commentRowContent = "# header\nA=1\n"

// Every case is a request a client can craft; the editor shows comment rows
// read-only. Each must be refused and leave the file untouched.
func TestEnvCommentRow_HTTP_RejectsRowsThatAreNotOneComment(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(entries []map[string]any) []map[string]any
	}{
		{"comment value with a newline", setComment("# x\nEVIL=1")},
		{"comment value that is a KEY line", setComment("EVIL=1")},
		{"comment value with a carriage return", setComment("# x\rEVIL=1")},
		{"comment value with leading space", setComment(" # padded EVIL")},
		{"comment row carrying a key", func(entries []map[string]any) []map[string]any {
			entries[0]["key"] = "EVIL"
			return entries
		}},
		{"appended comment row that is a KEY line", func(entries []map[string]any) []map[string]any {
			return append(entries, map[string]any{"key": "", "value": "EVIL=1", "comment": true})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, commentRowContent)
			entries := getEntriesAsJSON(t, router, stackID, token)
			require.Equal(t, true, entries[0]["comment"], "fixture: row 0 is the header comment")
			entries = tc.mutate(entries)

			w := putEntries(t, router, stackID, token, entries)

			assert.NotEqual(t, http.StatusOK, w.Code, "crafted comment row was accepted")
			onDisk := readEnvQuoteFile(t, envPath)
			assert.Equal(t, commentRowContent, onDisk, "a refused save must not touch the file")
			assert.NotContains(t, onDisk, "EVIL")
			assertBodyOmits(t, w.Body.String(), "EVIL")
		})
	}
}

func setComment(v string) func([]map[string]any) []map[string]any {
	return func(entries []map[string]any) []map[string]any {
		entries[0]["value"] = v
		return entries
	}
}

// PUT /compose-env takes the same rows through envEntries. The control saves a
// valid row on the same fixture, so a refusal is not the lint or the fixture.
func TestEnvCommentRow_HTTP_ComposeEnvRejectsRowsThatAreNotOneComment(t *testing.T) {
	const compose = "services:\n  web:\n    image: nginx\n"
	put := func(t *testing.T, envEntries []map[string]any) (int, string, string, string) {
		router, _, stackID, stackDir := fileLockFixture(t, true)
		body, err := json.Marshal(map[string]any{"composeContent": compose, "envEntries": envEntries})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, "/api/"+stackID+"/compose-env", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		read := func(name string) string {
			//nolint:gosec // test helper: name is a literal from this file, stackDir is the test's own TempDir
			b, err := os.ReadFile(filepath.Join(stackDir, name))
			require.NoError(t, err)
			return string(b)
		}
		return w.Code, w.Body.String(), read(".env"), read("compose.yaml")
	}

	t.Run("control: a valid row saves", func(t *testing.T) {
		code, body, env, _ := put(t, []map[string]any{{"key": "A", "value": "2"}})
		require.Equal(t, http.StatusOK, code, body)
		assert.Equal(t, "A=2\n", env)
	})

	for _, v := range []string{"# x\nEVIL=1", "EVIL=1"} {
		t.Run(strings.ReplaceAll(v, "\n", `\n`), func(t *testing.T) {
			code, body, env, composeOnDisk := put(t, []map[string]any{
				{"key": "A", "value": "2"},
				{"key": "", "value": v, "comment": true},
			})
			assert.NotEqual(t, http.StatusOK, code, "crafted comment row was accepted")
			assert.Equal(t, "A=1\n", env, "a refused save must not touch the env file")
			assert.Equal(t, "services: {}\n", composeOnDisk, "a refused save must not touch the compose file")
			assertBodyOmits(t, body, "EVIL")
		})
	}
}

// The value of an empty-key row may be a secret. secretCore is alphanumeric so
// one NotContains covers every escaping (%q, JSON, slog's quoting); the quote
// around it makes those escapings differ from the raw text.
const secretCore = "s3cr3t2jq6"

func TestEnvEmptyKeyError_OmitsTheValue(t *testing.T) {
	secret := `"` + secretCore + `"`

	t.Run("PUT env: body and ERROR log", func(t *testing.T) {
		router, stackID, token, envPath := envQuoteFixture(t, commentRowContent)
		logs := captureHandlerLogs(t)

		w := putEntries(t, router, stackID, token, []map[string]any{{"key": "", "value": secret}})

		require.NotEqual(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, commentRowContent, readEnvQuoteFile(t, envPath))
		assert.Contains(t, w.Body.String(), "index 0", "the error still names the row")
		assert.NotContains(t, w.Body.String(), secretCore)
		// Positive side on the same buffer: this request's failure was logged.
		require.Contains(t, logs.String(), "env validation failed")
		assert.NotContains(t, logs.String(), secretCore)
	})

	t.Run("PUT global env: body", func(t *testing.T) {
		_, router := newTestSettingsHandler(t)
		body, err := json.Marshal(map[string]any{"vars": []map[string]any{{"key": "", "value": secret}}})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPut, "/settings/global-env", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "index 0", "the error still names the row")
		// No log check: nothing logs the message of UpdateGlobalEnv's 400, so
		// one could only pass.
		assert.NotContains(t, w.Body.String(), secretCore)
	})
}

// PIN, green before and after the fix: a real file with "#" comments, a bare
// line, a YAML-style line and blank lines still saves byte-identical, so the
// comment-row check refuses nothing the parser produced. LEGACY_FLAG and
// "YAML_STYLE: v" are pinned as accepted BY DESIGN, not as safe: compose reads
// both as variables while the parser labels them comments (agent-os-n43b).
func TestEnvCommentRow_HTTP_RealFileSavesUnchanged(t *testing.T) {
	const content = "# header comment\nLEGACY_FLAG\nYAML_STYLE: v\nA=1\n\n#\n#no space\nB=2 # note\n\n"
	router, stackID, token, envPath := envQuoteFixture(t, content)

	entries := getEntriesAsJSON(t, router, stackID, token)
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	saved := readEnvQuoteFile(t, envPath)
	assert.Equal(t, content, saved)
	_, err := dotenv.UnmarshalWithLookup(saved, func(string) (string, bool) { return "", false })
	assert.NoError(t, err, "saved file is not valid compose dotenv")
}
