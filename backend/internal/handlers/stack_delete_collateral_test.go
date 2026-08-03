package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deleteWithParams is like deleteSiblingFixture.delete but lets a test control
// the full query string, so it can add confirmCollateral alongside confirm.
func (f *deleteSiblingFixture) deleteWithParams(t *testing.T, id string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodDelete, "/stacks/"+id+"?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

// TestStacksHandler_Delete_SoleStackWithCollateral_RequiresAcknowledgement is the
// regression guard for agent-os-lg2: deleting the sole stack registered under a
// directory used to os.RemoveAll the whole directory unconditionally, taking any
// bind-mounted data (a Postgres data dir, uploads, .git, operator notes) with it
// — with no warning and no way to say no. That is the same destructive path the
// xa7 fix (agent-os-xa7) already closed for the sibling-present case, so a bare
// delete on the sole-stack path must now refuse and enumerate what would be lost,
// rather than silently destroying it.
func TestStacksHandler_Delete_SoleStackWithCollateral_RequiresAcknowledgement(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "my-stack")

	f.create(t, "my-stack", "")

	// A bind-mounted data directory sitting next to the compose file — the
	// common Docker Compose idiom the bug report calls out.
	dataDir := filepath.Join(stackDir, "data", "pgdata")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	dbFile := filepath.Join(dataDir, "base.db")
	require.NoError(t, os.WriteFile(dbFile, []byte("irreplaceable"), 0o644))

	id := f.stackIDFor(t, stackDir, "compose.yaml")

	// First call: no acknowledgement of the collateral. Must refuse without
	// touching the filesystem, and must enumerate what it would have destroyed.
	w := f.delete(t, id)
	require.Equal(t, http.StatusPreconditionRequired, w.Code,
		"a sole-stack delete with collateral present must refuse until acknowledged, body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "data", "the response must enumerate the collateral entry that would be destroyed")

	assert.FileExists(t, dbFile, "collateral data must survive an unacknowledged delete")
	assert.DirExists(t, stackDir, "the directory must survive an unacknowledged delete")

	remaining, err := f.db.ListStacksByDirectory(stackDir)
	require.NoError(t, err)
	assert.Len(t, remaining, 1, "the stack row must survive a refused delete")

	// Second call: acknowledged. Now the whole directory goes, exactly as the
	// sole-stack path always has.
	req2 := f.deleteWithParams(t, id, map[string]string{"confirm": "true", "confirmCollateral": "true"})
	require.Equal(t, http.StatusOK, req2.Code, "an acknowledged delete must succeed, body=%s", req2.Body.String())
	assert.NoDirExists(t, stackDir, "an acknowledged delete must still remove the directory")
}

// TestStacksHandler_Delete_SiblingPresent_CollateralNeverAtRisk asserts the
// sibling-present case behaves identically to an acknowledged sole-stack
// delete: collateral data is never destroyed, so no new prompt is needed there
// either. This is the other half of "same effect on non-compose content whether
// or not a sibling stack is registered" from the bead's acceptance criteria.
func TestStacksHandler_Delete_SiblingPresent_CollateralNeverAtRisk(t *testing.T) {
	f := newDeleteSiblingFixture(t)
	stackDir := filepath.Join(f.tempDir, "my-stack")

	f.create(t, "my-stack", "")
	f.addSiblingStack(t, stackDir, "compose.api.yaml")

	dataDir := filepath.Join(stackDir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	dbFile := filepath.Join(dataDir, "base.db")
	require.NoError(t, os.WriteFile(dbFile, []byte("irreplaceable"), 0o644))

	defaultID := f.stackIDFor(t, stackDir, "compose.yaml")

	// No confirmCollateral needed: the sibling-present path never removes
	// anything but this stack's own compose/env file.
	w := f.delete(t, defaultID)
	require.Equal(t, http.StatusOK, w.Code, "delete must succeed without a collateral prompt, body=%s", w.Body.String())

	assert.FileExists(t, dbFile, "collateral data must survive when a sibling remains registered")
	assert.DirExists(t, stackDir)
}
