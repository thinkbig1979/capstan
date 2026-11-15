package handlers

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// Was TestPumpLogLines_LineOverLimitReturnsError_0h4p, which pinned the stream
// STOPPING at a line over 1 MiB. Edwin ruled (2026-11-15, agent-os-v8ig) that
// such a line is cut at 1 MiB with a note and the stream goes on: the client
// reconnects and replays the last 100 lines, so a stop happened again on every
// reconnect while that line was among them.
func TestPumpLogLines_LineOverLimitIsTruncatedAndStreamGoesOn_V8ig(t *testing.T) {
	huge := "web | " + strings.Repeat("a", 1<<20+512*1024)

	lines, err := pumpOnce(t, "web | first\n"+huge+"\nweb | short\nweb | last-no-newline")

	require.NoError(t, err)
	require.Len(t, lines, 4, "every line arrives, the long one cut")
	assert.Equal(t, "web | first", lines[0])
	assert.True(t, strings.HasPrefix(lines[1], huge[:1<<20]), "the first 1 MiB of the long line is kept")
	assert.True(t, strings.HasSuffix(lines[1], logLineTruncatedNote), "the cut is marked")
	assert.Len(t, lines[1], 1<<20+len(logLineTruncatedNote))
	assert.Equal(t, "web | short", lines[2])
	assert.Equal(t, "web | last-no-newline", lines[3])
}

func TestPumpLogLines_LineAtLimitIsNotTruncated_V8ig(t *testing.T) {
	exact := strings.Repeat("b", 1<<20)

	lines, err := pumpOnce(t, exact+"\r\nnext\n")

	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, exact, lines[0], "a line of exactly 1 MiB is whole, CR dropped as bufio.ScanLines does")
	assert.Equal(t, "next", lines[1])
}

func TestPumpLogLines_CutKeepsUTF8Whole_V8ig(t *testing.T) {
	// 1 MiB - 1 ASCII bytes, then a 3-byte rune straddling the limit.
	text := strings.Repeat("c", 1<<20-1) + "€€€"

	lines, err := pumpOnce(t, text+"\n")

	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.True(t, utf8.ValidString(lines[0]), "the cut must not split a rune")
	assert.Equal(t, strings.Repeat("c", 1<<20-1)+logLineTruncatedNote, lines[0])
}
