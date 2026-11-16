package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-rtf7: an inline KEY=value runs to the next space, quote, comma or
// closing bracket, and "(" is none of them (pw(abc) is one value), so
// `a=(` repeated handed the rest of the line to IsSensitiveEnvVar once per
// key. Past lineValueBudget the whole line is masked instead.

// Over budget: the whole line is masked, so the credential after the
// unstopped values is hidden too.
func TestMaskSecrets_rtf7_OverBudgetMasksWholeLine(t *testing.T) {
	out, n := MaskSecrets(strings.Repeat("a=(", 1000)+" SEKrtf7A", "", nil)
	assert.Equal(t, secretMask, out)
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

// A header before the inline budget stop stays hidden (review of 011faee,
// F2): the whole line is masked (F6 ruling).
func TestMaskSecrets_rtf7_OverBudgetLaterMaskersStillRun(t *testing.T) {
	in := `run Authorization: Bearer SEKr2d ` + strings.Repeat("a=(", 1000)
	out, _ := MaskSecrets(in, "", nil)
	assert.False(t, strings.Contains(out, "SEKr2d"), "a secret before the cut shows: %q", out[:min(len(out), 60)])
	assert.Equal(t, secretMask, out)
}
