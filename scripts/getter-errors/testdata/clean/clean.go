// Package clean is the MUST-NOT-FIRE half of check-getter-errors.sh's
// self-test. Every function here handles its error properly, and several are
// deliberately close to the fire/ shapes: same callees, same variable names,
// same `== nil` text. A check that fires on one known instance proves it
// matches SOMETHING; only a fixture that must stay silent shows it is not
// matching everything.
package clean

import (
	"database/sql"
	"errors"
	"fmt"
)

type row struct{ ID string }
type store struct{}

func (store) GetThing(string) (*row, error) { return nil, nil }
func (store) ListThings() ([]row, error)    { return nil, nil }
func log(...any)                            {}

var db store

// converted: the shape this family's fixes produce.
func converted() (string, error) {
	thing, err := db.GetThing("k")
	if err != nil {
		return "", err
	}
	return thing.ID, nil
}

// discriminated: `== nil` appears, but so does a real classification.
func discriminated() (string, error) {
	thing, err := db.GetThing("k")
	switch {
	case err == nil:
		return thing.ID, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	default:
		return "", fmt.Errorf("read thing: %w", err)
	}
}

// loggedThenSoftened: softened for the caller but the fault IS recorded, so it
// is not silent. `== nil` plus any other use is a hard use.
func loggedThenSoftened() int {
	things, err := db.ListThings()
	if err == nil {
		return len(things)
	}
	log("list failed", err)
	return -1
}

// reassignedBeforeUse: the first read's error is replaced before anything
// looks at it. The region rule must stop at the reassignment rather than
// borrow the second read's `== nil`.
func reassignedBeforeUse() (string, error) {
	thing, err := db.GetThing("a")
	thing, err = db.GetThing("b")
	if err != nil {
		return "", err
	}
	return thing.ID, nil
}

// blankIsNotTheLastValue: the blank is the VALUE, not the error, and the error
// is returned. A shape-only DISCARD rule that keyed on "any `_` on the left"
// would fire here.
func blankIsNotTheLastValue() error {
	_, err := db.GetThing("k")
	return err
}

// closureUse: the error escapes into a closure, which is a real use.
func closureUse() func() {
	thing, err := db.GetThing("k")
	if err == nil {
		log(thing)
	}
	return func() { log("later", err) }
}
