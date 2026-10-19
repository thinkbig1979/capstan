package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Deleting the sole stack in a folder removes the whole folder, and the
// survivor list used to be keyed on the exact stored path: a stack registered
// in a subfolder, or in the same folder through a symlink, was not a survivor,
// so its files went while its row stayed (agent-os-0s68).

var confirmAll = map[string]string{"confirm": "true", "confirmCollateral": "true"}

// addNestedStack registers a stack in a new subfolder of parentDir.
func (f *deleteSiblingFixture) addNestedStack(t *testing.T, parentDir, sub string) string {
	t.Helper()
	dir := filepath.Join(parentDir, sub)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	f.addSiblingStack(t, dir, "compose.yaml")
	return dir
}

func TestStacksHandler_DeleteNested_RefusesAndNamesNestedStack(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	f.create(t, "parent", "")
	subDir := f.addNestedStack(t, parentDir, "sub")
	parentID := f.stackIDFor(t, parentDir, "compose.yaml")
	nestedID := f.stackIDFor(t, subDir, "compose.yaml")
	nested, err := f.db.GetStack(nestedID)
	require.NoError(t, err)

	w := f.deleteWithParams(t, parentID, confirmAll)
	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())

	var body models.AppError
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, models.ErrStackDeleteNestedStacks, body.Code)
	assert.Contains(t, body.Message, nested.ProjectName+" ("+subDir+")",
		"the message is what the toast shows, so it must name the nested stack")
	details, ok := body.Details.(map[string]any)
	require.True(t, ok, "details must be an object, body=%s", w.Body.String())
	stacks, ok := details["stacks"].([]any)
	require.True(t, ok, "details.stacks must list the nested stacks, details=%v", details)
	require.Len(t, stacks, 1)
	assert.Equal(t, nestedID, stacks[0].(map[string]any)["id"])

	assert.FileExists(t, filepath.Join(subDir, "compose.yaml"), "the nested stack's files must survive")
	assert.FileExists(t, filepath.Join(parentDir, "compose.yaml"), "a refused delete removes nothing")
	_, err = f.db.GetStack(nestedID)
	assert.NoError(t, err, "the nested stack stays registered")
	_, err = f.db.GetStack(parentID)
	assert.NoError(t, err, "a refused delete keeps the deleted stack's row")
}

// The nested stack's stored path runs through a symlink, so it never names
// the parent's path; only the resolved comparison sees it.
func TestStacksHandler_DeleteNested_ThroughSymlinkStillRefused(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	f.create(t, "parent", "")
	require.NoError(t, os.MkdirAll(filepath.Join(parentDir, "sub"), 0o755))
	alias := filepath.Join(f.tempDir, "alias")
	require.NoError(t, os.Symlink(parentDir, alias))
	aliasSub := filepath.Join(alias, "sub")
	f.addSiblingStack(t, aliasSub, "compose.yaml")
	f.stackIDFor(t, aliasSub, "compose.yaml")

	w := f.deleteWithParams(t, f.stackIDFor(t, parentDir, "compose.yaml"), confirmAll)
	require.Equal(t, http.StatusConflict, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), models.ErrStackDeleteNestedStacks)
	assert.FileExists(t, filepath.Join(parentDir, "sub", "compose.yaml"))
}

// A stack registered through a symlink to the SAME folder is a sibling, not a
// nested stack: the delete takes only the deleted stack's own files, keeps the
// folder, and keeps the .env the sibling shares.
func TestStacksHandler_DeleteNested_AliasSiblingIsSurvivor(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	f.create(t, "parent", "SHARED=yes\n")
	alias := filepath.Join(f.tempDir, "alias")
	require.NoError(t, os.Symlink(parentDir, alias))
	f.addSiblingStack(t, alias, "compose.api.yaml")
	apiID := f.stackIDFor(t, alias, "compose.api.yaml")
	api, err := f.db.GetStack(apiID)
	require.NoError(t, err)
	require.Equal(t, ".env", api.EnvFile, "setup: the alias sibling must share the default .env")

	w := f.deleteWithParams(t, f.stackIDFor(t, parentDir, "compose.yaml"), confirmAll)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	assert.DirExists(t, parentDir, "the folder stays while a sibling registered through an alias lives in it")
	assert.FileExists(t, filepath.Join(parentDir, "compose.api.yaml"))
	assert.FileExists(t, filepath.Join(parentDir, ".env"), "the alias sibling's .env is the same file")
	assert.NoFileExists(t, filepath.Join(parentDir, "compose.yaml"), "the deleted stack's own file goes")
}

// An exact-folder sibling keeps the per-file path, so a nested stack is not at
// risk and nothing is refused.
func TestStacksHandler_DeleteNested_ExactSiblingUnchanged(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	f.create(t, "parent", "")
	f.addSiblingStack(t, parentDir, "compose.api.yaml")
	subDir := f.addNestedStack(t, parentDir, "sub")

	w := f.delete(t, f.stackIDFor(t, parentDir, "compose.yaml"))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.FileExists(t, filepath.Join(parentDir, "compose.api.yaml"))
	assert.FileExists(t, filepath.Join(subDir, "compose.yaml"))
	assert.NoFileExists(t, filepath.Join(parentDir, "compose.yaml"))
}

// A plain subfolder is collateral, not a stack: today's prompt, then removal.
func TestStacksHandler_DeleteNested_UnregisteredSubfolderKeepsCollateralPrompt(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	f.create(t, "parent", "")
	require.NoError(t, os.MkdirAll(filepath.Join(parentDir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, "sub", "notes.txt"), []byte("x"), 0o644))
	id := f.stackIDFor(t, parentDir, "compose.yaml")

	w := f.delete(t, id)
	require.Equal(t, http.StatusPreconditionRequired, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"collateral":["sub"]`)

	w = f.deleteWithParams(t, id, confirmAll)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.NoDirExists(t, parentDir)
}

// A stack in a sibling folder whose name only starts with the parent's is not
// inside it.
func TestStacksHandler_DeleteNested_SiblingFolderUnaffected(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	otherDir := filepath.Join(f.tempDir, "parent-other")
	f.create(t, "parent", "")
	f.create(t, "parent-other", "")
	otherID := f.stackIDFor(t, otherDir, "compose.yaml")

	w := f.deleteWithParams(t, f.stackIDFor(t, parentDir, "compose.yaml"), confirmAll)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.NoDirExists(t, parentDir)
	assert.FileExists(t, filepath.Join(otherDir, "compose.yaml"))
	_, err := f.db.GetStack(otherID)
	assert.NoError(t, err)
}

// A nested stack registered after Delete's refusal check, during compose down,
// is seen by removeStackFiles' own fresh read: the folder stays and only the
// deleted stack's files go. The nested stack's .env is a different file, so it
// must not keep the deleted stack's .env.
func TestRemoveStackFiles_DeleteNested_RegisteredMidDeleteKeepsFolder(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	parentDir := filepath.Join(f.tempDir, "parent")
	f.create(t, "parent", "A=1\n")
	parent, err := f.db.GetStack(f.stackIDFor(t, parentDir, "compose.yaml"))
	require.NoError(t, err)
	require.Equal(t, ".env", parent.EnvFile)

	subDir := filepath.Join(parentDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, ".env"), []byte("B=2\n"), 0o644))
	f.addSiblingStack(t, subDir, "compose.yaml")
	nested, err := f.db.GetStack(f.stackIDFor(t, subDir, "compose.yaml"))
	require.NoError(t, err)
	require.Equal(t, ".env", nested.EnvFile, "setup: the nested stack must have an .env of the same name")

	h := NewStacksHandler(&fakeStackDocker{}, f.scanner, services.NewLinterService(), f.db, f.cfg,
		services.NewActionLogger(f.db), services.NewOperationLock())
	require.NoError(t, h.removeStackFiles(*parent, parentDir, f.tempDir))

	assert.FileExists(t, filepath.Join(subDir, "compose.yaml"), "the nested stack's files must survive")
	assert.FileExists(t, filepath.Join(subDir, ".env"))
	assert.NoFileExists(t, filepath.Join(parentDir, "compose.yaml"))
	assert.NoFileExists(t, filepath.Join(parentDir, ".env"),
		"a nested stack's .env is another file and must not keep the deleted stack's .env")
}
