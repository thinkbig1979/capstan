package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-vq3i: a URL credential in an inline comment of a non-sensitive
// row showed in the locked stack view; comment rows were starred, inline
// comments were not.
func TestRedactEnvResponse_vq3i_InlineCommentURLCredentialStarred(t *testing.T) {
	raw := "TZ=UTC # https://u:SEKRITvq3i@h\nLANG=C # mirror sftp:u:SEKRITvq3i@nas:/p\nMODE=x # a plain note\n"
	resp := EnvResponse{Raw: raw, Entries: (&EnvHandler{}).parseEnvFile(raw)}
	redactEnvResponse(&resp)
	require.Len(t, resp.Entries, 3)
	assert.Equal(t, "UTC", resp.Entries[0].Value, "the value is not secret")
	assert.Equal(t, " # https://***@h", resp.Entries[0].InlineComment)
	assert.NotContains(t, resp.Entries[1].InlineComment, "SEKRIT")
	assert.Equal(t, " # a plain note", resp.Entries[2].InlineComment)
}

// The global page already blanks such a row's value (its value includes the
// comment, globalEnvVarOf); it stays as it is.
func TestRedactGlobalEnvVars_vq3i_InlineCommentRowUnchanged(t *testing.T) {
	raw := "TZ=UTC # https://u:SEKRITvq3i@h\n"
	vars, _ := globalEnvVars((&EnvHandler{}).parseEnvFile(raw))
	redactGlobalEnvVars(vars, raw)
	assert.Equal(t, []map[string]string{{"key": "TZ", "value": ""}}, vars)
}
