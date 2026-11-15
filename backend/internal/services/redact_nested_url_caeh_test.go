package services

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-caeh: a credential URL nested inside another URL was not
// redacted. The token scan took the outer URL whole and url.Parse put the
// inner one in the query, so its userinfo was never seen.

var nestedURLCasesCaeh = []struct {
	name, in, want string
}{
	{"nested in a query",
		"see https://proxy.example/fetch?url=https://user:secretpw@host/x",
		"see https://proxy.example/fetch?url=https://***@host/x"},
	{"nested twice",
		"see https://a.example/?u=https://b.example/?v=ftp://user:secretpw@host/x end",
		"see https://a.example/?u=https://b.example/?v=ftp://***@host/x end"},
	{"outer and inner both carry one",
		"https://bob:outerpw@a.example/?next=https://amy:secretpw@b.example/",
		"https://***@a.example/?next=https://***@b.example/"},
	{"percent-encoded",
		"see https://proxy.example/fetch?url=https%3A%2F%2Fuser%3Asecretpw%40host%2Fx&y=1",
		"see https://proxy.example/fetch?url=https%3A%2F%2F***%40host%2Fx&y=1"},
	{"percent-encoded, lower case",
		"see https://proxy.example/fetch?url=https%3a%2f%2fuser%3asecretpw%40host",
		"see https://proxy.example/fetch?url=https%3a%2f%2f***%40host"},
	{"percent-encoded, a '/' in the password",
		"see https://p.example/?url=https%3A%2F%2Fbob%3Asecret%2Fpw%40host%2Fx",
		"see https://p.example/?url=https%3A%2F%2F***%40host%2Fx"},
	{"percent-encoded, username only",
		"see https://p.example/?url=https%3A%2F%2Fsecretpw%40host%2Fx",
		"see https://p.example/?url=https%3A%2F%2F***%40host%2Fx"},
}

func TestRedactURLUserinfoInText_caeh_NestedCredentialRedacted(t *testing.T) {
	for _, c := range nestedURLCasesCaeh {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, RedactURLUserinfoInText(c.in))
			assert.True(t, HasURLUserinfo(c.in), "HasURLUserinfo must see it too")
			assert.NotContains(t, redactComposeOutput(c.in, nil), "secretpw")
			masked, _ := MaskSecrets(c.in, "", nil)
			assert.NotContains(t, masked, "secretpw")
		})
	}
}

// What carries no credential is left byte for byte: a nested URL without
// userinfo, an encoded one without %40, and an encoded "@" in a path after a
// clean host (the rule redactUnparseableUserinfo applies, agent-os-zzhs).
func TestRedactURLUserinfoInText_caeh_NoCredentialUnchanged(t *testing.T) {
	for _, in := range []string{
		"see https://proxy.example/fetch?url=https://host/x",
		"see https://proxy.example/fetch?url=https%3A%2F%2Fhost%2Fx",
		"see https://p.example/?url=https%3A%2F%2Fhost%2Fa%40b",
		"see https://p.example/?url=https%3A%2F%2F%40host%2Fx",
		"see https://p.example/?url=https%3A%2F%2F%3A%40host%2Fx",
		"a://a://a://",
		"git@github.com:org/repo.git and https://host/a@b",
	} {
		assert.Equal(t, in, RedactURLUserinfoInText(in))
		assert.False(t, HasURLUserinfo(in), "%q", in)
	}
}

// The splitter reads each nested URL once: a token of many "scheme://"
// markers must not cost a parse per marker of the whole rest (review of
// agent-os-h4ts asks this of every walk over a value). Bounded by how the
// time grows, as in TestRedactComposeOutput_buwq_PeriodicNeedleStaysLinearInTime.
func TestRedactURLUserinfoInText_caeh_ManyNestedMarkersStayLinear(t *testing.T) {
	small := "see " + strings.Repeat("a://", 2_500) + "u:p@h"
	large := "see " + strings.Repeat("a://", 20_000) + "u:p@h"
	ratio := growthRatioMask2(t, small, large, func(s string) {
		require.False(t, strings.Contains(RedactURLUserinfoInText(s), "u:p@"), "the innermost credential is shown")
	})
	assert.Less(t, ratio, 24.0, "8x the input took %.1fx as long: not linear", ratio)
}

// growthRatioMask2 is how much longer f takes on large than on small, each
// timed five times alternately and the fastest kept, so a load spike has to
// slow every run of one size to move it. A ratio of two sizes in one mode is
// what -race and a loaded runner cannot flip, unlike a wall-clock bound.
func growthRatioMask2(t *testing.T, small, large string, f func(string)) float64 {
	t.Helper()
	best := func(cur time.Duration, s string) time.Duration {
		start := time.Now()
		f(s)
		took := time.Since(start) // wall-clock ok: only a ratio is asserted, see above
		if cur == 0 || took < cur {
			return took
		}
		return cur
	}
	var ts, tl time.Duration
	for range 5 {
		ts = best(ts, small)
		tl = best(tl, large)
	}
	ratio := float64(tl) / float64(max(ts, time.Microsecond))
	t.Logf("%d bytes: %v, %d bytes: %v, ratio %.1f", len(small), ts, len(large), tl, ratio)
	return ratio
}

// wholeTokenRedactCaeh is RedactURLUserinfoInText before agent-os-caeh, kept
// as the oracle the new output must hide at least as much as.
func wholeTokenRedactCaeh(s string) string {
	return urlTokenRe.ReplaceAllStringFunc(s, RedactURLUserinfo)
}

// assertHidesSupersetMask2 fails when got shows a piece of text, between its
// placeholders, that base does not show in the same order.
func assertHidesSupersetMask2(t *testing.T, base, got, context string) {
	t.Helper()
	rest := base
	for _, seg := range strings.Split(got, userinfoPlaceholder) {
		i := strings.Index(rest, seg)
		require.GreaterOrEqual(t, i, 0, "shows %q, which the old pass hid: %s base=%q got=%q", seg, context, base, got)
		rest = rest[i+len(seg):]
	}
}

// Over a seeded corpus of URL fragments glued together, the new text hides a
// superset of the old, and HasURLUserinfo answers true whenever the old did.
func TestRedactURLUserinfoInText_caeh_SupersetOfWholeTokenPass(t *testing.T) {
	rng := rand.New(rand.NewSource(0xcae4)) //nolint:gosec // G404: a fixed seed keeps the corpus reproducible; nothing here is a secret
	parts := []string{
		"https://", "http://", "ftp://", "s3:", "rest:", "sftp:", "//",
		"user", ":", "pw", "@", "host", ".example", ":8080", "/", "x", "?",
		"url=", "&", "#", "=", " ", "%3A", "%2F", "%40", "%3a%2f%2f", "%25",
		"https%3A%2F%2F", "a@b", "\"", "'",
	}
	changed, hasNew := 0, 0
	const cases = 30000
	for c := 0; c < cases; c++ {
		var b strings.Builder
		for k := rng.Intn(12); k >= 0; k-- {
			b.WriteString(parts[rng.Intn(len(parts))])
		}
		text := b.String()
		base, got := wholeTokenRedactCaeh(text), RedactURLUserinfoInText(text)
		if base != got {
			changed++
		}
		assertHidesSupersetMask2(t, base, got, "text="+strconvQuoteMask2(text))
		if base != text {
			require.True(t, HasURLUserinfo(text), "the old pass found userinfo in %q", text)
		}
		if got != text {
			hasNew++
		}
	}
	t.Logf("%d of %d cases differ from the old pass; %d now redacted", changed, cases, hasNew)
}

func strconvQuoteMask2(s string) string { return strconv.Quote(s) }
