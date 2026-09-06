// merge.go is the MUST-NOT-FIRE half of the self-test for the MERGE kind
// (agent-os-8f2g). Every function here uses the same callees, the same error
// names and the same `!= nil` text as fire/merge.go, and none of them is a
// member of the class. A control that fires on a known instance proves the
// instrument matches SOMETHING; only a fixture that must stay silent shows it
// is not matching everything.
package clean

// errorCheckedAlone: the correct shape. An error checked on its own, with the
// value tested separately, is what the fix for every site in this class
// produces — so if this ever fires, the ratchet flags its own remedy.
func errorCheckedAlone() string {
	thing, err := db.GetThing("k")
	if err != nil {
		log("read failed", err)
		return ""
	}
	if thing == nil {
		return ""
	}
	return thing.ID
}

// twoErrorsNoValue is the membership rule's second half, live: two faults
// sharing a branch is a DIFFERENT question from a fault merged with a value,
// and it is deliberately out of class. services/scheduler.go:497 is this shape
// and belongs to agent-os-rltu; a ratchet that counted it would bless that
// bead's site as accepted and would also report a "fix" when rltu lands.
func twoErrorsNoValue() string {
	a, timeErr := db.GetThing("time")
	b, daysErr := db.GetThing("days")
	if timeErr != nil || daysErr != nil {
		log("both reads failed", timeErr, daysErr)
		return ""
	}
	return a.ID + b.ID
}

// andNotOr: `&&` does not merge anything. `err != nil && v == ""` is
// "the read failed AND the value is empty", which is a narrower branch, not a
// conflation of two states.
func andNotOr() string {
	thing, err := db.GetThing("k")
	if err != nil && thing == nil {
		return ""
	}
	if err != nil {
		log("read failed", err)
		return ""
	}
	return thing.ID
}

// noErrorOperand: an `||` of two plain value tests. Nothing here is a read
// that could have failed.
func noErrorOperand(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return true
}

// notAnErrorName: `ptr != nil || v == ""` reads exactly like the class in
// text, and is not it. The name anchor is the one thing this instrument cannot
// avoid, so this pins what it must NOT sweep in.
func notAnErrorName(v string) string {
	thing, err := db.GetThing("k")
	if err != nil {
		return ""
	}
	if thing != nil || v == "" {
		return "either"
	}
	return v
}

// commentedShape carries the class in PROSE. handlers/terminal.go:99 is a
// comment of exactly this kind and a text sweep counts it; so did a draft fix
// that quoted its own pre-fix code, which made the arm report an unchanged
// count after a real conversion. An AST walk cannot see either, and this
// function is the standing proof of that rather than a claim about it:
//
//	if err != nil || thing == nil {
//	    return ""
//	}
//
// The line above is not code. If the MERGE count for clean/ ever moves to 1,
// the detector has been reimplemented on text.
func commentedShape() string {
	return "no branch here at all"
}

// taggedSwitchCase: in `switch x { case a || b: }` the case expression is
// COMPARED against x rather than evaluated as a branch condition, so it is a
// value and not a member of this class. Counting it would be a false positive
// that no arm catches, because an over-wide instrument fires correctly on
// everything it matches.
func taggedSwitchCase(v bool) string {
	thing, err := db.GetThing("k")
	if err != nil {
		return ""
	}
	switch v {
	case thing == nil || v:
		return "matched"
	default:
		return thing.ID
	}
}
