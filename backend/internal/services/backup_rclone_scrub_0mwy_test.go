package services

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-0mwy: scrubRclone masked the remote, then each parameter value,
// one strings.ReplaceAll at a time, so two overlapping values left part of
// the second shown: "abcdefghijk" with values abcdef and defghijk came out
// "***ghijk".

func TestScrubRclone_0mwy_OverlappingValuesBothHidden(t *testing.T) {
	remote := ":s3,access_key_id=abcdef,secret_access_key=defghijk"
	secrets := rcloneSecrets(remote)
	assert.Equal(t, "error near *** end", scrubRclone("error near abcdefghijk end", secrets, RedactRcloneRemote(remote)))
}

// The remote echoed whole still reads as its redacted form (TestQnso pins
// "secret_access_key=***" in the stream).
func TestScrubRclone_0mwy_EchoedRemoteShowsRedactedForm(t *testing.T) {
	remote := ":s3,access_key_id=abcdef,secret_access_key=defghijk"
	got := scrubRclone(`Failed to create file system for "`+remote+`:bucket"`, rcloneSecrets(remote), RedactRcloneRemote(remote))
	assert.Equal(t, `Failed to create file system for ":s3,access_key_id=***,secret_access_key=***:bucket"`, got)
}

// sequentialScrub0mwy is scrubRclone before agent-os-0mwy, the oracle the new
// output must hide at least as much as.
func sequentialScrub0mwy(s string, secrets []string, masked string) string {
	if len(secrets) == 0 {
		return s
	}
	s = strings.ReplaceAll(s, secrets[0], masked)
	for _, v := range secrets[1:] {
		s = strings.ReplaceAll(s, v, userinfoPlaceholder)
	}
	return s
}

// Over a seeded corpus of remotes and texts built from their pieces, the
// new scrub hides a superset of the old. Text the redacted remote itself
// adds (its key names) is not text of the line, so a placeholder run of the
// new output is compared with that form taken out of both.
func TestScrubRclone_0mwy_SupersetOfSequentialScrub(t *testing.T) {
	rng := rand.New(rand.NewSource(0x0a3)) //nolint:gosec // G404: a fixed seed keeps the corpus reproducible; nothing here is a secret
	// Two letters, so values overlap each other and the text often.
	const alphabet = "ab"
	word := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(b)
	}
	differed := 0
	const cases = 20000
	for c := 0; c < cases; c++ {
		remote := ":s3"
		for k := 1 + rng.Intn(3); k > 0; k-- {
			remote += ",k" + word(1) + "=" + word(3+rng.Intn(4))
		}
		secrets := rcloneSecrets(remote)
		var b strings.Builder
		for k := rng.Intn(6); k >= 0; k-- {
			switch rng.Intn(3) {
			case 0:
				b.WriteString(word(1 + rng.Intn(8)))
			case 1:
				b.WriteString(secrets[1+rng.Intn(len(secrets)-1)])
			default:
				b.WriteString(remote)
			}
		}
		text := b.String()
		masked := RedactRcloneRemote(remote)
		base := strings.ReplaceAll(sequentialScrub0mwy(text, secrets, masked), masked, userinfoPlaceholder)
		got := scrubRclone(text, secrets, masked)
		if got != sequentialScrub0mwy(text, secrets, masked) {
			differed++
		}
		assertHidesSupersetMask2(t, base, strings.ReplaceAll(got, masked, userinfoPlaceholder), "remote="+strconvQuoteMask2(remote)+" text="+strconvQuoteMask2(text))
	}
	t.Logf("%d of %d cases differ from the old scrub", differed, cases)
}
