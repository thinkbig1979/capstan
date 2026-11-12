package handlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-qnso at the HTTP boundary: GET /settings/backup served the
// rclone remote in clear, and it can be a connection string carrying keys. ---

const qnsoHandlerRemote = ":s3,access_key_id=AKQNSOH,secret_access_key=SKQNSOH"

func qnsoGetSettings(t *testing.T, remote string) map[string]any {
	t.Helper()
	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", remote))
	h := NewBackupHandler(buildBackupSvc(t, db, true, false), db, slog.Default())
	t.Cleanup(h.Stop)
	w := httptest.NewRecorder()
	newBackupRouter(h).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "AKQNSOH")
	assert.NotContains(t, w.Body.String(), "SKQNSOH")
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func TestQnso_GetSettingsMasksTheRcloneRemote(t *testing.T) {
	t.Parallel()
	body := qnsoGetSettings(t, qnsoHandlerRemote)
	assert.Equal(t, ":s3,access_key_id=***,secret_access_key=***", body["rcloneRemote"])
	assert.Equal(t, true, body["rcloneRemoteHasCredential"])
}

// Control: a named remote holds no credential and is served as it is.
func TestQnso_GetSettingsNamedRemoteIsServedAsIs(t *testing.T) {
	t.Parallel()
	body := qnsoGetSettings(t, "myremote")
	assert.Equal(t, "myremote", body["rcloneRemote"])
	assert.Equal(t, false, body["rcloneRemoteHasCredential"])
}

func qnsoPut(t *testing.T, remote string) (int, string) {
	t.Helper()
	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("rclone_remote", qnsoHandlerRemote))
	h := NewBackupHandler(buildBackupSvc(t, db, true, false), db, slog.Default())
	t.Cleanup(h.Stop)
	raw, err := json.Marshal(map[string]string{"rcloneRemote": remote})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/settings/backup", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newBackupRouter(h).ServeHTTP(w, req)
	stored, err := db.GetSetting("rclone_remote")
	require.NoError(t, err)
	return w.Code, stored
}

// The form seeds its input from the masked GET value; posting it back must
// not overwrite the stored keys with "***".
func TestQnso_PutRefusesTheMaskedRemote(t *testing.T) {
	t.Parallel()
	code, stored := qnsoPut(t, ":s3,access_key_id=***,secret_access_key=***")
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.Equal(t, qnsoHandlerRemote, stored, "the stored credential must survive")
}

// Control: a real new remote is stored verbatim.
func TestQnso_PutStoresARealRemote(t *testing.T) {
	t.Parallel()
	const replacement = ":s3,access_key_id=AK2,secret_access_key=SK2"
	code, stored := qnsoPut(t, replacement)
	require.Less(t, code, 300)
	assert.Equal(t, replacement, stored)
}
