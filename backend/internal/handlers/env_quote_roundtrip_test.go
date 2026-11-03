package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-wcrq: saving the env table stripped quotes and inline comments, so
// PW='ab$cd' came back as PW=ab$cd (compose then interpolates $cd) and
// VAL="a # b" came back as VAL=a. These tests reach the quote fields only
// through behaviour and JSON, never by Go field name, so they compile against
// the pre-fix tree and fail there on their assertions.

// quoteFixture holds every quoting shape compose's dotenv parser distinguishes.
// Whitespace normalisation (indent, spaces around "=", CRLF) is out of scope
// and deliberately absent.
const quoteFixture = `# header comment
PW='ab$cd'
VAL="a # b"
ESC="x\n \"q\" ${OTHER}"
INTERP=${HOST}:8080
NOTE=plain  # trailing note
HASH=a#b
QHASH='x # y' # after quote
ESCQ='it\'s'
EMPTY=
EMPTYQ=""
export EXP='lit$'

LAST=end
`

func TestEnvQuote_ParseSerialize_ByteIdentical(t *testing.T) {
	h := &EnvHandler{}
	got := serializeEnvFile(h.parseEnvFile(quoteFixture))
	assert.Equal(t, quoteFixture, got, "an unedited parse/serialize round trip must not change the file")

	// Same bytes means same meaning, but say it on compose's own parser too, so
	// the fixture is known to be valid compose input.
	lookup := func(string) (string, bool) { return "", false }
	want, err := dotenv.UnmarshalWithLookup(quoteFixture, lookup)
	require.NoError(t, err)
	gotVars, err := dotenv.UnmarshalWithLookup(got, lookup)
	require.NoError(t, err, "serialized output is not valid compose dotenv")
	assert.Equal(t, want, gotVars)
}

func TestEnvQuote_TableShowsValueInsideQuotes(t *testing.T) {
	h := &EnvHandler{}
	values := map[string]string{}
	for _, e := range h.parseEnvFile(quoteFixture) {
		if !e.Comment && e.Key != "" {
			values[e.Key] = e.Value
		}
	}
	assert.Equal(t, "ab$cd", values["PW"])
	assert.Equal(t, "a # b", values["VAL"], "a # inside quotes is not a comment")
	assert.Equal(t, "x # y", values["QHASH"])
	assert.Equal(t, "plain", values["NOTE"], "an unquoted ' #' starts a comment")
	assert.Equal(t, "a#b", values["HASH"], "a # with no space before it is part of the value")
	assert.Equal(t, `it\'s`, values["ESCQ"], "the table shows the text between the quotes verbatim")
}

func TestEnvQuote_EditedValueKeepsItsQuotingAndComment(t *testing.T) {
	h := &EnvHandler{}
	entries := h.parseEnvFile(quoteFixture)
	for i := range entries {
		switch entries[i].Key {
		case "PW":
			entries[i].Value = "new$v"
		case "NOTE":
			entries[i].Value = "other"
		case "VAL":
			entries[i].Value = "b # c"
		}
	}
	want := strings.NewReplacer(
		"PW='ab$cd'", "PW='new$v'",
		"NOTE=plain  # trailing note", "NOTE=other  # trailing note",
		`VAL="a # b"`, `VAL="b # c"`,
	).Replace(quoteFixture)
	assert.Equal(t, want, serializeEnvFile(entries))
}

func TestEnvQuote_EmptiedUnquotedValueDropsItsComment(t *testing.T) {
	h := &EnvHandler{}
	entries := h.parseEnvFile("NOTE=plain # note\n")
	require.Len(t, entries, 1)
	entries[0].Value = ""
	// "NOTE= # note" would make compose read the value "# note".
	assert.Equal(t, "NOTE=\n", serializeEnvFile(entries))
}

// TestEnvQuote_ComposeReadsEscapedQuoteWithoutBackslash pins the compose-go
// behaviour the validation message relies on: \' inside single quotes is an
// escaped quote, not a literal backslash.
func TestEnvQuote_ComposeReadsEscapedQuoteWithoutBackslash(t *testing.T) {
	vars, err := dotenv.UnmarshalWithLookup("S='it\\'s'\nD=\"say \\\"hi\\\"\"\n", nil)
	require.NoError(t, err)
	assert.Equal(t, "it's", vars["S"])
	assert.Equal(t, `say "hi"`, vars["D"])
}

// --- HTTP: the frontend sends back what GET gave it --------------------------

func envQuoteFixture(t *testing.T, content string) (*gin.Engine, string, string, string) {
	t.Helper()

	tempDir := t.TempDir()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	stackDir := filepath.Join(tempDir, "stack1")
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	envPath := filepath.Join(stackDir, ".env")
	require.NoError(t, os.WriteFile(envPath, []byte(content), 0600))
	createTestDirectory(t, db, stackDir)

	stackID := filepath.Base(tempDir) + "~stack1:default"
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          stackID,
		Directory:   stackDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "stack1-default",
	}))

	store := services.NewEnvUnlockStore()
	token, _, err := store.Mint(gateTestUserID)
	require.NoError(t, err)
	handler := NewEnvHandler(db, &config.Config{StacksDir: tempDir})

	router := gin.New()
	group := router.Group("")
	group.Use(authContextMiddleware(gateTestUserID))
	group.Use(middleware.EnvUnlock(store, false))
	group.GET("/stacks/:id/env", handler.Get)
	group.PUT("/stacks/:id/env", handler.Put)
	return router, stackID, token, envPath
}

// getEntriesAsJSON returns the entries exactly as a client holds them: JSON
// objects with whatever fields the server sent, which is what the editor
// spreads into its rows and sends back (EnvEditor.tsx, useEnvMutations.ts).
func getEntriesAsJSON(t *testing.T, router *gin.Engine, stackID, token string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Entries
}

func putEntries(t *testing.T, router *gin.Engine, stackID, token string, entries []map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"entries": entries})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/stacks/"+stackID+"/env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(middleware.EnvUnlockHeader, token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func readEnvQuoteFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test helper: path is a file under the test's own t.TempDir()
	require.NoError(t, err)
	return string(b)
}

func TestEnvQuote_HTTP_UneditedSaveIsByteIdentical(t *testing.T) {
	router, stackID, token, envPath := envQuoteFixture(t, quoteFixture)

	entries := getEntriesAsJSON(t, router, stackID, token)
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	assert.Equal(t, quoteFixture, readEnvQuoteFile(t, envPath))
}

// entryByKey finds the client-side entry for key.
func entryByKey(t *testing.T, entries []map[string]any, key string) map[string]any {
	t.Helper()
	for _, e := range entries {
		if e["key"] == key {
			return e
		}
	}
	t.Fatalf("no entry for key %s", key)
	return nil
}

// Each case is a request a client can craft. Every one must be refused and
// leave the file untouched; before validation knew the quote fields they were
// accepted, and a newline in inlineComment would add a line to .env.
func TestEnvQuote_HTTP_RejectsUnsafeQuoteFields(t *testing.T) {
	const content = "PW='ab$cd'\nNOTE=plain # note\n"
	cases := []struct {
		name   string
		key    string
		mutate func(e map[string]any)
	}{
		{"inlineComment with newline", "NOTE", func(e map[string]any) { e["inlineComment"] = " # x\nEVIL=1" }},
		{"inlineComment with carriage return", "NOTE", func(e map[string]any) { e["inlineComment"] = " # x\rEVIL=1" }},
		{"inlineComment that is not a comment", "NOTE", func(e map[string]any) { e["inlineComment"] = " EVIL=1" }},
		{"inlineComment with no space before # on an unquoted value", "NOTE", func(e map[string]any) { e["inlineComment"] = "#x" }},
		{"unknown quote", "PW", func(e map[string]any) { e["quote"] = "`" }},
		{"multi-character quote", "PW", func(e map[string]any) { e["quote"] = "''" }},
		{"bare single quote inside single quotes", "PW", func(e map[string]any) { e["value"] = "it's" }},
		{"trailing backslash inside single quotes", "PW", func(e map[string]any) { e["value"] = `abc\` }},
		{"bare double quote inside double quotes", "PW", func(e map[string]any) {
			e["quote"] = `"`
			e["value"] = `say "hi"`
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, stackID, token, envPath := envQuoteFixture(t, content)
			entries := getEntriesAsJSON(t, router, stackID, token)
			tc.mutate(entryByKey(t, entries, tc.key))

			w := putEntries(t, router, stackID, token, entries)

			assert.NotEqual(t, http.StatusOK, w.Code, "crafted entry was accepted")
			onDisk := readEnvQuoteFile(t, envPath)
			assert.Equal(t, content, onDisk, "a refused save must not touch the file")
			assert.NotContains(t, onDisk, "EVIL")
			// Errors name the key, never the value this case submitted.
			assertBodyOmits(t, w.Body.String(), entryByKey(t, entries, tc.key)["value"].(string))
			assertBodyOmits(t, w.Body.String(), "EVIL")
		})
	}
}

// The positive side of the rejections above, on the same instrument: escaped
// quotes and a tab-led comment after a closing quote are accepted.
func TestEnvQuote_HTTP_AcceptsEscapedQuotes(t *testing.T) {
	const content = "PW='ab$cd'\n"
	router, stackID, token, envPath := envQuoteFixture(t, content)
	entries := getEntriesAsJSON(t, router, stackID, token)
	e := entryByKey(t, entries, "PW")
	e["value"] = `it\'s`
	e["inlineComment"] = "\t# note"

	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "PW='it\\'s'\t# note\n", readEnvQuoteFile(t, envPath))
}

// assertBodyOmits checks s is absent from a JSON body in every form an error
// could carry it: as written or Go-quoted (%s, %q), each then JSON-escaped.
// Checking the raw form alone misses a value with a quote or backslash, which
// reaches the body escaped twice.
func assertBodyOmits(t *testing.T, body, s string) {
	t.Helper()
	goQuoted := strconv.Quote(s)
	for _, form := range []string{s, goQuoted[1 : len(goQuoted)-1]} {
		assert.NotContains(t, body, form)
		enc, err := json.Marshal(form)
		require.NoError(t, err)
		assert.NotContains(t, body, string(enc[1:len(enc)-1]))
	}
}

// compose skips any Unicode space before a "#" after a closing quote, and cuts
// an unquoted value at " #" then trims Unicode space to its right, so a
// no-break space in either place is valid compose and must not make an
// unedited save fail validation.
func TestEnvQuote_HTTP_UnicodeSpaceBeforeCommentSavesUnchanged(t *testing.T) {
	const content = "PW='x'\u00a0# note\nNOTE=plain\u00a0 # note\n"
	router, stackID, token, envPath := envQuoteFixture(t, content)

	entries := getEntriesAsJSON(t, router, stackID, token)
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, content, readEnvQuoteFile(t, envPath))
}

// A locked response blanks a sensitive row's value; the quote fields can hold
// part of it too. Here the closing quote is followed by junk, so the line falls
// back to the unquoted cut and the inline comment carries the secret's tail.
func TestEnvQuote_HTTP_LockedGetHidesSensitiveQuoteFields(t *testing.T) {
	const content = "DB_PASSWORD=\"hunter #twotwo\" tailpart\nTZ=UTC # zonenote\n"
	router, stackID, token, _ := envQuoteFixture(t, content)

	get := func(withToken bool) string {
		req := httptest.NewRequest(http.MethodGet, "/stacks/"+stackID+"/env", nil)
		if withToken {
			req.Header.Set(middleware.EnvUnlockHeader, token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		return w.Body.String()
	}

	// Positive side: unlocked, the fixture really carries the secret parts.
	unlocked := get(true)
	for _, part := range []string{"hunter", "twotwo", "tailpart"} {
		require.Contains(t, unlocked, part)
	}

	locked := get(false)
	for _, part := range []string{"hunter", "twotwo", "tailpart"} {
		assert.NotContains(t, locked, part, "a locked response leaked part of a sensitive value")
	}
	assert.Contains(t, locked, "zonenote", "a non-sensitive row keeps its inline comment")
}
