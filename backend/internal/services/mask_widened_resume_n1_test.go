package services

import (
	"strings"
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

// agent-os-ns7f: a mask widened to a secret-list value that runs on into the
// next token hid that token's key (or its separator) from every later pass,
// so its value showed. Pre-existing on d793706. The reviewer's fuzz lines
// (rvprobe_eq seed 99, cases 7154 and 20782), the compose text built as the
// fuzz builds it.
func TestMaskSecrets_ns7f_WidenedMaskHidesTheNextKey(t *testing.T) {
	cases := []struct {
		line    string
		needles []string
		secret  string
	}{
		{` password=SEK7154"proxy-authorization=SEK7154 proxy-authorization: Bearer "qv""Authorization: Bearer "qv" }?NOTES=a&b`, []string{`54"proxy-`, "tion: Be"}, "SEK7154"},
		{` ',api_key=bar {'a': SEK20782},--auth SEK20782'Authorization: Bearer *** }`, []string{"a': SEK2", "20782},--aut"}, "SEK20782"},
	}
	for _, c := range cases {
		for _, path := range []string{"", ".env", "docker-compose.yml"} {
			text := c.line
			if path == "docker-compose.yml" {
				text = "services:\n  a:\n    command: " + strings.TrimSpace(c.line)
			}
			out, _ := MaskSecrets(text, path, c.needles)
			assert.NotContains(t, out, c.secret, "path %q", path)
		}
	}
	// The same through splice (the YAML pass and maskLeadKey).
	for _, c := range []struct{ text, needle, secret string }{
		{"services:\n  a:\n    environment:\n      DB_PASSWORD: \"abc\" ,--auth SEKsp1", `abc" ,--au`, "SEKsp1"},
		{"services:\n  a:\n    command: [\"DB_PASSWORD=abc\", \"--auth\", \"SEKsp2\"]", `abc", "--au`, "SEKsp2"},
		{"services:\n  a:\n    labels:\n      token: 'abc' x --auth SEKsp4", "abc' x --au", "SEKsp4"},
	} {
		out, _ := MaskSecrets(c.text, "docker-compose.yml", []string{c.needle})
		assert.NotContains(t, out, c.secret, "splice, needle %q", c.needle)
	}
}
