package lineio

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewScanner_TakesLinesUpToMaxLine(t *testing.T) {
	line := strings.Repeat("a", MaxLine-1) // plus its "\n": exactly MaxLine
	s := NewScanner(strings.NewReader(line + "\nshort\n"))

	require.True(t, s.Scan(), "err: %v", s.Err())
	assert.Len(t, s.Text(), MaxLine-1)
	require.True(t, s.Scan())
	assert.Equal(t, "short", s.Text())
	assert.False(t, s.Scan())
	assert.NoError(t, s.Err())
}

func TestNewScanner_LineOverMaxLineIsAnError(t *testing.T) {
	s := NewScanner(strings.NewReader(strings.Repeat("a", MaxLine) + "\n"))

	assert.False(t, s.Scan())
	assert.ErrorIs(t, s.Err(), bufio.ErrTooLong)
}
