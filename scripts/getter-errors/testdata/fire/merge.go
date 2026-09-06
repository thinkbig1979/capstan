// merge.go is the MUST-FIRE half of the self-test for the MERGE kind
// (agent-os-8f2g): an error that IS checked, but is fused by `||` to a value
// test, so "I could not read it" and "I read it and the answer is no" take one
// branch.
//
// Every function here is a shape that a text sweep for this class returned a
// FALSE ZERO on, on the real tree, at least once. They are named for the
// mechanism, not for the site, because the point of the fixture is the
// mechanism: a scanner that has silently stopped seeing one of these looks
// exactly like a clean tree, and a close reason will cite its zero.
package fire

// mergeLeftAnchored: the base shape, error on the LEFT of the ||. The only one
// every previously-used arm could see.
func mergeLeftAnchored() string {
	thing, err := db.GetThing("k")
	if err != nil || thing == nil {
		return ""
	}
	return thing.ID
}

// mergeRightAnchored: error on the RIGHT. Both arms in agent-os-koy9's brief
// were left-anchored and both were blind to handlers/auth.go:346, which is
// this shape. If this row ever disappears, the instrument has regrown the
// operand-order blindness the kind was created to end.
func mergeRightAnchored() string {
	thing, cmpErr := db.GetThing("k")
	if thing == nil || cmpErr != nil {
		return ""
	}
	return thing.ID
}

// mergeUnusualErrorName: sErr / listErr / dbErr / pErr all evaded the `err`
// anchor on the real tree (agent-os-g482, obgr, r1by).
func mergeUnusualErrorName() int {
	n, listErr := db.UserCount()
	if listErr != nil || n == 0 {
		return -1
	}
	return n
}

// mergeSelectorError: the error reached through a field rather than a bare
// ident, so a pattern anchored on an identifier alone cannot see it. The
// receiver expression is deliberately not part of what is matched.
type result struct {
	Err   error
	Value string
}

func mergeSelectorError(r result) string {
	if r.Err != nil || r.Value == "" {
		return "fallback"
	}
	return r.Value
}

// mergeIfInit: the call and the test on ONE line, with the error bound in the
// if's init clause. No arm anchored on a line beginning `if <err> !=` can
// match this, and it hid two sites (handlers/compose.go:452,
// services/backup_restic.go:351) from every sweep ever run on this class.
// Neither had been dispositioned when this fixture was written.
func mergeIfInit() string {
	if thing, rerr := db.GetThing("k"); rerr != nil || thing == nil {
		return ""
	}
	return "ok"
}

// mergeThreeOperands: `a || b || c` parses as `(a || b) || c`, so a check that
// looked only at Cond.X and Cond.Y would miss the first operand. Three-way
// conditions are real on this tree (handlers/settings.go,
// middleware/ratelimit.go).
func mergeThreeOperands() int {
	n, err := db.UserCount()
	if err != nil || n < 0 || n > 255 {
		return 0
	}
	return n
}

// mergeNilOnTheLeft: `nil != err` is legal Go, and checking only one operand
// order of the COMPARISON is the same category of blindness as checking only
// one operand order of the `||`.
func mergeNilOnTheLeft() string {
	thing, err := db.GetThing("k")
	if nil != err || thing == nil {
		return ""
	}
	return thing.ID
}

// mergeTaglessSwitchCase: a `switch { case ... }` is the same branch as an
// `if`, written the other way, and an IfStmt-only walk is blind to it. This
// row is here because the detector WAS blind to it: the first version passed
// every other arm in this file, and then stayed GREEN when a known in-class
// site was reintroduced into backend/internal/services/backup_runner.go as a
// switch case. That is the shape the fix for agent-os-89ut is itself written
// in, so the ratchet was blind exactly where its own remedy lives.
func mergeTaglessSwitchCase() string {
	thing, err := db.GetThing("k")
	switch {
	case thing == nil || err != nil:
		return ""
	default:
		return thing.ID
	}
}
