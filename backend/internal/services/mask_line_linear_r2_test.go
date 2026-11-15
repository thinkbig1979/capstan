package services

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

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

func growthNeedlesR2() []string {
	var out []string
	for i := 0; i < 40; i++ {
		out = append(out, fmt.Sprintf("needle%02dvalue", i))
	}
	return out
}

func TestMaskSecrets_r2_LinearInLineLength(t *testing.T) {
	needles := growthNeedlesR2()
	best := func(line string, runs int) time.Duration {
		d := time.Duration(1 << 62)
		for range runs {
			t0 := time.Now()
			MaskSecrets(line, "", needles)
			d = min(d, time.Since(t0))
		}
		return d
	}
	for name, u := range growthUnitsR2 {
		mk := func(n int) string { return strings.Repeat(u, n/len(u)+1)[:n] }
		small, large := best(mk(4096), 3), best(mk(32768), 3)
		ratio := float64(large) / float64(max(small, 10*time.Microsecond))
		t.Logf("%-11s n=4096 %v, n=32768 %v, ratio %.1f", name, small, large, ratio)
		assert.Less(t, ratio, 16.0, "%s: 8x the line costs more than linear", name)
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
