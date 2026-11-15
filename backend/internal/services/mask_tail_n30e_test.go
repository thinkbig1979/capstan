package services

import (
	"runtime"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-n30e: compose ends a quoted value at its closing quote and reads
// what follows on the line as the next statement (compose-go v2
// dotenv/parser.go, extractVarValue), so `NOTES="a" DB_PASSWORD="x"` is two
// variables. The line maskers read only the first.

// Each line holds SEKRIT in a sensitive variable compose reads after a
// closing quote.
var n30eTailLines = []string{
	`NOTES="a" DB_PASSWORD="SEKRITaaaa1"`,
	`NOTES="a" DB_PASSWORD="SEKRIT aaaa2"`,
	`NOTES="a" DB_PASSWORD='SEKRITaaaa3'`,
	`NOTES="a" API_TOKEN: SEKRITaaaa4`,
	`NOTES: 'a' API_TOKEN: SEKRITaaaa5`,
	`NOTES="a" DB_PASSWORD = SEKRITaaaa6`,
	`NOTES="a" API_TOKEN: "SEKRIT aaaa7"`,
	`NOTES="a" DB_PASSWORD="SEKRIT\"aaaa8"`,
	`NOTES="a" B="c" API_TOKEN: SEKRITaaaa9`,
	`NOTES="a" export API_TOKEN: 'SEKRITaaa10'`,
	`NOTES="a" BACKUP="sftp:user:SEKRITaaa11@host:/p"`,
}

func TestN30e_TailLinesAreSecretsToCompose(t *testing.T) {
	for _, l := range n30eTailLines {
		vars, err := dotenv.UnmarshalWithLookup(l+"\n", nil)
		require.NoError(t, err, l)
		read := false
		for k, v := range vars {
			read = read || (strings.Contains(v, "SEKRIT") && IsSensitiveEnvVar(k, v))
		}
		assert.True(t, read, "the oracle: compose reads SEKRIT as a secret in %s: %q", l, vars)
	}
}

// With no secret values to match, the line arm is all that masks an env
// line: a git history diff of an old .env revision, whose secret the
// current files no longer hold.
func TestMaskSecrets_n30e_TailSecretMasked(t *testing.T) {
	for _, l := range n30eTailLines {
		for _, eol := range []string{"\n", "\r\n"} {
			text := "TZ=UTC" + eol + l + eol + "B=1" + eol
			out, n := MaskSecrets(text, "stack/.env", nil)
			assert.NotContains(t, out, "SEKRIT", "%q", l)
			assert.Positive(t, n, "%q", l)
			assert.Contains(t, out, "NOTES", "the line's other variables stay visible")
			assert.Contains(t, out, "B=1"+eol)
		}
	}
}

func TestMaskFullContextDiff_n30e_OldRevisionTailSecretMasked(t *testing.T) {
	diff := "diff --git a/.env b/.env\n" +
		"--- a/.env\n" +
		"+++ b/.env\n" +
		"@@ -1,2 +1,2 @@\n" +
		" TZ=UTC\n" +
		"-NOTES=\"a\" API_TOKEN: \"SEKRIT old\"\n" +
		"+NOTES=\"a\" API_TOKEN: \"current\"\n"
	got := MaskFullContextDiff(diff, []string{"current"})
	assert.NotContains(t, got, "SEKRIT", "%s", got)
	assert.Contains(t, got, " TZ=UTC", "%s", got)
}

// Harmless tails stay visible: not stricter than it must be.
func TestMaskSecrets_n30e_HarmlessTailStaysVisible(t *testing.T) {
	text := "NOTES=\"a\" TZ=UTC\nNOTES=\"a\" # a note\nNOTES=\"a\" DB_PASSWORD=\"${DB_PASSWORD}\"\n"
	out, _ := MaskSecrets(text, "stack/.env", nil)
	assert.Equal(t, text, out)
}

// One line of many statements is masked in one pass: a splice per masked
// statement copied the whole line each time, quadratic. The check is the
// bytes allocated, not a clock: under -race the linear masker runs about
// 11x slower and the splice barely slower, so no wall-clock bound both
// passed the masker on a loaded CI runner and failed the splice (PR #736).
// At 40000 statements the masker allocates about 47x the line (177x under
// -race) and the splice about 43700x, in both modes.
func TestMaskSecrets_n30e_LongTailLineMasksInLinearTime(t *testing.T) {
	line := "NOTES=\"a\"" + strings.Repeat(" A_TOKEN=\"b\"", 40000) + "\n"
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	out, _ := MaskSecrets(line, "stack/.env", nil)
	runtime.ReadMemStats(&after)
	require.NotContains(t, out, "\"b\"")
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("%d bytes masked, %d bytes allocated (%.0fx)", len(line), allocated, float64(allocated)/float64(len(line)))
	assert.Less(t, allocated, uint64(1000*len(line)), "masking one line allocated %d bytes for %d: not linear", allocated, len(line))
}

// A tail value whose quote does not close on its line is masked to the
// line's end (review mutant m7: base showed it).
// The lines after a quote that never closes are masked too since
// agent-os-f3a5 (fail closed): where the value ends is unknown, as the locked
// env view's hideMultilineBodies and the secret list (sensitiveEnvValues)
// already read it. A blank line stays blank.
func TestMaskSecrets_n30e_OpenTailQuoteMasked(t *testing.T) {
	out, _ := MaskSecrets("NOTES=\"a\" DB_PASSWORD=\"SEKRIT\nB=1\n", "stack/.env", nil)
	assert.NotContains(t, out, "SEKRIT", "%q", out)
	assert.Equal(t, "NOTES=\"a\" DB_PASSWORD=\"***\n***\n", out)
}
