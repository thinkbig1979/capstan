package services

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-f3a5: with no secret list (an old .env revision's git diff),
// MaskSecrets showed a colon statement after a multi-line value's closing
// quote, a "KEY:value" with no space, and the body lines of a multi-line
// sensitive value.

// deletedFileDiffF3a5 is git's full-context diff of path whose old text is
// old and whose new text is "TZ=UTC".
func deletedFileDiffF3a5(path, old string) string {
	lines := strings.Split(old, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n--- a/%s\n+++ b/%s\n@@ -1,%d +1 @@\n", path, path, path, path, len(lines))
	for _, l := range lines {
		b.WriteString("-" + l + "\n")
	}
	b.WriteString("+TZ=UTC")
	return b.String()
}

func TestMaskFullContextDiff_f3a5_EnvShapesHidden(t *testing.T) {
	for name, old := range map[string]string{
		"colon statement after a closing quote": "MSG=\"x\nb\" API_TOKEN: SEKRITf3a5",
		"KEY:value with no space":               "API_TOKEN:SEKRITf3a5",
		"multi-line sensitive body":             "API_TOKEN=\"line1SEKRITf3a5\nline2SEKRITf3a5\nline3SEKRITf3a5\"\nB=1",
		"body of a tail value":                  "NOTES=\"a\" TLS_KEY=\"x\nbodySEKRITf3a5\n\"\nB=1",
		"body never closed":                     "API_TOKEN='a\nbodySEKRITf3a5\nmoreSEKRITf3a5",
	} {
		t.Run(name, func(t *testing.T) {
			got := MaskFullContextDiff(deletedFileDiffF3a5(".env", old), nil)
			assert.NotContains(t, got, "SEKRIT")
			assert.Contains(t, got, "+TZ=UTC", "the diff is still shown")
		})
	}
}

// What the pass must leave: a non-sensitive multi-line value's body, the
// lines after a sensitive body's closing quote, and YAML (the pass is for
// env-style files; a compose file has its own YAML pass).
func TestMaskSecrets_f3a5_LeavesNonSecretLines(t *testing.T) {
	got, _ := MaskSecrets("NOTES=\"hello\nworld\"\nAPI_TOKEN=\"s1\ns2\"\nTZ=UTC\nLANG=C", ".env", nil)
	assert.Equal(t, "NOTES=\"hello\nworld\"\nAPI_TOKEN=***\n***\"\nTZ=UTC\nLANG=C", got)

	yml := "services:\n  a:\n    environment:\n      NOTE: \"x\n      y\"\n    image: nginx\n"
	gotYML, _ := MaskSecrets(yml, "compose.yml", nil)
	assert.Equal(t, yml, gotYML)
}
