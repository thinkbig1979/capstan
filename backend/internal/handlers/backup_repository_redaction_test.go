package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const embeddedCredRepo = "rest:https://bob:RESTICSECRET999@backup.example.com/repo/"

// TestGetSettings_RepositoryCredentialNeverInResponse covers the restic half of
// agent-os-57xj at the HTTP boundary.
//
// A restic repository URI legitimately embeds credentials, and this response
// served it raw. The adjacent hasPassword/passwordSource fields already mask
// the password field, which is what made the gap easy to miss: the author
// masked the password and left the field that carries the same password.
//
// It exists as its own test because a mutation showed the service-level suite
// does not cover this call site — reverting the redaction here left everything
// green.
func TestGetSettings_RepositoryCredentialNeverInResponse(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", embeddedCredRepo))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "RESTICSECRET999",
		"the repository credential must never appear in the settings response")
	// Positive half: the operator must still recognise their own repository,
	// so this is not satisfied by blanking the field.
	assert.Contains(t, w.Body.String(), "backup.example.com",
		"the redacted repository must still identify the backend")
}

// TestUpdateSettings_RefusesToPersistARedactedRepository is the data-loss guard.
//
// The UI seeds its editable Repository input from the GET response, which is
// now redacted. Without this, an operator who edits any OTHER part of the field
// — fixing a typo in the path — POSTs back the "***" form and DESTROYS the
// stored credential, silently and unrecoverably.
//
// The assertion that matters is the LAST one: the stored value is unchanged.
// A guard that returns 422 while still writing would pass a status-only test.
func TestUpdateSettings_RefusesToPersistARedactedRepository(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", embeddedCredRepo))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	body := `{"repository":"rest:https://***@backup.example.com/repo-renamed/"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/backup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code,
		"a repository value still carrying the redaction marker must be rejected, not stored")

	stored, err := db.GetSetting("restic_repository")
	require.NoError(t, err)
	assert.Equal(t, embeddedCredRepo, stored,
		"the stored credential was destroyed — this is the data-loss the guard exists to prevent")
}

// TestUpdateSettings_AcceptsARealRepository is the control. Without it, the
// guard above is satisfied by rejecting every write.
func TestUpdateSettings_AcceptsARealRepository(t *testing.T) {
	t.Parallel()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", embeddedCredRepo))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	const replacement = "rest:https://bob:ANOTHERSECRET@backup.example.com/repo2/"
	body := `{"repository":"` + replacement + `"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings/backup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.True(t, w.Code < 300, "a genuine repository write must succeed, got %d: %s", w.Code, w.Body.String())

	stored, err := db.GetSetting("restic_repository")
	require.NoError(t, err)
	assert.Equal(t, replacement, stored, "a genuine repository write must be persisted verbatim")
	assert.False(t, strings.Contains(stored, "***"), "the stored value must be the real one")
}
