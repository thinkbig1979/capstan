package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-1nqr round 2 (found by the widened property corpus, pre-existing on
// 193b08c): a quoted value whose closing line carries a second statement is
// not folded into one row (multilineValueEnd needs a clean close), so its
// first line stayed an editable row. When its body looked secret, the body
// rows were hidden but the opener's own first line was shown. The opener of a
// hidden body is now sensitive in the locked views; unlocked is unchanged.

const openerLeakFile = "TZ=\"firstlinezq\npostgres://u:pw188zq@db/app\" X=1\n"

func TestEnvLocked_1nqr_OpenerOfAHiddenBodyIsHidden(t *testing.T) {
	for _, c := range []struct{ name, content string }{
		{"closing line carries a statement", openerLeakFile},
		{"control: clean close, one folded row", "TZ=\"firstlinezq\npostgres://u:pw188zq@db/app\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			router, stackID, token, _ := envQuoteFixture(t, c.content)
			// Unlocked: the value is shown as before.
			require.Contains(t, getEnvBody(t, router, stackID, token), "firstlinezq")
			assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "firstlinezq")
			assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "pw188zq")

			dataDir, grouter := globalEnvTestRouter(t)
			writeGlobalEnvFile(t, dataDir, c.content)
			_, unlocked := getGlobalVars(t, grouter, "/unlocked")
			require.Contains(t, unlocked, "firstlinezq")
			_, locked := getGlobalVars(t, grouter, "/locked")
			assertBodyOmits(t, locked, "firstlinezq")
			assertBodyOmits(t, locked, "pw188zq")
		})
	}
}

// Unlocked, the rows are exactly what they were: the opener keeps its value
// and its (non-)sensitive flag.
func TestEnvLocked_1nqr_OpenerUnlockedUnchanged(t *testing.T) {
	router, stackID, token, _ := envQuoteFixture(t, openerLeakFile)
	e := entryByKey(t, getEntriesAsJSON(t, router, stackID, token), "TZ")
	assert.Equal(t, `"firstlinezq`, e["value"])
	assert.Equal(t, false, e["sensitive"])
}

// Round 3 (review R2-1, already on base): the body was judged line by line,
// so a URL credential split across the lines of an unfolded span looked like
// nothing on each line and was shown, its second half even as a row key.
// The span is now also judged as compose reads it.
func TestEnvLocked_1nqr_CredentialSplitAcrossLinesIsHidden(t *testing.T) {
	const content = "TZ=\"postgres://u:\npw77zq@db/app\" X=1\n"
	router, stackID, token, _ := envQuoteFixture(t, content)
	require.Contains(t, getEnvBody(t, router, stackID, token), "pw77zq")
	assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "pw77zq")
	assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "postgres://u:")

	dataDir, grouter := globalEnvTestRouter(t)
	writeGlobalEnvFile(t, dataDir, content)
	_, unlocked := getGlobalVars(t, grouter, "/unlocked")
	require.Contains(t, unlocked, "pw77zq")
	_, locked := getGlobalVars(t, grouter, "/locked")
	assertBodyOmits(t, locked, "pw77zq")
	assertBodyOmits(t, locked, "postgres://u:")

	// Unlocked rows are as before.
	rows := getEntriesAsJSON(t, router, stackID, token)
	assert.Equal(t, `"postgres://u:`, entryByKey(t, rows, "TZ")["value"])
}
