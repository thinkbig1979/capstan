package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-m5uj: compose reads `export EXP_TOKEN=x` as EXP_TOKEN (compose-go
// v2 dotenv/parser.go locateKeyName trims the prefix). The table keyed the
// row "export EXP_TOKEN", so it showed a key compose does not read, and an
// edited row lost its prefix. The row now carries the compose key plus an
// export flag, and the writer puts the prefix back. These tests go through
// the JSON a client holds, so they compile against a tree without the flag.

func TestEnvExport_m5uj_TableKeysTheComposeKey(t *testing.T) {
	router, stackID, token, _ := envQuoteFixture(t, "export EXP_TOKEN=m5ujvalue\nPLAIN=1\n")
	entries := getEntriesAsJSON(t, router, stackID, token)
	require.Len(t, entries, 2)
	assert.Equal(t, "EXP_TOKEN", entries[0]["key"])
	assert.Equal(t, "m5ujvalue", entries[0]["value"])
	assert.Equal(t, true, entries[0]["export"])
	assert.Equal(t, true, entries[0]["sensitive"], "decided on the compose key")
	_, has := entries[1]["export"]
	assert.False(t, has, "export is omitted on a row without the prefix")
}

func TestEnvExport_m5uj_RenamedAndEditedRowsKeepThePrefix(t *testing.T) {
	router, stackID, token, envPath := envQuoteFixture(t, "export EXP_TOKEN=m5ujvalue\nexport B=1\n")
	entries := getEntriesAsJSON(t, router, stackID, token)
	require.Len(t, entries, 2)
	entries[0]["key"] = "NEW_TOKEN"
	entries[1]["value"] = "2"
	w := putEntries(t, router, stackID, token, entries)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "export NEW_TOKEN=m5ujvalue\nexport B=2\n", readEnvQuoteFile(t, envPath))
}

func TestEnvExport_m5uj_GlobalPageKeysAndKeepsThePrefix(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, "export EXP_TOKEN=m5ujvalue\nTZ=UTC\n")
	vars, _ := getGlobalVars(t, router, "/unlocked")
	require.Len(t, vars, 2)
	assert.Equal(t, "EXP_TOKEN", vars[0]["key"])

	vars[0]["value"] = "m5ujchanged"
	w := putGlobalVars(t, router, vars)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "export EXP_TOKEN=m5ujchanged\nTZ=UTC\n", readGlobalEnvTestFile(t, p))
}
