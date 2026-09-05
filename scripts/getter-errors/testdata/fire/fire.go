// Package fire is the MUST-FIRE half of check-getter-errors.sh's self-test.
// Every function here is a member of the discarded/softened getter family, and
// each one is a shape that a previously-used sweep MISSED on the real tree.
// A scanner that has silently stopped firing looks exactly like a clean tree,
// so the self-test asserts these by name before any verdict is believed.
package fire

import "errors"

type row struct{ ID string }
type store struct{}

func (store) GetThing(string) (*row, error)      { return nil, nil }
func (store) ListThings() ([]row, error)         { return nil, nil }
func (store) UserCount() (int, error)            { return 0, nil }
func (store) Status(string) (string, int, error) { return "", 0, nil }
func save(*row) error                            { return nil }
func log(...any)                                 {}

var db store

// discardPlain: the base DISCARD shape.
func discardPlain() string {
	thing, _ := db.GetThing("k")
	if thing != nil {
		return thing.ID
	}
	return ""
}

// discardNoVerbPrefix: callee has no Get/List/Find prefix at all, so every
// `\.Get[A-Z]` sweep this family used returns a false zero here
// (handlers/auth.go:127/:144, UserCount).
func discardNoVerbPrefix() bool {
	n, _ := db.UserCount()
	return n == 0
}

// discardThreeValues: the discarded error is the LAST of three, not the second.
func discardThreeValues() string {
	s, _, _ := db.Status("x")
	return s
}

// softUnusualErrorName: the error variable is not spelled `err`, so every
// name-anchored regex misses it (services/docker_update.go:471, sErr).
func softUnusualErrorName() string {
	thing, sErr := db.GetThing("k")
	if sErr == nil && thing != nil {
		return thing.ID
	}
	return ""
}

// softListCallee: the callee is List*, not Get*, so every verb-anchored sweep
// misses it (handlers/directories.go:55/:79/:81).
func softListCallee() int {
	things, e := db.ListThings()
	if e == nil {
		return len(things)
	}
	return -1
}

// softBareEqNil: `err == nil` with no `&& x != nil`, the shape both literal
// patterns this family used were blind to (services/git_credentials.go:158).
func softBareEqNil() string {
	if thing, err := db.GetThing("k"); err == nil {
		return thing.ID
	}
	return ""
}

// softThenShadowed is THE regression test for the prototype's fifth anchor.
// bud5's detector walked the whole enclosing function body and called the
// LATER, UNRELATED `err` at the `if err := save(...)` line a hard use of THIS
// one, so it reported this function clean. That is why it missed
// services/docker_update.go:204 and handlers/updates.go:179/:325/:812 --
// every one of them reuses or shadows `err` further down. The region here
// ends at the shadow, not at the end of the function.
func softThenShadowed() string {
	id := ""
	existing, err := db.GetThing("k")
	if err == nil && existing != nil {
		id = existing.ID
	}
	if err := save(&row{ID: id}); err != nil {
		log("save failed", err)
	}
	return id
}

// softInNestedBlock: the region is the enclosing BLOCK, not the function, so a
// site inside an `if` body is still classified (services/docker.go:452).
func softInNestedBlock(want bool) string {
	if want {
		thing, err := db.GetThing("k")
		if err == nil && thing != nil {
			return thing.ID
		}
	}
	return ""
}

var _ = errors.Is
