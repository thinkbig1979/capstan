// Package typed is the arm the predecessor could not have. It carries the SOFT
// and MERGE SHAPES with second values that are nillable but are NOT errors, so
// the only thing that can keep it silent is go/types.
//
// It is deliberately not a claim that typing removed existing findings: it did
// not. MEASURED on 6c0819c, all 63 sites the predecessor reported are already
// error-typed, and a typed MERGE arm with the name anchor removed finds the
// same 13. What this fixture pins is the PROSPECTIVE half -- that the shapes
// below cannot become findings later.
package typed

import "strings"

type cfg struct{ Name string }

// notAnError is nillable and its name ends "err" at every use site below,
// which is ALL the predecessor's one identifier anchor looked at. It has an
// Error2 method rather than an Error method, so it does not implement error.
type notAnError struct{}

func (notAnError) Error2() string { return "" }

func lookup(string) (string, *cfg)              { return "", nil }
func lookupErrish(string) (string, *notAnError) { return "", nil }
func realLookup(string) (string, error)         { return "", nil }

// CONTROL, and it is not optional. A fixture whose whole job is to stay silent
// cannot tell "the type gate works" from "the analyzer is not running on this
// package at all". This function is the same shape with a real error and MUST
// fire, so the silence of the three below means something.
func softenedRealError(k string) string {
	v, e := realLookup(k) // want "is softened"
	if e == nil {
		return "default"
	}
	return v
}

// softenedPointerSecondValue: the SOFT shape exactly -- a two-value call whose
// last value is only ever compared `== nil` -- with a *cfg in the error's
// position. The predecessor had no way to decline this.
func softenedPointerSecondValue(k string) string {
	v, c := lookup(k)
	if c == nil {
		return "default"
	}
	return v
}

// mergedErrNamedNonError: the MERGE shape with the predecessor's name anchor
// SATISFIED -- `cfgErr` ends in "err" -- and a type that is not an error.
func mergedErrNamedNonError(k string) string {
	v, cfgErr := lookupErrish(k)
	if cfgErr != nil || v == "" {
		return "fallback"
	}
	return v
}

// cutBool covers strings.Cut, whose LAST value -- the one this analyzer looks
// at -- is a bool. The predecessor reported three Cut sites as false positives
// (exec_env.go:81, redact_url.go:246 and :412 on 6c0819c); note that those
// three are DISCARD and are out of scope now for that reason, not because of
// typing. This function is the SOFTENED spelling of the same callee, which is
// the one typing has to decline.
//
// Two notes so this fixture is not over-read. A bool can never produce a SOFT
// finding in the first place, because a bool cannot be compared to nil -- so
// this guards the MERGE-shaped route, not SOFT. And `!found` is not an
// error-nil operand under any reading, so this line has two independent
// reasons to stay silent; softenedPointerSecondValue and
// mergedErrNamedNonError above are the ones where only the type decides.
func cutBool(s string) string {
	before, _, found := strings.Cut(s, "=")
	if !found || before == "" {
		return ""
	}
	return before
}
