package services

import (
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-buwq: masking used to replace one secret at a time, so once a
// secret became "***" a second secret overlapping it no longer matched and
// the part of it outside the first stayed visible.

const overlapTextBuwq = "out abcdefghijklmn end"

var overlapSecretsBuwq = []string{"abcdefgh", "fghijklmn"}

func TestMaskSecrets_buwq_OverlappingSecretsBothHidden(t *testing.T) {
	got, n := MaskSecrets(overlapTextBuwq, "", overlapSecretsBuwq)
	assert.Equal(t, "out *** end", got)
	assert.Positive(t, n)
}

func TestRedactComposeOutput_buwq_OverlappingSecretsBothHidden(t *testing.T) {
	for _, secrets := range [][]string{
		overlapSecretsBuwq,
		{overlapSecretsBuwq[1], overlapSecretsBuwq[0]},
	} {
		assert.Equal(t, "out *** end", redactComposeOutput(overlapTextBuwq, secrets), "secrets %q", secrets)
	}
}

// A line of a multi-line secret and a one-line secret overlap in a lint
// message: masking them in two passes let the line lose its match.
func TestLintCompose_buwq_LineAndSecretOverlapHidden(t *testing.T) {
	compose := "services:\n  db:\n    image: postgres:16\n    cpus: abcdefgh\n"
	env := LintEnv{Vars: map[string]string{"TLS_KEY": "xxxx\nabcdef\nyyyy"}, Secrets: []string{"defgh"}}
	results, err := NewLinterService().LintCompose(compose, t.TempDir(), env, true)
	require.NoError(t, err)
	var msgs []string
	for _, r := range results {
		msgs = append(msgs, r.Message)
	}
	got := strings.Join(msgs, "\n")
	require.NotEmpty(t, got, "the message must echo the masked literal for this test to mean anything")
	assert.Contains(t, got, userinfoPlaceholder)
	assert.NotContains(t, got, "abc")
}

// sequentialRedactBuwq is the masking before the fix, kept as the oracle the
// new output must hide at least as much as.
func sequentialRedactBuwq(s string, needles []string) string {
	for _, v := range needles {
		s = strings.ReplaceAll(s, v, userinfoPlaceholder)
	}
	return s
}

// coverageRedactBuwq masks, one byte at a time, every byte inside any
// occurrence (overlapping ones included) of any needle, each maximal run of
// overlapping occurrences as one placeholder. A naive second implementation.
func coverageRedactBuwq(s string, needles []string) string {
	end := make([]int, len(s))
	for p := range s {
		for _, v := range needles {
			if strings.HasPrefix(s[p:], v) && p+len(v) > end[p] {
				end[p] = p + len(v)
			}
		}
	}
	var b strings.Builder
	for p := 0; p < len(s); {
		if end[p] == 0 {
			b.WriteByte(s[p])
			p++
			continue
		}
		e := end[p]
		for q := p + 1; q < e; q++ {
			e = max(e, end[q])
		}
		b.WriteString(userinfoPlaceholder)
		p = e
	}
	return b.String()
}

// The new output hides a superset of what the old one hid: every piece it
// shows between placeholders is shown, in order, by the old one too. And it
// equals the naive per-byte union.
func TestRedactComposeOutput_buwq_SupersetOfSequentialPass(t *testing.T) {
	rng := rand.New(rand.NewSource(0xb0a)) //nolint:gosec // G404: a fixed seed keeps the corpus reproducible; nothing here is a secret
	// No '*': see the "***gh" pin below.
	const alphabet = "abcd "
	randStr := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(b)
	}
	differed := 0
	for c := 0; c < 20000; c++ {
		text := randStr(rng.Intn(41))
		var needles []string
		for k := 1 + rng.Intn(5); k > 0; k-- {
			l := 4 + rng.Intn(6)
			if len(text) >= l && rng.Intn(4) > 0 {
				a := rng.Intn(len(text) - l + 1)
				needles = append(needles, text[a:a+l])
			} else {
				needles = append(needles, randStr(l))
			}
		}
		got := redactComposeOutput(text, needles)
		require.Equal(t, coverageRedactBuwq(text, needles), got, "text=%q needles=%q", text, needles)
		base := sequentialRedactBuwq(text, needles)
		if base != got {
			differed++
		}
		rest := base
		for _, seg := range strings.Split(got, userinfoPlaceholder) {
			i := strings.Index(rest, seg)
			require.GreaterOrEqual(t, i, 0, "shows %q, which the old pass hid: text=%q needles=%q base=%q got=%q", seg, text, needles, base, got)
			rest = rest[i+len(seg):]
		}
	}
	t.Logf("%d of 20000 cases differ from the old pass", differed)
}

// Pin, not a regression: a secret with a '*' at an edge (one is enough, see
// "*ghi" below) can match the old pass's own placeholder, so the old pass hid
// bytes here by chaining. Those bytes are not inside any occurrence of a
// secret in the text; the single pass on the original shows them.
func TestRedactComposeOutput_buwq_SecretHoldingPlaceholderPin(t *testing.T) {
	needles := []string{"abcdef", "***gh"}
	assert.Equal(t, "***", sequentialRedactBuwq("abcdefgh", needles))
	assert.Equal(t, "***gh", redactComposeOutput("abcdefgh", needles))
	got, _ := MaskSecrets("x abcdefghi y", "", []string{"abcdef", "*ghi"})
	assert.Equal(t, "x ***ghi y", got)
	assert.Equal(t, "x ***** y", sequentialRedactBuwq("x abcdefghi y", []string{"abcdef", "*ghi"}))
}

func allocatedBuwq(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// Masking runs on whole files (PR #736 had a 560 KB one). Bounded by bytes
// allocated, not wall clock, so -race and a loaded runner cannot flip it.
// Blind to a quadratic that does not allocate: see the periodic test below.
func TestRedactComposeOutput_buwq_UnionAllocationStaysLinear(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		needles []string
	}{
		// One needle overlapping itself at every byte.
		{"periodic", strings.Repeat("a", 560_000), []string{strings.Repeat("a", 64), "bbbb"}},
		// Many short overlapping needles giving many separate runs: a
		// per-run copy of the text is quadratic here.
		{"many runs", strings.Repeat("abcdef ", 8000), []string{"abcd", "bcde", "cdef", "def ", "abcdef"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out string
			allocated := allocatedBuwq(func() { out = redactComposeOutput(c.text, c.needles) })
			require.NotContains(t, out, "abc")
			require.NotContains(t, out, "aaaa")
			ratio := float64(allocated) / float64(len(c.text))
			t.Logf("%d bytes masked, %d bytes allocated (%.1fx)", len(c.text), allocated, ratio)
			assert.Less(t, ratio, 50.0, "masking allocated %d bytes for %d: not linear", allocated, len(c.text))
		})
	}
}

// A needle that overlaps itself (a long run of one byte) found again from
// every hit compares the whole needle at every byte: n*m, about 6.5 s for a
// text of 1e6 bytes and a needle of 5e5 (review of agent-os-buwq, F1). A
// wall-clock bound cannot hold that apart from a linear scan under -race,
// which slows the Go loop about 16x and not strings.Index's assembly, so this
// bounds how the time GROWS: eight times the text and needle takes about 8x
// as long when linear and up to 64x when quadratic (31x measured under
// -race), whatever slows both runs alike.
// The sizes alternate, five runs each, and each takes its fastest, so a
// load spike has to slow every run of one size to move the ratio.
func TestRedactComposeOutput_buwq_PeriodicNeedleStaysLinearInTime(t *testing.T) {
	run := func(text string, needles []string) time.Duration {
		start := time.Now()
		out := redactComposeOutput(text, needles)
		took := time.Since(start) // wall-clock ok: a ratio of two sizes in one mode, see above
		require.Equal(t, userinfoPlaceholder, out)
		return took
	}
	smallText, largeText := strings.Repeat("a", 125_000), strings.Repeat("a", 1_000_000)
	smallNeedles := []string{smallText[:len(smallText)/2]}
	largeNeedles := []string{largeText[:len(largeText)/2]}
	small, large := time.Duration(1<<62), time.Duration(1<<62)
	for range 5 {
		small = min(small, run(smallText, smallNeedles))
		large = min(large, run(largeText, largeNeedles))
	}
	ratio := float64(large) / float64(small)
	t.Logf("n=125000 took %v, n=1000000 took %v: %.1fx", small, large, ratio)
	assert.Less(t, ratio, 20.0, "8x the input took %.1fx as long: not linear in time", ratio)
}
