package services

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-czik: the compose-output secret list read only each line's first
// statement, so a sensitive variable after a closing quote was in it only
// when compose-go parsed the file, and then only as compose's unescaped
// value, never as the text written in the file.

func stackSecretsCzik(t *testing.T, env string) []string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600))
	return StackSecretValues(models.Stack{Directory: dir, ProjectName: "p"}, "")
}

func TestRedactComposeOutput_czik_TailValueInRejectedFileHidden(t *testing.T) {
	env := "TZ=UTC\nBAD KEY=1\nNOTES=\"a\" DB_PASSWORD=SEKRITbrk01\nB=1\n"
	secrets := stackSecretsCzik(t, env)
	assert.NotContains(t, redactComposeOutput("connect: password SEKRITbrk01 rejected", secrets), "SEKRITbrk01")
}

func TestRedactComposeOutput_czik_TailValueRawTextHidden(t *testing.T) {
	env := "NOTES=\"a\" DB_PASSWORD=\"SEKRIT\\\"aaa15\"\n"
	secrets := stackSecretsCzik(t, env)
	// Output quoting the env line shows the value as written.
	assert.NotContains(t, redactComposeOutput("line 1: "+env, secrets), "aaa15")
	// And as compose resolved it.
	assert.NotContains(t, redactComposeOutput("value SEKRIT\"aaa15 end", secrets), "aaa15")
	masked, _ := MaskSecrets("line 1: "+env, "", secrets)
	assert.NotContains(t, masked, "aaa15")
}

// A tail value whose quote stays open runs on to its closing quote, as the
// first statement's does, also in a file compose rejects.
func TestRedactComposeOutput_czik_OpenTailValueFollowed(t *testing.T) {
	env := "BAD KEY=1\nNOTES=\"a\" TLS_KEY=\"-----BEGIN-----\nbodyLINEczik01\n-----END-----\"\nB=1\n"
	secrets := stackSecretsCzik(t, env)
	assert.NotContains(t, redactComposeOutput("echo: bodyLINEczik01", secrets), "bodyLINEczik01")
}
