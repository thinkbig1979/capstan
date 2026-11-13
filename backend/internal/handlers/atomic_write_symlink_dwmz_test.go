package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// These tests pin agent-os-dwmz: saving a compose or env file that is a
// symlink renamed the temp file onto the LINK, replacing it with a plain file
// and leaving its target, the file the link pointed at, unwritten.

func requireLinkTo(t *testing.T, link, want string) {
	t.Helper()
	info, err := os.Lstat(link)
	require.NoError(t, err)
	require.True(t, info.Mode()&os.ModeSymlink != 0, "%s is no longer a symlink (mode %v)", link, info.Mode())
	got, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixture under t.TempDir()
	require.NoError(t, err)
	return string(b)
}

func TestWriteComposeFileAtomic_SymlinkWritesTargetKeepsLink_dwmz(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	require.NoError(t, os.WriteFile(target, []byte("A"), 0640))
	require.NoError(t, os.Chmod(target, 0640)) //nolint:gosec // test fixture: the target's mode is under test
	link := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.Symlink("real.yaml", link))

	require.NoError(t, writeComposeFileAtomic(link, []byte("B")))

	requireLinkTo(t, link, "real.yaml")
	assert.Equal(t, "B", readString(t, target), "the link's target was not written")
	info, err := os.Stat(target)
	require.NoError(t, err)
	// A link's own mode is 0777 on Linux, so this also tells the target's mode
	// apart from the link's.
	assert.Equal(t, os.FileMode(0640), info.Mode().Perm(), "target mode not kept")
	assert.Empty(t, leftoverTemps(t, dir))
}

func TestWriteEnvFileAtomic_SymlinkChainWritesFinalTarget_dwmz(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "shared")
	require.NoError(t, os.MkdirAll(sub, 0755))
	target := filepath.Join(sub, "real.env")
	require.NoError(t, os.WriteFile(target, []byte("A=1\n"), 0644))
	mid := filepath.Join(dir, "mid.env")
	require.NoError(t, os.Symlink("shared/real.env", mid))
	link := filepath.Join(dir, ".env")
	require.NoError(t, os.Symlink("mid.env", link))

	require.NoError(t, writeEnvFileAtomic(link, "A=2\n"))

	requireLinkTo(t, link, "mid.env")
	requireLinkTo(t, mid, "shared/real.env")
	assert.Equal(t, "A=2\n", readString(t, target))
	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	assert.Empty(t, leftoverTemps(t, dir))
	assert.Empty(t, leftoverTemps(t, sub))
}

func TestWriteFileAtomic_RefusesUnwritableLinkShapes_dwmz(t *testing.T) {
	cases := map[string]struct {
		setup   func(t *testing.T, dir string) // makes dir/compose.yaml
		wantErr string
	}{
		"dangling": {
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.Symlink("missing.yaml", filepath.Join(dir, "compose.yaml")))
			},
			wantErr: "symlink to a missing file",
		},
		"loop": {
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.Symlink("other.yaml", filepath.Join(dir, "compose.yaml")))
				require.NoError(t, os.Symlink("compose.yaml", filepath.Join(dir, "other.yaml")))
			},
			wantErr: "cannot be followed",
		},
		"to a directory": {
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "adir"), 0755))
				require.NoError(t, os.Symlink("adir", filepath.Join(dir, "compose.yaml")))
			},
			wantErr: "not a regular file",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			link := filepath.Join(dir, "compose.yaml")
			before, err := os.Readlink(link)
			require.NoError(t, err)

			err = writeComposeFileAtomic(link, []byte("SECRET-dwmz"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), "SECRET-dwmz", "the error must not carry file contents")
			requireLinkTo(t, link, before)
			_, statErr := os.Lstat(filepath.Join(dir, "missing.yaml"))
			assert.True(t, os.IsNotExist(statErr), "a dangling link's target must not be created")
			assert.Empty(t, leftoverTemps(t, dir))
		})
	}
}

func TestWriteFileAtomic_RefusesNonRegularPlainPath_dwmz(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.MkdirAll(path, 0755))

	err := writeFileAtomic(path, []byte("new"), 0644)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
	info, statErr := os.Stat(path)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

// linkedComposeStack is one stack under root whose compose.yaml is a symlink
// to linkTarget, served by a real ComposeHandler.
func linkedComposeStack(t *testing.T, root, linkTarget string) (*gin.Engine, string) {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	dir := filepath.Join(root, "stack1")
	require.NoError(t, os.MkdirAll(dir, 0755))
	link := filepath.Join(dir, "compose.yaml")
	require.NoError(t, os.Symlink(linkTarget, link))
	createTestDirectory(t, db, dir)
	require.NoError(t, db.UpsertStack(models.Stack{
		ID: "stack1", Directory: dir, ComposeFile: "compose.yaml", ProjectName: "stack1",
	}))

	h := NewComposeHandler(services.NewLinterService(), db, &config.Config{StacksDir: root})
	h.SetOperationLock(services.NewOperationLock())
	router := gin.New()
	h.RegisterRoutes(router.Group("/api"))
	return router, link
}

const linkedComposeNew = "services:\n  web:\n    image: nginx\n"

func putCompose(t *testing.T, router *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(ComposeRequest{Content: linkedComposeNew})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/stack1/compose", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestComposePut_SymlinkInsideRootWritesTarget_dwmz(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	require.NoError(t, os.MkdirAll(shared, 0755))
	target := filepath.Join(shared, "compose.yaml")
	require.NoError(t, os.WriteFile(target, []byte("services: {}\n"), 0644))

	router, link := linkedComposeStack(t, root, "../shared/compose.yaml")
	w := putCompose(t, router)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	requireLinkTo(t, link, "../shared/compose.yaml")
	assert.Equal(t, linkedComposeNew, readString(t, target))
}

// Pins the existing refusal (validateStackPath resolves the link) so the fix,
// which follows links, cannot be used to write outside the stacks root.
func TestComposePut_SymlinkOutsideRootRefused_dwmz(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "compose.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("services: {}\n"), 0644))

	router, link := linkedComposeStack(t, t.TempDir(), outside)
	w := putCompose(t, router)

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Invalid compose file path")
	requireLinkTo(t, link, outside)
	assert.Equal(t, "services: {}\n", readString(t, outside), "a file outside the stacks root was written")
}
