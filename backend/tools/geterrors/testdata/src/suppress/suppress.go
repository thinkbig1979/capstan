// Package suppress pins the //geterrors:ignore directive: where it works,
// where it does not, and that the two forms that LOOK like suppression but
// cannot suppress are themselves reported.
//
// The want comments here use the /* ... */ form on purpose. analysistest reads
// a want out of any comment, and the malformed cases below need a bare
// //geterrors:ignore with NOTHING after it -- a want written as a trailing //
// comment would become the directive's reason and suppress the very finding
// the case exists to assert.
package suppress

func get() (string, error) { return "", nil }

// trailing: the directive on the site's own line. Silent.
func trailing() string {
	v, e := get() //geterrors:ignore the caller has a default and cannot act on the fault
	if e == nil {
		return "default"
	}
	return v
}

// above: the directive alone on the line immediately above the site. Silent.
func above() string {
	//geterrors:ignore the caller has a default and cannot act on the fault
	v, e := get()
	if e == nil {
		return "default"
	}
	return v
}

// twoAbove: a directive TWO lines above reaches nothing. A suppression that
// silently drifts off its site is worse than none, because the comment still
// reads as a decision someone made.
func twoAbove() string {
	//geterrors:ignore this one is too far away to apply

	v, e := get() // want "is softened"
	if e == nil {
		return "default"
	}
	return v
}

// trailingDoesNotReachTheNextLine: a TRAILING directive covers its own line
// only. Under a blanket line+1 rule the second site here would be silenced by
// the first site's comment, which is a gate going quiet with nobody deciding
// it should.
func trailingDoesNotReachTheNextLine() (string, string) {
	a, e1 := get() //geterrors:ignore the caller has a default and cannot act on the fault
	b, e2 := get() // want "is softened"
	if e1 == nil && e2 == nil {
		return a, b
	}
	return "", ""
}

// noReason: a directive with nothing after it is itself a finding, and it
// suppresses nothing -- so this line carries BOTH diagnostics.
func noReason() string {
	v, e := get() /* want "needs a reason" "is softened" */ //geterrors:ignore
	if e == nil {
		return "default"
	}
	return v
}

// spaced: `// geterrors:ignore` is an ordinary comment. It reads exactly like
// a suppression and suppresses nothing, so it is reported too.
func spaced() string {
	v, e := get() /* want "must have no space" "is softened" */ // geterrors:ignore a reason that never applies
	if e == nil {
		return "default"
	}
	return v
}
