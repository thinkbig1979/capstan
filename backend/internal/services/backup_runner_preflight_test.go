package services

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// settledGoroutines returns runtime.NumGoroutine() once the count has held
// steady across two consecutive samples. Goroutines left exiting by an earlier
// test in this package would otherwise be attributed to the arm under test.
// Bounded at ~1s so a genuinely churning process fails the assertion rather
// than hanging the suite.
func settledGoroutines(t *testing.T) int {
	t.Helper()
	prev := -1
	for i := 0; i < 50; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return prev
}

// newBareRegistry builds a registry without NewBackupRunnerRegistry so no
// gcLoop goroutine is started and no DB is needed: every arm below exercises
// only Attach's "run is in the registry" branch, which touches neither
// reg.db nor reg.logger.
func newBareRegistry() *BackupRunnerRegistry {
	return &BackupRunnerRegistry{runs: map[string]*durableRun{}}
}

// TestAttach_NilClientGone_StartsNoForwarder pins agent-os-jtax.
//
// wsAttach's pre-flight existence check and outcomeFromRegistry both call
// Attach(runID, nil) and use only the returned state. Before the fix, Attach
// had no branch keyed on clientGone: for any run present in the registry and
// not yet done it unconditionally allocated a 256-slot channel and started
// forwardLive, replacing a nil clientGone with a channel that is never closed.
// That forwarder could therefore never be told the client had left, nobody
// ever drained its buffer, and it survived for the whole run — one orphan per
// WS attach to a live backup, plus one per poll of the status-only callers.
//
// The three arms share one instrument on purpose:
//   - CONTROL proves runtime.NumGoroutine() CAN read a zero delta here, so a
//     non-zero reading is a real allocation and not instrument drift.
//   - REGRESSION is the defect: this arm read +1 before the fix.
//   - POSITIVE CONTROL proves the fix did not simply delete live forwarding:
//     a caller that supplies a real clientGone still gets its goroutine, and
//     still gets it back when the client leaves.
func TestAttach_NilClientGone_StartsNoForwarder(t *testing.T) {
	reg := newBareRegistry()

	// --- CONTROL: already-finished run, nil clientGone.
	// Attach returns early on isDone, so no forwarder is possible here.
	drDone := &durableRun{runID: "arm-done", kind: RunKindBackup, done: make(chan struct{}), outcome: "success"}
	close(drDone.done)
	reg.runs[drDone.runID] = drDone

	before := settledGoroutines(t)
	resDone, err := reg.Attach(drDone.runID, nil)
	require.NoError(t, err)
	require.True(t, resDone.Done, "control arm must report a finished run")
	controlDelta := settledGoroutines(t) - before
	assert.Equal(t, 0, controlDelta,
		"CONTROL: a finished run must start no goroutine — a non-zero reading here means the instrument is drifting, not that the code leaked")

	// --- REGRESSION: still-running run, nil clientGone.
	drRunning := &durableRun{runID: "arm-running", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[drRunning.runID] = drRunning
	// Release any forwarder this arm starts, so an unfixed build does not leak
	// its orphan into the arms and tests that follow.
	t.Cleanup(func() { close(drRunning.done) })

	before = settledGoroutines(t)
	resRunning, err := reg.Attach(drRunning.runID, nil)
	require.NoError(t, err)
	require.False(t, resRunning.Done, "regression arm must report a live run")
	regressionDelta := settledGoroutines(t) - before
	assert.Equal(t, 0, regressionDelta,
		"REGRESSION (agent-os-jtax): Attach with a nil clientGone must start no forwardLive goroutine — there is no client to forward to, and a nil clientGone can never be closed, so such a forwarder could only exit when the run itself ends")
	assert.Nil(t, resRunning.Live,
		"a state-only Attach must not hand back a Live channel it has nobody to fill")

	// --- POSITIVE CONTROL: still-running run, real clientGone.
	drClient := &durableRun{runID: "arm-client", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[drClient.runID] = drClient
	t.Cleanup(func() { close(drClient.done) })
	clientGone := make(chan struct{})

	before = settledGoroutines(t)
	resClient, err := reg.Attach(drClient.runID, clientGone)
	require.NoError(t, err)
	require.False(t, resClient.Done)
	require.NotNil(t, resClient.Live, "a real client must get a live stream")
	positiveDelta := settledGoroutines(t) - before
	assert.Equal(t, 1, positiveDelta,
		"POSITIVE CONTROL: a real client must still get exactly one forwardLive goroutine — a zero here means the fix removed live forwarding instead of the orphan")

	// And that forwarder must actually forward.
	drClient.appendLog(StreamLine{Type: "data", Line: "hello"})
	select {
	case line, ok := <-resClient.Live:
		require.True(t, ok, "Live must not be closed while the run is in flight")
		assert.Equal(t, "hello", line.Line)
	case <-time.After(3 * time.Second):
		t.Fatal("forwardLive did not deliver an appended line within 3s")
	}

	// Closing clientGone must give the goroutine back. forwardLive closes live
	// from a defer as it returns, so draining resClient.Live to closure is the
	// deterministic signal that the goroutine is gone — don't poll the count
	// with assert.Eventually, which runs its condition in a goroutine of its
	// own and so can never read back down to the baseline.
	close(clientGone)
	deadline := time.After(3 * time.Second)
	for open := true; open; {
		select {
		case _, ok := <-resClient.Live:
			open = ok
		case <-deadline:
			t.Fatal("forwardLive did not exit within 3s of the client going away")
		}
	}
	assert.LessOrEqual(t, settledGoroutines(t), before,
		"forwardLive must exit once the client is gone")
}

// TestAttach_BoundsAttachersPerRun pins agent-os-nt0m: Attach admits at most
// maxAttachersPerRun concurrent live attachers to one running run and refuses
// the surplus BY RESULT (Done=true, Outcome "failed", a Reason naming the
// limit, no Live), allocating nothing for it. Before the fix every attach to a
// still-running run unconditionally allocated a 256-slot channel and a
// forwardLive goroutine; the only gates were an authenticated session and a
// valid runID, and the WS route registers unmetered so the per-user
// connection cap never applied (agent-os-pu4y).
//
// Four arms on one instrument (settledGoroutines delta + the AttachResult
// shape + line delivery on Live), in one function so the arms share a
// baseline:
//   - UNDER-BOUND proves the bound admits exactly maxAttachersPerRun callers
//     and that every one of them still streams. Without this a bound that
//     refuses everything would pass the next arm.
//   - OVER-BOUND is the defect: the (max+1)th Attach must come back Done with
//     the limit reason and the goroutine count must not move. This arm read
//     Done=false and delta max+1 before the fix.
//   - RELEASE proves the counter decrements when a forwarder exits on
//     clientGone, so the bound is on CONCURRENT attachers, not on lifetime
//     attaches. A counter that never decrements passes the first two arms.
//   - CONCURRENT proves the check and the increment share one critical
//     section: bound+k callers starting together must yield exactly bound
//     admissions. A check-then-increment under two separate locks passes the
//     three sequential arms and fails only here.
//
// forwardLive releases the slot from a defer that runs BEFORE its deferred
// close(live), so draining Live to closure is a deterministic signal that
// the slot is free again; the release arm relies on that ordering.
func TestAttach_BoundsAttachersPerRun(t *testing.T) {
	reg := newBareRegistry()

	dr := &durableRun{runID: "arm-bound", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[dr.runID] = dr
	// Ends every forwarder this test starts, so an unfixed build cannot leak
	// its surplus into the tests that follow.
	t.Cleanup(func() { close(dr.done) })

	waitLine := func(t *testing.T, live <-chan StreamLine, want string, who string) {
		t.Helper()
		select {
		case line, ok := <-live:
			require.True(t, ok, "%s: Live must not be closed while the run is in flight", who)
			assert.Equal(t, want, line.Line, "%s: wrong line delivered", who)
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: forwardLive did not deliver %q within 3s", who, want)
		}
	}

	// --- UNDER-BOUND: exactly maxAttachersPerRun clients, all admitted, all streaming.
	before := settledGoroutines(t)
	gone := make([]chan struct{}, 0, maxAttachersPerRun)
	lives := make([]<-chan StreamLine, 0, maxAttachersPerRun)
	for i := 0; i < maxAttachersPerRun; i++ {
		cg := make(chan struct{})
		res, err := reg.Attach(dr.runID, cg)
		require.NoError(t, err, "UNDER-BOUND: attacher %d of %d must be admitted", i+1, maxAttachersPerRun)
		require.False(t, res.Done)
		require.NotNil(t, res.Live, "UNDER-BOUND: attacher %d must get a live stream", i+1)
		gone = append(gone, cg)
		lives = append(lives, res.Live)
	}
	underDelta := settledGoroutines(t) - before
	assert.Equal(t, maxAttachersPerRun, underDelta,
		"UNDER-BOUND: each admitted attacher must get exactly one forwardLive goroutine")

	dr.appendLog(StreamLine{Type: "data", Line: "under"})
	for i, live := range lives {
		waitLine(t, live, "under", "UNDER-BOUND attacher "+itoa(i+1))
	}

	// --- OVER-BOUND: the (max+1)th client is refused by result and costs nothing.
	surplusGone := make(chan struct{})
	defer close(surplusGone)
	surplus, err := reg.Attach(dr.runID, surplusGone)
	require.NoError(t, err, "OVER-BOUND: a refusal is a result, not an error")
	require.NotNil(t, surplus)
	assert.True(t, surplus.Done,
		"OVER-BOUND (agent-os-nt0m): attacher %d must be refused as Done, the run already has %d live attachers",
		maxAttachersPerRun+1, maxAttachersPerRun)
	assert.Equal(t, "failed", surplus.Outcome, "OVER-BOUND: a refusal reports a failed outcome")
	assert.Equal(t, tooManyAttachersReason, surplus.Reason,
		"OVER-BOUND: the reason must name the limit so the viewer knows what to do")
	assert.Nil(t, surplus.Live, "OVER-BOUND: a refused attach must get no live stream")
	assert.Len(t, surplus.ReplayLines, 1,
		"OVER-BOUND: the refused viewer still gets the replay it can show")
	overDelta := settledGoroutines(t) - before
	assert.Equal(t, maxAttachersPerRun, overDelta,
		"OVER-BOUND: a refused attach must start no forwardLive goroutine")

	// --- RELEASE: one client leaves, its slot comes back, the newcomer streams.
	close(gone[0])
	deadline := time.After(3 * time.Second)
	for open := true; open; {
		select {
		case _, ok := <-lives[0]:
			open = ok
		case <-deadline:
			t.Fatal("RELEASE: forwardLive did not exit within 3s of its client going away")
		}
	}

	againGone := make(chan struct{})
	defer close(againGone)
	again, err := reg.Attach(dr.runID, againGone)
	require.NoError(t, err)
	require.False(t, again.Done,
		"RELEASE: once an attacher's forwarder has exited its slot must be free again; a counter that never decrements fails here")
	require.NotNil(t, again.Live)
	releaseDelta := settledGoroutines(t) - before
	assert.Equal(t, maxAttachersPerRun, releaseDelta,
		"RELEASE: after one exit and one re-admission the goroutine count must be back at the bound")

	dr.appendLog(StreamLine{Type: "data", Line: "again"})
	waitLine(t, again.Live, "again", "RELEASE newcomer")
	// The survivors from the first arm still stream too: the release touched
	// only the departed attacher's slot.
	waitLine(t, lives[1], "again", "RELEASE survivor")

	// --- CONCURRENT: bound+k callers start together on a fresh run; exactly
	// bound are admitted. Each goroutine parks on start so they contend for
	// the counter at the same instant rather than trickling in.
	const surplusK = 8
	drRace := &durableRun{runID: "arm-race", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[drRace.runID] = drRace
	t.Cleanup(func() { close(drRace.done) })
	raceGone := make(chan struct{})
	defer close(raceGone)

	raceBefore := settledGoroutines(t)
	start := make(chan struct{})
	results := make(chan *AttachResult, maxAttachersPerRun+surplusK)
	var wg sync.WaitGroup
	for i := 0; i < maxAttachersPerRun+surplusK; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := reg.Attach(drRace.runID, raceGone)
			assert.NoError(t, err, "CONCURRENT: Attach never errors on a run in the registry")
			results <- res
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	admitted, refused := 0, 0
	for res := range results {
		require.NotNil(t, res)
		if res.Done {
			refused++
			assert.Nil(t, res.Live, "CONCURRENT: a refused attach must get no live stream")
		} else {
			admitted++
			assert.NotNil(t, res.Live, "CONCURRENT: an admitted attach must get a live stream")
		}
	}
	assert.Equal(t, maxAttachersPerRun, admitted,
		"CONCURRENT: exactly the bound may be admitted when callers race; more means the check and the increment are not in one critical section")
	assert.Equal(t, surplusK, refused, "CONCURRENT: every caller beyond the bound must be refused")
	raceDelta := settledGoroutines(t) - raceBefore
	assert.Equal(t, maxAttachersPerRun, raceDelta,
		"CONCURRENT: one forwardLive goroutine per admitted attacher, none for the refused")
}

// itoa avoids pulling strconv in for a test label.
func itoa(n int) string { return fmt.Sprint(n) }
