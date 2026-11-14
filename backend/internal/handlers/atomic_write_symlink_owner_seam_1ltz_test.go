package handlers

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-1ltz ownership arms; the fixtures and the policy are described in
// atomic_write_symlink_owner_1ltz_test.go.

// seamForeignOwner makes every file look owned by someone other than the
// process, and records the chown of the temp file instead of doing it.
func seamForeignOwner(t *testing.T, chownErr error) *[][2]int {
	t.Helper()
	calls := &[][2]int{}
	oldOwner, oldChown := processOwner, chownTempFile
	t.Cleanup(func() { processOwner, chownTempFile = oldOwner, oldChown })
	processOwner = func() (int, int) { return os.Geteuid() + 1, os.Getegid() + 1 }
	chownTempFile = func(_ *os.File, uid, gid int) error {
		*calls = append(*calls, [2]int{uid, gid})
		return chownErr
	}
	return calls
}

func TestEnvSave_1ltz_LinkKeepsTargetOwner_Seam(t *testing.T) {
	router, stackID, token, target := linkedEnvFixture(t, 0o644)
	uid, gid := ownerOf(t, target)
	calls := seamForeignOwner(t, nil)

	w := putEntriesRaw(t, router, stackID, token, "A=2\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, [][2]int{{uid, gid}}, *calls, "the new file is chowned to the target's owner before the rename")
}

// C1 (fail closed): when the owner cannot be kept, nothing is written and the
// 409 says why.
func TestEnvSave_1ltz_OwnerNotKeptRefuses409(t *testing.T) {
	router, stackID, token, target := linkedEnvFixture(t, 0o644)
	seamForeignOwner(t, &os.PathError{Op: "fchown", Path: "x", Err: errors.New("operation not permitted")})

	w := putEntriesRaw(t, router, stackID, token, "A=2\n")
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "owner")
	assert.Equal(t, "A=1\n", readString(t, target), "nothing written")
	assert.Empty(t, leftoverTemps(t, filepath.Dir(target)))
}

// Not chowned at all when the target is already the process's own, so a
// normal non-root install never starts refusing saves (orch-dm-53 ruling D7).
func TestEnvSave_1ltz_OwnTargetNotChowned(t *testing.T) {
	router, stackID, token, _ := linkedEnvFixture(t, 0o644)
	calls := &[][2]int{}
	old := chownTempFile
	t.Cleanup(func() { chownTempFile = old })
	chownTempFile = func(_ *os.File, uid, gid int) error {
		*calls = append(*calls, [2]int{uid, gid})
		return errors.New("must not be called")
	}

	w := putEntriesRaw(t, router, stackID, token, "A=2\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Empty(t, *calls)
}

// The real chown, root only: a target owned by another user keeps that owner.
func TestEnvSave_1ltz_LinkKeepsTargetOwner_RealChown(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own a file as another user; the seam test above covers the call, this arm runs where tests run as root")
	}
	router, stackID, token, target := linkedEnvFixture(t, 0o644)
	require.NoError(t, os.Chown(target, 12345, 12346))

	w := putEntriesRaw(t, router, stackID, token, "A=2\n")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	uid, gid := ownerOf(t, target)
	assert.Equal(t, [2]int{12345, 12346}, [2]int{uid, gid})
}
