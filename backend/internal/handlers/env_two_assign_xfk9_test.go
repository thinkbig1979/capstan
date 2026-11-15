package handlers

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-xfk9 (REFUTED, pin only): a line holding two assignments was
// suspected to show the second one's secret under the first, non-sensitive
// key. Since agent-os-zpbi the quoted shape is one read-only row masked when
// either statement is secret, and the unquoted shape is ONE value to compose
// (an unquoted value runs to the end of the line), so the table agrees with
// compose on both. These tests pin that.

const xfk9Quoted = `A="x" SECRET=hunterxfk9quoted` + "\n"

func xfk9ComposeReads(t *testing.T, content string) map[string]string {
	t.Helper()
	vars, err := dotenv.UnmarshalWithLookup(content, func(string) (string, bool) { return "", false })
	require.NoError(t, err)
	return vars
}

func TestEnvTwoAssign_Xfk9_QuotedSecondAssignmentHiddenWhileLocked(t *testing.T) {
	// Positive side: compose really reads a second variable holding the secret.
	require.Equal(t, map[string]string{"A": "x", "SECRET": "hunterxfk9quoted"}, xfk9ComposeReads(t, xfk9Quoted))

	t.Run("stack", func(t *testing.T) {
		router, stackID, token, _ := envQuoteFixture(t, xfk9Quoted)
		require.Contains(t, getEnvBody(t, router, stackID, token), "hunterxfk9quoted")
		assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "hunterxfk9quoted")
	})
	t.Run("global", func(t *testing.T) {
		dataDir, router := globalEnvTestRouter(t)
		writeGlobalEnvFile(t, dataDir, xfk9Quoted)
		_, unlocked := getGlobalVars(t, router, "/unlocked")
		require.Contains(t, unlocked, "hunterxfk9quoted")
		_, locked := getGlobalVars(t, router, "/locked")
		assertBodyOmits(t, locked, "hunterxfk9quoted")
	})
}

// Unquoted, compose reads one variable whose value is the rest of the line,
// and the table's row is that same key and value.
func TestEnvTwoAssign_Xfk9_UnquotedIsOneValueAsComposeReadsIt(t *testing.T) {
	const content = "B=y OTHER=z\n"
	require.Equal(t, map[string]string{"B": "y OTHER=z"}, xfk9ComposeReads(t, content))

	router, stackID, token, _ := envQuoteFixture(t, content)
	entries := getEntriesAsJSON(t, router, stackID, token)
	require.Len(t, entries, 1)
	assert.Equal(t, "B", entries[0]["key"])
	assert.Equal(t, "y OTHER=z", entries[0]["value"])
}

// The unquoted shape with a secret-named second word: compose reads it as
// B's value, so it is B's sensitivity that decides; a sensitive B hides it.
func TestEnvTwoAssign_Xfk9_UnquotedSensitiveKeyHiddenWhileLocked(t *testing.T) {
	const content = "DB_PASSWORD=y SECRET=hunterxfk9unquoted\n"
	require.Equal(t, map[string]string{"DB_PASSWORD": "y SECRET=hunterxfk9unquoted"}, xfk9ComposeReads(t, content))

	router, stackID, token, _ := envQuoteFixture(t, content)
	require.Contains(t, getEnvBody(t, router, stackID, token), "hunterxfk9unquoted")
	assertBodyOmits(t, getEnvBody(t, router, stackID, ""), "hunterxfk9unquoted")
}
