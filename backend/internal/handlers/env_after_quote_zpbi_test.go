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
// an 80 KB line, against milliseconds on the base.
//
// The check is how the time grows, not a clock: each shape is parsed and
// redacted at n and 8n, the fastest of 3 interleaved runs each, and the time
// may grow less than its shape's bound (growthBounds). Linear code grows
// about 8x, a quadratic one about 64x. A loaded machine or the race detector slows both
// sizes, so the ratio holds where a 2 s bound did not (agent-os-yzmh: a loaded
// -race run took 18.9 s on the sensitive chain). An allocation bound, as in
// services' n30e test, would not do here: the no-watermark mutant (pass 1
// re-blanking every body from every opener) writes in place and allocates
// exactly what the fix does.
func TestEnvAfterQuote_zpbi_LongTailParsesInLinearTime(t *testing.T) {
	shapes := []struct {
		name  string
		build func(n int) string
	}{
		{"plain tail", func(n int) string { return "A=\"\"" + strings.Repeat(" A=\"\"", n) + "\n" }},
		// Sensitive, so the locked view also walks its tail for a quote left
		// open (opensMultilineTail).
		{"sensitive tail", func(n int) string { return "DB_PASSWORD=\"\"" + strings.Repeat(" A=\"\"", n) + "\n" }},
		// A file, not a line: a chain of values each re-opened by the
		// previous one's closing line, which the locked view follows to its
		// end (review R2, P2). Scanned again from every row it was quadratic:
		// 34 s at 4000 lines.
		{"chain", func(n int) string { return "NOTES=\"a\" X=\"x\n" + strings.Repeat("b\" Y=\"c\n", n) + "end\"\n" }},
		// agent-os-olcn: every sensitive opener is followed, also one inside
		// another's body, so in a chain whose every row is a sensitive opener
		// the body is walked from every row: each walk must be a table lookup
		// and each row blanked once. Per row, a rescan took 61 s here and
		// blanking the body again 5 s.
		{"sensitive chain", func(n int) string {
			return "NOTES=\"a\" X_TOKEN=\"x\n" + strings.Repeat("b\" Y_TOKEN=\"c\n", n) + "end\"\n"
		}},
	}
	sizes := map[string]int{"plain tail": 16400, "sensitive tail": 16400, "chain": 10300, "sensitive chain": 40000}
	require.Len(t, growthBounds, len(shapes))
	run := func(content string) time.Duration {
		start := time.Now()
		resp := EnvResponse{Raw: content, Entries: (&EnvHandler{}).parseEnvFile(content)}
		redactEnvResponse(&resp)
		vars := parseGlobalEnvText(content)
		redactGlobalEnvVars(vars, content)
		elapsed := time.Since(start)
		require.NotEmpty(t, resp.Entries)
		return elapsed
	}
	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			big := sizes[sh.name]
			small, large := sh.build(big/8), sh.build(big)
			require.Greater(t, len(large), 80*1024)
			fastest := func(d, best time.Duration) time.Duration {
				if best == 0 || d < best {
					return d
				}
				return best
			}
			var tSmall, tLarge time.Duration
			for range 3 {
				tSmall = fastest(run(small), tSmall)
				tLarge = fastest(run(large), tLarge)
			}
			growth := float64(tLarge) / float64(max(tSmall, time.Microsecond))
			t.Logf("%d bytes in %s, %d bytes in %s: %.1fx", len(small), tSmall, len(large), tLarge, growth)
			bound, ok := growthBounds[sh.name]
			require.True(t, ok, "no growth bound for %s", sh.name)
			assert.Less(t, growth, bound, "8x the input took %.1fx the time (%s against %s): not linear", growth, tLarge, tSmall)
		})
	}
}

// growthBounds is the growth TestEnvAfterQuote_zpbi_LongTailParsesInLinearTime
// allows each shape for 8x the input, measured 2026-11-15.
//
// The sensitive chain is the shape the no-watermark mutant fails on (51x to
// 72x plain, 39x to 42x under -race), and its loaded maximum was 13.3x, so it
// keeps 20. The two tail shapes' small input runs in microseconds to
// milliseconds, inside one scheduler slice, while the large one loses CPU
// share under load: the sensitive tail reached 23.5x with 12 CPU hogs on 8
// cores (review of agent-os-yzmh). Those three get 32, still half the 64x a
// quadratic pass gives. Taking the fastest of more runs did not help (22.0x
// with 9).
var growthBounds = map[string]float64{
	"plain tail":      32,
	"sensitive tail":  32,
	"chain":           32,
	"sensitive chain": 20,
}

// Review round 3 (R1, P3, R2, P1, P2): every statement compose reads on a
// line is checked with its own value, and a body that a later statement
// opens, or that a closing line's tail opens again, is followed. Each content
// holds the secret SEKRIT where compose reads it as (part of) a variable's
// value; the locked stack table and global page must not show it.
var round3Leaks = []struct{ name, content string }{
	{"R1 unquoted opaque url in tail", "TZ=UTC\nNOTES=\"a\" BACKUP=sftp:user:SEKRIT@host:/path\nB=1\n"},
	{"R1 quoted opaque url in tail", "TZ=UTC\nNOTES=\"a\" BACKUP=\"sftp:user:SEKRIT@host:/path\"\nB=1\n"},
	{"R1 url in third statement", "TZ=UTC\nNOTES=\"a\" B=\"c\" BACKUP=sftp:user:SEKRIT@host:/path\nB=1\n"},
	{"R1 colon form", "TZ=UTC\nNOTES: \"a\" BACKUP: sftp:user:SEKRIT@host:/path\nB=1\n"},
	{"P3 url in first statement before a tail", "TZ=UTC\nBACKUP=\"sftp:user:SEKRIT@host:/path\" NOTES=x\nB=1\n"},
	{"R2 closing line tail reopens", "TZ=UTC\nNOTES=\"a\" DB_PASSWORD=\"x\nb\" API_TOKEN=\"c\nSEKRIT\"\nB=1\n"},
	{"P1 first statement body reopens", "TZ=UTC\nDB_PASSWORD=\"x\nb\" API_TOKEN=\"c\nSEKRIT\"\nB=1\n"},
	{"P2 pem body opened by a plain tail key", "TZ=UTC\nNOTES=\"a\" CERT=\"\n-----BEGIN PRIVATE KEY-----\nSEKRITbase64==\n-----END PRIVATE KEY-----\"\nB=1\n"},
	{"P2 json key body opened by a plain tail key", "TZ=UTC\nNOTES='a' GCP_CREDENTIALS='{\n \"private_key\": \"SEKRIT\"\n}'\nB=1\n"},
}

func TestEnvAfterQuote_zpbi_Round3SecretsHiddenWhenLocked(t *testing.T) {
	for _, c := range round3Leaks {
		t.Run(c.name, func(t *testing.T) {
			_, err := dotenv.UnmarshalWithLookup(c.content, nil)
			require.NoError(t, err, "compose reads the fixture")

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
			require.Contains(t, get(true), "SEKRIT", "positive side")
			locked := get(false)
			assertBodyOmits(t, locked, "SEKRIT")
			assert.Contains(t, locked, `"key":"B","value":"1"`, "rows after the secret stay visible")

			dataDir, grouter := globalEnvTestRouter(t)
			writeGlobalEnvFile(t, dataDir, c.content)
			_, glocked := getGlobalVars(t, grouter, "/locked")
			assertBodyOmits(t, glocked, "SEKRIT")
		})
	}
}
