package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// registerThenSwapForSymlink registers a real stack directory at
// root/<rel>, then replaces the directory at root/<swap> with a symlink to
// target. That is how a stack row comes to name a path running through a link:
// the scanner never follows symlinks, so the link has to appear after the scan
// (an operator's mv, or a git pull that turns a directory into a link).
func registerThenSwapForSymlink(t *testing.T, f *deleteSiblingFixture, rel, swap, target string) string {
	t.Helper()
	stackDir := filepath.Join(f.tempDir, rel)
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "compose.yaml"), []byte(deleteSiblingCompose), 0o644))
	require.NoError(t, f.scanner.ScanDirectoryWithRoot(stackDir, f.tempDir))

	swapPath := filepath.Join(f.tempDir, swap)
	require.NoError(t, os.RemoveAll(swapPath))
	require.NoError(t, os.Symlink(target, swapPath))
	return stackDir
}

// A directory BETWEEN the root and the stack directory is a symlink to a place
// outside the root. The lexical guard saw root/a/app as inside the root and
// os.RemoveAll then walked through the link and deleted outside/app
// (agent-os-z91e.22).
func TestStacksHandler_Delete_RefusesSymlinkedAncestorOutsideRoot(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	outside := t.TempDir()
	victim := filepath.Join(outside, "app")
	require.NoError(t, os.MkdirAll(victim, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(victim, "compose.yaml"), []byte(deleteSiblingCompose), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(victim, "data.db"), []byte("keep me"), 0o644))

	stackDir := registerThenSwapForSymlink(t, f, filepath.Join("a", "app"), "a", outside)
	id := f.stackIDFor(t, stackDir, "compose.yaml")

	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?confirm=true&confirmCollateral=true", nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)

	assert.GreaterOrEqual(t, w.Code, 400, "a stack dir reached through a symlink leaving the root must be refused, body=%s", w.Body.String())
	assert.FileExists(t, filepath.Join(victim, "data.db"), "nothing outside the stacks root may be deleted")
	assert.FileExists(t, filepath.Join(victim, "compose.yaml"), "nothing outside the stacks root may be deleted")
}

// The stack directory ITSELF is a symlink inside the root. os.RemoveAll does
// not follow a final symlink, so the delete removes only the link and must keep
// working: the containment check is applied to the parent, not the full path.
func TestStacksHandler_Delete_StackDirItselfSymlinkRemovesOnlyTheLink(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "compose.yaml"), []byte(deleteSiblingCompose), 0o644))

	stackDir := registerThenSwapForSymlink(t, f, "linked", "linked", target)
	id := f.stackIDFor(t, stackDir, "compose.yaml")

	w := f.delete(t, id)

	assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	_, err := os.Lstat(stackDir)
	assert.True(t, os.IsNotExist(err), "the link itself is removed, Lstat err=%v", err)
	assert.FileExists(t, filepath.Join(target, "compose.yaml"), "the link's target survives")
}

// The stack directory is a symlink to a place outside the root and a sibling
// stack shares it, so Delete takes the per-file branch: os.Remove of
// link/compose.api.yaml follows the link and deletes a file outside the root.
// The directory files are removed FROM must itself resolve inside the root.
func TestStacksHandler_Delete_PerFileRemovalRefusesStackDirLinkedOutsideRoot(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.MkdirAll(target, 0o755))

	stackDir := filepath.Join(f.tempDir, "linked")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	for _, name := range []string{"compose.yaml", "compose.api.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, name), []byte(deleteSiblingCompose), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(target, name), []byte(deleteSiblingCompose), 0o644))
	}
	require.NoError(t, f.scanner.ScanDirectoryWithRoot(stackDir, f.tempDir))
	id := f.stackIDFor(t, stackDir, "compose.api.yaml")
	require.NoError(t, os.RemoveAll(stackDir))
	require.NoError(t, os.Symlink(target, stackDir))

	w := f.delete(t, id)

	assert.GreaterOrEqual(t, w.Code, 400, "body=%s", w.Body.String())
	assert.FileExists(t, filepath.Join(target, "compose.api.yaml"), "nothing outside the stacks root may be deleted")
}

// A stack in an EXTRA stacks dir with a surviving sibling takes the per-file
// branch, which checks the stack dir against the root Delete's guard matched.
// That root must be the extra dir, not the main one, or every such delete is
// refused (agent-os-z91e.22).
func TestStacksHandler_Delete_PerFileRemovalInExtraStacksDir(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	extra := t.TempDir()
	f.cfg.ExtraStacksDirs = []string{extra}

	stackDir := filepath.Join(extra, "shared")
	require.NoError(t, os.MkdirAll(stackDir, 0o755))
	for _, name := range []string{"compose.yaml", "compose.api.yaml"} {
		require.NoError(t, os.WriteFile(filepath.Join(stackDir, name), []byte(deleteSiblingCompose), 0o644))
	}
	require.NoError(t, f.scanner.ScanDirectoryWithRoot(stackDir, extra))
	id := f.stackIDFor(t, stackDir, "compose.api.yaml")

	w := f.delete(t, id)

	assert.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.NoFileExists(t, filepath.Join(stackDir, "compose.api.yaml"), "the deleted stack's own compose file goes")
	assert.FileExists(t, filepath.Join(stackDir, "compose.yaml"), "the surviving sibling's compose file stays")
}
