package handlers

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-9deh (Edwin, 2026-11-15): an atomic rewrite of a compose file or a
// plain env file keeps the owner and group the file had, best effort. The
// rename puts a new inode in place, created by the process user; without a
// chown a host user who owned the file finds it owned by Capstan's uid.
//
// Best effort, unlike the symlinked env file (agent-os-1ltz, still 409): a
// chown that fails is logged and the save goes ahead as the process owner,
// which is what happened before. seamForeignOwner (1ltz) makes the existing
// file look foreign and records the chown.

func TestAtomicWrite_KeepsOwner_9deh(t *testing.T) {
	writers := map[string]func(path string, roots []string) error{
		"compose": func(path string, roots []string) error {
			return writeComposeFileAtomic(path, []byte("services: {}\n"), roots)
		},
		"plain env": func(path string, roots []string) error {
			return writeEnvFileAtomic(path, "A=2\n", roots)
		},
		// agent-os-b6u8 item 4: the base helper kept the owner too, so a
		// future caller of it cannot hand a host user's file to Capstan's uid.
		"writeFileAtomic": func(path string, roots []string) error {
			return writeFileAtomic(path, []byte("x\n"), 0o644, roots)
		},
	}
	for name, write := range writers {
		t.Run(name+"/existing file is chowned to its owner", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file")
			require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644)) //nolint:forbidigo // test fixture, not a stack write
			uid, gid := ownerOf(t, path)
			calls := seamForeignOwner(t, nil)

			require.NoError(t, write(path, []string{dir}))
			assert.Equal(t, [][2]int{{uid, gid}}, *calls, "the temp file takes the existing file's owner before the rename")
		})

		t.Run(name+"/a new file is not chowned", func(t *testing.T) {
			dir := t.TempDir()
			calls := seamForeignOwner(t, nil)

			require.NoError(t, write(filepath.Join(dir, "file"), []string{dir}))
			assert.Empty(t, *calls)
		})

		t.Run(name+"/a failed chown still saves, and says so", func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "file")
			require.NoError(t, os.WriteFile(path, []byte("old\n"), 0o644)) //nolint:forbidigo // test fixture, not a stack write
			seamForeignOwner(t, &os.PathError{Op: "fchown", Path: "x", Err: errors.New("operation not permitted")})
			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })

			require.NoError(t, write(path, []string{dir}))
			assert.NotEqual(t, "old\n", readString(t, path), "the save went ahead")
			assert.Contains(t, logs.String(), "level=WARN")
			assert.Contains(t, logs.String(), "file="+path)
			assert.Empty(t, leftoverTemps(t, dir))
		})
	}
}
