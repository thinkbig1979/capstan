package truth

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-0h4p: a pull message over 64 KiB ended DrainPullStream with an
// error and left the rest of the stream unread. Messages up to 1 MiB now pass,
// and past that the reader is still drained, as the doc promises.

func TestDrainPullStream_MessageOver64KiBThenShort_0h4p(t *testing.T) {
	long := `{"status":"` + strings.Repeat("a", 70*1024) + `"}`
	var lines []string

	err := DrainPullStream(strings.NewReader(long+"\n"+`{"status":"short"}`+"\n"), func(l string) { lines = append(lines, l) })

	require.NoError(t, err)
	assert.Contains(t, lines, "short")
}

func TestDrainPullStream_MessageOverLimitStillDrains_0h4p(t *testing.T) {
	r, w := io.Pipe()
	wrote := make(chan struct{})
	go func() {
		defer close(wrote)
		_, _ = io.WriteString(w, `{"status":"`+strings.Repeat("a", 1<<20)+`"}`+"\n") //nolint:errcheck // the reader side asserts
		_, _ = io.WriteString(w, `{"status":"more"}`+"\n")                           //nolint:errcheck // blocks forever if nothing drains: what the select below detects
		_ = w.Close()
	}()

	err := DrainPullStream(r, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading pull stream")
	select {
	case <-wrote:
	case <-time.After(5 * time.Second):
		_ = r.Close()
		t.Fatal("the writer is still blocked: DrainPullStream stopped reading after the long message")
	}
}
