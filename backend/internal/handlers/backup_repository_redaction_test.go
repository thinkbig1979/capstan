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

// qonwRejectCorpus is agent-os-qonw's REJECT corpus: opaque repository forms
// carrying an inline credential with NO "://", which RedactURLUserinfo served
// in clear with an ordinary password. None is a form restic can use (restic
// 0.18.0 reads "sftp:user:PW@host:/path" as host "user"; its s3 and sftp
// parsers never read a URL password; rclone finds no remote named "user"; a
// connection-string pass= IS consumed, but belongs in rclone.conf), so a save
// is refused with a message naming the supported form, and a row stored before
// the validator existed is served starred.
//
// Every row reuses RESTICSECRET999 so assertRepositoryCredentialHidden is the
// instrument TestGetSettings_CredentialLeakAssertionCanFail already proves.
var qonwRejectCorpus = []struct {
	name     string
	stored   string
	served   string
	mustSay  string
	mustName string
}{
	{"sftp opaque with password", "sftp:user:RESTICSECRET999@host:/path",
		"sftp:***@host:/path", "sftp:", "sftp:user@host:/path"},
	{"s3 opaque with key and secret", "s3:AKIAKEY:RESTICSECRET999@host/bucket",
		"s3:***@host/bucket", "s3:", "AWS_SECRET_ACCESS_KEY"},
	{"rclone connection string with an inline credential", "rclone::sftp,host=h,user=u,pass=RESTICSECRET999:path",
		"rclone::sftp,host=h,user=u,pass=***:path", `"pass"`, "rclone.conf"},
}

// qonwLeaveCorpus is the discriminating half: every row saves 2xx and is served
// BYTE-FOR-BYTE with the flag false. Over-starring any of them is a lockout —
// backup.go's marker guard then refuses every save of the field (agent-os-zzhs
// arm 2) — and a validator that refuses any of them is a worse outcome than
// the leak.
var qonwLeaveCorpus = []string{
	"sftp:user@host:/srv/backups",
	"b2:bucketname:path/to/repo",
	"rclone:remote:path",
	"git@github.com:org/repo.git",
	"s3:https://s3.host/bucket",
	"/var/backups/repo",
	"rclone::sftp,host=h,user=u:path",
	// rclone's remote:path grammar has no credential position: this is
	// remote "user" with path "PW@remote:path". Only connection-string
	// parameters can carry a secret.
	"rclone:user:PW@remote:path",
	// Reviewer rows at 5cacb66: paths a container-tooling product WILL have,
	// every one starred by the first rule and then locked out by the marker
	// guard.
	"sftp:user@host:/backups/nginx@sha256:abc123",
	"sftp:host:/a@b:c",
	"rclone:remote:backups/nginx@sha256:abc123",
	"rclone::sftp,host=h,user=u,ask_password=true:path",
}

// qonwRedactedAsTodayCorpus: saved 2xx, served STARRED, flag true, unchanged by
// this bead. These are not byte-for-byte rows: rest: documents an inline
// credential, and the sftp URL form's username-only userinfo is redacted by
// design (agent-os-57xj; precedent ssh://git@host/repo.git).
var qonwRedactedAsTodayCorpus = []struct{ stored, served string }{
	{"rest:https://user:pw@host/", "rest:https://***@host/"},
	{"sftp://user@host:2222/path", "sftp://***@host:2222/path"},
}

func putRepository(t *testing.T, r http.Handler, repo string) *httptest.ResponseRecorder {
	t.Helper()
	req := jsonReq(t, http.MethodPut, "/api/settings/backup", map[string]interface{}{"repository": repo})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestUpdateSettings_RefusesAnOpaqueRepositoryWithAnInlineCredential is
// agent-os-qonw's save-time arm.
//
// The assertion that matters is the LAST one: the stored value is untouched. A
// validator that returns 422 while still writing passes a status-only test.
func TestUpdateSettings_RefusesAnOpaqueRepositoryWithAnInlineCredential(t *testing.T) {
	t.Parallel()

	for _, tc := range qonwRejectCorpus {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newBackupHandlerDB(t)
			require.NoError(t, db.SetSetting("restic_repository", plainRepoNoCredential))
			svc := buildBackupSvc(t, db, true, false)
			h := NewBackupHandler(svc, db, slog.Default())
			t.Cleanup(h.Stop)
			r := newBackupRouter(h)

			w := putRepository(t, r, tc.stored)
			t.Logf("PUT %q -> %d %s", tc.stored, w.Code, w.Body.String())

			require.Equal(t, http.StatusUnprocessableEntity, w.Code,
				"an opaque repository carrying an inline credential is not a form restic can use; it must be refused, not stored and starred")
			// The message is read DECODED: the JSON body escapes the quotes
			// around a parameter name, so a raw-body Contains on `"pass"`
			// is a false negative.
			message, _ := decodeBody(t, w)["message"].(string)
			assert.Contains(t, message, tc.mustSay, "the refusal must name the scheme")
			assert.Contains(t, message, tc.mustName, "the refusal must name the supported form")
			assert.NotContains(t, w.Body.String(), "RESTICSECRET999", "the refusal must not echo the credential")

			stored, err := db.GetSetting("restic_repository")
			require.NoError(t, err)
			assert.Equal(t, plainRepoNoCredential, stored,
				"a refused value must not be persisted — a 422 that still writes is the leak with a warning attached")
		})
	}
}

// TestUpdateSettings_AcceptsEveryDocumentedRepositoryForm is the negative arm
// on the same instrument, over the LEAVE corpus and the redacted-as-today
// corpus. Each row is saved, stored verbatim, and then read back: LEAVE rows
// byte-for-byte with the flag false, the other two starred with the flag true.
func TestUpdateSettings_AcceptsEveryDocumentedRepositoryForm(t *testing.T) {
	t.Parallel()

	type row struct {
		stored, served string
		flagged        bool
	}
	var rows []row
	for _, in := range qonwLeaveCorpus {
		rows = append(rows, row{in, in, false})
	}
	for _, tc := range qonwRedactedAsTodayCorpus {
		rows = append(rows, row{tc.stored, tc.served, true})
	}

	for _, tc := range rows {
		t.Run(tc.stored, func(t *testing.T) {
			t.Parallel()

			db := newBackupHandlerDB(t)
			require.NoError(t, db.SetSetting("restic_repository", plainRepoNoCredential))
			svc := buildBackupSvc(t, db, true, false)
			h := NewBackupHandler(svc, db, slog.Default())
			t.Cleanup(h.Stop)
			r := newBackupRouter(h)

			w := putRepository(t, r, tc.stored)
			require.True(t, w.Code < 300,
				"a supported repository form must save; refusing it is a worse outcome than the leak. got %d: %s", w.Code, w.Body.String())

			stored, err := db.GetSetting("restic_repository")
			require.NoError(t, err)
			assert.Equal(t, tc.stored, stored, "must be persisted verbatim")

			getReq := httptest.NewRequest(http.MethodGet, "/api/settings/backup", nil)
			getW := httptest.NewRecorder()
			r.ServeHTTP(getW, getReq)
			require.Equal(t, http.StatusOK, getW.Code)
			decoded := decodeBody(t, getW)
			t.Logf("stored %q -> served %q flag=%v", tc.stored, decoded["repository"], decoded["hasEmbeddedCredential"])

			assert.Equal(t, tc.served, decoded["repository"], "served form")
			flagged, ok := decoded["hasEmbeddedCredential"].(bool)
			require.True(t, ok, "hasEmbeddedCredential must be a bool in the settings response")
			assert.Equal(t, tc.flagged, flagged, "hasEmbeddedCredential")
			if !tc.flagged {
				// The PUT-back of what was served must succeed too: that is
				// the lockout arm, and "served byte-for-byte" is what makes
				// it hold.
				w2 := putRepository(t, r, decoded["repository"].(string))
				assert.True(t, w2.Code < 300, "round-tripping a served value that hides nothing must save, got %d: %s", w2.Code, w2.Body.String())
			}
		})
	}
}

// TestGetSettings_RepositoryLegacyOpaqueCredentialIsStarred is agent-os-qonw's read-path
// arm (R1): a row persisted in a REJECT shape BEFORE the validator existed is
// served starred and flagged, never in clear. Its round-trip is then refused —
// by the marker guard for the "***@" shapes, by the validator for the
// connection string — which is what a legacy rest:https://user:pw@host/ row
// gets today; the operator's way out is a form the validator accepts.
func TestGetSettings_RepositoryLegacyOpaqueCredentialIsStarred(t *testing.T) {
	t.Parallel()

	for _, tc := range qonwRejectCorpus {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body, decoded := fetchBackupSettings(t, tc.stored)
			t.Logf("legacy %q -> served %q flag=%v", tc.stored, decoded["repository"], decoded["hasEmbeddedCredential"])

			assert.NotContains(t, body, "RESTICSECRET999",
				"a legacy row in a rejected shape must not be served in clear")
			assert.Equal(t, tc.served, decoded["repository"], "the starred form keeps what the operator needs to recognise the backend")
			flagged, ok := decoded["hasEmbeddedCredential"].(bool)
			require.True(t, ok, "hasEmbeddedCredential must be a bool in the settings response")
			assert.True(t, flagged,
				"the flag is derived from the redaction, so a starred legacy row must read true; before this bead it read false while the secret sat in the response")
		})
	}
}
