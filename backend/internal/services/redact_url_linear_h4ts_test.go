package services

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-h4ts: RedactURLUserinfo re-parsed the whole rest of a value for
// every "rest:" prefix it peeled, so HasURLUserinfo on "rest:rest:...:https://u:p@h"
// took quadratic time (100 ms at 20 KB, about 1.6 s at 80 KB), and the env
// table asks it of every line. The bead asked for "< 50 ms at 80 KB"; a wall
// clock bound flakes under -race on a loaded runner (agent-os-yzmh), so this
// bounds how the time GROWS instead: eight times the input takes about 8x as
// long when linear and about 64x when quadratic.
func TestHasURLUserinfo_h4ts_PrefixChainStaysLinearInTime(t *testing.T) {
	chain := func(n int) string { return strings.Repeat("rest:", n/5) + "https://u:p@h" }
	ratio := growthRatioMask2(t, chain(10_000), chain(80_000), func(s string) {
		require.True(t, HasURLUserinfo(s))
	})
	assert.Less(t, ratio, 24.0, "8x the input took %.1fx as long: not linear", ratio)

	ratio = growthRatioMask2(t, chain(10_000), chain(80_000), func(s string) {
		require.False(t, strings.Contains(RedactURLUserinfo(s), "u:p@"), "the credential is shown")
	})
	assert.Less(t, ratio, 24.0, "RedactURLUserinfo: 8x the input took %.1fx as long", ratio)
}

// Past maxRepoPrefixDepth nested prefixes the rest is masked whole, with the
// marker, so the backup settings save guard still refuses a round trip of it.
func TestRedactURLUserinfo_h4ts_BeyondDepthFailsClosed(t *testing.T) {
	v := strings.Repeat("rest:", maxRepoPrefixDepth+3) + "https://u:p@h/x"
	got := RedactURLUserinfo(v)
	assert.Contains(t, got, UserinfoRedactionMarker)
	assert.NotContains(t, got, "u:p")
	assert.True(t, HasURLUserinfo(v))

	// No "://" past the prefixes: nothing nested to read, left as it is.
	plain := strings.Repeat("rest:", maxRepoPrefixDepth+3) + "host/x"
	assert.Equal(t, plain, RedactURLUserinfo(plain))
}

// Up to maxRepoPrefixDepth prefixes the output is exactly the unbounded
// walk's (the code before agent-os-h4ts); deeper, it hides a superset of it.
func TestRedactURLUserinfo_h4ts_SameAsUnboundedWalkWithinDepth(t *testing.T) {
	rng := rand.New(rand.NewSource(0x4475)) //nolint:gosec // G404: a fixed seed keeps the corpus reproducible; nothing here is a secret
	prefixes := []string{"rest:", "s3:", "sftp:", "rclone:", "REST:", " rest:", "b2:"}
	tails := []string{
		"https://u:p@h/x", "https://h/x", "http://u@h", "sftp:user:pw@host:/p",
		"s3:KEY:SECRET@host/bucket", "rclone::sftp,pass=xx:path", "https://u:p@h/x?q=1#f",
		"//u:p@h", "host:/a@b:c", "https://bob:AB/CD@host/r",
	}
	same, deeper := 0, 0
	const cases = 20000
	for c := 0; c < cases; c++ {
		depth := rng.Intn(maxRepoPrefixDepth + 5)
		var b strings.Builder
		for range depth {
			b.WriteString(prefixes[rng.Intn(len(prefixes))])
		}
		b.WriteString(tails[rng.Intn(len(tails))])
		v := b.String()
		unbounded := redactURLUserinfoDepth(v, 0, 1<<30)
		got := RedactURLUserinfo(v)
		if depth <= maxRepoPrefixDepth {
			require.Equal(t, unbounded, got, "value %q", v)
			same++
			continue
		}
		deeper++
		// url.Parse lower-cases a scheme, so a fail-closed value shows its
		// prefixes lower-cased where the walk would have left them as typed.
		assertHidesSupersetMask2(t, asciiLower(unbounded), asciiLower(got), "value="+strconvQuoteMask2(v))
	}
	t.Logf("%d cases within depth identical, %d deeper hide a superset", same, deeper)
}
