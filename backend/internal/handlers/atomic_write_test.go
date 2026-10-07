package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func leftoverTemps(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestWriteFileAtomic_ModeIsExactlyTheArgument(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0644, 0664} {
		path := filepath.Join(t.TempDir(), "f")
		require.NoError(t, writeFileAtomic(path, []byte("x"), mode))
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, mode, info.Mode().Perm())
	}
}

func TestWriteFileAtomic_RenameFailureRemovesTemp(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory at path makes os.Rename fail after the temp file
	// was fully written, the last step that can fail.
	target := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0755))

	err := writeFileAtomic(target, []byte("new"), 0644)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rename temp file into place")
	assert.Empty(t, leftoverTemps(t, dir), "temp file survived a failed rename")
}

func TestWriteFileAtomic_CreateTempFailureLeavesTargetAlone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "missing")
	err := writeFileAtomic(filepath.Join(dir, "f"), []byte("x"), 0644)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create temp file")
}

func TestWriteComposeFileAtomic_KeepsExistingModeAndDefaultsNew(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.yaml")
	require.NoError(t, os.WriteFile(existing, []byte("old"), 0664))
	require.NoError(t, os.Chmod(existing, 0664)) //nolint:gosec // test fixture: the mode is the thing under test, on a t.TempDir() file
	require.NoError(t, writeComposeFileAtomic(existing, []byte("new")))
	info, err := os.Stat(existing)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0664), info.Mode().Perm(), "existing mode dropped")

	fresh := filepath.Join(dir, "fresh.yaml")
	require.NoError(t, writeComposeFileAtomic(fresh, []byte("new")))
	info, err = os.Stat(fresh)
	require.NoError(t, err)
	assert.Equal(t, composeFileMode, info.Mode().Perm())
}

func TestWriteEnvFileAtomic_TightensLooserExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	require.NoError(t, os.Chmod(path, 0644)) //nolint:gosec // test fixture: a deliberately loose existing mode, on a t.TempDir() file
	require.NoError(t, writeEnvFileAtomic(path, "A=1\n"))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestWriteComposeFileAtomic_RefusesReadOnlyExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0444))  //nolint:gosec // test fixture: a deliberately read-only compose file on a t.TempDir() path
	if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil { //nolint:gosec // test probe on a t.TempDir() path, write-only without O_TRUNC
		f.Close()
		t.Skip("running as a user that ignores file modes (root); read-only cannot be simulated")
	}

	err := writeComposeFileAtomic(path, []byte("new"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not writable")
	//nolint:gosec // path is under the test's own TempDir
	got, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "old", string(got), "a refused write must leave the file alone")
	assert.Empty(t, leftoverTemps(t, dir))
}
