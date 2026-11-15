package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Found by the superset corpus of agent-os-caeh/vq3i: the URL pass, which now
// stars nested, encoded and sftp:/s3: credentials, runs before the secret
// values are looked for, so a value holding such a URL no longer matched as
// written and the rest of it showed.
func TestRedactComposeOutput_mask2_ValueHoldingAStarredURLStillHidden(t *testing.T) {
	for _, v := range []string{
		"hunter2pw sftp:u:x@h:/p",
		"hunter2pw https://p/?url=https://u:x@h",
		"hunter2pw https://p/?url=https%3A%2F%2Fu%3Ax%40h",
	} {
		assert.NotContains(t, redactComposeOutput("echo "+v+" done", []string{v}), "hunter2pw", "%q", v)
		got, _ := MaskSecrets("echo "+v+" done", "", []string{v})
		assert.NotContains(t, got, "hunter2pw", "MaskSecrets %q", v)
	}
}

// The same through a line pass: a KEY=value inside the value is masked
// before the values are looked for (the corpus case, compose path).
func TestMaskSecrets_mask2_ValueHoldingAMaskedStatementStillHidden(t *testing.T) {
	v := "user:hunter2pw@host JSONCFG=https://p/?url=https://u:x@h"
	got, _ := MaskSecrets("BAD KEY = "+v+"\n", "compose.yml", []string{v})
	assert.NotContains(t, got, "hunter2pw")
}
