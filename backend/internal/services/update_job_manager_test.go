package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestManager creates a manager with a short TTL suitable for tests.
func newTestManager(ttl time.Duration) *UpdateJobManager {
	return NewUpdateJobManager(ttl)
}

// TestUpdateJobManager_Enqueue_ReturnsQueuedJob checks that Enqueue returns a
// job in queued state immediately.
func TestUpdateJobManager_Enqueue_ReturnsQueuedJob(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	// Block the worker so the job stays queued long enough to assert.
	gate := make(chan struct{})
	run := func(_ context.Context, _ string, _ func(LogLine), _ func(Status)) error {
		<-gate
		return nil
	}

	spec := JobSpec{TargetType: "container", TargetID: "abc123", Name: "myapp", StackID: ""}
	job := m.Enqueue(spec, run)

	assert.Equal(t, StatusQueued, job.Status)
	assert.Equal(t, "container", job.TargetType)
	assert.Equal(t, "abc123", job.TargetID)
	assert.Equal(t, "myapp", job.Name)
	assert.NotEmpty(t, job.ID)
	assert.False(t, job.CreatedAt.IsZero())
	assert.True(t, job.StartedAt.IsZero())
	assert.True(t, job.FinishedAt.IsZero())

	close(gate)
}

// TestUpdateJobManager_StatusTransitions_Success checks the happy-path state machine:
// queued → (startedAt set) → success, finishedAt set.
func TestUpdateJobManager_StatusTransitions_Success(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	done := make(chan struct{})
	run := func(_ context.Context, _ string, emit func(LogLine), setStatus func(Status)) error {
		setStatus(StatusPulling)
		emit(LogLine{Ts: time.Now().UTC(), Text: "pulling image", Stream: StreamStatus})
		setStatus(StatusRecreating)
		close(done)
		return nil
	}

	spec := JobSpec{TargetType: "container", TargetID: "ctr1", Name: "web", StackID: "stack1"}
	job := m.Enqueue(spec, run)
	assert.Equal(t, StatusQueued, job.Status)

	<-done
	// Give the worker a moment to finish and write finishedAt.
	require.Eventually(t, func() bool {
		j := m.Get(job.ID)
		return j != nil && j.Status == StatusSuccess
	}, 2*time.Second, 10*time.Millisecond)

	final := m.Get(job.ID)
	require.NotNil(t, final)
	assert.Equal(t, StatusSuccess, final.Status)
	assert.False(t, final.StartedAt.IsZero(), "startedAt must be set")
	assert.False(t, final.FinishedAt.IsZero(), "finishedAt must be set")
	assert.Empty(t, final.Error)
}

// TestUpdateJobManager_StatusTransitions_Error checks that a failing run func
// results in status=error, Error set, and finishedAt set.
func TestUpdateJobManager_StatusTransitions_Error(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	sentinel := errors.New("pull failed: auth error")
	run := func(_ context.Context, _ string, _ func(LogLine), setStatus func(Status)) error {
		setStatus(StatusPulling)
		return sentinel
	}

	spec := JobSpec{TargetType: "container", TargetID: "ctr2", Name: "db", StackID: ""}
	job := m.Enqueue(spec, run)

	require.Eventually(t, func() bool {
		j := m.Get(job.ID)
		return j != nil && (j.Status == StatusError || j.Status == StatusSuccess)
	}, 2*time.Second, 10*time.Millisecond)

	final := m.Get(job.ID)
	require.NotNil(t, final)
	assert.Equal(t, StatusError, final.Status)
	assert.Equal(t, sentinel.Error(), final.Error)
	assert.False(t, final.StartedAt.IsZero())
	assert.False(t, final.FinishedAt.IsZero())
}

// TestUpdateJobManager_SequentialQueue_OneAtATime enqueues 3 jobs and asserts
// that only one is non-queued at any time and they complete in enqueue order.
func TestUpdateJobManager_SequentialQueue_OneAtATime(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	const n = 3
	// gates[i] is closed to unblock job i.
	gates := make([]chan struct{}, n)
	// started[i] is closed when job i begins running.
	started := make([]chan struct{}, n)
	for i := range gates {
		gates[i] = make(chan struct{})
		started[i] = make(chan struct{})
	}

	var completionOrder []int
	var orderMu sync.Mutex

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		idx := i
		run := func(_ context.Context, _ string, _ func(LogLine), _ func(Status)) error {
			close(started[idx])
			<-gates[idx]
			orderMu.Lock()
			completionOrder = append(completionOrder, idx)
			orderMu.Unlock()
			return nil
		}
		job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "x", Name: "svc", StackID: ""}, run)
		ids[i] = job.ID
	}

	// Job 0 should start; jobs 1 and 2 must remain queued.
	select {
	case <-started[0]:
	case <-time.After(2 * time.Second):
		t.Fatal("job 0 did not start within timeout")
	}

	// Verify jobs 1 and 2 are still queued.
	for _, idx := range []int{1, 2} {
		j := m.Get(ids[idx])
		require.NotNil(t, j)
		assert.Equal(t, StatusQueued, j.Status, "job %d should still be queued", idx)
	}

	// Unblock jobs in order and confirm sequential execution.
	for i := 0; i < n; i++ {
		close(gates[i])
		if i+1 < n {
			select {
			case <-started[i+1]:
			case <-time.After(2 * time.Second):
				t.Fatalf("job %d did not start after job %d completed", i+1, i)
			}
		}
	}

	// Wait for all to finish.
	require.Eventually(t, func() bool {
		for _, id := range ids {
			j := m.Get(id)
			if j == nil || j.Status != StatusSuccess {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond)

	orderMu.Lock()
	defer orderMu.Unlock()
	assert.Equal(t, []int{0, 1, 2}, completionOrder, "jobs must complete in enqueue order")
}

// TestUpdateJobManager_Subscribe_ReplayAndLive tests that a subscriber gets:
// 1. A snapshot with already-emitted lines.
// 2. Subsequent live lines with no gaps or duplicates.
func TestUpdateJobManager_Subscribe_ReplayAndLive(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	// Gate lets us pause the run func mid-way so we can subscribe late.
	const earlyLines = 5
	const lateLines = 3
	midpoint := make(chan struct{})
	resume := make(chan struct{})

	var jobID string
	var jobIDMu sync.Mutex

	run := func(_ context.Context, _ string, emit func(LogLine), _ func(Status)) error {
		for i := 0; i < earlyLines; i++ {
			emit(LogLine{
				Ts:     time.Now().UTC(),
				Text:   "early line",
				Stream: StreamStdout,
			})
		}
		close(midpoint) // signal that early lines are emitted
		<-resume        // wait for subscriber to register
		for i := 0; i < lateLines; i++ {
			emit(LogLine{
				Ts:     time.Now().UTC(),
				Text:   "late line",
				Stream: StreamStdout,
			})
		}
		return nil
	}

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "ctr3", Name: "svc3", StackID: ""}, run)
	jobIDMu.Lock()
	jobID = job.ID
	jobIDMu.Unlock()

	// Wait until early lines are emitted.
	select {
	case <-midpoint:
	case <-time.After(2 * time.Second):
		t.Fatal("run func did not reach midpoint")
	}

	// Subscribe while job is still running (after early lines).
	snapshot, ch, unsubscribe := m.Subscribe(jobID)
	require.NotNil(t, snapshot, "Subscribe must return a snapshot")
	defer unsubscribe()

	// Snapshot must contain at least the earlyLines.
	assert.GreaterOrEqual(t, len(snapshot.Lines), earlyLines,
		"snapshot should contain already-emitted lines")

	// Resume the run func to emit late lines.
	close(resume)

	// Collect all events from the channel until it is closed or we get lateLines line events.
	var received []JobEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			received = append(received, ev)
			if ev.Kind == EventKindDone {
				goto done
			}
		case <-timeout:
			t.Fatal("timed out waiting for events from subscriber channel")
		}
	}
done:

	lineEvents := 0
	gotDone := false
	for _, ev := range received {
		if ev.Kind == EventKindLine {
			lineEvents++
		}
		if ev.Kind == EventKindDone {
			gotDone = true
		}
	}

	assert.Equal(t, lateLines, lineEvents, "channel must deliver exactly the late lines")
	assert.True(t, gotDone, "channel must deliver a done event")

	// Total unique lines = snapshot + channel line events with no overlap.
	total := len(snapshot.Lines) + lineEvents
	assert.Equal(t, earlyLines+lateLines, total,
		"snapshot + channel lines must equal total emitted with no gaps or duplicates")
}

// TestUpdateJobManager_Subscribe_TerminalJob_SnapshotIsTerminal tests that
// subscribing to an already-finished job returns a snapshot whose Status is
// terminal and a live channel that never delivers further events. The WS handler
// relies on snapshot.Status being terminal to emit a final "done" frame and close,
// rather than blocking on the (non-delivering) channel — see update_jobs_ws.go.
func TestUpdateJobManager_Subscribe_TerminalJob_SnapshotIsTerminal(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	run := func(_ context.Context, _ string, emit func(LogLine), _ func(Status)) error {
		emit(LogLine{Ts: time.Now().UTC(), Text: "done", Stream: StreamStdout})
		return nil
	}

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "fin", Name: "finished", StackID: ""}, run)

	// Wait for it to finish.
	require.Eventually(t, func() bool {
		j := m.Get(job.ID)
		return j != nil && j.Status == StatusSuccess
	}, 2*time.Second, 10*time.Millisecond)

	snapshot, ch, unsubscribe := m.Subscribe(job.ID)
	require.NotNil(t, snapshot)
	defer unsubscribe()

	// Contract the WS handler depends on: the snapshot carries the terminal status,
	// so the handler can derive the "done" frame without waiting on the channel.
	assert.Equal(t, StatusSuccess, snapshot.Status,
		"terminal job snapshot must report its terminal status")

	// The channel must not deliver any further events for a terminal job.
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected no live events for a terminal job")
		}
		// a closed channel would also be acceptable
	case <-time.After(100 * time.Millisecond):
		// no events — correct
	}
}

// TestUpdateJobManager_ErrorPath_SubscriberGetsDoneEvent checks that when run
// returns an error, the subscriber receives a done event with the error string.
func TestUpdateJobManager_ErrorPath_SubscriberGetsDoneEvent(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	ready := make(chan struct{})
	gate := make(chan struct{})
	sentinel := errors.New("container exit 1")

	run := func(_ context.Context, _ string, _ func(LogLine), setStatus func(Status)) error {
		setStatus(StatusPulling)
		close(ready)
		<-gate
		return sentinel
	}

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "fail1", Name: "fail", StackID: ""}, run)

	<-ready
	snapshot, ch, unsubscribe := m.Subscribe(job.ID)
	require.NotNil(t, snapshot)
	defer unsubscribe()

	close(gate)

	var doneEv *JobEvent
	timeout := time.After(3 * time.Second)
	for doneEv == nil {
		select {
		case ev, ok := <-ch:
			if !ok {
				// channel closed without done — treat last state as final
				goto checkFinal
			}
			if ev.Kind == EventKindDone {
				cp := ev
				doneEv = &cp
			}
		case <-timeout:
			t.Fatal("timed out waiting for done event")
		}
	}

checkFinal:
	if doneEv == nil {
		// If the channel was closed before we received done, verify via Get.
		final := m.Get(job.ID)
		require.NotNil(t, final)
		assert.Equal(t, StatusError, final.Status)
		assert.Equal(t, sentinel.Error(), final.Error)
	} else {
		assert.Equal(t, EventKindDone, doneEv.Kind)
		assert.Equal(t, StatusError, doneEv.Status)
		assert.Equal(t, sentinel.Error(), doneEv.Error)
	}
}

// TestUpdateJobManager_TTL_Eviction checks:
// - a finished job is NOT removed before TTL expires.
// - a finished job IS removed after TTL expires.
func TestUpdateJobManager_TTL_Eviction(t *testing.T) {
	t.Parallel()

	ttl := 5 * time.Minute
	m := newTestManager(ttl)
	defer m.Stop()

	run := func(_ context.Context, _ string, _ func(LogLine), _ func(Status)) error {
		return nil
	}

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "evict1", Name: "evict", StackID: ""}, run)

	require.Eventually(t, func() bool {
		j := m.Get(job.ID)
		return j != nil && j.Status == StatusSuccess
	}, 2*time.Second, 10*time.Millisecond)

	// Just-finished: should NOT be evicted by a "now" that is only 1 min ahead.
	m.evictExpired(time.Now().Add(1 * time.Minute))
	assert.NotNil(t, m.Get(job.ID), "job must survive eviction before TTL")

	// "now" is ttl+1s in the future — should evict.
	m.evictExpired(time.Now().Add(ttl + time.Second))
	assert.Nil(t, m.Get(job.ID), "job must be evicted after TTL")
}

// TestUpdateJobManager_TTL_RunningJobNotEvicted ensures an in-progress job is
// never evicted regardless of the clock.
func TestUpdateJobManager_TTL_RunningJobNotEvicted(t *testing.T) {
	t.Parallel()

	m := newTestManager(1 * time.Millisecond) // very short TTL
	defer m.Stop()

	gate := make(chan struct{})
	run := func(_ context.Context, _ string, _ func(LogLine), _ func(Status)) error {
		<-gate
		return nil
	}

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "running1", Name: "svc", StackID: ""}, run)

	// Force eviction with a time far in the future while job is still running.
	m.evictExpired(time.Now().Add(24 * time.Hour))
	assert.NotNil(t, m.Get(job.ID), "running job must never be evicted")

	close(gate)
}

// TestUpdateJobManager_List_NewestFirst checks List ordering.
func TestUpdateJobManager_List_NewestFirst(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	gate := make(chan struct{})
	var wg sync.WaitGroup
	ids := make([]string, 3)

	for i := 0; i < 3; i++ {
		wg.Add(1)
		idx := i
		run := func(_ context.Context, _ string, _ func(LogLine), _ func(Status)) error {
			defer wg.Done()
			<-gate
			return nil
		}
		job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "x", Name: "svc", StackID: ""}, run)
		ids[idx] = job.ID
		time.Sleep(2 * time.Millisecond) // ensure distinct CreatedAt
	}

	close(gate)
	wg.Wait()

	require.Eventually(t, func() bool {
		list := m.List()
		for _, j := range list {
			if j.Status != StatusSuccess {
				return false
			}
		}
		return len(list) == 3
	}, 3*time.Second, 10*time.Millisecond)

	list := m.List()
	require.Len(t, list, 3)
	// Newest first: ids[2], ids[1], ids[0]
	assert.Equal(t, ids[2], list[0].ID)
	assert.Equal(t, ids[1], list[1].ID)
	assert.Equal(t, ids[0], list[2].ID)
}

// TestUpdateJobManager_RingBuffer caps lines at maxJobLines.
func TestUpdateJobManager_RingBuffer(t *testing.T) {
	t.Parallel()

	m := newTestManager(15 * time.Minute)
	defer m.Stop()

	const emit = maxJobLines + 100
	run := func(_ context.Context, _ string, emitFn func(LogLine), _ func(Status)) error {
		for i := 0; i < emit; i++ {
			emitFn(LogLine{Ts: time.Now().UTC(), Text: "x", Stream: StreamStdout})
		}
		return nil
	}

	job := m.Enqueue(JobSpec{TargetType: "container", TargetID: "ring", Name: "ring", StackID: ""}, run)

	require.Eventually(t, func() bool {
		j := m.Get(job.ID)
		return j != nil && j.Status == StatusSuccess
	}, 5*time.Second, 10*time.Millisecond)

	final := m.Get(job.ID)
	assert.LessOrEqual(t, len(final.Lines), maxJobLines, "ring buffer must not exceed cap")
}
