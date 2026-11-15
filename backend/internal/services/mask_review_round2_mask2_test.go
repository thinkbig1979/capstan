package services

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Review of run/mask2, F1: every percent-encoded "://" marker scanned the
// rest of its token, so a token of many markers was quadratic (495.7 ms at
// 80 KB, 54x for 8x input). Bounded by how the time grows, as h4ts is.
func TestHasURLUserinfo_mask2_EncodedMarkersStayLinear(t *testing.T) {
	tok := func(n int) string { return "https://p/?u=" + strings.Repeat("x%3a%2f%2fa:b:", n/14) }
	ratio := growthRatioMask2(t, tok(10_000), tok(80_000), func(s string) { HasURLUserinfo(s) })
	assert.Less(t, ratio, 24.0, "8x the input took %.1fx as long: not linear", ratio)
}

// Past maxEncodedNestedURLs markers a token is masked from there on: fail
// closed, as the restic prefix walk does past its depth.
func TestRedactURLUserinfoInText_mask2_EncodedMarkersPastCapFailClosed(t *testing.T) {
	in := "https://p/?u=" + strings.Repeat("x%3a%2f%2fhost%2f&", maxEncodedNestedURLs) + "y%3a%2f%2fuser%3aSEKRIT%40host"
	got := RedactURLUserinfoInText(in)
	assert.False(t, strings.Contains(got, "SEKRIT"), "the credential past the cap is shown")
	assert.True(t, HasURLUserinfo(in))
}

// Review of run/mask2, F2: a quoted value left open was followed by joining
// every later line, once per such value, so 4000 open tails allocated 411 MB.
// Bounded by bytes allocated, not wall clock.
func TestStackSecretValues_mask2_OpenValuesAllocateLinearly(t *testing.T) {
	for name, line := range map[string]string{
		"open tail value":       "NOTES=\"a\" DB_PASSWORD='x",
		"open first value":      "DB_PASSWORD='x",
		"open tail, other kind": "NOTES='a' DB_PASSWORD=\"x",
	} {
		t.Run(name, func(t *testing.T) {
			env := strings.Repeat(line+"\n", 4000)
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600))
			stack := models.Stack{Directory: dir, ProjectName: "p"}
			allocated := allocatedBuwq(func() { StackSecretValues(stack, "") })
			ratio := float64(allocated) / float64(len(env))
			t.Logf("%d bytes of env, %d bytes allocated (%.1fx)", len(env), allocated, ratio)
			assert.Less(t, ratio, 400.0, "allocated %d bytes for %d: not linear", allocated, len(env))
		})
	}
}

// The value a line-by-line follow finds is the one a whole-text search
// found, escapes across a line end included.
func TestSensitiveEnvValues_mask2_FollowedValueUnchanged(t *testing.T) {
	for _, env := range []string{
		"NOTES=\"a\" DB_PASSWORD='x\nbodyLINEmask2a\nend'\nB=1",
		"DB_PASSWORD=\"x\\\nbodyLINEmask2b\\\"\nend\"\nB=1",
		"NOTES=\"a\" TLS_KEY=\"x\nbodyLINEmask2c",
	} {
		joined := strings.Join(sensitiveEnvValues(env), "\x00")
		for _, want := range []string{"bodyLINEmask2"} {
			assert.Contains(t, joined, want, "%q", env)
		}
	}
	assert.Contains(t, sensitiveEnvValues("DB_PASSWORD=\"x\\\nbody\\\"\nend\"\nB=1"), "\"x\\\nbody\\\"\nend\"")
}

// Review of run/mask2, F4: envQuotePass judged a multi-line opener by key
// and URL only, so a quoted JSON value holding a password under a plain key
// showed its body in an old revision's diff, which the locked env view
// blanks (IsSensitiveEnvVar).
func TestMaskFullContextDiff_mask2_MultiLineJSONSecretBodyHidden(t *testing.T) {
	old := "JSONCFG='{\"password\":\"SEKRITf4a\",\n\"user\":\"SEKRITf4b\"}'\nTZ=UTC"
	got := MaskFullContextDiff(deletedFileDiffF3a5(".env", old), nil)
	assert.NotContains(t, got, "SEKRIT")
	assert.Contains(t, got, "-TZ=UTC")
	_ = fmt.Sprint()
}
