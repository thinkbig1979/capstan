package services

import (
	"regexp"
	"testing"
)

// TestOperationLock_StaleReleaseKeepsNewerHolder is agent-os-a1ye.4's
// acceptance (a). operations.go used to release twice (an explicit Release and
// a deferred one); with no token, the second one freed whatever lock the stack
// had by then, including one a different request had just acquired.
func TestOperationLock_StaleReleaseKeepsNewerHolder(t *testing.T) {
	lock := NewOperationLock()

	tokenA, err := lock.Acquire("s", OpKindStart)
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	lock.Release("s", tokenA) // A's explicit release

	tokenB, err := lock.Acquire("s", OpKindBackup) // B now holds the stack
	if err != nil {
		t.Fatalf("B: %v", err)
	}
	if tokenA == tokenB {
		t.Fatalf("two acquisitions got the same token %q", tokenA)
	}

	lock.Release("s", tokenA) // A's deferred, stale release

	if _, err := lock.Acquire("s", OpKindStop); err == nil {
		t.Fatal("A's stale release freed B's lock: a third caller acquired the stack while B still holds it")
	}

	lock.Release("s", tokenB)
	if _, err := lock.Acquire("s", OpKindStop); err != nil {
		t.Fatalf("B's own release did not free the stack: %v", err)
	}
}

// TestOperationLock_ReleasePrunesEntry checks that a released stack leaves no
// map entry behind, so the map does not grow with every stack ever operated on.
func TestOperationLock_ReleasePrunesEntry(t *testing.T) {
	lock := NewOperationLock()
	token, err := lock.Acquire("s", OpKindStart)
	if err != nil {
		t.Fatal(err)
	}
	lock.Release("s", "not-the-token")
	if n := len(lock.locks); n != 1 {
		t.Fatalf("a stale release changed the map: %d entries, want 1", n)
	}
	lock.Release("s", token)
	if n := len(lock.locks); n != 0 {
		t.Fatalf("%d entries left after release, want 0", n)
	}
}

// TestOperationLock_BusyMessageNamesKindAndStart pins the 409 text: it used to
// repeat the stack id twice ("for stack X (started by X)"), which told the
// reader nothing about what was running.
func TestOperationLock_BusyMessageNamesKindAndStart(t *testing.T) {
	lock := NewOperationLock()
	if _, err := lock.Acquire("s", OpKindBackup); err != nil {
		t.Fatal(err)
	}
	_, err := lock.Acquire("s", OpKindStart)
	if err == nil {
		t.Fatal("second Acquire on a held stack succeeded")
	}
	want := regexp.MustCompile(`^backup in progress since \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	if !want.MatchString(err.Error()) {
		t.Fatalf("message = %q, want %s", err.Error(), want)
	}
}
