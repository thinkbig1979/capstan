package handlers

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-1gqn: the third sibling of agent-os-7lg1's 404 collapse and
// agent-os-8tqd's 401 collapse. Here the error is not mis-mapped, it is
// DISCARDED (`x, _ := db.Get...`) or softened (`err == nil && x != nil`), so a
// database fault has no representation at all: it reads as "no setting", "no
// policy", "no runs", "no directories".
//
// WHY THE FIXTURES ARE NOT ALL faultyDB. Two reasons, each of which produced a
// green test against a live defect in an earlier bead of this family:
//
//  1. A closed database faults EVERY read in a function, so the FIRST guarded
//     read refuses and the site under test is never reached (agent-os-obgr /
//     xzoe / r1by / a6bc all hit this). Most sites converted by this bead sit
//     BELOW a read that already refuses — that adjacency is precisely the
//     argument for converting them. So the fault has to be aimed at one table:
//     hiddenTableDB renames a single table through a side connection, which is
//     what a writer holding a lock past busy_timeout produces in production.
//
//  2. For upsertAutoUpdatePolicy the damage is a WRITE that follows the failed
//     read. A closed database fails that write too and so HIDES the damage; the
//     defect's observable form needs a read that fails while the write
//     succeeds. corruptedPolicyRowDB is that fixture, and the damage it exposes
//     — the operator's stored policy row silently replaced — is the reason this
//     bead exists.

// hiddenTableDB returns a migrated on-disk DB plus hide/restore closures that
// make ONE table transiently unreadable through a SECOND connection, leaving
// every other table answering normally. Modelled on
// services/scanner_dbfault_test.go's hiddenSettingsDB (agent-os-obgr).
//
// The dataDir must be a real directory and never ":memory:" — in-memory SQLite
// is per-connection, so the side connection would be a different database.
//
// The fault arrives as "no such table: X", which is NOT sql.ErrNoRows: exactly
// the branch every conversion in this bead has to discriminate.
func hiddenTableDB(t *testing.T, table string) (*database.DB, func(), func()) {
	t.Helper()

	dataDir := newMigratedDBDir(t)
	db, err := database.New(dataDir)
	require.NoError(t, err, "open migrated on-disk db")
	t.Cleanup(func() { _ = db.Close() })

	side, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err, "open side connection")
	t.Cleanup(func() { _ = side.Close() })

	hidden := false
	hide := func() {
		t.Helper()
		_, err := side.Exec("ALTER TABLE " + table + " RENAME TO " + table + "_hidden")
		require.NoError(t, err, "hide table %s", table)
		hidden = true
	}
	restore := func() {
		if !hidden {
			return
		}
		_, err := side.Exec("ALTER TABLE " + table + "_hidden RENAME TO " + table)
		require.NoError(t, err, "restore table %s", table)
		hidden = false
	}
	t.Cleanup(restore)
	return db, hide, restore
}

// TestHiddenTableDB_FaultsOneTableAndNotTheOthers is the two-sided control for
// every hide()-based test below. Without it a hide() that silently did nothing
// (or one that broke the whole database) would be indistinguishable from a
// correct fixture, and the tests would be measuring something else.
func TestHiddenTableDB_FaultsOneTableAndNotTheOthers(t *testing.T) {
	db, hide, restore := hiddenTableDB(t, "settings")

	// BEFORE: settings answers with the ordinary not-found predicate.
	_, err := db.GetSetting("nope")
	require.Error(t, err, "healthy settings read returned no error; the control never fired")
	require.True(t, errors.Is(err, sql.ErrNoRows),
		"healthy settings read did not fail with sql.ErrNoRows, got %v — the not-found assumption is wrong", err)

	hide()

	// DURING: settings faults, and NOT with the not-found predicate.
	_, err = db.GetSetting("nope")
	require.Error(t, err, "hide() did not make the settings table unreadable")
	require.False(t, errors.Is(err, sql.ErrNoRows),
		"hidden settings failed with the SAME predicate as ordinary not-found: %v — this fixture cannot discriminate the branch under test", err)
	require.Contains(t, err.Error(), "no such table")

	// DURING, the other half: a DIFFERENT table still answers, which is what
	// separates this fixture from faultyDB and is why the sites below their
	// handler's own refusing read are reachable at all.
	_, err = db.ListDirectories()
	require.NoError(t, err, "hide(\"settings\") also broke directories; the fault is not aimed at one table")

	restore()
	_, err = db.GetSetting("nope")
	require.True(t, errors.Is(err, sql.ErrNoRows), "restore() did not put the settings table back: %v", err)
}

// corruptedPolicyRowDB seeds one auto_update_policies row and then writes a
// non-integer into its consecutive_failures column through a side connection.
// SQLite columns are not STRICT, so the value is stored as TEXT and the row's
// Scan into an int fails — while INSERT OR REPLACE against the same table
// still succeeds.
//
// OBSERVED, verbatim, from the probe that established this fixture:
//
//	GET    -> policy=<nil> err=sql: Scan error on column index 4, name
//	          "consecutive_failures": converting driver.Value type string
//	          ("corrupt") to a int: invalid syntax   isNoRows=false
//	UPSERT -> err=<nil>
//	ROW AFTER -> id="new-id" created_at="2030-01-01T00:00:00Z"
//	          (was orig-id / 2020-01-01T00:00:00Z)
//
// It returns the handler's DB and a readback closure that opens a fresh handle
// on the same file, so the assertion about what is STORED does not go through
// the same connection the handler used.
func corruptedPolicyRowDB(t *testing.T, seed *models.AutoUpdatePolicy) (*database.DB, func() (id, createdAt string, failures int, paused bool)) {
	t.Helper()

	dataDir := newMigratedDBDir(t)
	db, err := database.New(dataDir)
	require.NoError(t, err, "open migrated on-disk db")
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.UpsertAutoUpdatePolicy(seed), "seed the existing policy row")

	side, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err, "open side connection")
	t.Cleanup(func() { _ = side.Close() })

	_, err = side.Exec(
		"UPDATE auto_update_policies SET consecutive_failures = 'corrupt' WHERE id = ?", seed.ID)
	require.NoError(t, err, "corrupt consecutive_failures")

	readback := func() (string, string, int, bool) {
		t.Helper()
		var id, createdAt, failures string
		var paused bool
		require.NoError(t, side.QueryRow(
			"SELECT id, created_at, consecutive_failures, paused FROM auto_update_policies WHERE target_type = ? AND target_id = ?",
			seed.TargetType, seed.TargetID).Scan(&id, &createdAt, &failures, &paused),
			"read the policy row back")
		// consecutive_failures is read as TEXT because the seeded value is the
		// corruption itself; -1 means "still corrupt", which is the state that
		// proves the row was NOT replaced.
		n := -1
		if failures != "corrupt" {
			n = 0
			for _, ch := range failures {
				n = n*10 + int(ch-'0')
			}
		}
		return id, createdAt, n, paused
	}
	return db, readback
}

func newDBFaultRouter(register func(r *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.RequestID())
	register(r)
	return r
}

// requireOneFaultLineWithCause is requireOneServerFaultLine's sibling for the
// faults this bead injects. It cannot reuse that helper: that one requires the
// cause to be "database is closed", and every fixture here fails with "no such
// table" or a Scan conversion error instead — the point being that a fault does
// not have to be a dead connection to be a fault.
func requireOneFaultLineWithCause(t *testing.T, captured, sentinel, cause, what string) string {
	t.Helper()
	requirePlantLanded(t, captured)
	lines := errorLinesFor(captured, sentinel)
	if len(lines) != 1 {
		t.Fatalf("%s produced %d ERROR line(s) carrying %s, want exactly 1. captured = %q", what, len(lines), sentinel, captured)
	}
	// slog's TextHandler quotes the cause attribute, so the inner quotes of a
	// wrapped key (`read setting "update_scan_interval"`) arrive as \". Unescape
	// before matching rather than writing the escapes into every call site: a
	// caller that got the escaping wrong would fail against a CORRECT log line,
	// which is a false red and hides whatever the next change breaks.
	unescaped := strings.ReplaceAll(lines[0], `\"`, `"`)
	if !strings.Contains(lines[0], "cause=") || !strings.Contains(unescaped, cause) {
		t.Fatalf("%s logged an ERROR line that does not carry the underlying cause %q, so the fault is still undiagnosable: %q", what, cause, lines[0])
	}
	return lines[0]
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. updates.go upsertAutoUpdatePolicy — the site that MUTATES persisted state.
//    Done first, and this is the arm the bead's acceptance is about.
// ─────────────────────────────────────────────────────────────────────────────

func seedPolicy() *models.AutoUpdatePolicy {
	return &models.AutoUpdatePolicy{
		ID:                  "policy-kept-1gqn",
		TargetType:          "container",
		TargetID:            "c1",
		Enabled:             true,
		ConsecutiveFailures: 3,
		Paused:              true,
		CreatedAt:           "2020-01-01T00:00:00Z",
		UpdatedAt:           "2020-01-01T00:00:00Z",
	}
}

func newUpdatesRouter(t *testing.T, db *database.DB) *gin.Engine {
	t.Helper()
	h := NewResourcesHandler(nil, db, nil)
	return newDBFaultRouter(func(r *gin.Engine) {
		r.PUT("/api/resources/auto-update-policies/:targetType/:targetId", h.upsertAutoUpdatePolicy)
		r.GET("/api/resources/auto-update-policies", h.listAutoUpdatePolicies)
		r.GET("/api/resources/updates", h.checkUpdates)
	})
}

// TestUpsertAutoUpdatePolicy_ReadFaultDoesNotReplaceTheStoredPolicy is the
// seen-failing-first regression for updates.go's SOFT site.
//
// The assertions are ordered state-first, status-last on purpose: a
// require.Error-style status check short-circuits and the red output then says
// only "expected 500, got 200", never naming the defect (agent-os-1gqn brief).
// Ordered this way the RED message is "the stored policy was replaced", which
// is the actual bug.
func TestUpsertAutoUpdatePolicy_ReadFaultDoesNotReplaceTheStoredPolicy(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	seed := seedPolicy()
	db, readback := corruptedPolicyRowDB(t, seed)
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut,
		"/api/resources/auto-update-policies/container/c1", `{"enabled":true}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	gotID, gotCreated, gotFailures, gotPaused := readback()
	if gotID != seed.ID || gotCreated != seed.CreatedAt {
		t.Fatalf("the stored auto-update policy was REPLACED by a request whose read of it failed: "+
			"id = %q (want %q), created_at = %q (want %q), consecutive_failures = %d (want -1, i.e. untouched), paused = %v (want true). "+
			"UpsertAutoUpdatePolicy is INSERT OR REPLACE against UNIQUE(target_type,target_id), so the operator's row was deleted and a fresh one written in its place.",
			gotID, seed.ID, gotCreated, seed.CreatedAt, gotFailures, gotPaused)
	}
	if gotFailures != -1 || !gotPaused {
		t.Fatalf("the stored policy's failure state was overwritten: consecutive_failures = %d (want -1, untouched), paused = %v (want true)", gotFailures, gotPaused)
	}

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: a database fault on the existing-policy read is being reported to the operator as success. body = %s", w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, "consecutive_failures",
		"PUT auto-update-policies against a policy row that cannot be scanned")
}

// CONTROL A: healthy DB, absent policy -> a fresh policy is created exactly as
// before this change, and no ERROR line is produced for this request.
func TestUpsertAutoUpdatePolicy_AbsentPolicyStillCreatesAFreshOne(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut,
		"/api/resources/auto-update-policies/container/brand-new", `{"enabled":true}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	created, err := db.GetAutoUpdatePolicy("container", "brand-new")
	require.NoError(t, err, "a fresh policy should have been created")
	require.True(t, created.Enabled)
	require.NotEmpty(t, created.ID)
	require.Equal(t, 0, created.ConsecutiveFailures)
	require.False(t, created.Paused)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "PUT auto-update-policies with no existing policy")
}

// CONTROL B: healthy DB, existing policy -> the pre-existing update path,
// field for field. This is the arm that would go red if the conversion had
// turned "row found" into "refuse".
func TestUpsertAutoUpdatePolicy_ExistingPolicyKeepsItsIdentity(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	seed := seedPolicy()
	require.NoError(t, db.UpsertAutoUpdatePolicy(seed))
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut,
		"/api/resources/auto-update-policies/container/c1", `{"enabled":true}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	got, err := db.GetAutoUpdatePolicy("container", "c1")
	require.NoError(t, err)
	require.Equal(t, seed.ID, got.ID, "the existing policy's ID must be kept")
	require.Equal(t, seed.CreatedAt, got.CreatedAt, "the existing policy's CreatedAt must be kept")
	// enabled=true against a paused policy clears the pause and the counter,
	// which is the pre-existing behaviour at updates.go's `req.Enabled &&
	// existing.Paused` branch.
	require.False(t, got.Paused)
	require.Equal(t, 0, got.ConsecutiveFailures)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "PUT auto-update-policies over an existing policy")
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. readSettings / settingOrFault — the collapse that makes the grouped
//    handler tests below mean something.
//
//    settings.go:518-524 was seven fault-capable reads and backup.go:240-250
//    was eleven. A fixture that faults all of them satisfies a bare "it
//    refused" assertion from ANY one of them, so no site is independently
//    pinned (agent-os-a6bc). The conversion collapses each group to ONE
//    fault-capable read, so there is one site left to pin — and the wrapped key
//    in the error names WHICH read failed, which is what these two tests
//    assert. Per-key coverage after that is the loop's uniformity, not seven
//    separate assertions, and the report says so.
// ─────────────────────────────────────────────────────────────────────────────

func TestSettingOrFault_AbsentIsNotAFault(t *testing.T) {
	db, hide, _ := hiddenTableDB(t, "settings")

	// Absent row -> the pre-existing default, and NO error. This is the arm the
	// discarded-error form got right and the conversion must not break.
	v, err := settingOrFault(db, "never_set")
	require.NoError(t, err, "an absent settings row must stay a default, not become a fault")
	require.Equal(t, "", v)

	// Present row -> its value.
	require.NoError(t, db.SetSetting("present_key", "present-value"))
	v, err = settingOrFault(db, "present_key")
	require.NoError(t, err)
	require.Equal(t, "present-value", v)

	// Fault -> an error naming the key. Same instrument, opposite result.
	hide()
	_, err = settingOrFault(db, "present_key")
	require.Error(t, err, "a database that cannot answer must not read as an absent row")
	require.False(t, errors.Is(err, sql.ErrNoRows))
	require.Contains(t, err.Error(), `read setting "present_key"`,
		"the error must name WHICH read failed; without that a grouped refusal cannot be attributed to a site")
	require.Contains(t, err.Error(), "no such table")
}

func TestReadSettings_ReportsTheFaultingKey(t *testing.T) {
	db, hide, restore := hiddenTableDB(t, "settings")
	require.NoError(t, db.SetSetting("a", "1"))

	got, err := readSettings(db, "a", "b")
	require.NoError(t, err)
	require.Equal(t, map[string]string{"a": "1", "b": ""}, got,
		"absent keys must come back as the empty default, exactly as the discarded-error form produced")

	hide()
	defer restore()
	_, err = readSettings(db, "a", "b")
	require.Error(t, err)
	require.Contains(t, err.Error(), `read setting "a"`, "readSettings must report the first key that faulted")
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. settings.go — GetUpdateSettings (7 keys), UpdateUpdateSettings (the
//    scheduler decision) and GetGitSettings (the credential selection).
// ─────────────────────────────────────────────────────────────────────────────

func newSettingsRouter(t *testing.T, db *database.DB) *gin.Engine {
	t.Helper()
	h := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil, &config.Config{})
	return newDBFaultRouter(func(r *gin.Engine) {
		r.GET("/api/settings/updates", h.GetUpdateSettings)
		r.PUT("/api/settings/updates", h.UpdateUpdateSettings)
		r.GET("/api/settings/git", h.GetGitSettings)
	})
}

func TestGetUpdateSettings_FaultRefusesInsteadOfFabricatingAForm(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	hide()
	r := newSettingsRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/updates", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: an unreadable database is being rendered as a complete update-settings form "+
			"(auto-update off, immediate mode, 03:00, all days) that the operator's next Save writes back over the real values. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read setting "update_scan_interval"`,
		"GET /api/settings/updates with an unreadable settings table")
}

// CONTROL: healthy DB with NO settings rows at all — the fresh-install case —
// still renders the documented defaults, with no ERROR line of its own.
func TestGetUpdateSettings_AbsentRowsStillRenderTheDefaults(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := newSettingsRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/updates", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	body := w.Body.String()
	require.Contains(t, body, `"applyMode":"immediate"`)
	require.Contains(t, body, `"applyTime":"03:00"`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/settings/updates on a fresh database")
}

func TestUpdateUpdateSettings_FaultRefusesBeforeTheSchedulerDecision(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	hide()
	r := newSettingsRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut, "/api/settings/updates", `{"scanIntervalMinutes":30}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: the old scan interval could not be read, so the restart-or-stop decision below it is being made on invented state. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read setting "update_scan_interval"`,
		"PUT /api/settings/updates with an unreadable settings table")
}

func TestGetGitSettings_FaultRefusesInsteadOfSelectingTheEnvFallback(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	hide()
	r := newSettingsRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/git", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: an unreadable git_ssh_key reads as \"not configured\" and hands the next git operation the env-supplied fallback identity. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read setting "git_ssh_key"`,
		"GET /api/settings/git with an unreadable settings table")
}

// CONTROL: healthy DB, no git rows -> the pre-existing cfg fallback, 200, and
// no ERROR line. This is the arm that goes red if the conversion turned
// "absent" into "refuse".
func TestGetGitSettings_AbsentRowsStillFallBackToConfig(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	h := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil,
		&config.Config{GitSSHKey: "/etc/keys/id_ed25519", GitHTTPSUser: "cfg-user"})
	r := newDBFaultRouter(func(r *gin.Engine) { r.GET("/api/settings/git", h.GetGitSettings) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/git", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"sshKey":"/etc/keys/id_ed25519"`)
	require.Contains(t, w.Body.String(), `"httpsUser":"cfg-user"`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/settings/git on a fresh database")
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. updates.go — checkUpdates' two refusing reads and listAutoUpdatePolicies.
//    All three sit BELOW a read that already refuses, which is why the fault
//    has to be aimed at the settings table alone: with faultyDB the earlier
//    guard fires and none of these sites is ever reached.
// ─────────────────────────────────────────────────────────────────────────────

func TestCheckUpdates_EmptyCacheFaultOnLastScanTimeRefuses(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	hide()
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/resources/updates", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: cached_updates answered and update_scan_last_run did not, and the response reports scannedAt:\"\" as if the scan simply never ran. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read setting "update_scan_last_run"`,
		"GET /api/resources/updates with an empty cache and an unreadable settings table")
}

func TestCheckUpdates_PopulatedCacheFaultOnLastScanTimeRefuses(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	require.NoError(t, db.SetCachedUpdates([]models.CachedUpdate{{
		ContainerID: "c1", ContainerName: "one", Image: "img", ImageRef: "img:1", State: "running",
	}}), "seed the cache so the populated branch is the one under test")
	hide()
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/resources/updates", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 on the POPULATED-cache branch. body = %s", w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read setting "update_scan_last_run"`,
		"GET /api/resources/updates with a populated cache and an unreadable settings table")
}

// CONTROL for both branches: healthy DB, no update_scan_last_run row ->
// 200 with scannedAt:"" exactly as before, and no ERROR line.
func TestCheckUpdates_AbsentLastScanTimeStillReturns200(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/resources/updates", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"scannedAt":""`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/resources/updates on a fresh database")
}

func TestListAutoUpdatePolicies_FaultOnGlobalFlagRefuses(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "settings")
	hide()
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/resources/auto-update-policies", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: auto_update_policies answered and auto_update_enabled did not, and globalEnabled:false renders every container's auto-update as locked off. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, `read setting "auto_update_enabled"`,
		"GET /api/resources/auto-update-policies with an unreadable settings table")
}

// CONTROL: healthy DB, no auto_update_enabled row -> 200 with
// globalEnabled:false, which is what an absent row has always meant.
func TestListAutoUpdatePolicies_AbsentGlobalFlagStillReturns200(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := newUpdatesRouter(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/resources/auto-update-policies", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"globalEnabled":false`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/resources/auto-update-policies on a fresh database")
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. auth.go — UserCount at the public /status probe and the setup fast path.
//    No sql.ErrNoRows arm exists for either: UserCount is a COUNT(*), which
//    always yields a row, so faultyDB is the right fixture here.
// ─────────────────────────────────────────────────────────────────────────────

func TestStatus_DBFaultRefusesInsteadOfClaimingSetupIsNeeded(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	h := NewAuthHandler(faultyDB(t), dbFaultTestSecret, false)
	r := newDBFaultRouter(func(r *gin.Engine) { r.GET("/status", h.Status) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/status", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: an unauthenticated caller is being told needsSetup:true on a database that could not be counted, "+
			"which on a provisioned instance routes the operator to the setup form. body = %s", w.Code, w.Body.String())
	}
	requireOneServerFaultLine(t, buf.String(), sentinel, "GET /status against a faulty DB")
}

// CONTROL: healthy DB with no users -> the pre-existing needsSetup:true, 200,
// and no ERROR line. This is the arm that distinguishes "no users" from "could
// not count users", which is the whole point of the conversion.
func TestStatus_HealthyEmptyDBStillReportsNeedsSetup(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	h := NewAuthHandler(db, dbFaultTestSecret, false)
	r := newDBFaultRouter(func(r *gin.Engine) { r.GET("/status", h.Status) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/status", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"needsSetup":true`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /status on a healthy empty database")
}

func TestSetup_DBFaultRefusesBeforeHashing(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	h := NewAuthHandler(faultyDB(t), dbFaultTestSecret, false)
	r := newDBFaultRouter(func(r *gin.Engine) { r.POST("/setup", h.Setup) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPost, "/setup", `{"username":"admin","password":"correct-horse-battery"}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: the fast-path guard read count==0 from a database that failed, so the request spends a bcrypt and then fails for a reason the operator is never told. body = %s",
			w.Code, w.Body.String())
	}
	requireOneServerFaultLine(t, buf.String(), sentinel, "POST /setup against a faulty DB")
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. directories.go — the per-directory stack count, which sits below a read
//    that already refuses.
// ─────────────────────────────────────────────────────────────────────────────

func TestDirectoriesList_StackCountFaultRefuses(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, hide, _ := hiddenTableDB(t, "stacks")
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: "/opt/stacks/one", Name: "one", ScannedAt: testTime,
	}))
	hide()
	h := NewDirectoriesHandler(nil, db)
	r := newDBFaultRouter(func(r *gin.Engine) { r.GET("/api/directories", h.List) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/directories", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: directories answered and stacks did not, so every directory is reported with stackCount 0. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, "no such table",
		"GET /api/directories with an unreadable stacks table")
}

// CONTROL: healthy DB, a directory with no stacks -> the pre-existing
// stackCount 0, 200, and no ERROR line. Without this arm the fault test above
// could not tell "the count is genuinely 0" from "the count could not be read",
// which is exactly the collapse being fixed.
func TestDirectoriesList_GenuinelyEmptyDirectoryStillReportsZero(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.UpsertDirectory(models.Directory{
		Path: "/opt/stacks/one", Name: "one", ScannedAt: testTime,
	}))
	h := NewDirectoriesHandler(nil, db)
	r := newDBFaultRouter(func(r *gin.Engine) { r.GET("/api/directories", h.List) })

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/directories", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"stackCount":0`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/directories with a genuinely empty directory")
}

// ─────────────────────────────────────────────────────────────────────────────
// 7. backup.go — the settings-form helper, the policy upsert (the second site
//    that loses persisted state) and the status page's last run.
// ─────────────────────────────────────────────────────────────────────────────

func newBackupFaultHandler(t *testing.T, db *database.DB) *gin.Engine {
	t.Helper()
	svc := buildBackupSvc(t, db, true, false)
	h := NewBackupHandler(svc, db, slog.Default())
	// h.Stop() blocks until every durable-run goroutine has finished its DB
	// write, so it must run before the DB is closed (agent-os-80n).
	t.Cleanup(h.Stop)
	return newDBFaultRouter(func(r *gin.Engine) {
		r.GET("/api/settings/backup", h.getSettings)
		r.PUT("/api/backup/policies/:stackId", h.upsertPolicy)
		r.GET("/api/backup/status", h.getStatus)
	})
}

// corruptedBackupPolicyRowDB is corruptedPolicyRowDB's twin for
// backup_policies: a non-boolean in `enabled` fails the row's Scan while
// UpsertBackupPolicy against the same table still succeeds.
//
// A whole-table fault is NOT enough here and the mutation control proved it:
// with backup_policies hidden, the OLD code's write fails too, so it answers
// 500 anyway and a status assertion passes against the live defect. OBSERVED —
// this test passed under a backup.go-reverted mutant until the fixture changed.
//
// OBSERVED from the probe that established it:
//
//	GET    -> <nil> err=sql: Scan error on column index 3, name "enabled":
//	          sql/driver: couldn't convert "corrupt" into type bool
//	UPSERT -> err=<nil>
//	ROW AFTER -> id="new" created="2020-01-01T00:00:00Z"
//
// created_at survives (it is not in the upsert's SET list) but the ID does
// not: the policy the operator has is silently given a different identity.
func corruptedBackupPolicyRowDB(t *testing.T, stackID, policyID string) (*database.DB, func() string) {
	t.Helper()

	dataDir := newMigratedDBDir(t)
	db, err := database.New(dataDir)
	require.NoError(t, err, "open migrated on-disk db")
	t.Cleanup(func() { _ = db.Close() })

	seedHandlerStack(t, db, stackID)
	require.NoError(t, db.UpsertBackupPolicy(&models.BackupPolicy{
		ID: policyID, TargetType: "stack", TargetID: stackID, Enabled: true,
		StopPolicy: "stop", CreatedAt: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z",
	}), "seed the existing backup policy")

	side, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err, "open side connection")
	t.Cleanup(func() { _ = side.Close() })
	_, err = side.Exec("UPDATE backup_policies SET enabled = 'corrupt' WHERE id = ?", policyID)
	require.NoError(t, err, "corrupt enabled")

	readbackID := func() string {
		t.Helper()
		var id string
		require.NoError(t, side.QueryRow(
			"SELECT id FROM backup_policies WHERE target_type = 'stack' AND target_id = ?", stackID).Scan(&id))
		return id
	}
	return db, readbackID
}

func TestBackupUpsertPolicy_ReadFaultDoesNotReplaceTheStoredPolicysIdentity(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db, readbackID := corruptedBackupPolicyRowDB(t, "stack-1gqn", "policy-kept-1gqn")
	r := newBackupFaultHandler(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut, "/api/backup/policies/stack-1gqn", `{"enabled":true}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	// State first, status last: a status assertion that short-circuits reports
	// only "expected 500, got 200" and never names the defect.
	if got := readbackID(); got != "policy-kept-1gqn" {
		t.Fatalf("the stored backup policy's ID was REPLACED by a request whose read of it failed: id = %q, want %q. "+
			"The handler read the failure as \"no policy yet\", minted a fresh generateID() and wrote it over the operator's row.",
			got, "policy-kept-1gqn")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: the existing backup policy could not be read and the request reports success. body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, "couldn't convert",
		"PUT /api/backup/policies over a policy row that cannot be scanned")
}

// CONTROL: healthy DB, no policy yet -> the pre-existing create path, 200.
func TestBackupUpsertPolicy_AbsentPolicyStillCreatesOne(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db := newBackupHandlerDB(t)
	seedHandlerStack(t, db, "stack-1gqn")
	r := newBackupFaultHandler(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodPut, "/api/backup/policies/stack-1gqn", `{"enabled":true}`, id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	created, err := db.GetBackupPolicy("stack-1gqn")
	require.NoError(t, err, "a fresh policy should have been created")
	require.True(t, created.Enabled)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "PUT /api/backup/policies with no existing policy")
}

func TestBackupGetStatus_LastRunFaultRefuses(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	// backup_runs alone: GetEnabledBackupPolicies above already refuses on a
	// backup_policies fault, so aiming at that table would test the wrong guard.
	db, hide, _ := hiddenTableDB(t, "backup_runs")
	hide()
	r := newBackupFaultHandler(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/backup/status", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: backup_policies answered and backup_runs did not, so lastRun is reported as null — indistinguishable from \"no backup has ever run\". body = %s",
			w.Code, w.Body.String())
	}
	requireOneFaultLineWithCause(t, buf.String(), sentinel, "no such table: backup_runs",
		"GET /api/backup/status with an unreadable backup_runs table")
}

// CONTROL: healthy DB with genuinely no runs -> lastRun:null and 200, which is
// the state the fault arm above must be distinguishable from.
func TestBackupGetStatus_GenuinelyNoRunsStillReturns200(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db := newBackupHandlerDB(t)
	r := newBackupFaultHandler(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/backup/status", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"lastRun":null`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/backup/status with no runs recorded")
}

// CONTROL for the eleven-key settings read: a healthy database with none of
// the eleven rows set still renders the documented defaults, so the collapse
// into readSettings did not turn "absent" into "refuse".
//
// There is deliberately NO whole-table fault arm for this handler here, and the
// report says why: ResolveBackupConfigWithCfg and RepoSettingSources run first
// (backup.go:154-163, agent-os-l42o) and already refuse on a settings fault, so
// a hidden settings table never reaches the eleven reads. The fault arm for the
// converted expression is TestReadSettings_ReportsTheFaultingKey, at the helper
// where the single remaining fault-capable read now lives.
func TestBackupGetSettings_AbsentRowsStillRenderTheDefaults(t *testing.T) {
	buf := captureHandlerLogs(t)
	plant := plantStrayServerFaultLine(t)

	db := newBackupHandlerDB(t)
	r := newBackupFaultHandler(t, db)

	id, sentinel := requestIDSentinel()
	w := doDBFaultRequest(t, r, http.MethodGet, "/api/settings/backup", "", id)
	<-plant
	requireSentinelHonoured(t, w, id)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	body := w.Body.String()
	require.Contains(t, body, `"keepDaily":7`)
	require.Contains(t, body, `"keepWeekly":4`)
	require.Contains(t, body, `"autoPrune":true`)
	require.Contains(t, body, `"rcloneTransfers":4`)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "GET /api/settings/backup on a fresh database")
}
