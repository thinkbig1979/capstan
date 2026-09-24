package handlers

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// isValidHash is the SOLE gate on GET /git/:id/diff/:hash (git.go:221-229) and
// measured 0.0% (agent-os-wo9x). Whatever it lets through is interpolated into
// a git invocation downstream, so its rejections are the thing worth pinning.

func TestIsValidHash_AcceptsRealHashes(t *testing.T) {
	cases := map[string]string{
		"full 40-char sha1":  "0123456789abcdef0123456789abcdef01234567",
		"7-char short hash":  "0123456",
		"8-char short hash":  "01234567",
		"12-char short hash": "0123456789ab",
		"all digits":         "1234567",
		"all letters":        "abcdefa",
	}

	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			assert.True(t, isValidHash(hash))
		})
	}
}

func TestIsValidHash_RejectsMalformedRefs(t *testing.T) {
	cases := map[string]string{
		"empty":                     "",
		"too short at 6":            "012345",
		"single character":          "a",
		"uppercase 40-char":         strings.ToUpper("0123456789abcdef0123456789abcdef01234567"),
		"uppercase short":           "ABCDEF0",
		"mixed case short":          "AbCdEf0",
		"non-hex letters":           "zzzzzzz",
		"hex with a trailing g":     "0123456g",
		"branch name":               "main",
		"HEAD":                      "HEAD",
		"HEAD with offset":          "HEAD~1",
		"ref path":                  "refs/heads/main",
		"range":                     "abc1234..def5678",
		"leading whitespace":        " 0123456",
		"trailing whitespace":       "0123456 ",
		"internal whitespace":       "012 3456",
		"newline injection":         "0123456\nrm -rf /",
		"semicolon injection":       "0123456;id",
		"backtick injection":        "0123456`id`",
		"dollar-paren injection":    "0123456$(id)",
		"pipe injection":            "0123456|id",
		"path traversal":            "../../etc/passwd",
		"flag injection":            "--upload-pack=touch",
		"40 chars with one non-hex": "0123456789abcdef0123456789abcdef0123456g",
	}

	for name, hash := range cases {
		t.Run(name, func(t *testing.T) {
			assert.False(t, isValidHash(hash), "must reject %q", hash)
		})
	}
}

func TestIsValidHash_LengthBoundary(t *testing.T) {
	// 7 is the shortest git will abbreviate to, and the function's cut-off.
	assert.False(t, isValidHash(strings.Repeat("a", 6)))
	assert.True(t, isValidHash(strings.Repeat("a", 7)))
}

func TestIsValidHash_UpperBoundIsSHA256Length(t *testing.T) {
	// 41..64 can be a SHA-256 object id or a prefix of one, so they pass.
	// Past 64 no object id exists; this used to pass too and answer 500
	// (agent-os-tyl6).
	assert.True(t, isValidHash(strings.Repeat("a", 41)))
	assert.True(t, isValidHash(strings.Repeat("a", 64)))
	assert.False(t, isValidHash(strings.Repeat("a", 65)))
	assert.False(t, isValidHash(strings.Repeat("a", 200000)))
}
