package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// unreadableRoot3b2d is a root IsContained cannot check: a path under a
// regular file, so resolving it fails with ENOTDIR rather than "not exist".
func unreadableRoot3b2d(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "plain")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	return filepath.Join(file, "sub")
}

// A root that cannot be checked does not count as containing the target:
// with no other root, the write fails (a server fault, not a refusal) and
// nothing is written.
func TestWriteHelpers_UncheckableRootFailsClosed_3b2d(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")

	err := writeFileAtomic(path, []byte("x"), 0o644, []string{unreadableRoot3b2d(t)})

	require.Error(t, err)
	var refusal *writeRefusal
	assert.False(t, errors.As(err, &refusal), "an unchecked root is a fault, not the user's link: %v", err)
	_, statErr := os.Stat(path)
	assert.ErrorIs(t, statErr, os.ErrNotExist, "written although no root could be checked")
}

// The skip rule: an uncheckable root is passed over, and another root that
// does contain the target still allows the write.
func TestWriteHelpers_UncheckableRootSkipped_3b2d(t *testing.T) {
	good := t.TempDir()
	path := filepath.Join(good, "f")

	require.NoError(t, writeFileAtomic(path, []byte("x"), 0o644, []string{unreadableRoot3b2d(t), good}))

	got, err := os.ReadFile(path) //nolint:gosec // test fixture under t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "x", string(got))
}

// PUT /compose-env checks the path before refuseMaskedSave reads the file, as
// PUT /compose does (TestComposePut_SymlinkOutsideRootRefused_dwmz): a compose
// link out of the stacks roots answers 400, not the write helper's 409, and
// the file outside is neither read for the masked check nor written.
func TestComposeEnvPut_SymlinkOutsideRootRefused_3b2d(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "compose.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("services: {}\n"), 0o644))

	router, link := linkedComposeStack(t, t.TempDir(), outside)
	body, err := json.Marshal(ComposeEnvRequest{ComposeContent: linkedComposeNew})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/stack1/compose-env", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Invalid compose file path")
	requireLinkTo(t, link, outside)
	assert.Equal(t, "services: {}\n", readString(t, outside), "a file outside the stacks root was written")
}
