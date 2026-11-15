package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Review round 3 of run/maskfu, N1: a mask widened to a secret-list needle
// that runs on into the next token made the pass resume at the widened end,
// inside the next token's separator or key, so a sensitive value after it
// was never matched. The reviewer's fuzz lines (rvprobe_eq seed 99) and one
// hand-built case that leaked before run/maskfu too.
func TestMaskSecrets_n1_WidenedMaskDoesNotSkipTheNextToken(t *testing.T) {
	cases := []struct{ line, needle, secret string }{
		{`,--requirepass=https://u:p@h/x --[--db-pass=SEK23467`, "/x --[--d", "SEK23467"},
		{`[--db-pass=https://u:p@h/x[--db-pass=q;r,--password=SEK17134 "`, ";r,--passwo", "SEK17134"},
		{`[--x=x {token: bar, b: c} {"password":SEK8444[--x a&b`, `r, b: c} {"p`, "SEK8444"},
		{`x --password abc --password SEKd2d1`, "abc --pa", "SEKd2d1"},
	}
	for _, c := range cases {
		for _, path := range []string{"", ".env", "docker-compose.yml"} {
			out, _ := MaskSecrets(c.line, path, []string{c.needle})
			assert.NotContains(t, out, c.secret, "path %q, needle %q", path, c.needle)
		}
	}
}

// Review round 4, P1: with the merge of overlapping spans in lineWalk.mask
// off, a later value inside a widened span made apply slice backwards and
// panic, and a panic in a log stream goroutine is not recovered.
func TestMaskSecrets_n1_OverlappingSpansMergeWithoutPanic(t *testing.T) {
	line := `;api_key=https://u:p@h/x&token=q;r ? {'a'=***},--requirepass u:p@h,api_key="qv",password=SEK1202`
	needles := []string{"x&token=q;", "//u:p@h/x&toke"}
	for _, path := range []string{"", ".env", "docker-compose.yml"} {
		var out string
		assert.NotPanics(t, func() { out, _ = MaskSecrets(line, path, needles) }, "path %q", path)
		assert.NotContains(t, out, "SEK1202", "path %q", path)
	}
}
