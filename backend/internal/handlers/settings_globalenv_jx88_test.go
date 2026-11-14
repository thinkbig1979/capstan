package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/template"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-jx88: UpdateGlobalEnv rebuilt global.env from the variable rows
// it received, and the page has no comment or blank rows, so every save
// dropped the file's comments and blank lines. The save is now merged into
// the file (mergeGlobalEnv, agent-os-rd1l), which keeps them.

const jx88Content = "# Shared settings\n\nTZ=Europe/Amsterdam\n\n# mail\nSMTP_HOST=mail.example.com\n"

func TestGlobalEnv_Jx88_CommentsAndBlankLinesSurviveSave(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, jx88Content)

	vars, _ := getGlobalVars(t, router, "/unlocked")
	w := putGlobalVars(t, router, vars)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, jx88Content, readGlobalEnvTestFile(t, p))
}

func TestGlobalEnv_Jx88_CommentsSurviveAnEdit(t *testing.T) {
	dataDir, router := globalEnvTestRouter(t)
	p := writeGlobalEnvFile(t, dataDir, jx88Content)

	w := putGlobalVars(t, router, []map[string]string{
		{"key": "TZ", "value": "UTC"},
		{"key": "SMTP_HOST", "value": "mail.example.com"},
	})
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	assert.Equal(t, "# Shared settings\n\nTZ=UTC\n\n# mail\nSMTP_HOST=mail.example.com\n", readGlobalEnvTestFile(t, p))
}

// The frontend half (useExtractToEnv.envLineFor). The selection sits in the
// compose YAML, where compose substitutes $VAR, ${VAR} and $$; the extracted
// .env line must give the variable the value compose read at that spot.
// Observed with compose's own code: template.Substitute for the YAML side,
// dotenv for the .env side, over the table the frontend test also asserts.
const extractCasesPath = "../../../frontend/src/components/stack/compose-editor/__tests__/extractToEnvCases.json"

func TestEnvExtract_Jx88_EnvLineReadsAsComposeReadTheYAML(t *testing.T) {
	raw, err := os.ReadFile(extractCasesPath)
	require.NoError(t, err)
	var table struct {
		Cases []struct{ Selected, Line string }
	}
	require.NoError(t, json.Unmarshal(raw, &table))
	require.NotEmpty(t, table.Cases)
	env := map[string]string{"DB_HOST": "db.internal", "HOME": "/home/u"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	for _, c := range table.Cases {
		fromYAML, err := template.Substitute(c.Selected, lookup)
		require.NoError(t, err, c.Selected)
		got, err := dotenv.UnmarshalWithLookup(c.Line+"\n", lookup)
		require.NoError(t, err, c.Line)
		assert.Equal(t, fromYAML, got["MY_VAR"], "selected %q, line %q", c.Selected, c.Line)
		assert.Len(t, got, 1, "line %q", c.Line)
	}

	// The other side: the single-quoting this replaced reads ${DB_HOST}
	// literally, which compose did not.
	got, err := dotenv.UnmarshalWithLookup("MY_VAR='${DB_HOST}:5432'\n", lookup)
	require.NoError(t, err)
	assert.NotEqual(t, "db.internal:5432", got["MY_VAR"])
}
