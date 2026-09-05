package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// The two shapes that separate a flag DERIVED FROM THE REDACTION from one
// GUESSED FROM THE STRING. Both contain an "@" and neither hides a secret, so a
// naive strings.Contains(repo, "@") implementation passes the two arms above and
// FAILS these — rendering a hint that tells the operator a credential is hidden
// when none is. RedactURLUserinfo states the principle these pin: a false
// marker is worse than no marker, because the marker's whole purpose is that
// signal.
const (
	// An empty userinfo: an "@" with nothing in front of it.
	emptyUserinfoRepo = "http://@host/path"
	// restic's documented SFTP form. The more realistic of the two: an operator
	// can plausibly have this configured, and "user" is a username, not a
	// secret. RedactURLUserinfo leaves it alone (it has no "://", so the
	// opaque-part fallback returns it untouched).
	sftpUserRepo = "sftp:user@host:/srv/restic-repo"
	// An empty username AND an empty password: what a compose template renders
	// when both variables are unset. Go parses that password as empty but SET,
	// so u.User.String() is ":" rather than "", the empty-userinfo guard missed
	// it, and the marker was spliced into a URI carrying nothing
	// (agent-os-zzhs).
	emptyBothUserinfoRepo = "rest:http://:@backup.example.com:8000/repo/"
)

// slashCredRepo is agent-os-zzhs arm 1 at the HTTP boundary: the SAME secret as
// embeddedCredRepo, with a "/" in front of it. url.Parse reads that "/" as
// ending the authority, so the parse fails and the whole credential was served
// in clear. An AWS secret access key contains a "/" about 46% of the time.
//
// It reuses RESTICSECRET999 deliberately, so assertRepositoryCredentialHidden
// is literally the same instrument that is already proven to fire, by
// TestGetSettings_CredentialLeakAssertionCanFail.
const slashCredRepo = "rest:https://bob:AB/CD+RESTICSECRET999@backup.example.com/repo/"

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

// recordingT captures what a testify assertion said when it fired, without
// failing the enclosing test. It is the whole mechanism of the positive control.
//
// It stores the MESSAGE, not merely a bool. A recorder that stored only "it
// fired" can show the helper ran but not what it caught — a helper that failed
// for an unrelated reason (a typo'd key, a changed marker) would look identical
// to one that caught the credential. Those are different claims, and only the
// text tells them apart.
type recordingT struct{ messages []string }

func (r *recordingT) Errorf(format string, args ...interface{}) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}

func (r *recordingT) fired() bool  { return len(r.messages) > 0 }
func (r *recordingT) text() string { return strings.Join(r.messages, "\n") }

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
	slashBody, slashed := fetchBackupSettings(t, slashCredRepo)

	// Baseline probe artifact: the two responses as the client actually sees
	// them. Logged so the "before" state is legible in the failing run.
	t.Logf("credential-bearing repo -> repository=%q flag=%v", redacted["repository"], redacted["hasEmbeddedCredential"])
	t.Logf("plain repo              -> repository=%q flag=%v", plain["repository"], plain["hasEmbeddedCredential"])

	// The security half comes FIRST: this code path's entire purpose is keeping
	// the credential out of the body, and a new field must not weaken that.
	assertRepositoryCredentialHidden(t, redactedBody)

	// agent-os-zzhs arm 1. Same instrument, same secret, one "/" added — which
	// is the whole difference between a credential that was stripped and one
	// that reached the browser DOM in clear.
	t.Logf("slashed credential repo  -> repository=%q flag=%v", slashed["repository"], slashed["hasEmbeddedCredential"])
	assertRepositoryCredentialHidden(t, slashBody)
	slashFlagged, ok := slashed["hasEmbeddedCredential"].(bool)
	require.True(t, ok, "hasEmbeddedCredential must be a bool in the settings response")
	assert.True(t, slashFlagged,
		"a credential containing a / must be redacted AND flagged; before agent-os-zzhs the flag read false while the secret sat in the response, so the ABSENCE of the hint was an affirmative and wrong all-clear")

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

	// The arms that kill the impostor. Both values contain an "@" and neither
	// hides a secret, so an implementation that looked for "@" rather than
	// asking the redactor what it actually removed would flag both TRUE and
	// warn about a field that hides nothing.
	for _, tc := range []struct {
		name string
		repo string
	}{
		{"empty userinfo", emptyUserinfoRepo},
		{"sftp username, restic's documented form", sftpUserRepo},
		{"empty username AND empty password", emptyBothUserinfoRepo},
	} {
		body, decoded := fetchBackupSettings(t, tc.repo)
		t.Logf("%-40s -> repository=%q flag=%v", tc.name, decoded["repository"], decoded["hasEmbeddedCredential"])

		flagged, ok := decoded["hasEmbeddedCredential"].(bool)
		require.True(t, ok, "hasEmbeddedCredential must be a bool in the settings response")
		assert.False(t, flagged,
			"%s carries an @ but no secret, so it must NOT be flagged — a hint claiming a hidden credential where there is none is the false marker RedactURLUserinfo's empty-userinfo guard exists to avoid",
			tc.name)
		// And the value itself must come back untouched, so "not flagged" and
		// "nothing was removed" remain the same claim rather than two hopes.
		assert.Contains(t, body, tc.repo,
			"%s must be served byte-for-byte", tc.name)
	}
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
	require.True(t, rec.fired(),
		"the leak instrument did not fire on a body containing the raw credential — the green in the test above would be meaningless")
	t.Logf("FIRED, verbatim:\n%s", rec.text())
	// It fired for the RIGHT REASON. "An assertion ran" and "the assertion
	// caught the credential" are different claims, and only the message text
	// separates them: a helper broken in some unrelated way would also fire.
	assert.Contains(t, rec.text(), "RESTICSECRET999",
		"the instrument fired, but not about the credential — it is not the leak that was detected")

	// And the other direction: it must NOT fire on a properly redacted body, or
	// it would be an assertion that always fails and proves nothing either.
	rec = &recordingT{}
	assertRepositoryCredentialHidden(rec, `{"repository":"rest:https://***@backup.example.com/repo/"}`)
	assert.False(t, rec.fired(),
		"the leak instrument fired on a correctly redacted body — it is not discriminating; it said: %s", rec.text())
	t.Logf("DID NOT FIRE on a redacted body, captured messages: %d (%q)", len(rec.messages), rec.text())
}
