package handlers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-9fsu: the body lines of a multi-line secret are rows of their own to
// the line parser, and a base64 line ending in "==" reads as KEY=value. Only
// the locked payload flagged them, so an editor that kept the unlocked rows
// across a lock showed the key. Every payload now flags such a row `sensitive`
// and `bodyLine`; the unlocked one keeps its key and value, because a table save
// writes them back, and the locked one blanks them as before. The flags are not
// part of what a save writes (sameEnvRow).

const bodyLineFile = "PORT=8080\n" +
	"#OLD_KEY=\"-----BEGIN RSA PRIVATE KEY-----\n" +
	"MIIEbodylineAAAA\n" +
	"QkZHSlastLineXyZ==\n" +
	"-----END RSA PRIVATE KEY-----\"\n" +
	"TZ=UTC\n"

// envRowsByLine decodes a GET /env body to its entries as raw JSON maps, so an
// assertion reads the wire field and not a Go struct field.
func envRowsByLine(t *testing.T, body string) map[int]map[string]any {
	t.Helper()
	var resp struct {
		Entries []map[string]any `json:"entries"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	rows := map[int]map[string]any{}
	for _, e := range resp.Entries {
		rows[int(e["line"].(float64))] = e
	}
	return rows
}

func TestEnvBodyLine_9fsu_UnlockedPayloadFlagsEveryBodyRowAndKeepsItsText(t *testing.T) {
	router, stackID, token, _ := envQuoteFixture(t, bodyLineFile)
	rows := envRowsByLine(t, getEnvBody(t, router, stackID, token))

	// The row the bead is about: base64 padding parsed as KEY=.
	pad := rows[4]
	require.NotNil(t, pad, "line 4 is a row of its own")
	assert.Equal(t, "QkZHSlastLineXyZ", pad["key"], "unlocked keeps the key a save writes back")
	assert.Equal(t, "=", pad["value"])
	assert.Equal(t, true, pad["sensitive"])
	assert.Equal(t, true, pad["bodyLine"])
	for _, line := range []int{3, 5} {
		assert.Equal(t, true, rows[line]["sensitive"], "line %d", line)
		assert.Equal(t, true, rows[line]["bodyLine"], "line %d", line)
	}
	// Positive control from the other side: rows outside the secret carry neither flag.
	for _, line := range []int{1, 6} {
		assert.Equal(t, false, rows[line]["sensitive"], "line %d", line)
		assert.NotContains(t, rows[line], "bodyLine", "line %d", line)
	}
	assert.NotContains(t, rows[2], "bodyLine", "the opener is not a body row")
}

func TestEnvBodyLine_9fsu_LockedPayloadFlagsTheSameRowsAndBlanksThem(t *testing.T) {
	router, stackID, token, _ := envQuoteFixture(t, bodyLineFile)
	locked := envRowsByLine(t, getEnvBody(t, router, stackID, ""))
	unlocked := envRowsByLine(t, getEnvBody(t, router, stackID, token))

	for line := 1; line <= 6; line++ {
		assert.Equal(t, unlocked[line]["bodyLine"], locked[line]["bodyLine"], "line %d: both payloads flag the same rows", line)
	}
	pad := locked[4]
	assert.Equal(t, "", pad["key"])
	assert.Equal(t, "", pad["value"])
	assert.Equal(t, true, pad["sensitive"])
	assert.Equal(t, true, pad["bodyLine"])
}
