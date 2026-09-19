//line generated.go:1
package linewiden

// This file pins the SECOND consequence of keying decisions on a position that
// applies //line directives, which is subtler than the forged test-file skip
// in lineforge/ and was OBSERVED rather than reasoned about.
//
// collectDirectives reads the file to tell a TRAILING directive (covers its own
// line only) from a STANDALONE one (covers its line and the next). It reads by
// filename. Under a //line directive the adjusted filename is "generated.go",
// which does not exist, so the read FAILED and the srcErr fallback widened
// EVERY directive in the file to cover line+1 -- silently suppressing the
// unrelated, undirectived site below.
//
// The directive name here is deliberately NOT a _test.go name: if it were, the
// whole file would be skipped and this consequence would be invisible behind
// the other one.

func get() (string, error) { return "", nil }

func two() (string, string) {
	a, e1 := get() //geterrors:ignore the first site is deliberately suppressed, with a reason
	b, e2 := get() // want "is softened"
	if e1 == nil && e2 == nil {
		return a, b
	}
	return "", ""
}
