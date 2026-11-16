package services

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-6wqp: a sensitive value whose quote does not close on its line
// was masked to the line's end only, or (maskFrom) with the lines indented
// deeper than it, so its body lines showed: always for the in-line maskers
// (inline KEY="..., --flag "..., Authorization: "...), and on the YAML path,
// where envQuotePass does not run, for the rest. The body is masked up to
// the line where the quote closes now.
var openQuoteShapes6wqp = map[string]string{
	"inlineKV":    "x: x DB_PASSWORD=\"SEKpart1\n    SEKpart2\n    SEKpart3\"\ny: 1",
	"inlineKV0":   "x DB_PASSWORD=\"SEKpart1\nSEKpart2\nSEKpart3\"\nY=1",
	"leadKey0":    "DB_PASSWORD=\"x\nSEKpart2\nSEKpart3\"\nY=1",
	"leadKeyInd":  "  DB_PASSWORD=\"x\n  SEKpart2\n  SEKpart3\"\nY=1",
	"leadColon0":  "DB_PASSWORD: \"x\nSEKpart2\nSEKpart3\"\nY: 1",
	"flagNext":    "x: run --password \"SEKpart1\n    SEKpart2\n    SEKpart3\"\ny: 1",
	"flagNext0":   "run --password \"SEKpart1\nSEKpart2\nSEKpart3\"\nY=1",
	"flagEq0":     "run --password=\"SEKpart1\nSEKpart2\nSEKpart3\"\nY=1",
	"authHdr0":    "Authorization: \"Bearer SEKpart1\nSEKpart2\nSEKpart3\"\nY=1",
	"authHdrInl0": "x Authorization: \"Bearer SEKpart1\nSEKpart2\nSEKpart3\"\nY=1",
	"envTail0":    "NOTES=\"a\" DB_PASSWORD=\"SEKpart1\nSEKpart2\nSEKpart3\"\nY=1",
	"flow0":       "x [DB_PASSWORD=\"SEKpart1\nSEKpart2\nSEKpart3\"]\nY=1",
	"flowInd":     "x: [DB_PASSWORD=\"SEKpart1\n    SEKpart2\n    SEKpart3\"]\ny: 1",
	// The value's own quote closes on the first quote of the JSON inside it,
	// which leaves the line's last quote open (reviewer's 14th shape, small
	// enough to stay within lineValueBudget).
	"jsonInQuote": `DB_PASSWORD="` + strings.Repeat(`{"ka":{`, 3) + "\nSEKpart2\nSEKpart3\"\nY=1",
}

func TestMaskSecrets_6wqp_OpenQuoteBodyHidden(t *testing.T) {
	for name, in := range openQuoteShapes6wqp {
		for _, fp := range []string{"", "x.yml", ".env"} {
			out, _ := MaskSecrets(in, fp, nil)
			for _, s := range []string{"SEKpart1", "SEKpart2", "SEKpart3"} {
				assert.NotContains(t, out, s, "%s, path %q", name, fp)
			}
			// The line after the closing quote is read as before.
			assert.True(t, strings.HasSuffix(out, "\ny: 1") || strings.HasSuffix(out, "\nY=1") || strings.HasSuffix(out, "\nY: 1"), "%s, path %q: %q", name, fp, out)
		}
	}
}

// The 14th shape as the reviewer wrote it: its 200 opens also trip
// lineValueBudget, which masks the line whole; the body line below it is
// hidden all the same.
func TestMaskSecrets_6wqp_JSONInQuoteOverBudget(t *testing.T) {
	in := `DB_PASSWORD="` + strings.Repeat(`{"ka":{`, 200) + "\n" + `SEKbody2"` + "\nY=1"
	for _, fp := range []string{"", "x.yml", ".env"} {
		out, _ := MaskSecrets(in, fp, nil)
		assert.Equal(t, secretMask+"\n"+secretMask+"\"\nY=1", out, "path %q", fp)
	}
}

// agent-os-c2dp: an open quote masked its line at once and marked it done,
// so the maskers after it never read the text before the quote.
func TestMaskSecrets_c2dp_SecretBeforeOpenQuoteHidden(t *testing.T) {
	for _, in := range []string{
		`run --password=SEKpre1 [PASSWORD="SEKopen`,
		`run TOKEN=SEKpre2 x {"a":"b", "token":"SEKopen`,
	} {
		for _, fp := range []string{"", "x.yml", ".env"} {
			out, _ := MaskSecrets(in, fp, nil)
			assert.NotContains(t, out, "SEKpre", "path %q: %q", fp, out)
			assert.NotContains(t, out, "SEKopen", "path %q: %q", fp, out)
		}
	}
}

// A quote that closes on its line leaves the lines below as they were.
func TestMaskSecrets_6wqp_ClosedQuoteNextLineUnchanged(t *testing.T) {
	for _, fp := range []string{"", "x.yml", ".env"} {
		out, _ := MaskSecrets("DB_PASSWORD=\"SEKclosed\"\nNEXT=visible\nx: [TOKEN=\"SEKc2\"] y\n  more visible", fp, nil)
		assert.NotContains(t, out, "SEKclosed", "path %q", fp)
		assert.NotContains(t, out, "SEKc2", "path %q", fp)
		assert.Contains(t, out, "\nNEXT=visible\n", "path %q", fp)
		assert.True(t, strings.HasSuffix(out, "\n  more visible"), "path %q: %q", fp, out)
	}
}

// PR #762 review, P1: body lines were masked whole before linePass reached
// them, so the maskers that read them on main never ran and the text after
// the body's closing quote showed. Multi-line fuzz seed 13, case 1629,
// verbatim; main hides these four.
func TestMaskSecrets_6wqp_BodyLineOwnMaskersRun(t *testing.T) {
	in := "    \"JSONCFG\":\"SEK13422 secret=user:SEK13423@host\n    it's SEK13424\n      export SEK13425 {\"DB_PASSWORD\":\"SEK13426\"} [Authorization=\"SEK13427\n      TZ=user:SEK13428@host\n  secret='SEK13429'\n\n      SEK13430' DB_PASSWORD: \"SEK13431 SEK13432\n    JSONCFG=user:SEK13433@host it's SEK13434 CONFIG='SEK13435\n- CONFIG=user:SEK13436@host"
	for _, fp := range []string{"", "x.yml", ".env"} {
		out, _ := MaskSecrets(in, fp, nil)
		for _, s := range []string{"SEK13431", "SEK13432", "SEK13434", "SEK13435"} {
			assert.NotContains(t, out, s, "path %q", fp)
		}
	}
}

// PR #762 review, P2: the statement before an open quote is read by the env
// passes after linePass, which skip a line marked done.
func TestMaskSecrets_c2dp_EnvStatementBeforeOpenQuote(t *testing.T) {
	for _, fp := range []string{"", ".env"} {
		out, _ := MaskSecrets("TLS_KEY:'SEKx1' TLS_KEY='open\nbody\n", fp, nil)
		assert.NotContains(t, out, "SEKx1", "path %q", fp)
		assert.NotContains(t, out, "body", "path %q", fp)
	}
}

// PR #762 review, P3: a YAML single-quoted scalar escapes a quote as ”;
// its body runs on past it.
func TestMaskSecrets_6wqp_YAMLDoubledSingleQuote(t *testing.T) {
	out, _ := MaskSecrets("a:\n  PASSWORD: 'SEKa\n  it''s SEKb\n  end'", "x.yml", nil)
	assert.NotContains(t, out, "SEKa")
	assert.NotContains(t, out, "SEKb")
}
