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
