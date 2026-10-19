package handlers

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-k81p.5: GetGitSettings reads four keys, each through settingOrFault
// and each refusing with a 500. hiddenTableDB hides the whole settings table,
// so only the FIRST read could ever be shown to fail closed; discarding the
// error of the other three left every test green.
//
// The fault here is aimed at ONE key. SQLite has no SELECT trigger, but a VIEW
// named settings can raise an error for a single row: abs(-9223372036854775808)
// is "integer overflow" at read time, and a CASE evaluates it only for the row
// whose key matches. The settings table is renamed out of the way through a
// side connection, so production code needs no seam and settingOrFault stays
// the only read path. The error is not errdefs.ErrNotFound, which is the branch
// settingOrFault must not fold into "absent".

// Distinct values so a leak of any one of them shows up in a failure message.
var gitSettingsKeyFaultSeed = map[string]string{
	"git_ssh_key":                "/stored/id_ed25519",
	"git_https_user":             "stored-user",
	models.SettingGitAuthorName:  "Stored Author",
	models.SettingGitAuthorEmail: "stored@example.com",
}

// gitSettingsUnrelatedKey is seeded in every fixture and is the key the control
// faults. It has to EXIST: a fault aimed at an absent row never evaluates, so a
// view that broke every read whenever the target row was present would still
// pass the control (agent-os-k81p.5 review, S4).
const gitSettingsUnrelatedKey = "zz_unrelated_key"

const (
	gitSettingsEnvSSHKey    = "/env/fallback_key"
	gitSettingsEnvHTTPSUser = "env-fallback-user"
)

// keyFaultDB returns a migrated on-disk DB holding gitSettingsKeyFaultSeed
// whose settings reads for faultKey, and only faultKey, fail. The dataDir is a
// real directory because in-memory SQLite is per connection and the side
// connection would be a different database.
func keyFaultDB(t *testing.T, faultKey string) *database.DB {
	t.Helper()

	dataDir := newMigratedDBDir(t)
	db, err := database.New(dataDir)
	require.NoError(t, err, "open migrated on-disk db")
	t.Cleanup(func() { _ = db.Close() })
	for k, v := range gitSettingsKeyFaultSeed {
		require.NoError(t, db.SetSetting(k, v), "seed %s", k)
	}
	require.NoError(t, db.SetSetting(gitSettingsUnrelatedKey, "unrelated-value"), "seed %s", gitSettingsUnrelatedKey)

	side, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err, "open side connection")
	t.Cleanup(func() { _ = side.Close() })
	_, err = side.Exec("ALTER TABLE settings RENAME TO settings_real")
	require.NoError(t, err, "move the settings table aside")
	// A view definition cannot bind parameters, so the key is spliced in.
	// faultKey is a test constant, never user input.
	_, err = side.Exec(`CREATE VIEW settings AS SELECT key, CASE WHEN key = '` + faultKey + `' THEN abs(-9223372036854775808) ELSE value END AS value FROM settings_real`) //nolint:gosec // test fixture: a view definition cannot bind parameters, faultKey is a test constant
	require.NoError(t, err, "create the per-key fault view")
	return db
}

func getGitSettingsWithKeyFault(t *testing.T, faultKey string) (int, string) {
	t.Helper()
	db := keyFaultDB(t, faultKey)
	h := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil,
		&config.Config{GitSSHKey: gitSettingsEnvSSHKey, GitHTTPSUser: gitSettingsEnvHTTPSUser})
	r := newDBFaultRouter(func(r *gin.Engine) { r.GET("/api/settings/git", h.GetGitSettings) })
	id, _ := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/git", "", id)
	return w.Code, w.Body.String()
}

// requireGitKeyFaultRefuses is the shared assertion: a fault on faultKey alone
// is a 500, and the body carries neither env fallback nor ANY of the four
// stored values (not just the faulted one: a body that served the other three
// would still be a half-answer built on an unreadable setting).
func requireGitKeyFaultRefuses(t *testing.T, faultKey string) {
	t.Helper()
	code, body := getGitSettingsWithKeyFault(t, faultKey)
	require.Equal(t, http.StatusInternalServerError, code,
		"an unreadable %s was answered with a 200, so GetGitSettings discarded its error. body = %s", faultKey, body)
	require.NotContains(t, body, gitSettingsEnvSSHKey, "env SSH key fallback leaked into the refusal")
	require.NotContains(t, body, gitSettingsEnvHTTPSUser, "env HTTPS user fallback leaked into the refusal")
	for k, v := range gitSettingsKeyFaultSeed {
		require.NotContains(t, body, v, "stored value of %s present in the refusal body", k)
	}
}

func TestGetGitSettings_SSHKeyReadFaultRefuses(t *testing.T) {
	requireGitKeyFaultRefuses(t, "git_ssh_key")
}

func TestGetGitSettings_HTTPSUserReadFaultRefuses(t *testing.T) {
	requireGitKeyFaultRefuses(t, "git_https_user")
}

func TestGetGitSettings_AuthorNameReadFaultRefuses(t *testing.T) {
	requireGitKeyFaultRefuses(t, models.SettingGitAuthorName)
}

func TestGetGitSettings_AuthorEmailReadFaultRefuses(t *testing.T) {
	requireGitKeyFaultRefuses(t, models.SettingGitAuthorEmail)
}

// CONTROL (two-sided half of the four above): the same view fixture faulting a
// SEEDED key GetGitSettings does not read leaves all four settings answering.
// A fixture that broke every read whenever the target row exists would turn
// the four refusal tests green for the wrong reason; this one goes red if it
// did.
func TestGetGitSettings_UnrelatedKeyFaultStillAnswersAllFour(t *testing.T) {
	code, body := getGitSettingsWithKeyFault(t, gitSettingsUnrelatedKey)
	require.Equal(t, http.StatusOK, code, "body: %s", body)
	require.Contains(t, body, `"sshKey":"`+gitSettingsKeyFaultSeed["git_ssh_key"]+`"`)
	require.Contains(t, body, `"httpsUser":"`+gitSettingsKeyFaultSeed["git_https_user"]+`"`)
	require.Contains(t, body, `"authorName":"`+gitSettingsKeyFaultSeed[models.SettingGitAuthorName]+`"`)
	require.Contains(t, body, `"authorEmail":"`+gitSettingsKeyFaultSeed[models.SettingGitAuthorEmail]+`"`)
	require.NotContains(t, body, gitSettingsEnvSSHKey)
	require.NotContains(t, body, gitSettingsEnvHTTPSUser)
}
