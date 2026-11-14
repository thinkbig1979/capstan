package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-3b2d: a write helper follows a symlink and must not land outside
// the root the write belongs under. The stack handlers check containment
// before writing (validateStackPath), but a link swapped after that check was
// written through; global.env had no check at all.

const keep3b2d = "KEEP=1\n"

// outsideLink3b2d makes a file outside root and a link to it at root/name.
func outsideLink3b2d(t *testing.T, root, name string) (link, outside string) {
	t.Helper()
	outside = filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(outside, []byte(keep3b2d), 0o600))
	link = filepath.Join(root, name)
	require.NoError(t, os.Symlink(outside, link))
	return link, outside
}

func assertUntouched3b2d(t *testing.T, outside string) {
	t.Helper()
	got, err := os.ReadFile(outside) //nolint:gosec // test fixture under t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, keep3b2d, string(got), "the file outside the root was written")
}

func TestWriteHelpers_RefuseLinkOutsideRoots_3b2d(t *testing.T) {
	cases := map[string]func(path string, roots []string) error{
		"writeEnvFileAtomic": func(p string, roots []string) error { return writeEnvFileAtomic(p, "A=2\n", roots) },
		"writeComposeFileAtomic": func(p string, roots []string) error {
			return writeComposeFileAtomic(p, []byte("services: {}\n"), roots)
		},
		"writeFileAtomic": func(p string, roots []string) error { return writeFileAtomic(p, []byte("x"), 0o644, roots) },
	}
	for name, write := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			link, outside := outsideLink3b2d(t, root, "f")

			err := write(link, []string{root})

			assertUntouched3b2d(t, outside)
			var refusal *writeRefusal
			require.ErrorAs(t, err, &refusal)
			assert.Contains(t, refusal.msg, "outside")
		})
	}
}

// A link to a file elsewhere inside one of the roots is written through, as
// before (agent-os-dwmz): only leaving every root is refused.
func TestWriteHelpers_LinkInsideAnotherRootWritten_3b2d(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	shared := filepath.Join(rootB, "shared.env")
	require.NoError(t, os.WriteFile(shared, []byte("A=1\n"), 0o600))
	link := filepath.Join(rootA, ".env")
	require.NoError(t, os.Symlink(shared, link))

	require.NoError(t, writeEnvFileAtomic(link, "A=2\n", []string{rootA, rootB}))

	got, err := os.ReadFile(shared) //nolint:gosec // test fixture under t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "A=2\n", string(got))
}

// No roots means nowhere is allowed: fail closed, not open.
func TestWriteHelpers_NoRootsRefused_3b2d(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	var refusal *writeRefusal
	require.ErrorAs(t, writeFileAtomic(path, []byte("x"), 0o644, nil), &refusal)
	_, err := os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
