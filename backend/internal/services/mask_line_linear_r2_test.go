package services

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Review round 2 of run/maskfu (rv-dm-maskfu, HIGH): the line maskers
// rescanned a line from its start after every value they masked, and each
// mask recomputed the needle runs, so one long line of many maskable
// tokens cost n*k. maskLogLine runs this on every locked log line up to
// 1 MiB. Each masker is now one left-to-right pass.

// growthUnitsR2 are the reviewer's shapes (a, c, f), one per line rule, and
// the two base shapes the reviewer measured on d793706.
var growthUnitsR2 = map[string]string{
	"query":       "&token=x",
	"json":        ` {"password":"x"}`,
	"requirepass": " --requirepass x",
	"auth":        " Authorization: Bearer x",
	"inlineKV":    " DB_PASSWORD=x",
	"flowToken":   " [API_TOKEN=x]",
	"flowColon":   ` {"api_key": "x"}`,
	"flagEq":      " --masterauth=x",
	"statements":  ` NOTES="a" DB_PASSWORD="x"`,
	// Base shapes: the rescan was there before run/maskfu.
	"basePassword": " --password x",
	"baseJSON":     ` {"password": "x"}`,
}

// growthUnitsExtra are later shapes, kept out of growthUnitsR2 because that
// map also builds the equality corpus (corpusR2), whose recordings would
// then no longer match: JSON headers and cookies (agent-os-qmw2) and quoted
// values (agent-os-bfqc), unclosed flow values (agent-os-ez5s), inline values (agent-os-rtf7).
var growthUnitsExtra = map[string]string{
	"jsonCookie":    ` {"cookie":"a"}`,
	"jsonAuthArray": ` {"Authorization":["Bearer x"]}`,
	"quotedFlag":    ` --requirepass='x y'`,
	"quotedPlain":   ` x='a b=c d'`,
	// Unclosed flow values (agent-os-ez5s): each ran to the line's end.
	"flowOpenArr": ` {"foo":[`,
	"flowOpenObj": ` {"cookie":{`,
	"flowOpenKey": ` {"a":`,
	"flowOpenEq":  ` [a=`,
	// Inline values a ( does not stop (agent-os-rtf7).
	"inlineOpenParen":  `a=(`,
	"inlineParenKey":   `(a=`,
	"inlineQueryParen": `?a=(`,
}

func growthNeedlesR2() []string {
	var out []string
	for i := 0; i < 40; i++ {
		out = append(out, fmt.Sprintf("needle%02dvalue", i))
	}
	return out
}

// allocBytes is the fewest bytes one call of f allocated over three calls
// (after a warm-up): bytes, not time, so a GC pause or a loaded machine
// cannot move it (review round 3, N2: the wall-clock bound flaked on CI).
// The rescan built a new line per masked value, n*k bytes; one pass
// allocates O(n). TotalAlloc is process-wide, so the fewest of three
// discards a call that overlapped a stray goroutine's allocation.
func allocBytes(f func()) uint64 {
	f()
	best := uint64(1 << 63)
	var a, b runtime.MemStats
	for range 3 {
		runtime.ReadMemStats(&a)
		f()
		runtime.ReadMemStats(&b)
		best = min(best, b.TotalAlloc-a.TotalAlloc)
	}
	return best
}

// growthBoundR2 is the most 8x the input may multiply the bytes allocated
// by. Measured on e184387: 5.7-8.9 (MaskSecrets), 6.6-8.4 (runtime list);
// on 1e4d8e4, the rescan: 68-90 for every rescanning rule, 43-46 for the
// URL values.
const growthBoundR2 = 16.0

func TestMaskSecrets_r2_LinearInLineLength(t *testing.T) {
	needles := growthNeedlesR2()
	units := map[string]string{}
	for _, m := range []map[string]string{growthUnitsR2, growthUnitsExtra} {
		for k, v := range m {
			units[k] = v
		}
	}
	for name, u := range units {
		mk := func(n int) string { return strings.Repeat(u, n/len(u)+1)[:n] }
		smallLine, largeLine := mk(4096), mk(32768)
		small := allocBytes(func() { MaskSecrets(smallLine, "", needles) })
		large := allocBytes(func() { MaskSecrets(largeLine, "", needles) })
		ratio := float64(large) / float64(max(small, 1))
		t.Logf("%-12s n=4096 %d B, n=32768 %d B, ratio %.1f", name, small, large, ratio)
		assert.Less(t, ratio, growthBoundR2, "%s: 8x the line allocates more than linear", name)
	}
}

// corpusR2 is the equality corpus: the mask2 env corpus through every path,
// and random lines of the growth units mixed with the corpus values.
func corpusR2() []string {
	rng := rand.New(rand.NewSource(0x5a2)) //nolint:gosec // G404: a fixed seed keeps the corpus reproducible
	var out []string
	for c := 0; c < 1500; c++ {
		out = append(out, corpusEnvMask2(rng))
	}
	var units []string
	for _, u := range growthUnitsR2 {
		units = append(units, u)
	}
	units = append(units, corpusValuesMask2...)
	units = append(units, " ", ",", "(", "[", "?", "&", ";", "=", ":", `"`, "'", "--password", " -- ", "x(--a ", "${X}", "#")
	// Map order is random: sort for a reproducible corpus.
	sortStrings(units)
	for c := 0; c < 3000; c++ {
		var b strings.Builder
		for n := 1 + rng.Intn(12); n > 0; n-- {
			b.WriteString(units[rng.Intn(len(units))])
		}
		out = append(out, b.String())
	}
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// With MASK_R2_WRITE=<file> it records every output; with
// MASK_R2_CHECK=<file> (recorded before the change) every output must be
// byte-for-byte the recorded one. With neither it only runs the corpus.
func TestMaskSecrets_r2_SameAsBeforeOverCorpus(t *testing.T) {
	writePath, checkPath := os.Getenv("MASK_R2_WRITE"), os.Getenv("MASK_R2_CHECK")
	needles := append(growthNeedlesR2(), "SEKRITa1", "x(--a", "u:SEKRITd4@h")
	var got []string
	for _, text := range corpusR2() {
		for _, path := range []string{"docker-compose.yml", ".env", ""} {
			for _, sec := range [][]string{nil, needles} {
				out, n := MaskSecrets(text, path, sec)
				got = append(got, fmt.Sprintf("%d\x00%s", n, out))
			}
		}
	}
	if writePath != "" {
		f, err := os.Create(writePath) //nolint:gosec // G304: a test fixture path from the person running it
		require.NoError(t, err)
		w := bufio.NewWriter(f)
		for _, g := range got {
			line, err := json.Marshal(g)
			require.NoError(t, err)
			_, err = w.Write(append(line, '\n'))
			require.NoError(t, err)
		}
		require.NoError(t, w.Flush())
		require.NoError(t, f.Close())
	}
	if checkPath == "" {
		return
	}
	f, err := os.Open(checkPath) //nolint:gosec // G304: as above
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	k := 0
	for sc.Scan() {
		var want string
		require.NoError(t, json.Unmarshal(sc.Bytes(), &want))
		require.Less(t, k, len(got))
		require.Equal(t, want, got[k], "case %d", k)
		k++
	}
	require.NoError(t, sc.Err())
	require.Equal(t, len(got), k, "the corpus must be the recorded one")
	t.Logf("compared %d outputs", k)
}

// Review round 2 F3: the runtime list read each env value through the
// masker at every span depth, so it inherited the rescan; 8 KiB of value
// cost 30x 1 KiB. The reviewer's shapes.
func TestContainerSecretValues_r2_LinearInValueLength(t *testing.T) {
	for _, u := range []string{` {"k":"https://u:p@h"}`, " a=b c", ` "password":"x"`, " https://u:p@h?token=q"} {
		took := func(n int) uint64 {
			v := strings.Repeat(u, n/len(u)+1)[:n]
			in := container.InspectResponse{Config: &container.Config{Env: []string{"CFG=" + v}}, ContainerJSONBase: &container.ContainerJSONBase{}}
			return allocBytes(func() { containerSecretValues(in) })
		}
		small, large := took(1024), took(8192)
		ratio := float64(large) / float64(max(small, 1))
		t.Logf("%-26q 1 KiB %d B, 8 KiB %d B, ratio %.1f", u, small, large, ratio)
		assert.Less(t, ratio, growthBoundR2, "%q: 8x the value allocates more than linear", u)
	}
}
