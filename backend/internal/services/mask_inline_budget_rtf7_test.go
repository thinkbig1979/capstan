package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-rtf7: an inline KEY=value runs to the next space, quote, comma or
// closing bracket, and "(" is none of them (pw(abc) is one value), so
// `a=(` repeated handed the rest of the line to IsSensitiveEnvVar once per
// key. Past lineValueBudget the rest of the line is masked instead.

// Over budget: masked from a value to the line's end, so the credential
// after the unstopped values is hidden too.
func TestMaskSecrets_rtf7_OverBudgetMasksToLineEnd(t *testing.T) {
	line := strings.Repeat("a=(", 1000) + " SEKrtf7A"
	out, n := MaskSecrets(line, "", nil)
	assert.False(t, strings.Contains(out, "SEKrtf7A"), "the credential after the unstopped values shows: %q", out[max(0, len(out)-40):])
	require.True(t, strings.HasSuffix(out, secretMask), "masked to the line's end")
	kept := strings.TrimSuffix(out, secretMask)
	assert.True(t, strings.HasPrefix(line, kept), "the text before the mask is the line's own")
	assert.NotEmpty(t, kept, "values within the budget are still judged, not masked")
	assert.Equal(t, 1, n)
}

// Within the budget nothing changes: a value with a parenthesis is not cut
// at the "(" (the output base 76505db gives, pinned).
func TestMaskSecrets_rtf7_ParenValueUnchanged(t *testing.T) {
	out, _ := MaskSecrets("x DB_PASSWORD=pw(SEKrtf7B) y", "", nil)
	assert.Equal(t, "x DB_PASSWORD=***", out)
}

// The inline budget stop fails closed on the lines below too, as the flow
// one does (review of 76505db, rv-dm-ez5s HIGH). Without the padding this
// line's continuation shows on 55c7033 as well: an inline open quote never
// hid the lines below (a separate, older gap); past the budget it now does.
func TestMaskSecrets_rtf7_OverBudgetHidesContinuationLines(t *testing.T) {
	in := "x: " + strings.Repeat("a=(", 150) + ` PASSWORD="SEKpart1` + "\n    SEKpart2\n    SEKpart3\"\ny: 1"
	for _, fp := range []string{"", "x.yml", ".env"} {
		out, _ := MaskSecrets(in, fp, nil)
		for _, s := range []string{"SEKpart1", "SEKpart2", "SEKpart3"} {
			assert.NotContains(t, out, s, "path %q", fp)
		}
	}
}
