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
// closed, as the restic prefix walk does past its depth. The nested URL past
// the cap carries no credential, so only the cap can mask it.
func TestRedactURLUserinfoInText_mask2_EncodedMarkersPastCapFailClosed(t *testing.T) {
	head := "https://p/?u=" + strings.Repeat("x%3a%2f%2fhost%2f&", maxEncodedNestedURLs) + "y%3a%2f%2f"
	assert.Equal(t, head+userinfoPlaceholder, RedactURLUserinfoInText(head+"plainhost%2fpath"))
	assert.True(t, HasURLUserinfo(head+"plainhost"))
	// One marker fewer is read in full and left as it is.
	under := "https://p/?u=" + strings.Repeat("x%3a%2f%2fhost%2f&", maxEncodedNestedURLs-1) + "y%3a%2f%2fplainhost"
	assert.Equal(t, under, RedactURLUserinfoInText(under))
}

// Review of run/mask2, F2: a quoted value left open was followed by joining
// every later line, once per such value, so 4000 open tails allocated 431 MB.
// Bounded by how the bytes allocated GROW, eight times the lines: about 8x
// when linear, 64x when quadratic. Not an absolute bytes-per-input bound: under
// -race sync.Pool drops what is put back, so every regexp match allocates a
// fresh machine and the absolute figure says nothing (4223x the input under
// -race on the fixed code, against 66x without).
func TestStackSecretValues_mask2_OpenValuesAllocateLinearly(t *testing.T) {
	for name, line := range map[string]string{
		"open tail value":       "NOTES=\"a\" DB_PASSWORD='x",
		"open first value":      "DB_PASSWORD='x",
		"open tail, other kind": "NOTES='a' DB_PASSWORD=\"x",
	} {
		t.Run(name, func(t *testing.T) {
			alloc := func(lines int) uint64 {
				dir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(strings.Repeat(line+"\n", lines)), 0o600))
				stack := models.Stack{Directory: dir, ProjectName: "p"}
				return allocatedBuwq(func() { StackSecretValues(stack, "") })
			}
			small, large := alloc(1000), alloc(8000)
			growth := float64(large) / float64(max(small, 1))
			t.Logf("1000 lines: %d bytes allocated, 8000 lines: %d, growth %.1fx", small, large, growth)
			// Measured: 8.1-9.0x plain and 2.9-6.9x under -race on the fix; the
			// quadratic code was 59-60x plain and 16.1-30.4x under -race.
			assert.Less(t, growth, 12.0, "8x the lines allocated %.1fx as much: not linear", growth)
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
