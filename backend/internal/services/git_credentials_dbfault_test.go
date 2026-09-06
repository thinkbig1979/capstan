package services

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// agent-os-xzoe. httpsCredentials read git_https_user as
// `if v, err := s.db.GetSetting("git_https_user"); err == nil { user = v }`:
// a database fault and an absent row were the same event, and a fault fell
// through to s.config.GitHTTPSUser -- the "authenticate with a DIFFERENT
// credential than the one the operator believes is configured" failure the
// token read eighteen lines above already refuses.
//
// WHAT THIS FILE CAN AND CANNOT PIN, stated because the gap is the point.
//
// It CANNOT pin the new default branch by driving a fault into it, because no
// such route exists. git_credentials.go:158 is reached only when the token read
// at :127 returned nil or sql.ErrNoRows -- both other branches `return "", ""`
// -- and git_https_user is not in sensitiveSettingKeys (database/settings.go:9-12
// = {git_https_token, restic_password}), so its only failure mode is a
// database-level error, which the token read would have hit first. There is no
// seam between two consecutive statements in one function. OBSERVED, not
// assumed: a `go test -overlay` mutant that swallows the new default branch
// entirely (no log, no return) leaves ./internal/services GREEN -- 0 `--- FAIL`
// lines -- with `go build ./...` exit 0 in the same run, so it is a real green
// and not a non-compiling mutant read as a kill.
// `go run scripts/getter-errors/main.go reach` cannot answer this question
// here: its site shape is an `if err != nil` guard, so a switch-based
// conversion is not in its site set at all. (That limitation belongs to the
// analyser, not to its former shell driver check-getter-fault-reach.sh, which
// was deleted in agent-os-1hig.)
//
// So what it DOES pin is the property that actually protects the site today:
// the ORDERING. The token read runs first and refuses first. That is an
// invariant, not a value, and it is asserted through the LOG rather than the
// return value on purpose -- `return "", ""` uses explicit empty literals, so
// swapping the two reads does not change what the function returns. Exactly one
// ERROR record, naming git_https_token, discriminates both ways a future edit
// could break this: reordering the reads makes the first record name
// git_https_user, and weakening either token-branch `return "", ""` lets
// execution fall into the user read and emit a SECOND record.

const (
	// A value that must never appear in a log line. Asserted by ABSENCE only;
	// no test here prints a token.
	xzoeStoredToken = "xzoe-pat-do-not-log"
	xzoeStoredUser  = "stored-user"

	xzoeConfigToken = "config-token"
	xzoeConfigUser  = "config-user"
)

// TestHiddenSettingsDB_ReachesTheSettingsReadInHTTPSCredentials is the
// INSTRUMENT'S OWN CONTROL, and it is not a formality: the package's other two
// fault fixtures both fail to reach the code under test here, which is the same
// blind spot that left eight of ten sites unpinned in agent-os-l42o.
//
//   - A closed database faults s.db.GetDirectoryCredentials at
//     git_credentials.go:69 FIRST, so httpsCredentials returns "", "" at :93
//     and the global settings block at :117 never runs at all.
//   - gitServiceWithUndecryptableGlobalCredential (git_credentials_decrypt_test.go)
//     rotates STORAGE_KEY, which faults only the ENCRYPTED git_https_token.
//     git_https_user is not encrypted, so it would read back fine and the
//     fixture cannot discriminate ordering.
//
// hiddenSettingsDB (scanner_dbfault_test.go, agent-os-obgr) is the one that
// works: it hides only `settings`, leaving `directories` readable, so
// GetDirectoryCredentials returns sql.ErrNoRows and execution falls through to
// the settings block that both reads live in.
func TestHiddenSettingsDB_ReachesTheSettingsReadInHTTPSCredentials(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("git_https_token", xzoeStoredToken))
	require.NoError(t, db.SetSetting("git_https_user", xzoeStoredUser))

	hide()

	_, credErr := db.GetDirectoryCredentials("/stacks/never-registered")
	t.Logf("FAULTED   GetDirectoryCredentials -> err=%v", credErr)
	require.ErrorIs(t, credErr, sql.ErrNoRows,
		"the directory read must stay healthy and answer ErrNoRows, or httpsCredentials returns at :93 and never reaches either settings read")

	_, tokErr := db.GetSetting("git_https_token")
	t.Logf("FAULTED   GetSetting(git_https_token) -> err=%v", tokErr)
	require.Error(t, tokErr, "settings must be unreadable while hidden")
	require.NotErrorIs(t, tokErr, sql.ErrNoRows,
		"the fault must NOT be sql.ErrNoRows -- that is the arm the fix keeps, not the arm it refuses on")

	_, userErr := db.GetSetting("git_https_user")
	t.Logf("FAULTED   GetSetting(git_https_user)  -> err=%v", userErr)
	require.Error(t, userErr,
		"the user read must fault too, or the ordering test below could not tell a reorder from a no-op")
	require.NotErrorIs(t, userErr, sql.ErrNoRows)

	restore()
	v, err := db.GetSetting("git_https_user")
	require.NoError(t, err, "the fault must be transient, not a broken database")
	require.Equal(t, xzoeStoredUser, v)
}

// TestHTTPSCredentials_UnreadableSettings_TokenReadRefusesBeforeUserRead guards
// an ORDERING PROPERTY, not a value.
//
// git_credentials.go:158's fail-open behaviour was unreachable because the token
// read above it refuses first, and that is a property of statement order alone.
// This test fails if that order stops holding: reordering the two reads, or
// weakening either of the token branches' `return "", ""`, changes the ERROR
// records this asserts even though the returned pair stays "", "".
//
// Assertions are state-first on purpose: a leading require on the error text
// would short-circuit and the red output would never name the defect.
func TestHTTPSCredentials_UnreadableSettings_TokenReadRefusesBeforeUserRead(t *testing.T) {
	db, hide, _ := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("git_https_token", xzoeStoredToken))
	require.NoError(t, db.SetSetting("git_https_user", xzoeStoredUser))
	svc := NewGitService(&config.Config{GitHTTPSUser: xzoeConfigUser, GitHTTPSToken: xzoeConfigToken}, db)

	hide()
	buf := captureSlog(t)
	user, token := svc.httpsCredentials("/stacks/never-registered")
	out := buf.String()

	require.Equal(t, "", user,
		"an unreadable settings table must not resolve to the config username; got %q", user)
	require.Equal(t, "", token, "an unreadable settings table must not resolve to a token")

	require.Equal(t, 1, strings.Count(out, "level=ERROR"),
		"want exactly one ERROR record. More than one means a `return \"\", \"\"` in the token switch was weakened and execution fell into the user read; none means neither read refused.\nlog:\n%s", out)
	require.Contains(t, out, "setting=git_https_token",
		"the single ERROR must come from the TOKEN read, which runs first. If it names git_https_user the two reads have been reordered and git_credentials.go:158's fail-open branch is now live.\nlog:\n%s", out)
	require.NotContains(t, out, "setting=git_https_user",
		"the user read must not be reached at all under this fault")

	require.NotContains(t, out, xzoeStoredToken, "log leaked the stored token value")
}

// TestHTTPSCredentials_StoredUser_HealthyDB_Wins is the first half of the
// control pair: the same instrument, unfaulted, must still resolve the STORED
// username. Without it, "the config username was not used" is equally satisfied
// by a fix that never reads git_https_user at all.
func TestHTTPSCredentials_StoredUser_HealthyDB_Wins(t *testing.T) {
	db, _, _ := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("git_https_token", xzoeStoredToken))
	require.NoError(t, db.SetSetting("git_https_user", xzoeStoredUser))
	svc := NewGitService(&config.Config{GitHTTPSUser: xzoeConfigUser, GitHTTPSToken: xzoeConfigToken}, db)

	buf := captureSlog(t)
	user, token := svc.httpsCredentials("/stacks/never-registered")

	require.Equal(t, xzoeStoredUser, user, "a readable stored username must win over the config value")
	require.Equal(t, xzoeStoredToken, token)
	require.Equal(t, 0, strings.Count(buf.String(), "level=ERROR"),
		"a healthy read must log no ERROR:\n%s", buf.String())
}

// TestHTTPSCredentials_NoStoredUserRow_HealthyDB_FallsBackToConfig is the second
// half: an ABSENT row (sql.ErrNoRows) keeps today's config fallback
// byte-for-byte and stays silent. This is the arm the fix must NOT convert --
// treating absence as a fault would refuse every install that has never stored
// a username.
//
// The token row IS stored, so the token read takes its `err == nil` arm and the
// only ErrNoRows in play is the user read's own.
func TestHTTPSCredentials_NoStoredUserRow_HealthyDB_FallsBackToConfig(t *testing.T) {
	db, _, _ := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("git_https_token", xzoeStoredToken))
	svc := NewGitService(&config.Config{GitHTTPSUser: xzoeConfigUser, GitHTTPSToken: xzoeConfigToken}, db)

	buf := captureSlog(t)
	user, token := svc.httpsCredentials("/stacks/never-registered")

	require.Equal(t, xzoeConfigUser, user, "no stored username row must still fall back to GIT_HTTPS_USER")
	require.Equal(t, xzoeStoredToken, token)
	require.Equal(t, 0, buf.Len(),
		"an absent username row is the healthy default state and must log nothing at all:\n%s", buf.String())
}

// TestHTTPSCredentials_NoStoredUserRowAndNoConfig_UsesDefaultUser pins the last
// link of the same fallback chain, which the fix must not shorten:
// no stored row, no configured value, so defaultGitHTTPSUser applies.
func TestHTTPSCredentials_NoStoredUserRowAndNoConfig_UsesDefaultUser(t *testing.T) {
	db, _, _ := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting("git_https_token", xzoeStoredToken))
	svc := NewGitService(&config.Config{}, db)

	user, _ := svc.httpsCredentials("/stacks/never-registered")

	require.Equal(t, defaultGitHTTPSUser, user)
}
