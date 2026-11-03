package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runlogLine names line n (1-based) so order and duplicates are checkable.
func runlogLine(n int) StreamLine {
	return StreamLine{Type: "data", Line: fmt.Sprintf("line-%d", n)}
}

// runlogRead reads n lines from live, failing (not hanging) if they stop.
func runlogRead(t *testing.T, live <-chan StreamLine, n int, who string) []string {
	t.Helper()
	got := make([]string, 0, n)
	for len(got) < n {
		select {
		case l, ok := <-live:
			require.True(t, ok, "%s: Live closed after %d of %d lines", who, len(got), n)
			got = append(got, l.Line)
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no line within 3s after %d of %d", who, len(got), n)
		}
	}
	return got
}

// runlogDrain reads live until the forwarder closes it.
func runlogDrain(t *testing.T, live <-chan StreamLine, who string) []string {
	t.Helper()
	var got []string
	for {
		select {
		case l, ok := <-live:
			if !ok {
				return got
			}
			got = append(got, l.Line)
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: Live not closed within 3s of done (%d lines read)", who, len(got))
		}
	}
}

func runlogRange(from, to int) []string {
	out := make([]string, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, runlogLine(i).Line)
	}
	return out
}

// TestForwardLive_PastReplayBufMax: the replay buffer slides once it holds
// replayBufMax lines, so a cursor taken as an index into it stops matching
// the lines it meant. Viewers attached before and after the buffer filled
// must both keep receiving new lines, in order and none twice (agent-os-7ifk).
func TestForwardLive_PastReplayBufMax(t *testing.T) {
	const total = 5000
	reg := newBareRegistry()
	dr := &durableRun{runID: "runlog-full", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[dr.runID] = dr

	goneA := make(chan struct{})
	defer close(goneA)
	resA, err := reg.Attach(dr.runID, goneA)
	require.NoError(t, err)
	require.NotNil(t, resA.Live)

	for i := 1; i <= replayBufMax; i++ {
		dr.appendLog(runlogLine(i))
	}
	gotA := runlogRead(t, resA.Live, replayBufMax, "early viewer")

	goneB := make(chan struct{})
	defer close(goneB)
	resB, err := reg.Attach(dr.runID, goneB)
	require.NoError(t, err)
	require.NotNil(t, resB.Live)
	require.Len(t, resB.ReplayLines, replayBufMax, "late viewer replays the full buffer")

	for i := replayBufMax + 1; i <= total; i++ {
		dr.appendLog(runlogLine(i))
	}
	close(dr.done)

	gotA = append(gotA, runlogDrain(t, resA.Live, "early viewer")...)
	gotB := runlogDrain(t, resB.Live, "late viewer")

	assert.Equal(t, runlogRange(1, total), gotA, "early viewer must get every line, in order, none twice")
	assert.Equal(t, runlogRange(replayBufMax+1, total), gotB, "late viewer must get every line after its replay")
}

// TestForwardLive_FellBehindDropPoint: a viewer whose cursor is overtaken by
// the slide (more lines dropped than it has read) resumes at the oldest
// retained line, then runs contiguous to the end. Read as an index, its
// cursor instead skips cursor-many retained lines as well (agent-os-7ifk).
//
// The forwarder is held still without a clock: nobody reads Live, so it
// blocks in its send once the 256-slot channel is full, having already
// taken at most prefix lines. Everything appended after that point is
// out of its reach until the test starts draining.
func TestForwardLive_FellBehindDropPoint(t *testing.T) {
	const prefix, total = 300, 5000
	reg := newBareRegistry()
	dr := &durableRun{runID: "runlog-behind", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[dr.runID] = dr

	gone := make(chan struct{})
	defer close(gone)
	res, err := reg.Attach(dr.runID, gone)
	require.NoError(t, err)
	require.NotNil(t, res.Live)

	for i := 1; i <= prefix; i++ {
		dr.appendLog(runlogLine(i))
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(res.Live) < cap(res.Live) {
		require.True(t, time.Now().Before(deadline), "forwarder never filled Live (%d buffered)", len(res.Live))
		time.Sleep(5 * time.Millisecond)
	}
	for i := prefix + 1; i <= total; i++ {
		dr.appendLog(runlogLine(i))
	}
	close(dr.done)

	got := runlogDrain(t, res.Live, "lagging viewer")
	oldest := total - replayBufMax + 1 // 905: the oldest line the buffer still holds

	// Lines the forwarder took before the slide arrive first, contiguous from 1.
	k := 0
	for k < len(got) && got[k] == runlogLine(k+1).Line {
		k++
	}
	require.GreaterOrEqual(t, k, cap(res.Live), "the buffered prefix must arrive intact")
	require.LessOrEqual(t, k, prefix)
	assert.Equal(t, runlogRange(oldest, total), got[k:],
		"after the gap the viewer must resume at the oldest retained line and run contiguous to the end")
}

// TestForwardLive_UnderReplayBufMax is the control arm: below the bound
// nothing slides and a viewer sees every line.
func TestForwardLive_UnderReplayBufMax(t *testing.T) {
	reg := newBareRegistry()
	dr := &durableRun{runID: "runlog-small", kind: RunKindBackup, done: make(chan struct{})}
	reg.runs[dr.runID] = dr

	gone := make(chan struct{})
	defer close(gone)
	res, err := reg.Attach(dr.runID, gone)
	require.NoError(t, err)
	require.NotNil(t, res.Live)

	for i := 1; i <= 100; i++ {
		dr.appendLog(runlogLine(i))
	}
	close(dr.done)

	assert.Equal(t, runlogRange(1, 100), runlogDrain(t, res.Live, "viewer"))
}
