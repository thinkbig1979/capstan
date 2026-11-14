package handlers

import (
	"net/http"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
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

// The frontend half (useExtractToEnv.envLineFor) single-quotes a value that
// unquoted would change meaning. Observed here with compose's own parser:
// each line it writes reads back as exactly the selected text, and the same
// value unquoted does not.
func TestEnvExtract_Jx88_ComposeReadsSingleQuotedLiterally(t *testing.T) {
	for _, value := range []string{"two words", "pa$word", "abc #def", "x#y", `say "hi"`, `C:\dir`, " lead"} {
		got, err := dotenv.UnmarshalWithLookup("MY_VAR='"+value+"'\n", func(string) (string, bool) { return "", false })
		require.NoError(t, err, value)
		assert.Equal(t, value, got["MY_VAR"], "single-quoted %q", value)
	}
	unquoted, err := dotenv.UnmarshalWithLookup("MY_VAR=abc #def\n", func(string) (string, bool) { return "", false })
	require.NoError(t, err)
	assert.NotEqual(t, "abc #def", unquoted["MY_VAR"], "positive side: unquoted, compose cuts the comment")
}
