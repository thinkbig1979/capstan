package handlers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-0h4p: the live log stream read `docker compose logs` with a
// default bufio.Scanner, whose 64 KiB token limit ended the loop on a longer
// line with no error read: the stream just stopped. pumpLogLines now takes
// lines up to 1 MiB and returns the scanner's error past that, which
// StreamLogs reports to the client before it kills the child.

// pumpOnce runs pumpLogLines over text written through a pipe, as the child's
// stdout is, and returns the lines and the error. The writer has no reader once
// the pump stops, so a pump that quits early without the caller noticing shows
// up as a write that never returns, bounded here.
func pumpOnce(t *testing.T, text string) ([]string, error) {
	t.Helper()
	r, w := io.Pipe()
	out := make(chan string, 16)
	done := make(chan error, 1)
	go func() {
		defer close(out)
		done <- pumpLogLines(context.Background(), r, out)
		// As StreamLogs' cleanup kills the child: the writer must not hang.
		_ = r.CloseWithError(io.ErrClosedPipe)
	}()
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		_, _ = io.WriteString(w, text) //nolint:errcheck // a write the stopped pump refuses is the case under test; the read side asserts
		_ = w.Close()
	}()

	var lines []string
	timeout := time.After(time.Until(hangGuardDeadline(t)))
	for {
		select {
		case l, ok := <-out:
			if !ok {
				select {
				case <-wrote:
				case <-timeout:
					t.Fatal("the writer is still blocked after the pump returned: the child would hang")
				}
				return lines, <-done
			}
			lines = append(lines, l)
		case <-timeout:
			t.Fatalf("pumpLogLines did not return before the hang guard; %d lines so far", len(lines))
		}
	}
}

func TestPumpLogLines_LineOver64KiBThenShort_0h4p(t *testing.T) {
	long := strings.Repeat("a", 70*1024)

	lines, err := pumpOnce(t, "web | "+long+"\nweb | short\n")

	require.NoError(t, err)
	require.Len(t, lines, 2, "a 70 KiB line ended the stream")
	assert.Equal(t, "web | short", lines[1])
}

func TestPumpLogLines_LineOverLimitReturnsError_0h4p(t *testing.T) {
	huge := strings.Repeat("a", 1<<20+1)

	_, err := pumpOnce(t, "web | first\nweb | "+huge+"\nweb | short\n")

	require.Error(t, err, "a line over the limit ended the stream with no error")
	assert.Contains(t, err.Error(), "too long")
}
