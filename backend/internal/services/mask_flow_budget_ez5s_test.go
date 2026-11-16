package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-ez5s: a flow value with no closing , } or ] ran to the line's end,
// so every unclosed bracket judged the rest of the line again. Past
// lineValueBudget the whole line is masked instead of judged.

const ez5sUnit = ` {"foo":[`

// (a) Over budget: the whole line is masked, so a credential the judging
// would have reached is hidden too (review of 1e4aef0, F6: masking from
// the cut erased the evidence later judges read).
func TestMaskSecrets_ez5s_OverBudgetMasksWholeLine(t *testing.T) {
	line := strings.Repeat(ez5sUnit, 300) + " SEKez5sA"
	out, n := MaskSecrets(line, "", nil)
	assert.Equal(t, secretMask, out)
	assert.Equal(t, 1, n)
}

// (b) A line within the budget is masked exactly as before when a line
// past it follows: only its own value.
func TestMaskSecrets_ez5s_LineAboveOverBudgetUnchanged(t *testing.T) {
	head := `{"password":"SEKez5sB", "user":"bob"}`
	out, _ := MaskSecrets(head+"\n"+strings.Repeat(ez5sUnit, 300), "", nil)
	assert.Equal(t, `{"password":"***", "user":"bob"}`+"\n"+secretMask, out)
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

// Past the budget a nested line is masked whole.
func TestMaskSecrets_ez5s_NestedJSONPastBudgetMasksWholeLine(t *testing.T) {
	out, n := MaskSecrets(nestedJSONLog(20, 2000), "", nil)
	assert.Equal(t, secretMask, out)
	assert.Equal(t, 1, n)
}

// Review of 76505db (rv-dm-ez5s, HIGH): the budget stop masked its own line
// only, so an open quoted secret continued on deeper lines showed there,
// where before the budget the open-quote rule hid them (maskFrom).
func TestMaskSecrets_ez5s_OverBudgetHidesContinuationLines(t *testing.T) {
	in := "x: " + strings.Repeat("[a=", 150) + ` [PASSWORD="SEKpart1` + "\n    SEKpart2\n    SEKpart3\"]\ny: 1"
	for _, fp := range []string{"", "x.yml", ".env"} {
		out, _ := MaskSecrets(in, fp, nil)
		for _, s := range []string{"SEKpart1", "SEKpart2", "SEKpart3"} {
			assert.NotContains(t, out, s, "path %q", fp)
		}
	}
}

// Review of 011faee (rv-dm-ez5s, F2 HIGH): masking from the cut and marking
// the line done skipped the inline, flag and auth maskers, so the secrets
// before the cut showed. The whole line is masked now (F6 ruling).
func TestMaskSecrets_ez5s_OverBudgetLaterMaskersStillRun(t *testing.T) {
	for _, in := range []string{
		`run --password=SEKr2a ` + strings.Repeat(`{"ka":{`, 200),
		`run Authorization: Bearer SEKr2b ` + strings.Repeat(` {"foo":[`, 200),
	} {
		out, _ := MaskSecrets(in, "", nil)
		assert.False(t, strings.Contains(out, "SEKr2"), "a secret before the cut shows: %q", out[:min(len(out), 60)])
		assert.Equal(t, secretMask, out)
	}
}

// Review of 1e4aef0 (rv-dm-ez5s, F6 HIGH): a judge after the line pass
// (maskStatements, reading a value to the line's end) lost its evidence to
// the cut's mask, so CONFIG=user:SEK18130@host before the cut showed.
// The reviewer's fuzz case, seed 2026 case 1362, verbatim.
const ez5sCaseF6 = `     {"Authorization":"Bearer SEK18129"}[a={"ka":{{"ka":{[a= a=(?a=( {"foo":[{"ka":{a=( {"a":(a=( a=( [ x=y( [(a= {"foo":[{k: a=( a=({"ka":{ [a= [a= CONFIG=user:SEK18130@host(a= [ [ {"foo":[?a=(a=(a=({k: a=( {"a":(?a=( [ [ [a=a=((a=(a=((a=a=(a=( a=([a= {"foo":[ a=( [a=a=( [( {"foo":[ {"a":?a=(a=({"ka":{{"ka":{[a={k: a=( [a=(a=a=( {"foo":[(a=a=((a= a=((a=(a=(a=( [a= [a={"ka":{ [a=(?a=( {"a":{"ka":{ {"a": [a=a=({"ka":{(a={"ka":{([a= {"a": {"a":a=({k: {k:  a=( [ a=(a=( a=({"ka":{( [(a=[a= {"a": [a=a=(a=({"ka":{ {"foo":[ [{k: ?a=( a=(?a=(?a=([a=a=(a=(( {"foo":[[a=(a=( a=({"ka":{ [ [a={k:  [a=?a=(a=(([a=?a=( {"foo":[[a={k:  a=(( {"a": [a=a=(?a=( {"a":?a=(a=(?a=((?a=({"ka":{a=(a=((a= a=(a=(a=( [a=(a={k: (a=a=( CONFIG=user:SEK18131@host{k: [a={"ka":{ a=(?a=(( [a=(a= [a= {"a": {"foo":[( [{"ka":{a=( a=(?a=( a=( {"a":[a=a=( {"foo":[ {"foo":[a=( {"a":( x=y [( a=( [a={"ka":{ [TOKEN=SEK18132] [a=( [a=a=({"ka":{{k:  {"foo":[ [a=((?a=([a= [a=a=(a=(((a=[a=[a=a=(a=(a=((a=( {"a":a=(?a=( [TOKEN=SEK18133] a=( {"password":"SEK18134"}a=({"ka":{ [a=(a=a=( {"foo":[ [a= [a={"ka":{a=({"ka":{a=( {"foo":[a=(a=({"ka":{a=({k:  {"foo":[( [{"ka":{[a= {"a":{"ka":{a=([a= [{"ka":{( {secret: SEK18135}`

func TestMaskSecrets_ez5s_OverBudgetLaterJudgeEvidence(t *testing.T) {
	for _, fp := range []string{"", "x.yml", ".env"} {
		out, _ := MaskSecrets(ez5sCaseF6, fp, nil)
		assert.False(t, strings.Contains(out, "SEK18130"), "path %q: %q", fp, out[:min(len(out), 80)])
	}
}
