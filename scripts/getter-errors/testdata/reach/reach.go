// Package reach is the fixture for `getter-errors reach --self-test`, arms 1
// and 2. (Until agent-os-1hig its driver was check-getter-fault-reach.sh; that
// script was deleted and its self-test arms moved into the tool, because
// nothing else on this tree exercises `reach` at all.)
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
