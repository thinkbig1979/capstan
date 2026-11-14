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

// agent-os-zpbi: compose ends a quoted value at its closing quote and reads
// what follows on the line as the next statement (compose-go v2
// dotenv/parser.go: extractVarValue returns src[i+1:], parse starts over), so
// `NOTES="a" DB_PASSWORD=secret` is two variables. The table showed it as one
// NOTES row, keyed and masked as NOTES, so the password was shown to a locked
// session.

var afterQuoteLines = []struct {
	name, line, secret, composeKey string
}{
	{"equals", "NOTES=\"a\" DB_PASSWORD=zpbiequalsecret\n", "zpbiequalsecret", "DB_PASSWORD"},
	// The read-only "KEY: value" line has the same tail.
	{"colon", "NOTES: 'a' API_TOKEN=zpbicolonsecret\n", "zpbicolonsecret", "API_TOKEN"},
}

func TestEnvAfterQuote_zpbi_ComposeReadsTheTailAsAVariable(t *testing.T) {
	for _, c := range afterQuoteLines {
		t.Run(c.name, func(t *testing.T) {
			vars, err := dotenv.UnmarshalWithLookup(c.line, nil)
			require.NoError(t, err)
			assert.Equal(t, c.secret, vars[c.composeKey], "the oracle: compose reads the tail as its own variable")
			assert.Equal(t, "a", vars["NOTES"])
		})
	}
}

func TestEnvAfterQuote_zpbi_LockedGetHidesTheTail(t *testing.T) {
	for _, c := range afterQuoteLines {
		t.Run(c.name, func(t *testing.T) {
			router, stackID, token, _ := envQuoteFixture(t, "TZ=UTC\n"+c.line)
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
			require.Contains(t, get(true), c.secret, "positive side: unlocked shows it")
			assertBodyOmits(t, get(false), c.secret)
		})
	}
}

// The line is one read-only row holding the whole line, so the table does not
// call the tail part of NOTES's value, and is masked.
func TestEnvAfterQuote_zpbi_IsOneSensitiveReadOnlyRow(t *testing.T) {
	for _, c := range afterQuoteLines {
		t.Run(c.name, func(t *testing.T) {
			rows := (&EnvHandler{}).parseEnvFile(c.line)
			require.Len(t, rows, 1)
			assert.Equal(t, "NOTES", rows[0].Key)
			assert.True(t, rows[0].Comment, "read-only: edited in the raw view")
			assert.True(t, rows[0].Sensitive)
			assert.Equal(t, c.line[:len(c.line)-1], rows[0].Value)
		})
	}
}

// Not stricter than it must be: a closing quote followed by a comment, or by
// a tail that holds no secret, is not masked.
func TestEnvAfterQuote_zpbi_HarmlessTailStaysVisible(t *testing.T) {
	rows := (&EnvHandler{}).parseEnvFile("NOTES=\"a\" # c\nNOTES2=\"a\" TZ=UTC\n")
	require.Len(t, rows, 2)
	assert.False(t, rows[0].Comment)
	assert.Equal(t, "a", rows[0].Value)
	assert.False(t, rows[1].Sensitive)
}

func TestEnvAfterQuote_zpbi_UneditedSaveIsByteIdentical(t *testing.T) {
	content := "TZ=UTC\n" + afterQuoteLines[0].line + afterQuoteLines[1].line
	router, stackID, token, envPath := envQuoteFixture(t, content)
	w := putEntries(t, router, stackID, token, getEntriesAsJSON(t, router, stackID, token))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, content, readEnvQuoteFile(t, envPath))
}
