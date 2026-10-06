package services

import (
	"sync"
	"testing"
)

// TestOperationLockConcurrentAcquireRelease hammers one stackID from many
// goroutines so that contended Acquire calls (the busy branch, which formats
// the holder's kind and start time) overlap with Release deleting the holder.
//
// Under -race this is the regression test for agent-os-y10: Acquire used to
// unlock its mutex and only then read the holder to format its error, racing
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
				token, err := lock.Acquire("stack-under-contention", OpKindStart)
				if err != nil {
					continue
				}
				lock.Release("stack-under-contention", token)
			}
		}()
	}

	wg.Wait()

	// Every successful Acquire above was paired with a Release, so the slot must
	// be free — a leaked count would block the stack permanently in production.
	if _, err := lock.Acquire("stack-under-contention", OpKindStart); err != nil {
		t.Fatalf("lock not released after concurrent acquire/release: %v", err)
	}
}
