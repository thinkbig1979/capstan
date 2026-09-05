// Package reach is the fixture for check-getter-fault-reach.sh's self-test.
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
