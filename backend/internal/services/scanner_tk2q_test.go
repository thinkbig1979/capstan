package services

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// leakProbeLister hangs like a daemon that never answers and records whether
// the lookup has returned. It offers both the bare and the ctx-taking method,
// so the scanner's lookup is probed whichever one it calls.
type leakProbeLister struct {
	release  chan struct{}
	returned atomic.Bool
}

func (l *leakProbeLister) GetContainerList(string) ([]models.Container, error) {
	defer l.returned.Store(true)
	<-l.release
	return nil, nil
}

func (l *leakProbeLister) GetContainerListCtx(ctx context.Context, _ string) ([]models.Container, error) {
	defer l.returned.Store(true)
	select {
	case <-l.release:
	case <-ctx.Done():
	}
	return nil, ctx.Err()
}

// agent-os-tk2q (review G3): the scanner bounded a hung lookup by waiting on
// a goroutine it then abandoned, so every pass against a hung daemon left one
// goroutine and one connection behind. The lookup must be over once the pass
// that made it has returned.
func TestScannerTk2q_HungLookupIsOverWhenThePassReturns(t *testing.T) {
	old := containerLookupBound
	containerLookupBound = 200 * time.Millisecond
	t.Cleanup(func() { containerLookupBound = old })

	app, s := epc5Folder(t)
	require.NoError(t, os.Remove(filepath.Join(app, "compose.worker.yaml")))
	lister := &leakProbeLister{release: make(chan struct{})}
	t.Cleanup(func() { close(lister.release) })
	s.containers = lister

	require.NoError(t, s.ScanDirectory(app))
	require.Equal(t, []string{"compose.worker.yaml", "compose.yaml"}, composeFilesIn(t, s, app), "control: the timed out lookup keeps the row")
	require.True(t, lister.returned.Load(), "the container lookup was still running after the scan pass returned: it was left behind on a goroutine")
}
