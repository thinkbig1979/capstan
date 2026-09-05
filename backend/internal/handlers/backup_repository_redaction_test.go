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

// TestUpdateSettings_AcceptsARepositoryThatHidesNothing is agent-os-zzhs arm 2
// at the HTTP boundary, and it is a LOCKOUT test rather than a leak test.
//
// "rest:http://:@host:8000/repo/" is what a compose template renders when both
// the user and the password variable are unset. It carries no secret, but the
// redactor spliced the marker into it anyway, so the served value contained
// "***@" — and the agent-os-57xj guard above then rejected EVERY save of that
// field. The operator was told a credential was embedded in a repository that
// has none, and could not save an edit to it. No attacker involved.
//
// This drives the operator's actual flow: GET the settings, PUT back exactly
// what the API served. Asserting on the STORED value, not just the status, for
// the same reason as the guard test above.
func TestUpdateSettings_AcceptsARepositoryThatHidesNothing(t *testing.T) {
	t.Parallel()

	const repo = "rest:http://:@backup.example.com:8000/repo/"

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", repo))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	getReq := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)
	require.Equal(t, http.StatusOK, getW.Code)

	served, ok := decodeBody(t, getW)["repository"].(string)
	require.True(t, ok, "repository must be a string in the settings response")
	require.Equal(t, repo, served,
		"a repository with an empty username AND an empty password hides nothing, so it must be served byte-for-byte")

	// Now put back what the UI was given, unchanged. Under the defect this
	// returned 422 and the field could never be saved.
	body := `{"repository":"` + served + `"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/settings/backup", bytes.NewBufferString(body))
	putReq.Header.Set("Content-Type", "application/json")
	putW := httptest.NewRecorder()
	r.ServeHTTP(putW, putReq)

	require.True(t, putW.Code < 300,
		"the operator cannot save a field the API told them carries a credential it does not, got %d: %s", putW.Code, putW.Body.String())

	stored, err := db.GetSetting("restic_repository")
	require.NoError(t, err)
	assert.Equal(t, repo, stored, "the round-tripped value must be persisted verbatim")
}
