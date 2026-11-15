package handlers

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-omzj: a table save keeps each unedited row's own text. An edited
// or added row is written in the parser's form, with the file's line ending,
// and the file keeps its final-newline state.

const omzjFile = "  A=1\r\n# c\r\n\tB=2  # two\r\nC=3"

func TestEnvKeepLines_omzj_EditedRowUsesTheFileLineEnding(t *testing.T) {
	router, stackID, token, envPath := envQuoteFixture(t, omzjFile)
	entries := getEntriesAsJSON(t, router, stackID, token)
	require.Len(t, entries, 4)
	entries[0]["value"] = "5"
	entries = append(entries, map[string]any{"key": "D", "value": "4", "sensitive": false})
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "A=5\r\n# c\r\n\tB=2  # two\r\nC=3\r\nD=4", readEnvQuoteFile(t, envPath))
}

// The file changed between the client's GET and its PUT. A row is kept as
// the file's text only when the file's row at its line is still that same
// row; otherwise it is written from the request, as before this fix. The save
// writes the client's rows either way, and never a line of the new file the
// client did not send.
func TestEnvKeepLines_omzj_ExternalEditFallsBackToTheRequestRows(t *testing.T) {
	cases := []struct{ name, external, want string }{
		// Line 1 now holds a different value: written from the request.
		{"value changed", "  A=9\r\n# c\r\n\tB=2  # two\r\nC=3", "A=1\r\n# c\r\n\tB=2  # two\r\nC=3"},
		// Every row moved down a line: none matches, all from the request.
		{"line inserted", "X=0\r\n  A=1\r\n# c\r\n\tB=2  # two\r\nC=3", "A=1\r\n# c\r\nB=2  # two\r\nC=3"},
		// The file now ends in a newline and uses LF: the request rows are
		// written with the file's current line ending and final newline.
		{"line endings changed", "A=1\n# c\nB=2  # two\nC=3\n", "A=1\n# c\nB=2  # two\nC=3\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, omzjFile)
			entries := getEntriesAsJSON(t, router, stackID, token)
			require.NoError(t, os.WriteFile(envPath, []byte(c.external), 0o600))
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			got := readEnvQuoteFile(t, envPath)
			assert.Equal(t, c.want, got)
			assert.NotContains(t, got, "X=0")
		})
	}
}

// Shapes beyond the corpus fixture: an unedited save keeps each of them.
func TestEnvKeepLines_omzj_UneditedSaveKeepsOddLineEnds(t *testing.T) {
	for _, content := range []string{
		"A=1\nB=2\r",              // a last line ending in a bare CR
		"A=1\r\nB=2\n",            // mixed endings
		"  A = 1  \n\t# c\n   \n", // spaces around key and value, a blank line of spaces
		"export  E=1\nM=\"l1\r\nl2\"\r\n",
	} {
		router, stackID, token, envPath := envQuoteFixture(t, content)
		w := putEntries(t, router, stackID, token, getEntriesAsJSON(t, router, stackID, token))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, content, readEnvQuoteFile(t, envPath))
	}
}

// Review E4: in a file with no final newline, a request ending in a blank
// row keeps that row. The blank last line can only be written as a final
// newline, so the row wins over the final-newline state; a request ending in
// a variable still keeps the state.
func TestEnvKeepLines_omzj_TrailingBlankRowSurvivesNoFinalNewline(t *testing.T) {
	cases := []struct {
		name  string
		extra []map[string]any
		want  string
	}{
		{"blank row added", []map[string]any{{"key": "", "value": "", "sensitive": false}}, "A=1\n\n"},
		{"nothing added (control)", nil, "A=1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, "A=1")
			entries := append(getEntriesAsJSON(t, router, stackID, token), c.extra...)
			w := putEntries(t, router, stackID, token, entries)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			got := readEnvQuoteFile(t, envPath)
			assert.Equal(t, c.want, got)
			assert.Len(t, (&EnvHandler{}).parseEnvFile(got), len(entries), "every row the client sent reads back")
		})
	}
}

// Review E5: a request row equal to the file's row except in one written
// field is written from the request, not as the file's text. Each file line
// is indented, so the file's text and the request's form differ.
func TestEnvKeepLines_omzj_EachWrittenFieldBreaksTheMatch(t *testing.T) {
	cases := []struct {
		field, file string
		change      func(*EnvEntry)
		want        string
	}{
		{"Quote", "  A='x'\n", func(e *EnvEntry) { e.Quote = `"` }, "A=\"x\"\n"},
		{"InlineComment", "  A=x # c\n", func(e *EnvEntry) { e.InlineComment = " # d" }, "A=x # d\n"},
		{"Comment", "  FOO\n", func(e *EnvEntry) { e.Comment = false }, "FOO=FOO\n"},
		{"Export", "  export A=1\n", func(e *EnvEntry) { e.Export = false }, "A=1\n"},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			rows := (&EnvHandler{}).parseEnvFile(c.file)
			require.Len(t, rows, 1)
			out, err := serializeEnvFileOver(c.file, rows)
			require.NoError(t, err)
			require.Equal(t, c.file, out, "unchanged: the file's own text")
			c.change(&rows[0])
			out, err = serializeEnvFileOver(c.file, rows)
			require.NoError(t, err)
			assert.Equal(t, c.want, out)
		})
	}
}
