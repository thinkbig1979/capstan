package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/middleware"
)

// agent-os-n43b: parseEnvFile labelled every line without "=" a comment and
// split every other line at its first "=". compose's dotenv parser ends a key
// at the first '=' or ':' and inherits a bare one, so `DB_PASSWORD: x` and
// `API_TOKEN: a=b` are variables compose reads, but the table showed them as
// an unkeyed comment and a key "API_TOKEN: a", and the locked view, which
// masks by key, sent both secrets. These tests reach the handlers through
// HTTP and existing functions only, so they compile against the pre-fix tree
// and fail there on their assertions.

func getEnvBody(t *testing.T, router http.Handler, stackID, token string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	if token != "" {
		req.Header.Set(middleware.EnvUnlockHeader, token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String()
}

func TestEnvComposeLines_N43b_LockedViewMasksColonShapes(t *testing.T) {
	const content = "DB_PASSWORD: secretcolonone\n" +
		"API_TOKEN: a=secretcolontwo\n" +
		"export SVC_SECRET: secretexportthree\n" +
		"APP_PASSWORD=secretequalsfour\n" +
		"A\tB_PASSWORD: secrettabfive\n" +
		"TZ: UTC\n"
	secrets := []string{"secretcolonone", "secretcolontwo", "secretexportthree", "secretequalsfour", "secrettabfive"}
	router, stackID, token, _ := envQuoteFixture(t, content)

	// Positive side: unlocked, the fixture really carries every secret.
	unlocked := getEnvBody(t, router, stackID, token)
	for _, s := range secrets {
		require.Contains(t, unlocked, s)
	}

	locked := getEnvBody(t, router, stackID, "")
	for _, s := range secrets {
		assertBodyOmits(t, locked, s)
	}
	// A non-sensitive colon line keeps its text, so the masking is by key.
	assert.Contains(t, locked, "TZ: UTC")
}

func TestEnvComposeLines_N43b_KeysMatchCompose(t *testing.T) {
	// compose reads the variable lines as FOO (inherited), BAR=1,
	// API_TOKEN=a=b, and names holding a tab or no-break space (it skips
	// those inside a name and rejects only ' '); run its parser here so the
	// expected keys are observed, not assumed.
	lookup := func(k string) (string, bool) {
		if k == "FOO" {
			return "inherited", true
		}
		return "", false
	}
	const variables = "FOO\nBAR: 1\nAPI_TOKEN: a=b\nA\tB_PASSWORD: s\nC\u00a0D_TOKEN: t\n"
	got, err := dotenv.UnmarshalWithLookup(variables, lookup)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"FOO": "inherited", "BAR": "1", "API_TOKEN": "a=b", "A\tB_PASSWORD": "s", "C\u00a0D_TOKEN": "t",
	}, got)
	_, err = dotenv.UnmarshalWithLookup("a b\n", lookup)
	require.Error(t, err, "compose rejects a key with a space")

	router, stackID, token, _ := envQuoteFixture(t, variables+"a b\n")
	entries := getEntriesAsJSON(t, router, stackID, token)
	require.Len(t, entries, 6)

	want := []struct {
		key, value string
		sensitive  bool
	}{
		{"FOO", "FOO", false},
		{"BAR", "BAR: 1", false},
		{"API_TOKEN", "API_TOKEN: a=b", true},
		{"A\tB_PASSWORD", "A\tB_PASSWORD: s", true},
		{"C\u00a0D_TOKEN", "C\u00a0D_TOKEN: t", true},
		{"", "a b", false}, // compose rejects the file; the table keeps the line as text
	}
	for i, w := range want {
		e := entries[i]
		assert.Equal(t, w.key, e["key"], "row %d key", i)
		assert.Equal(t, w.value, e["value"], "row %d keeps its line verbatim", i)
		assert.Equal(t, w.sensitive, e["sensitive"], "row %d sensitive", i)
		assert.Equal(t, true, e["comment"], "row %d is read-only", i)
	}

	t.Run("bare key on the last line without a newline", func(t *testing.T) {
		// Pins what compose reads, not what the table does: with no '\n' to
		// end it, locateKeyName never closes the key, so compose reads the
		// line as the VALUE of an empty key and FOO is not defined.
		got, err := dotenv.UnmarshalWithLookup("FOO", lookup)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"": "FOO"}, got)
	})
}

// A ':' after the first '=' is part of the value, as compose reads it, so
// these stay ordinary editable rows.
func TestEnvComposeLines_N43b_ColonAfterEqualsStaysKeyRow(t *testing.T) {
	const content = "TIME=12:00\nK=\"a:b\"\nDATABASE_URL=postgres://u:p@h\n"
	got, err := dotenv.UnmarshalWithLookup(content, nil)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"TIME": "12:00", "K": "a:b", "DATABASE_URL": "postgres://u:p@h"}, got)

	entries := (&EnvHandler{}).parseEnvFile(content)
	assert.Equal(t, []EnvEntry{
		{Key: "TIME", Value: "12:00", Line: 1},
		{Key: "K", Value: "a:b", Line: 2, Quote: `"`},
		{Key: "DATABASE_URL", Value: "postgres://u:p@h", Line: 3},
	}, entries)
}

// PIN, green before and after the fix: these rows were comments written back
// verbatim before, and stay verbatim now that they carry keys. It exists for
// the mutant that writes a keyed row as KEY=, which turns an inherited FOO
// into an empty one.
func TestEnvComposeLines_N43b_UneditedSaveByteIdentical(t *testing.T) {
	const content = "LEGACY_FLAG\nYAML_STYLE: v\nAPI_TOKEN: a=b\nA=1\n"
	router, stackID, token, envPath := envQuoteFixture(t, content)

	entries := getEntriesAsJSON(t, router, stackID, token)
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	saved := readEnvQuoteFile(t, envPath)
	assert.Equal(t, content, saved)
	assert.NotContains(t, saved, "LEGACY_FLAG=")
}

func TestEnvComposeLines_N43b_UnkeyedVariableLineRefused(t *testing.T) {
	assert.Error(t, validateEnvEntries([]EnvEntry{{Comment: true, Value: "EVIL: 1"}}),
		"an unkeyed row compose reads as EVIL")
	assert.Error(t, validateEnvEntries([]EnvEntry{{Comment: true, Key: "OTHER", Value: "EVIL: 1"}}),
		"a row keyed differently from its line")
	assert.NoError(t, validateEnvEntries([]EnvEntry{{Comment: true, Key: "BAR", Value: "BAR: 1"}}),
		"the row the parser produces")

	t.Run("HTTP", func(t *testing.T) {
		router, stackID, token, envPath := envQuoteFixture(t, commentRowContent)
		entries := getEntriesAsJSON(t, router, stackID, token)
		entries = append(entries, map[string]any{"key": "", "value": "EVIL: 1", "comment": true})

		w := putEntries(t, router, stackID, token, entries)

		assert.NotEqual(t, http.StatusOK, w.Code, "unkeyed variable row was accepted")
		assert.Equal(t, commentRowContent, readEnvQuoteFile(t, envPath), "a refused save must not touch the file")
	})
}
