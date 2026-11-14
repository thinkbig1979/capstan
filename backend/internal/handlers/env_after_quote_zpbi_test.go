package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// Review E3: the statement after a closing quote can open a quoted value
// compose reads across newlines. Its body lines are the secret's, so the
// locked views hide them as they do a multi-line value opened by the first
// statement (hideMultilineBodies), on the stack table and the global page.
var tailOpensMultiline = []struct{ name, content string }{
	{"body", "TZ=UTC\nNOTES=\"a\" DB_PASSWORD=\"S3cr3tXYZ\nXYZbody\"\nB=1\n"},
	{"body then var", "TZ=UTC\nNOTES=\"a\" DB_PASSWORD=\"S3cr3tXYZ\nXYZbody\" OTHER=1\nB=1\n"},
	{"colon opener", "TZ=UTC\nNOTES: 'a' API_TOKEN='S3cr3tXYZ\nXYZbody'\nB=1\n"},
}

func TestEnvAfterQuote_zpbi_TailOpensMultilineHiddenWhenLocked(t *testing.T) {
	for _, c := range tailOpensMultiline {
		t.Run(c.name, func(t *testing.T) {
			vars, err := dotenv.UnmarshalWithLookup(c.content, nil)
			require.NoError(t, err)
			require.Contains(t, vars["DB_PASSWORD"]+vars["API_TOKEN"], "XYZbody", "the oracle: compose reads the body as the secret")

			router, stackID, token, _ := envQuoteFixture(t, c.content)
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
			require.Contains(t, get(true), "XYZbody", "positive side")
			locked := get(false)
			assertBodyOmits(t, locked, "XYZbody")
			assertBodyOmits(t, locked, "S3cr3tXYZ")
			assert.Contains(t, locked, `"key":"B","value":"1"`, "rows after the body stay visible")
		})
	}
}

// The global page shows only rows with a key, so a body line reaches it only
// when the closing line carries a variable (`XYZbody" OTHER=1` is keyed
// `XYZbody" OTHER`); the other shapes never show the body there, unlocked
// either.
func TestEnvAfterQuote_zpbi_TailOpensMultilineHiddenOnLockedGlobalPage(t *testing.T) {
	for _, c := range tailOpensMultiline[1:2] {
		t.Run(c.name, func(t *testing.T) {
			dataDir, router := globalEnvTestRouter(t)
			writeGlobalEnvFile(t, dataDir, c.content)
			_, unlocked := getGlobalVars(t, router, "/unlocked")
			_, locked := getGlobalVars(t, router, "/locked")
			require.Contains(t, unlocked, "XYZbody", "positive side")
			assertBodyOmits(t, locked, "XYZbody")
			assertBodyOmits(t, locked, "S3cr3tXYZ")
		})
	}
}

// Review E2: the tail is checked statement by statement in one pass. A
// recursive check rescanned the rest of the line per quoted segment: 42 s for
// this 80 KB line, against milliseconds on the base. The bound leaves room
// for a loaded machine and the race detector, and is far below the
// super-linear time.
func TestEnvAfterQuote_zpbi_LongTailParsesInLinearTime(t *testing.T) {
	tail := strings.Repeat(" A=\"\"", 16400) + "\n"
	// The second line is sensitive, so the locked view also walks its tail
	// for a quote left open (opensMultilineTail).
	for _, line := range []string{"A=\"\"" + tail, "DB_PASSWORD=\"\"" + tail} {
		require.Greater(t, len(line), 80*1024)
		start := time.Now()
		resp := EnvResponse{Raw: line, Entries: (&EnvHandler{}).parseEnvFile(line)}
		redactEnvResponse(&resp)
		elapsed := time.Since(start)
		require.Len(t, resp.Entries, 1)
		assert.Less(t, elapsed, 2*time.Second, "an 80 KB line took %s to parse and redact", elapsed)
	}
}
