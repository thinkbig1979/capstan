// Package lineio builds the line scanners that read a child process's output
// or a stream (agent-os-0h4p). bufio.NewScanner's default 64 KiB token limit
// stopped a loop at a longer line and, unless the caller read Err(), looked
// like a clean end of output. NewScanner raises the limit to MaxLine; a caller
// still reads Err() for a line past it, and keeps draining its pipe so the
// writer is not left blocked.
package lineio

import (
	"bufio"
	"io"
)

const (
	// initialBuffer is the scanner's starting buffer; it grows up to MaxLine.
	initialBuffer = 64 * 1024
	// MaxLine bounds a line a scanner from NewScanner returns: the line and
	// its line ending must fit in it. A longer one stops the scanner with
	// bufio.ErrTooLong.
	MaxLine = 1 << 20
)

// NewScanner is bufio.NewScanner with lines up to MaxLine. The forbidigo rule
// in backend/.golangci.yml keeps every scanner in the backend on it.
func NewScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r) //nolint:forbidigo // the one place bufio.NewScanner is called
	s.Buffer(make([]byte, 0, initialBuffer), MaxLine)
	return s
}
