package handlers

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-li67 (review F7 on #746): deleteWithoutComposeFile takes the
// no-compose-file path only on a definite absence. An Lstat that fails any
// other way must leave the choice to Delete's own compose down, so
// DeleteVerified runs. ENOTDIR stands in for "permission denied": the same
// branch (not fs.ErrNotExist), and it holds when the tests run as root.
func TestDeleteLi67_LstatErrorOtherThanNotExistStillRunsComposeDown(t *testing.T) {
	docker := &epc5Docker{}
	f := newEpc5Fixture(t, docker)

	// Point the row's compose file through a regular file, so its Lstat fails
	// with ENOTDIR rather than "does not exist".
	stack, err := f.db.GetStack(f.workerID)
	require.NoError(t, err)
	stack.ComposeFile = filepath.Join("compose.yaml", "compose.worker.yaml")
	require.NoError(t, f.db.UpsertStack(*stack))
	_, statErr := os.Lstat(filepath.Join(stack.Directory, stack.ComposeFile))
	require.Error(t, statErr, "setup: the Lstat must fail")
	require.False(t, errors.Is(statErr, fs.ErrNotExist), "setup: and not as a definite absence, got %v", statErr)

	w := f.delete(t)

	assert.Equal(t, 1, docker.deleteCalls, "an Lstat error other than NotExist must still run compose down (DeleteVerified); body=%s", w.Body.String())
}
