package services

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSchedulerRestart_AfterCloseStaysStopped is agent-os-z91e.5's latch
// check. main.go stops the update scheduler before srv.Shutdown, so a settings
// save still being served can call Restart after that stop. Before the latch,
// Start reset `stopped` and re-armed the scan ticker and the apply loop, so
// auto-apply could begin a compose update in the shutdown window. After Close,
// both Restart and Start must leave it stopped.
func TestSchedulerRestart_AfterCloseStaysStopped(t *testing.T) {
	svc := newTestScheduler(t, &fakeUpdateChecker{})

	svc.Start(time.Minute)
	if !svc.IsRunning() {
		t.Fatal("precondition: IsRunning should be true after Start")
	}

	svc.Close()
	if svc.IsRunning() {
		t.Fatal("IsRunning should be false after Close")
	}

	svc.Restart(time.Minute) // a late settings save
	if svc.IsRunning() {
		t.Fatal("Restart after Close re-armed the scheduler during shutdown (agent-os-z91e.5)")
	}

	svc.Start(time.Minute)
	if svc.IsRunning() {
		t.Fatal("Start after Close re-armed the scheduler during shutdown (agent-os-z91e.5)")
	}
}

// TestSchedulerRestart_ConcurrentTransitionsSerialised runs Restart(1m),
// Restart(2m) and Stop() from three goroutines, 100 times, and asserts the
// final IsRunning() matches the transition applied last (recorded through the
// test-only onTransition seam, which runs under lifecycleMu). Before
// agent-os-z91e.5, Restart was Stop then Start with the lock released in
// between, so a concurrent Stop could land between the halves. Run it with
// -race: the detector is half of what it checks.
func TestSchedulerRestart_ConcurrentTransitionsSerialised(t *testing.T) {
	svc := newTestScheduler(t, &fakeUpdateChecker{})
	var lastRunning atomic.Bool
	svc.onTransition = func(running bool) { lastRunning.Store(running) }
	t.Cleanup(svc.Stop)

	for i := 0; i < 100; i++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for _, op := range []func(){
			func() { svc.Restart(time.Minute) },
			func() { svc.Restart(2 * time.Minute) },
			svc.Stop,
		} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				op()
			}()
		}
		close(start)
		wg.Wait()

		if got, want := svc.IsRunning(), lastRunning.Load(); got != want {
			t.Fatalf("iteration %d: IsRunning() = %v, but the last applied transition left it %v", i, got, want)
		}
	}
}
