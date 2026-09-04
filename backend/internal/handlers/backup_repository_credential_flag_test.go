package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// plainRepoNoCredential is the contrast arm: a repository an operator can edit
// freely, with nothing hidden inside it. It is what makes the flag a
// DISCRIMINATOR rather than a constant — a hint that always shows is a worse UI
// than none, because it teaches operators to ignore it.
const plainRepoNoCredential = "/data/restic-repo"

// fetchBackupSettings drives the REAL handler over the real router with
// storedRepo persisted, and returns the raw response body alongside the decoded
// JSON. Both are returned on purpose: the flag assertions need the decoded map,
// and the leak assertions must look at the bytes actually sent, not at a value
// re-serialised by the test.
func fetchBackupSettings(t *testing.T, storedRepo string) (string, map[string]interface{}) {
	t.Helper()

	db := newBackupHandlerDB(t)
	require.NoError(t, db.SetSetting("restic_repository", storedRepo))

	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	t.Cleanup(h.Stop)
	r := newBackupRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	return w.Body.String(), decodeBody(t, w)
}

// assertRepositoryCredentialHidden is the leak instrument, factored out so the
// positive control below can fire the SAME assertions at a body that is known to
// leak. It takes assert.TestingT rather than *testing.T for exactly that reason.
//
// Both halves are required. "the credential is absent" alone is satisfied by
// blanking the field; "the marker is present" alone is satisfied by a value that
// carries both the marker and the secret.
func assertRepositoryCredentialHidden(t assert.TestingT, body string) {
	assert.Contains(t, body, services.UserinfoRedactionMarker,
		"the redaction marker must still be in the response — the flag is a label for it, not a replacement")
	assert.NotContains(t, body, "RESTICSECRET999",
		"the repository credential must never appear in the settings response")
}

// recordingT captures whether a testify assertion fired, without failing the
// enclosing test. It is the whole mechanism of the positive control.
type recordingT struct{ failed bool }

func (r *recordingT) Errorf(string, ...interface{}) { r.failed = true }

// TestGetSettings_FlagsARedactedRepositoryCredential is the discriminating test.
//
// Before this bead, a repository whose credential had been stripped and one that
// never had a credential were INDISTINGUISHABLE to the client: both arrived as a
// plain string in an ordinary editable input. The operator got no warning that
// editing the path would cost them a credential they may not know, until the
// agent-os-57xj guard returned a 422 after the fact.
//
// Both arms run on the same instrument. The "false" arm is the load-bearing one.
func TestGetSettings_FlagsARedactedRepositoryCredential(t *testing.T) {
	t.Parallel()

	redactedBody, redacted := fetchBackupSettings(t, embeddedCredRepo)
	plainBody, plain := fetchBackupSettings(t, plainRepoNoCredential)

	// Baseline probe artifact: the two responses as the client actually sees
	// them. Logged so the "before" state is legible in the failing run.
	t.Logf("credential-bearing repo -> repository=%q flag=%v", redacted["repository"], redacted["hasEmbeddedCredential"])
	t.Logf("plain repo              -> repository=%q flag=%v", plain["repository"], plain["hasEmbeddedCredential"])

	// The security half comes FIRST: this code path's entire purpose is keeping
	// the credential out of the body, and a new field must not weaken that.
	assertRepositoryCredentialHidden(t, redactedBody)

	withCred, ok := redacted["hasEmbeddedCredential"].(bool)
	require.True(t, ok, "hasEmbeddedCredential must be a bool in the settings response")
	assert.True(t, withCred,
		"a repository whose credential was redacted must be flagged, so the UI can say so before the operator edits it")

	withoutCred, ok := plain["hasEmbeddedCredential"].(bool)
	require.True(t, ok, "hasEmbeddedCredential must be a bool in the settings response")
	assert.False(t, withoutCred,
		"a repository that never had a credential must NOT be flagged — a hint that always shows is a worse UI than none")

	// The contrast arm must also be clean of the marker, otherwise "flag is
	// false" and "value is unredacted" are not the same claim.
	assert.NotContains(t, plainBody, services.UserinfoRedactionMarker,
		"a repository with no credential must be served byte-for-byte, with no marker spliced in")
}

// TestGetSettings_CredentialLeakAssertionCanFail is the positive control for the
// leak assertions above.
//
// Without it, "the credential is not in the response" is a result that could only
// have come out one way — an assertion pointed at the wrong string, or a helper
// that silently asserts nothing, produces the identical green. This feeds the
// same helper a body that DOES leak and confirms it fires.
func TestGetSettings_CredentialLeakAssertionCanFail(t *testing.T) {
	t.Parallel()

	leaking := `{"repository":"` + embeddedCredRepo + `","hasEmbeddedCredential":true}`

	rec := &recordingT{}
	assertRepositoryCredentialHidden(rec, leaking)
	require.True(t, rec.failed,
		"the leak instrument did not fire on a body containing the raw credential — the green in the test above would be meaningless")

	// And the other direction: it must NOT fire on a properly redacted body, or
	// it would be an assertion that always fails and proves nothing either.
	rec = &recordingT{}
	assertRepositoryCredentialHidden(rec, `{"repository":"rest:https://***@backup.example.com/repo/"}`)
	require.False(t, rec.failed,
		"the leak instrument fired on a correctly redacted body — it is not discriminating")
}
