package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-ez5s: a flow value with no closing , } or ] ran to the line's end,
// so every unclosed bracket judged the rest of the line again. Past
// flowValueBudget the rest of the line is masked instead of judged.

const ez5sUnit = ` {"foo":[`

// (a) Over budget: the line is masked from a value to its end, so a
// credential the judging would have reached is hidden too.
func TestMaskSecrets_ez5s_OverBudgetMasksToLineEnd(t *testing.T) {
	line := strings.Repeat(ez5sUnit, 300) + " SEKez5sA"
	out, n := MaskSecrets(line, "", nil)
	assert.False(t, strings.Contains(out, "SEKez5sA"), "the credential after the unclosed values shows: %q", out[max(0, len(out)-40):])
	require.True(t, strings.HasSuffix(out, secretMask), "masked to the line's end: %q", out[max(0, len(out)-40):])
	kept := strings.TrimSuffix(out, secretMask)
	assert.True(t, strings.HasPrefix(line, kept), "the text before the mask is the line's own")
	assert.NotEmpty(t, kept, "values within the budget are still judged, not masked")
	assert.Equal(t, 1, n)
}

// (b) A sensitive value before the budget runs out is masked exactly as
// before: only its value, the rest of its line judged as usual.
func TestMaskSecrets_ez5s_SensitiveKeyBeforeBudgetUnchanged(t *testing.T) {
	head := ` {"password":"SEKez5sB"}`
	out, _ := MaskSecrets(head+strings.Repeat(ez5sUnit, 300), "", nil)
	assert.True(t, strings.HasPrefix(out, ` {"password":"***"}`+ez5sUnit), "got %q", out[:min(len(out), 60)])
	assert.NotContains(t, out, "SEKez5sB")
}

// (c) The longest line of the shape still within budget is unchanged: the
// shape holds no secret, so before the budget nothing on it was masked,
// and one unit more is the first line masked.
func TestMaskSecrets_ez5s_JustUnderBudgetUnchanged(t *testing.T) {
	first := 0
	for k := 1; k <= 200 && first == 0; k++ {
		line := strings.Repeat(ez5sUnit, k)
		if out, n := MaskSecrets(line, "", nil); n != 0 || out != line {
			first = k
		}
	}
	require.Greater(t, first, 1, "some line of the shape must fit the budget")
	under := strings.Repeat(ez5sUnit, first-1)
	out, n := MaskSecrets(under, "", nil)
	assert.Equal(t, under, out)
	assert.Equal(t, 0, n)
	t.Logf("first masked at %d units (%d bytes)", first, first*len(ez5sUnit))
}

// nestedJSONLog is a JSON log line whose "ctx" nests depth objects around
// one quoted string of leaf bytes: each nested value runs to the first },
// so the values overlap depth deep.
func nestedJSONLog(depth, leaf int) string {
	var b strings.Builder
	b.WriteString(`{"level":"info","ctx":`)
	for d := range depth {
		b.WriteString(`{"k` + string(rune('a'+d)) + `":`)
	}
	b.WriteString(`"` + strings.Repeat("x", leaf) + `"`)
	b.WriteString(strings.Repeat("}", depth+1))
	return b.String()
}

// Nested JSON log lines within the budget are unchanged: no secret, nothing
// masked, as before it.
func TestMaskSecrets_ez5s_NestedJSONWithinBudgetUnchanged(t *testing.T) {
	for _, c := range [][2]int{{8, 2000}, {4, 20000}} {
		line := nestedJSONLog(c[0], c[1])
		out, n := MaskSecrets(line, "", nil)
		assert.True(t, out == line, "depth %d leaf %d changed", c[0], c[1])
		assert.Equal(t, 0, n, "depth %d leaf %d", c[0], c[1])
	}
}

// Past the budget a nested line is masked from a key's value to the end.
func TestMaskSecrets_ez5s_NestedJSONPastBudgetMasksToEnd(t *testing.T) {
	line := nestedJSONLog(20, 2000)
	out, n := MaskSecrets(line, "", nil)
	require.True(t, strings.HasSuffix(out, secretMask), "masked to the line's end")
	kept := strings.TrimSuffix(out, secretMask)
	assert.True(t, strings.HasPrefix(line, kept), "the text before the mask is the line's own")
	assert.True(t, strings.HasPrefix(kept, `{"level":"info","ctx":{"ka":`), "values within the budget are still judged: %q", kept)
	assert.NotContains(t, out, "xxxx")
	assert.Equal(t, 1, n)
}
