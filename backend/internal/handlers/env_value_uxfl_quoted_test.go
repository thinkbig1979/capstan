package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-uxfl review F1: an edited row that already had quotes was written
// with them unchecked, because the client sends a row's quote back as it got
// it. compose then read a different value (PW="pa$word" as pa, '\'' as a
// quote), and a value another quoting would keep was refused. Every edited
// row is now written as-is, single- or double-quoted, whichever compose-go
// reads back as the typed text (literal, D5), else refused.

func TestEnvValueUxfl_F1_EditedQuotedRowReadsBackAsTyped(t *testing.T) {
	cases := []struct{ file, typed string }{
		{`PW="old"` + "\n", "pa$word"},
		{`PW="old"` + "\n", `a\nb`},
		{`PW="old"` + "\n", "${HOME}"},
		{`PW="old"` + "\n", `a"b`},
		{"PW='old'\n", `a\'b`},
		{"PW='old'\n", "it's"},
		{"PW='old'\n", "plain"},
	}
	for _, c := range cases {
		t.Run(c.file+" "+c.typed, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, c.file)
			entries := getEntriesAsJSON(t, router, stackID, token)
			entries[0]["value"] = c.typed
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			saved := readEnvQuoteFile(t, envPath)
			assert.Equal(t, c.typed, composeReadsFile(t, saved)["PW"], "saved: %q", saved)
			assert.Equal(t, c.typed, entryByKey(t, getEntriesAsJSON(t, router, stackID, token), "PW")["value"])
		})
	}
}

// A row the uxfl fix itself quoted (K="'") edited to a value no quoting
// keeps ('pa$word holds a ' and a $) is refused, and the file is unchanged.
func TestEnvValueUxfl_F1_EditedQuotedRowNoQuotingKeepsRefused(t *testing.T) {
	const content = `K="'"` + "\n"
	router, stackID, token, envPath := envQuoteFixture(t, content)
	entries := getEntriesAsJSON(t, router, stackID, token)
	require.Equal(t, `"`, entries[0]["quote"])
	entries[0]["value"] = "'pa$word"
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, content, readEnvQuoteFile(t, envPath))
}

// Round 3 (review R2-2): an edited quoted row is also tried unquoted, so a
// value no quote keeps but compose reads as typed unquoted (a trailing
// backslash) saves instead of being refused.
func TestEnvValueUxfl_R22_EditedQuotedRowMayBeWrittenUnquoted(t *testing.T) {
	for _, c := range []struct{ file, typed, want string }{
		{`PW="old"` + "\n", `\`, `PW=\` + "\n"},
		{`PW="old"` + "\n", `C:\dir\`, `PW=C:\dir\` + "\n"},
		{"PW='old'\n", `C:\dir\`, `PW=C:\dir\` + "\n"},
	} {
		t.Run(c.file+" "+c.typed, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, c.file)
			entries := getEntriesAsJSON(t, router, stackID, token)
			entries[0]["value"] = c.typed
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Equal(t, c.want, readEnvQuoteFile(t, envPath))
			assert.Equal(t, c.typed, composeReadsFile(t, readEnvQuoteFile(t, envPath))["PW"])
		})
	}
}
