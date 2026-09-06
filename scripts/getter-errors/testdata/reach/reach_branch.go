// Package reach's SECOND fixture: a converted site whose `if err != nil` body
// holds SEVERAL coverage blocks. One of them running proves a fault ARRIVED;
// it does not prove every exit of the handler is exercised. Two hand-paired
// profiles pin both answers over this one source -- cov_branch_all.txt, where
// every in-body block ran, and cov_branch_partial.txt, where the fall-through
// `return "", err` at line 23 never did.
//
// This is the shape that made services/git.go:101 and :726 read REACHED while
// one exit of each was dead (agent-os-hsj7).
package reach

import "errors"

type brow struct{ ID string }
type bstore struct{}

var errAbsent = errors.New("absent")
var errBroken = errors.New("broken")

func (bstore) GetThing(k string) (*brow, error) {
	if k == "a" {
		return nil, errAbsent
	}
	return nil, errBroken
}

var bdb bstore

func branchRead(k string) (string, error) {
	thing, err := bdb.GetThing(k)
	if err != nil {
		if errors.Is(err, errAbsent) {
			return "", nil
		}
		return "", err
	}
	return thing.ID, nil
}

// DO NOT ADD OR REMOVE LINES ABOVE THIS POINT. cov_branch_all.txt and
// cov_branch_partial.txt name the line numbers of the site (30) and of every
// block inside its error body (31, 32, 35). A single line added to the header
// comment shifts all of them and both arms fail at once. Same coupling as
// reach.go; same reason this note is below the code rather than above it.
//
// Both profiles were GENERATED from a real `go test -coverprofile` run over
// this exact source, not hand-written, so they pin Go's actual block layout
// rather than a belief about it.

