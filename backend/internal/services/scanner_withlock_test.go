package services

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// agent-os-z91e.23: a scan that globbed a compose file before a delete removed
// it writes the row back after the delete, leaving a ghost stack. When the
// directory survives (a sibling stack lives there), ScanAll never prunes that
// row, because its directory is still active. A delete run through WithLock
// waits for the scan to finish and then removes what the scan wrote.
func TestScannerService_WithLock_ScanParkedBeforeUpsertDoesNotResurrectDeletedStack(t *testing.T) {
	s, db, root := newSerialiseTestScanner(t)
	dir := filepath.Join(root, "app")
	require.NoError(t, writeComposeStackDir(dir))
	apiFile := filepath.Join(dir, "compose.api.yaml")
	require.NoError(t, os.WriteFile(apiFile, []byte("services:\n  api:\n    image: nginx\n"), 0o644))
	_, err := s.ScanAll()
	require.NoError(t, err)

	stacks, err := db.ListStacksByDirectory(dir)
	require.NoError(t, err)
	require.Len(t, stacks, 2, "setup: both compose files must register")
	apiID := ""
	for _, st := range stacks {
		if st.ComposeFile == "compose.api.yaml" {
			apiID = st.ID
		}
	}
	require.NotEmpty(t, apiID)

	parked := make(chan struct{})
	release := make(chan struct{})
	scanBeforeUpsertStackHook = func(stackID string) {
		if stackID == apiID {
			close(parked)
			<-release
		}
	}
	t.Cleanup(func() { scanBeforeUpsertStackHook = nil })

	scanDone := make(chan error, 1)
	go func() { scanDone <- s.ScanDirectory(dir) }()
	<-parked

	deleteDone := make(chan error, 1)
	go func() {
		var err error
		s.WithLock(func() {
			if err = os.Remove(apiFile); err == nil {
				err = db.DeleteStack(apiID)
			}
		})
		deleteDone <- err
	}()

	// Unlocked, the delete finishes here, before the parked scan writes. Locked,
	// it is still waiting on mu, and this wait just times out.
	select {
	case err := <-deleteDone:
		deleteDone <- err
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-scanDone)
	require.NoError(t, <-deleteDone)

	_, err = db.GetStack(apiID)
	assert.ErrorIs(t, err, errdefs.ErrNotFound,
		"the scan parked before UpsertStack wrote the deleted stack's row back: ghost stack %s", apiID)
}

func TestScannerService_WithLock_NilReceiverRunsFn(t *testing.T) {
	var s *ScannerService
	ran := false
	s.WithLock(func() { ran = true })
	assert.True(t, ran)
}
