package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-4qiw: PUT compose-env built the env file from its entries with
// serializeEnvFile alone, so an unchanged indented or CRLF line was
// normalised. It now writes over the file's text as the table save does
// (serializeEnvFileOver, agent-os-omzj).

const keepLines4qiw = "  A=1\r\nB=2\r\n"

func TestComposeEnvKeepLines4qiw_UnchangedLinesByteForByte(t *testing.T) {
	s, envPath := composeEnvFixture(t, keepLines4qiw)
	w := s.put(t, composeEnvEntriesBody(t, []map[string]any{
		{"key": "A", "value": "1", "line": 1},
		{"key": "B", "value": "2", "line": 2},
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, keepLines4qiw, readEnvQuoteFile(t, envPath))
}

func TestComposeEnvKeepLines4qiw_EditedRowTakesTheFileLineEnding(t *testing.T) {
	s, envPath := composeEnvFixture(t, keepLines4qiw)
	w := s.put(t, composeEnvEntriesBody(t, []map[string]any{
		{"key": "A", "value": "1", "line": 1},
		{"key": "B", "value": "3", "line": 2},
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "  A=1\r\nB=3\r\n", readEnvQuoteFile(t, envPath))
}

// The fresh-row quoting applies here too (agent-os-uxfl): this route writes
// the same file.
func TestComposeEnvKeepLines4qiw_NewValueReadsBackAsTyped(t *testing.T) {
	s, envPath := composeEnvFixture(t, "A=1\n")
	w := s.put(t, composeEnvEntriesBody(t, []map[string]any{
		{"key": "A", "value": "1", "line": 1},
		{"key": "N", "value": "a # b"},
	}))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "a # b", composeReadsFile(t, readEnvQuoteFile(t, envPath))["N"])
}
