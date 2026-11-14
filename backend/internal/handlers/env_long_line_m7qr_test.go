package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-m7qr: parseEnvFile read the file with a bufio.Scanner, whose
// default token limit is 64 KiB, and never read the scanner's error. A longer
// line ended the parse silently: GET showed the table cut off there, and a
// table save of those rows wrote a file missing every later variable.

func m7qrFixture() string {
	return "Z=0\nA=" + strings.Repeat("x", 70*1024) + "\nB=1\n# after\nC=2\n"
}

func TestEnvTable_M7qr_LongLineKeepsLaterRows(t *testing.T) {
	content := m7qrFixture()
	router, stackID, token, _ := envQuoteFixture(t, content)

	entries := getEntriesAsJSON(t, router, stackID, token)
	keys := []string{}
	for _, e := range entries {
		if k, _ := e["key"].(string); k != "" {
			keys = append(keys, k)
		}
	}
	assert.Equal(t, []string{"Z", "A", "B", "C"}, keys, "every variable after the long line is a row")
	require.Len(t, entries, 5)
	assert.Len(t, entries[1]["value"], 70*1024, "the long value is whole")
}

func TestEnvTable_M7qr_TableSaveKeepsEveryLine(t *testing.T) {
	content := m7qrFixture()
	router, stackID, token, envPath := envQuoteFixture(t, content)

	entries := getEntriesAsJSON(t, router, stackID, token)
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	got := readEnvQuoteFile(t, envPath)
	assert.Contains(t, got, "\nB=1\n# after\nC=2\n", "no line after the long one is dropped")
	// Not assert.Equal: testify's diff of a 70 KiB line hits the same limit.
	assert.True(t, got == content, "an unedited table save is byte-identical (got %d bytes, want %d)", len(got), len(content))
}

// The line splitting the scanner did: one trailing \r per line is dropped (a
// CRLF file reads as LF), and a final newline adds no phantom row.
func TestEnvTable_M7qr_LineSplittingMatchesScanLines(t *testing.T) {
	h := &EnvHandler{}
	assert.Len(t, h.parseEnvFile(""), 0)
	assert.Len(t, h.parseEnvFile("A=1\n"), 1)
	assert.Len(t, h.parseEnvFile("A=1"), 1)
	assert.Len(t, h.parseEnvFile("A=1\n\n"), 2)
	got := h.parseEnvFile("A=1\r\nB=2\r\n")
	require.Len(t, got, 2)
	assert.Equal(t, "1", got[0].Value)
	assert.Equal(t, 2, got[1].Line)
}
