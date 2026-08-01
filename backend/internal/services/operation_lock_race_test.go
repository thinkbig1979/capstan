package services

import (
	"sync"
	"testing"
)

// TestOperationLockConcurrentAcquireRelease hammers one stackID from many
// goroutines so that contended Acquire calls (the sl.count > 0 branch) overlap
// with Release clearing ownerID.
//
// Under -race this is the regression test for agent-os-y10: Acquire used to
// unlock sl.mu and only then read sl.ownerID to format its error, racing
// Release's write to the same field. The assertion that matters here is not the
// counter at the end but the absence of a race report, so the test is only
// meaningful when run with -race.
func TestOperationLockConcurrentAcquireRelease(t *testing.T) {
	const (
		goroutines = 16
		iterations = 200
	)

	lock := NewOperationLock()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// Contention is the point: most of these lose the race to
				// acquire and take the error path that reads ownerID.
				if _, err := lock.Acquire("stack-under-contention"); err != nil {
					continue
				}
				lock.Release("stack-under-contention")
			}
		}()
	}

	wg.Wait()

	// Every successful Acquire above was paired with a Release, so the slot must
	// be free — a leaked count would block the stack permanently in production.
	if _, err := lock.Acquire("stack-under-contention"); err != nil {
		t.Fatalf("lock not released after concurrent acquire/release: %v", err)
	}
}
