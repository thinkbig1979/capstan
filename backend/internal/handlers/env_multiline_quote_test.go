package handlers

import (
	"net/http"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-fw2q: compose reads a quoted value across newlines to its closing
// quote, but parseEnvFile classified line by line, so the table showed each
// body line as a row of its own: "abc: def" as a read-only variable "abc", a
// body line holding "=" as an editable variable. The table now shows the
// whole value as ONE read-only row (edit it in the raw view), and an unedited
// table save writes it back byte for byte. Edwin's ruling, 2026-11-14.

const fw2qCert = "CERT_KEY=\"-----BEGIN\nabc: def\nTOP_SECRET_PASSWORD: fw2qinner\nx=fw2qbody\n-----END\""

const fw2qContent = "A=1\n" + fw2qCert + "\nB=2\n"

func TestEnvMultilineQuote_ComposeReadsOneVariable(t *testing.T) {
	// Observed, not assumed: compose's own parser reads A, CERT_KEY and B.
	vars, err := dotenv.UnmarshalWithLookup(fw2qContent, func(string) (string, bool) { return "", false })
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"A": "1", "B": "2",
		"CERT_KEY": "-----BEGIN\nabc: def\nTOP_SECRET_PASSWORD: fw2qinner\nx=fw2qbody\n-----END",
	}, vars)
}

func TestEnvMultilineQuote_OneReadOnlyRow(t *testing.T) {
	got := (&EnvHandler{}).parseEnvFile(fw2qContent)
	require.Len(t, got, 3, "A, CERT_KEY and B: %+v", got)
	assert.Equal(t, EnvEntry{Key: "A", Value: "1", Line: 1}, got[0])
	assert.Equal(t, "CERT_KEY", got[1].Key)
	assert.Equal(t, fw2qCert, got[1].Value, "the row holds every line of the value, verbatim")
	assert.True(t, got[1].Comment, "read-only in the table: edited in the raw view")
	assert.True(t, got[1].Sensitive, "CERT_KEY's value is masked when locked")
	assert.Equal(t, 2, got[1].Line)
	assert.Equal(t, EnvEntry{Key: "B", Value: "2", Line: 7}, got[2], "the row after keeps its own line number")
}

func TestEnvMultilineQuote_TableSaveByteIdentical(t *testing.T) {
	router, stackID, token, envPath := envQuoteFixture(t, fw2qContent)
	entries := getEntriesAsJSON(t, router, stackID, token)
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, fw2qContent, readEnvQuoteFile(t, envPath))
}

// Pin (green on base via agent-os-yt2f's hideMultilineBodies): a locked GET
// shows no line of a sensitive multi-line value. New with the one-row shape:
// the rows after it are not blanked.
func TestEnvMultilineQuote_LockedShowsNoLineAndKeepsLaterRows(t *testing.T) {
	content := "API_TOKEN=\"fw2qline1\nfw2qline2\nfw2qline3\"\nTZ=Europe/Amsterdam\nB=2\n"
	router, stackID, token, _ := envQuoteFixture(t, content)

	unlocked := getEnvBody(t, router, stackID, token)
	for _, s := range []string{"fw2qline1", "fw2qline2", "fw2qline3"} {
		require.Contains(t, unlocked, s, "positive side")
	}
	locked := getEnvBody(t, router, stackID, "")
	for _, s := range []string{"fw2qline1", "fw2qline2", "fw2qline3"} {
		assertBodyOmits(t, locked, s)
	}
	assert.Contains(t, locked, "Europe/Amsterdam", "a row after the value is not blanked")
	assert.Contains(t, locked, `"key":"B"`)
}

// The bead's own fixture: a body line shaped like a sensitive variable is
// part of CERT_KEY's value, and stays hidden when locked.
func TestEnvMultilineQuote_LockedHidesBodyLines(t *testing.T) {
	router, stackID, token, _ := envQuoteFixture(t, fw2qContent)
	require.Contains(t, getEnvBody(t, router, stackID, token), "fw2qinner", "positive side")
	locked := getEnvBody(t, router, stackID, "")
	for _, s := range []string{"fw2qinner", "fw2qbody", "abc: def"} {
		assertBodyOmits(t, locked, s)
	}
}

// compose rejects a file whose quote never closes, so where the value ends is
// unknown: the rows stay one per line, and the locked view blanks every row
// after a sensitive opener (hideMultilineBodies, fail closed). Unchanged.
func TestEnvMultilineQuote_UnterminatedStaysPerLine(t *testing.T) {
	content := "A=1\nTOKEN=\"fw2qopen\nfw2qafter=1\n"
	got := (&EnvHandler{}).parseEnvFile(content)
	require.Len(t, got, 3)
	assert.False(t, got[1].Comment)

	router, stackID, _, _ := envQuoteFixture(t, content)
	assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "fw2qafter")
}

// A closing quote followed by anything but a comment is not folded into one
// row. compose DOES read `K="a\n"EVIL=1` (as K plus EVIL); the table keeps
// such lines one row each, a deliberate simplification (review C5).
func TestEnvMultilineQuote_TextAfterClosingQuoteNotFolded(t *testing.T) {
	got := (&EnvHandler{}).parseEnvFile("K=\"a\n\"EVIL=1\n")
	assert.Len(t, got, 2)
	got = (&EnvHandler{}).parseEnvFile("K=\"a\nb\" # note\n")
	require.Len(t, got, 1)
	assert.Equal(t, "K=\"a\nb\" # note", got[0].Value)
}

// A client can send a multi-line read-only row back only as the parser
// produced it; a "\r" stays refused, and a newline that would add a line is
// refused as before (TestValidateEnvEntries_CommentRows).
func TestEnvMultilineQuote_ValidateCommentRows(t *testing.T) {
	assert.NoError(t, validateEnvEntries([]EnvEntry{{Comment: true, Key: "K", Value: "K=\"a\nb\""}}))
	for _, e := range []EnvEntry{
		{Comment: true, Key: "K", Value: "K=\"a\r\nb\""},
		{Comment: true, Key: "K", Value: "K=\"a\"\nEVIL=1"},
		{Comment: true, Key: "OTHER", Value: "K=\"a\nb\""},
		{Comment: true, Key: "", Value: "K=\"a\nb\""},
	} {
		assert.Error(t, validateEnvEntries([]EnvEntry{e}), "%q", e.Value)
	}
}

// No regression against the per-line rows: under a key that looks harmless, a
// body line shaped like a secret was masked as its own row. Folded into the
// value, it masks the whole value.
func TestEnvMultilineQuote_SecretShapedBodyLineHidesValue(t *testing.T) {
	for _, body := range []string{"DB_PASSWORD: fw2qbodysecret", "#OLD_TOKEN=fw2qbodysecret", "https://u:fw2qbodysecret@h/x"} {
		content := "MOTD=\"hi\n" + body + "\nbye\"\nTZ=UTC\n"
		router, stackID, token, _ := envQuoteFixture(t, content)
		require.Contains(t, getEnvBody(t, router, stackID, token), "fw2qbodysecret", "positive side")
		locked := getEnvBody(t, router, stackID, "")
		assertBodyOmits(t, locked, "fw2qbodysecret")
		assert.Contains(t, locked, "UTC")
	}
}

// The other side (agent-os-yt2f pins it too): a multi-line value with nothing
// secret-shaped stays visible to a locked session.
func TestEnvMultilineQuote_NonSecretValueStaysVisible(t *testing.T) {
	router, stackID, _, _ := envQuoteFixture(t, "MOTD=\"hello\nfw2qvisibleworld\"\n")
	assert.Contains(t, getEnvBody(t, router, stackID, ""), "fw2qvisibleworld")
}

// Review S1: a multi-line private key under a key name that is not
// secret-shaped (TLS_CERT, GCP_CREDENTIALS) showed whole in the locked views.
// The PEM marker and a JSON "private_key" field mark the value sensitive.
func TestEnvMultilineQuote_PrivateKeyBodyHiddenBothViews(t *testing.T) {
	for name, value := range map[string]string{
		"pem":  "TLS_CERT=\"-----BEGIN PRIVATE KEY-----\nS1pemsecretbody\n-----END PRIVATE KEY-----\"\n",
		"json": "GCP_CREDENTIALS='{\"type\": \"service_account\",\n\"private_key\": \"S1jsonsecretbody\"\n}'\n",
	} {
		t.Run(name, func(t *testing.T) {
			secret := map[string]string{"pem": "S1pemsecretbody", "json": "S1jsonsecretbody"}[name]
			content := value + "TZ=UTC\n"

			router, stackID, token, _ := envQuoteFixture(t, content)
			require.Contains(t, getEnvBody(t, router, stackID, token), secret, "positive side, stack view")
			locked := getEnvBody(t, router, stackID, "")
			assertBodyOmits(t, locked, secret)
			assert.Contains(t, locked, "UTC")

			dataDir, grouter := globalEnvTestRouter(t)
			writeGlobalEnvFile(t, dataDir, content)
			_, gUnlocked := getGlobalVars(t, grouter, "/unlocked")
			require.Contains(t, gUnlocked, secret, "positive side, global view")
			_, gLocked := getGlobalVars(t, grouter, "/locked")
			assertBodyOmits(t, gLocked, secret)
		})
	}
}
