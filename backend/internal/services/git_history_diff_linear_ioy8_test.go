package services

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// agent-os-ioy8: the history diff's token-span masker searched each needle
// again from every hit plus one, n*m for a self-overlapping needle (the
// shape agent-os-buwq fixed in maskNeedleUnion), and partlyCovered also
// scanned every placeholder span per occurrence. Both are now linear in the
// text: 8x the needle length costs about the same.
func TestHistoryTokenSpans_ioy8_LinearInNeedleLength(t *testing.T) {
	s := strings.Repeat("a", 1<<19)
	best := func(f func()) time.Duration {
		d := time.Duration(1 << 62)
		for range 3 {
			t0 := time.Now()
			f()
			d = min(d, time.Since(t0))
		}
		return d
	}
	for name, run := range map[string]func(m int) func(){
		"maskTokenSpans": func(m int) func() {
			tok := strings.Repeat("a", m)
			return func() { maskTokenSpans(s, tok, "Zq1", nil) }
		},
		"partlyCovered": func(m int) func() {
			v := strings.Repeat("a", m)
			sp := s[:len(s)/2] + "Zq1" + s[len(s)/2:]
			return func() { partlyCovered(sp, "Zq1", []string{v}) }
		},
	} {
		small, large := best(run(8192)), best(run(65536))
		ratio := float64(large) / float64(max(small, time.Microsecond))
		t.Logf("%s: m=8192 %v, m=65536 %v, ratio %.1f", name, small, large, ratio)
		require.Less(t, ratio, 4.0, "%s grows with the needle length", name)
	}
}

// The new code returns what the old did, over a seeded corpus dense in
// overlaps: a two-letter alphabet, short tokens and needles, sentinels
// planted in the text.
func TestHistoryTokenSpans_ioy8_SameAsOld(t *testing.T) {
	rng := rand.New(rand.NewSource(0x1078)) //nolint:gosec // G404: a fixed seed keeps the corpus reproducible
	word := func(lo, hi int) string {
		b := make([]byte, lo+rng.Intn(hi-lo+1))
		for i := range b {
			b[i] = "ab"[rng.Intn(2)]
		}
		return string(b)
	}
	masked, partly := 0, 0
	for c := 0; c < 20000; c++ {
		s := word(0, 40)
		if rng.Intn(3) == 0 {
			i := rng.Intn(len(s) + 1)
			s = s[:i] + "Zq" + s[i:]
		}
		token := word(1, 5)
		var secrets []string
		for n := rng.Intn(4); n > 0; n-- {
			secrets = append(secrets, word(1, 7))
		}
		old := oldMaskTokenSpansIoy8(s, token, "Zq", secrets)
		require.Equal(t, old, maskTokenSpans(s, token, "Zq", secrets), "s=%q token=%q secrets=%q", s, token, secrets)
		if old != s {
			masked++
		}
		needles := secretNeedles(secrets)
		if rng.Intn(2) == 0 {
			needles = append(needles, word(1, 3)+"Z", "q"+word(1, 3), "Zq")
		}
		was := oldPartlyCoveredIoy8(s, "Zq", needles)
		require.Equal(t, was, partlyCovered(s, "Zq", needles), "s=%q needles=%q", s, needles)
		if was {
			partly++
		}
	}
	t.Logf("masked %d, partly covered %d of 20000", masked, partly)
	// The corpus reaches both outcomes of both functions.
	require.Greater(t, masked, 1000)
	require.Less(t, masked, 19000)
	require.Greater(t, partly, 500)
}

// oldPartlyCoveredIoy8 is partlyCovered as it was before agent-os-ioy8.
func oldPartlyCoveredIoy8(s, sentinel string, needles []string) bool {
	var spans [][2]int
	for i := 0; ; {
		j := strings.Index(s[i:], sentinel)
		if j < 0 {
			break
		}
		spans = append(spans, [2]int{i + j, i + j + len(sentinel)})
		i += j + len(sentinel)
	}
	for _, v := range needles {
		for i := 0; ; {
			j := strings.Index(s[i:], v)
			if j < 0 {
				break
			}
			a, b := i+j, i+j+len(v)
			for _, sp := range spans {
				if a < sp[1] && b > sp[0] && (a > sp[0] || b < sp[1]) {
					return true
				}
			}
			i = a + 1
		}
	}
	return false
}

// oldMaskTokenSpansIoy8 is maskTokenSpans as it was before agent-os-ioy8.
func oldMaskTokenSpansIoy8(s, token, sentinel string, secrets []string) string {
	if token == "" || !strings.Contains(s, token) {
		return s
	}
	type span struct {
		a, b int
		tok  bool
	}
	var spans []span
	find := func(v string, tok bool) {
		for i := 0; i <= len(s)-len(v); {
			j := strings.Index(s[i:], v)
			if j < 0 {
				return
			}
			spans = append(spans, span{i + j, i + j + len(v), tok})
			i += j + 1
		}
	}
	find(token, true)
	for _, v := range secretNeedles(secrets) {
		find(v, false)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].a < spans[j].a })
	var b strings.Builder
	at := 0
	for i := 0; i < len(spans); {
		cur := spans[i]
		for i++; i < len(spans) && spans[i].a < cur.b; i++ {
			cur.b = max(cur.b, spans[i].b)
			cur.tok = cur.tok || spans[i].tok
		}
		if cur.tok {
			b.WriteString(s[at:cur.a])
			b.WriteString(sentinel)
			at = cur.b
		}
	}
	b.WriteString(s[at:])
	return b.String()
}
