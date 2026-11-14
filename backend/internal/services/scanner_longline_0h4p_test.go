package services

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-0h4p: restic's output (execRunner.Run) and an update's compose
// output (streamComposeCmd) were read with a default bufio.Scanner. Its 64 KiB
// token limit stopped the loop on a longer line, and the rest of the output
// was drained unread with no error: lines vanished. Lines up to 1 MiB now pass;
// past that, Run fails (its callers count and parse the lines) and
// streamComposeCmd leaves a marker in the update log.

const (
	longLine0h4p = `head -c 71680 /dev/zero | tr '\0' a; echo; echo short`
	// Over 1 MiB, then more output: the child exits only if the pipe is
	// still drained after the scanner gives up.
	hugeLine0h4p = `head -c 1100000 /dev/zero | tr '\0' a; echo; head -c 200000 /dev/zero | tr '\0' b; echo; echo short`
	bound0h4p    = 10 * time.Second
)

// runRestic0h4p runs script through execRunner.Run and collects its lines,
// failing if Run does not return within bound0h4p (a child stuck on a full
// pipe).
func runRestic0h4p(t *testing.T, script string) ([]string, error) {
	t.Helper()
	out := make(chan StreamLine, 64)
	var lines []string
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for l := range out {
			lines = append(lines, l.Line)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- (&execRunner{}).Run(context.Background(), "sh", []string{"-c", script}, nil, out) }()
	select {
	case err := <-done:
		close(out)
		<-collected
		return lines, err
	case <-time.After(bound0h4p):
		t.Fatalf("Run did not return within %s: the child is stuck on its output pipe", bound0h4p)
		return nil, nil
	}
}

func TestExecRunnerRun_LineOver64KiBThenShort_0h4p(t *testing.T) {
	lines, err := runRestic0h4p(t, longLine0h4p)

	require.NoError(t, err)
	assert.Contains(t, lines, "short", "the line after a 70 KiB line never reached the consumer")
	var longest int
	for _, l := range lines {
		longest = max(longest, len(l))
	}
	assert.Equal(t, 71680, longest, "the 70 KiB line did not arrive whole")
}

func TestExecRunnerRun_LineOverLimitFailsClosed_0h4p(t *testing.T) {
	_, err := runRestic0h4p(t, hugeLine0h4p)

	require.Error(t, err, "a line over the limit was dropped and Run still succeeded")
	assert.Contains(t, err.Error(), "output line over 1 MiB")
}

func streamCompose0h4p(t *testing.T, script string) ([]LogLine, error) {
	t.Helper()
	stubComposeBySubcommand(t, script, "true")
	var mu sync.Mutex // stdout and stderr are emitted from two goroutines
	var lines []LogLine
	done := make(chan error, 1)
	go func() {
		done <- streamComposeCmd(context.Background(), time.Minute, []string{"compose", "pull", "--", "web"}, t.TempDir(), nil,
			func(l LogLine) { mu.Lock(); lines = append(lines, l); mu.Unlock() })
	}()
	select {
	case err := <-done:
		mu.Lock()
		defer mu.Unlock()
		return lines, err
	case <-time.After(bound0h4p):
		t.Fatalf("streamComposeCmd did not return within %s: the child is stuck on its output pipe", bound0h4p)
		return nil, nil
	}
}

func texts0h4p(lines []LogLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Text
	}
	return out
}

func TestStreamComposeCmd_LineOver64KiBThenShort_0h4p(t *testing.T) {
	lines, err := streamCompose0h4p(t, longLine0h4p)

	require.NoError(t, err)
	assert.Contains(t, texts0h4p(lines), "short", "the line after a 70 KiB line never reached the update log")
}

func TestStreamComposeCmd_LineOverLimitLeavesMarker_0h4p(t *testing.T) {
	lines, err := streamCompose0h4p(t, hugeLine0h4p)

	require.NoError(t, err, "a dropped output line is not a failed update")
	assert.Contains(t, strings.Join(texts0h4p(lines), "\n"), "output line over 1 MiB",
		"the rest of the output was dropped with nothing in the log saying so")
}
