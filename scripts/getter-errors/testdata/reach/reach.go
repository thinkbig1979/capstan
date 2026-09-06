// Package reach is the fixture for `getter-errors reach --self-test` arms 1+2.
// It holds two CONVERTED sites -- the shape this family's fixes produce -- and
// is paired with two hand-written coverage profiles: cov_all.txt, where both
// error branches ran, and cov_partial.txt, where the second one never did.
// The same instrument must report 0 misses on the first and exactly 1 on the
// second. A reachability check that cannot tell those apart is measuring
// nothing, which is precisely how agent-os-l42o shipped ten converted sites
// with eight of them pinned by nothing.
package reach

type row struct{ ID string }
type store struct{}

func (store) GetThing(string) (*row, error) { return nil, nil }

var db store

func firstRead() (string, error) {
	thing, err := db.GetThing("a")
	if err != nil {
		return "", err
	}
	return thing.ID, nil
}

func secondRead() (string, error) {
	thing, err := db.GetThing("b")
	if err != nil {
		return "", err
	}
	return thing.ID, nil
}

// DO NOT ADD OR REMOVE LINES ABOVE THIS POINT. cov_all.txt and cov_partial.txt
// are hand-written and name the line numbers of the two sites (19 and 27) and
// of their error branches. A single line added to the header comment shifts
// both sites and every arm fails at once -- OBSERVED while writing
// agent-os-1hig, which is why this warning is here and why it is down here,
// where prose can grow without moving anything.
//
// HISTORY. Until agent-os-1hig the driver was scripts/check-getter-fault-reach.sh.
// That script was deleted (it selected its tests by the filename convention
// `*_dbfault_test.go` while its site set included non-DB getters), and its
// self-test arms moved into the tool, because scripts/getter-errors holds no
// _test.go file and the repo root has no go.mod -- so without them nothing on
// this tree would exercise `reach` at all.
