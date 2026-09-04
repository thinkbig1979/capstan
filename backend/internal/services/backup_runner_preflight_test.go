package services

import (
	"runtime"
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
