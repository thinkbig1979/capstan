package services

import (
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// agent-os-30iu: maskPatch swaps the token's spans for a placeholder while
// the secret masker runs. A secret that is part of the placeholder, or that
// matches across its edge, was masked inside it, so the placeholder was no
// longer found and a fragment of it was shown.

const (
	tok30iu  = "ghp_tokenvalue30iu"
	diff30iu = "diff --git a/n.txt b/n.txt\n--- a/n.txt\n+++ b/n.txt\n@@ -1 +1 @@\n-old\n+url " + tok30iu + " here"
)

func assertNoPlaceholder30iu(t *testing.T, out string) {
	t.Helper()
	assert.NotContains(t, out, tok30iu)
	for _, frag := range []string{"MaskedTokenSpan", "Masked", "9wwa", "Zq"} {
		assert.NotContains(t, out, frag, "a fragment of the placeholder is shown")
	}
}

func TestMaskPatch30iu_ShortSecretInsidePlaceholder(t *testing.T) {
	// "Capstan" and "Span" sat inside the first fixed placeholder; "9wwa here"
	// spanned its end and the text after it.
	for _, secret := range []string{"Capstan", "Span", "9wwa here"} {
		t.Run(secret, func(t *testing.T) {
			out := maskPatch(diff30iu, tok30iu, &diffMasking{secrets: []string{secret}})
			assert.Contains(t, out, "\n+url ***", "the token span is not masked as ***")
			assertNoPlaceholder30iu(t, out)
		})
	}
}

// A secret that continues the first candidate's end into the text after the
// span: that candidate is passed over, not the diff.
func TestMaskPatch30iu_SecretAcrossCandidateEdge(t *testing.T) {
	c := tokenSpanCandidate(0)
	out := maskPatch(diff30iu, tok30iu, &diffMasking{secrets: []string{c[len(c)-3:] + " here"}})
	assert.Equal(t, "+url *** here", out[strings.LastIndex(out, "\n")+1:])
	assertNoPlaceholder30iu(t, out)
}

// The control: a secret unrelated to the placeholder.
func TestMaskPatch30iu_UnrelatedSecret(t *testing.T) {
	out := maskPatch(diff30iu, tok30iu, &diffMasking{secrets: []string{"hunter22"}})
	assert.Contains(t, out, "\n+url *** here")
}

// Review S5: the diff already holds the first candidate's text. The restore
// must not rewrite it.
func TestMaskPatch30iu_DiffAlreadyHoldsCandidate(t *testing.T) {
	c := tokenSpanCandidate(0)
	diff := "diff --git a/n.txt b/n.txt\n--- a/n.txt\n+++ b/n.txt\n@@ -1,2 +1,2 @@\n note " + c + "\n-old\n+url " + tok30iu + " here"
	out := maskPatch(diff, tok30iu, &diffMasking{secrets: []string{"hunter22"}})
	assert.Contains(t, out, "\n note "+c+"\n", "the diff's own text was rewritten")
	assert.Contains(t, out, "\n+url *** here")
	assert.NotContains(t, out, tok30iu)
}

// No candidate is safe: "l Zq" runs from the text before every span into
// the prefix every candidate starts with. The diff fails closed to its file
// names.
func TestMaskPatch30iu_NoSafePlaceholderFailsClosed(t *testing.T) {
	out := maskPatch(diff30iu, tok30iu, &diffMasking{secrets: []string{"l Zq"}})
	assert.Equal(t, "diff --git a/n.txt b/n.txt", out)
}

// Review S4: a PEM-sized secret (64-character base64 lines) must not push an
// ordinary diff to fail closed. With a placeholder chosen by its edges alone,
// 9% (2048-bit) to 41% (4096-bit) of such diffs showed only file names.
func TestMaskPatch30iu_PEMSecretRarelyFailsClosed(t *testing.T) {
	// Base64 lines from sha256 of a counter: repeatable, and spread like a
	// real key's.
	n := 0
	line := func() string {
		var raw []byte
		for len(raw) < 48 {
			n++
			sum := sha256.Sum256([]byte(strconv.Itoa(n)))
			raw = append(raw, sum[:]...)
		}
		return base64.StdEncoding.EncodeToString(raw[:48]) // 64 characters
	}
	failed := 0
	const trials = 200
	for range trials {
		var lines []string
		for range 50 { // ~4096-bit key
			lines = append(lines, line())
		}
		pem := "-----BEGIN PRIVATE KEY-----\n" + strings.Join(lines, "\n") + "\n-----END PRIVATE KEY-----"
		out := maskPatch(diff30iu, tok30iu, &diffMasking{secrets: []string{pem}})
		if !strings.Contains(out, "\n+url *** here") {
			failed++
		}
	}
	assert.Zero(t, failed, "%d of %d diffs failed closed", failed, trials)
}
