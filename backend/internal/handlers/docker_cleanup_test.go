package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// agent-os-fn7x.3 — the cleanup HTTP surface.
//
// The two seams (cleanup service, cleanup scheduler) are injected by setter, so
// these tests drive the real routes with fakes that RECORD what the handlers
// asked for. That is the only way to assert "which floor was the prune run
// under" and "was the tick re-armed", both of which are decisions the handler
// makes and neither of which is visible in the response body.

type fn7x3HandlerExecuteCall struct {
	trigger     string
	minAgeHours int
}

type fn7x3FakeCleanup struct {
	mu           sync.Mutex
	previewCalls []int
	executeCalls []fn7x3HandlerExecuteCall

	preview    *services.DockerCleanupPreview
	previewErr error
	executeErr error
}

func (f *fn7x3FakeCleanup) Preview(_ context.Context, minAgeHours int) (*services.DockerCleanupPreview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewCalls = append(f.previewCalls, minAgeHours)
	if f.previewErr != nil {
		return nil, f.previewErr
	}
	if f.preview != nil {
		return f.preview, nil
	}
	return &services.DockerCleanupPreview{
		Candidates:  []services.DockerCleanupCandidate{},
		MinAgeHours: minAgeHours,
	}, nil
}

func (f *fn7x3FakeCleanup) Execute(_ context.Context, trigger string, minAgeHours int) (*models.DockerCleanupRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executeCalls = append(f.executeCalls, fn7x3HandlerExecuteCall{trigger: trigger, minAgeHours: minAgeHours})
	if f.executeErr != nil {
		return nil, f.executeErr
	}
	return &models.DockerCleanupRun{
		ID:             "fn7x3-handler-run",
		Trigger:        trigger,
		Status:         "success",
		MinAgeHours:    minAgeHours,
		ImagesDeleted:  2,
		BytesReclaimed: 4096,
	}, nil
}

func (f *fn7x3FakeCleanup) previewed() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.previewCalls...)
}

func (f *fn7x3FakeCleanup) executed() []fn7x3HandlerExecuteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fn7x3HandlerExecuteCall(nil), f.executeCalls...)
}

// fn7x3FakeArmer records re-arm calls. Counting them is the only way to see that
// a policy PUT took effect without a process restart.
type fn7x3FakeArmer struct {
	mu    sync.Mutex
	calls int
}

func (a *fn7x3FakeArmer) StartFromPolicy() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
}

func (a *fn7x3FakeArmer) armCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// fn7x3Router builds the real ResourcesHandler routes over the given db with the
// cleanup seams installed, plus an auth context so audit rows carry a real user.
func fn7x3Router(t *testing.T, db *database.DB) (*gin.Engine, *fn7x3FakeCleanup, *fn7x3FakeArmer) {
	t.Helper()
	createTestUser(t, db, "fn7x3-admin", "correct-horse-battery")

	h := NewResourcesHandler(nil, db, nil)
	cleanup := &fn7x3FakeCleanup{}
	armer := &fn7x3FakeArmer{}
	h.SetCleanupService(cleanup)
	h.SetCleanupScheduler(armer)

	r := gin.New()
	r.Use(authContextMiddleware("test-user-id"))
	h.RegisterRoutes(r.Group("/api"))
	return r, cleanup, armer
}

func fn7x3MemoryDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func fn7x3Do(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestDockerCleanupPolicyRejectsLowFloor is the bead's floor arm, TWO-SIDED IN ONE
// TEST.
//
// The rejection alone proves nothing: a route that 400s on every body, or one
// that is not registered at all, passes it. The accepting half — the same route,
// the same shape of request, a value AT the floor — is what makes the 400 mean
// "the floor was enforced". And the stored-value assertion is what separates
// "rejected" from "clamped silently", which is the outcome that would leave an
// operator believing 0 was saved.
//
// WHICH ARM DIES IF THE CHECK IS DELETED: the rejecting one. Removing the
// `*req.MinAgeHours < services.MinCleanupAgeHours` guard leaves the accepting arm
// green and turns the rejecting arm red.
func TestDockerCleanupPolicyRejectsLowFloor(t *testing.T) {
	t.Run("below the floor is rejected and stores nothing", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		r, _, armer := fn7x3Router(t, db)

		w := fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy",
			`{"enabled":true,"minAgeHours":0}`)

		require.Equal(t, http.StatusBadRequest, w.Code,
			"minAgeHours=0 was accepted; the age floor is this feature's ONLY retention mechanism and a prune is irreversible. body = %s", w.Body.String())
		// State first, then status: the harm is a stored sub-floor policy, and
		// asserting it separately means the red message names that rather than
		// only "expected 400".
		_, err := db.GetSetting(services.SettingDockerCleanupMinAgeHours)
		require.Error(t, err, "a rejected request still wrote the age floor")
		_, err = db.GetSetting(services.SettingDockerCleanupEnabled)
		require.Error(t, err,
			"a request rejected for its age floor still stored its OTHER field, leaving the policy half applied")
		require.Zero(t, armer.armCalls(), "a rejected policy PUT re-armed the scheduler")
	})

	t.Run("at the floor is accepted and stored", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		r, _, armer := fn7x3Router(t, db)

		w := fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy",
			fmt.Sprintf(`{"enabled":true,"minAgeHours":%d}`, services.MinCleanupAgeHours))

		require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
		var got cleanupPolicyResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		require.True(t, got.Enabled)
		require.Equal(t, services.MinCleanupAgeHours, got.MinAgeHours)

		stored, err := db.GetSetting(services.SettingDockerCleanupMinAgeHours)
		require.NoError(t, err, "an accepted policy was not persisted")
		require.Equal(t, fmt.Sprint(services.MinCleanupAgeHours), stored)
		require.Equal(t, 1, armer.armCalls(),
			"an accepted policy PUT did not re-arm the scheduler, so opting in would need a process restart")
	})

	t.Run("an interval below its floor is rejected too", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		r, _, _ := fn7x3Router(t, db)

		w := fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy", `{"intervalHours":0}`)
		require.Equal(t, http.StatusBadRequest, w.Code,
			"intervalHours=0 was accepted; time.NewTicker panics on a non-positive duration. body = %s", w.Body.String())

		w = fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy", `{"intervalHours":6}`)
		require.Equal(t, http.StatusOK, w.Code, "a legal interval was rejected: %s", w.Body.String())
	})

	t.Run("an empty policy body is rejected", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		r, _, _ := fn7x3Router(t, db)

		w := fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy", `{}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "body = %s", w.Body.String())
	})
}

// fn7x3SeedRuns writes n cleanup runs with strictly decreasing started_at, so the
// newest-first ordering is deterministic and a truncated page is identifiable.
func fn7x3SeedRuns(t *testing.T, db *database.DB, n int) {
	t.Helper()
	base := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		require.NoError(t, db.CreateDockerCleanupRun(&models.DockerCleanupRun{
			ID:          fmt.Sprintf("fn7x3-run-%03d", i),
			Trigger:     services.TriggerScheduled,
			Status:      "success",
			StartedAt:   base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			MinAgeHours: services.DefaultCleanupMinAgeHours,
		}))
	}
}

// TestDockerCleanupHistoryClampsLimit proves the limit is bounded ON ARRIVAL
// rather than handed to the database.
//
// THREE ARMS, and the third is the one that makes the other two mean anything. A
// handler that hardcoded 50 and ignored the query entirely would pass arms A and
// B; only the pass-through arm shows the value is read at all, which is what turns
// "100 rows" into evidence of a CAP rather than of a coincidence.
//
// Arm B is not a hypothetical. MEASURED against modernc.org/sqlite with
// `LIMIT 2 -> 2 rows` as the firing positive control: `LIMIT -1` and `LIMIT -100`
// each returned 3 of 3 rows. An unclamped negative returns the WHOLE TABLE, which
// is agent-os-s21h's open defect on /updates/history.
func TestDockerCleanupHistoryClampsLimit(t *testing.T) {
	db := fn7x3MemoryDB(t)
	// One more than the cap, so a capped page is strictly shorter than the table
	// and the difference cannot be an artefact of an under-seeded fixture.
	fn7x3SeedRuns(t, db, maxCleanupHistoryLimit+1)
	r, _, _ := fn7x3Router(t, db)

	readRuns := func(t *testing.T, query string) ([]models.DockerCleanupRun, int) {
		t.Helper()
		w := fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/history"+query, "")
		require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
		var body struct {
			Runs  []models.DockerCleanupRun `json:"runs"`
			Limit int                       `json:"limit"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body.Runs, body.Limit
	}

	// Control first: the fixture really does hold more rows than the cap, or arm
	// A below would pass against a short table.
	all, err := db.GetDockerCleanupRuns(maxCleanupHistoryLimit + 1)
	require.NoError(t, err)
	require.Len(t, all, maxCleanupHistoryLimit+1, "the fixture is under-seeded; arm A could not discriminate")

	// ARM A — an absurd limit is CAPPED at the maximum, not substituted with the
	// default. GetAuditLog's rule (settings.go:1108) would yield 50 here.
	runs, limit := readRuns(t, "?limit=1000")
	require.Len(t, runs, maxCleanupHistoryLimit,
		"limit=1000 returned %d rows; the maximum is not enforced", len(runs))
	require.Equal(t, maxCleanupHistoryLimit, limit)

	// ARM B — a negative limit does NOT reach SQLite, where it would mean "no
	// limit" and return the whole table.
	runs, limit = readRuns(t, "?limit=-1")
	require.Len(t, runs, defaultCleanupHistoryLimit,
		"limit=-1 returned %d rows; a negative limit means UNLIMITED in SQLite", len(runs))
	require.Equal(t, defaultCleanupHistoryLimit, limit)

	// ARM B2 — zero is a different lie: an empty page reported as the history.
	runs, _ = readRuns(t, "?limit=0")
	require.Len(t, runs, defaultCleanupHistoryLimit,
		"limit=0 returned %d rows; LIMIT 0 in SQLite returns an EMPTY page", len(runs))

	// ARM B3 — unparseable falls back to the documented default, not to the
	// minimum. parseQueryParamInt(l, 1, 100) would have returned a one-row page.
	runs, _ = readRuns(t, "?limit=nonsense")
	require.Len(t, runs, defaultCleanupHistoryLimit,
		"limit=nonsense returned %d rows instead of the documented default", len(runs))

	// ARM C — THE DISCRIMINATOR. A legal in-range limit passes through
	// unchanged. Without this arm a handler that ignored the query entirely, or
	// one that always answered 50, would satisfy every arm above.
	runs, limit = readRuns(t, "?limit=2")
	require.Len(t, runs, 2, "a legal limit=2 was not honoured; the query parameter is not read at all")
	require.Equal(t, 2, limit)

	// No limit at all: the documented default.
	runs, limit = readRuns(t, "")
	require.Len(t, runs, defaultCleanupHistoryLimit)
	require.Equal(t, defaultCleanupHistoryLimit, limit)
}

// TestDockerCleanupHistoryEmptyIsAnArrayNotNull: `runs: null` breaks a client that
// iterates, and the DB layer returns a nil slice for an empty table.
func TestDockerCleanupHistoryEmptyIsAnArrayNotNull(t *testing.T) {
	db := fn7x3MemoryDB(t)
	r, _, _ := fn7x3Router(t, db)

	w := fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/history", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"runs":[]`, "body = %s", w.Body.String())
}

// TestDockerCleanupPolicyGetReturnsDefaultsAndTheFloor: the absent case returns
// the defaults AND the server floors, so the UI can show what will be rejected
// instead of discovering it on submit. Precedent: minRetentionDays at
// settings.go:442.
func TestDockerCleanupPolicyGetReturnsDefaultsAndTheFloor(t *testing.T) {
	db := fn7x3MemoryDB(t)
	r, _, _ := fn7x3Router(t, db)

	w := fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/policy", "")
	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())

	var got cleanupPolicyResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.False(t, got.Enabled, "cleanup must be DISABLED by default (FR7)")
	require.Equal(t, services.DefaultCleanupMinAgeHours, got.MinAgeHours)
	require.Equal(t, services.DefaultCleanupIntervalHours, got.IntervalHours)
	require.Equal(t, services.MinCleanupAgeHours, got.MinAllowedAgeHours,
		"the server floor is not on the wire, so the UI cannot show what it will reject")
	require.Equal(t, services.MinCleanupIntervalHours, got.MinAllowedIntervalHours)
}

// TestDockerCleanupPolicyGetRefusesOnAReadFault: absent returns defaults, a FAULT
// returns 500. A policy page rendering "disabled" during a database fault is
// indistinguishable from an operator having turned it off, and the only other
// symptom is the disk filling (spec.md:10-13). Same rule as
// upsertAutoUpdatePolicy (updates.go:890-895) and GetLogRetention.
//
// Two-sided on the same fixture: hiddenTableDB's own control
// (TestHiddenTableDB_FaultsOneTableAndNotTheOthers) establishes that the fault
// arrives as "no such table", NOT sql.ErrNoRows — which is precisely the branch
// this handler has to discriminate.
func TestDockerCleanupPolicyGetRefusesOnAReadFault(t *testing.T) {
	db, hide, restore := hiddenTableDB(t, "settings")
	require.NoError(t, db.SetSetting(services.SettingDockerCleanupEnabled, "true"),
		"seed the opt-in this test is about")

	h := NewResourcesHandler(nil, db, nil)
	r := gin.New()
	h.RegisterRoutes(r.Group("/api"))

	hide()
	w := fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/policy", "")
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"the policy page served an invented policy while the stored one could not be read. body = %s", w.Body.String())
	require.NotContains(t, w.Body.String(), `"enabled":false`,
		"the response reports cleanup as disabled, which is the exact lie this guard exists to prevent")

	restore()
	w = fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/policy", "")
	require.Equal(t, http.StatusOK, w.Code,
		"the same route stopped working on a HEALTHY database, so the 500 above was not about the fault. body = %s", w.Body.String())
	require.Contains(t, w.Body.String(), `"enabled":true`)
}

// TestDockerCleanupRunUsesThePolicyFloorAndAudits covers C6: a manual run is
// destructive and must leave the same trail POST /resources/images/prune leaves.
func TestDockerCleanupRunUsesThePolicyFloorAndAudits(t *testing.T) {
	db := fn7x3MemoryDB(t)
	require.NoError(t, db.SetSetting(services.SettingDockerCleanupMinAgeHours, "200"))
	r, cleanup, _ := fn7x3Router(t, db)

	w := fn7x3Do(t, r, http.MethodPost, "/api/resources/cleanup/run", "")
	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())

	calls := cleanup.executed()
	require.Len(t, calls, 1)
	require.Equal(t, services.TriggerManual, calls[0].trigger,
		"a manual run was recorded as scheduled, so the history cannot tell an operator's action from the tick's")
	require.Equal(t, 200, calls[0].minAgeHours,
		"the run ignored the stored age floor and used %d", calls[0].minAgeHours)

	entries := auditEntries(t, db, services.ActionPrune)
	require.Len(t, entries, 1,
		"a destructive cleanup run left NO audit trail while POST /resources/images/prune leaves one")
	require.Contains(t, entries[0].Detail, "docker_cleanup")
	require.Contains(t, entries[0].Detail, `"min_age_hours":200`,
		"the audit row does not record the floor the run applied, so the run is uninterpretable after the policy changes: %s", entries[0].Detail)
}

// TestDockerCleanupRunWorksWhileTheScheduleIsDisabled: `enabled` governs the TICK.
// A manual run is an operator explicitly asking, the same distinction
// POST /resources/images/prune already embodies. Asserted so a future reader does
// not "fix" it into a refusal.
func TestDockerCleanupRunWorksWhileTheScheduleIsDisabled(t *testing.T) {
	db := fn7x3MemoryDB(t)
	r, cleanup, _ := fn7x3Router(t, db)

	w := fn7x3Do(t, r, http.MethodPost, "/api/resources/cleanup/run", "")
	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
	require.Len(t, cleanup.executed(), 1)
}

// TestDockerCleanupPreviewFloorAndOverride: preview removes nothing, so its body
// is allowed to carry a candidate floor — but that floor goes through the SAME
// server check the PUT uses, or "what would a 0h floor remove" becomes askable for
// a floor the policy could never hold.
func TestDockerCleanupPreviewFloorAndOverride(t *testing.T) {
	db := fn7x3MemoryDB(t)
	require.NoError(t, db.SetSetting(services.SettingDockerCleanupMinAgeHours, "72"))
	r, cleanup, _ := fn7x3Router(t, db)

	// No body: the stored policy floor.
	w := fn7x3Do(t, r, http.MethodPost, "/api/resources/cleanup/preview", "")
	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
	require.Equal(t, []int{72}, cleanup.previewed())

	// An explicit legal floor is honoured.
	w = fn7x3Do(t, r, http.MethodPost, "/api/resources/cleanup/preview", `{"minAgeHours":5}`)
	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
	require.Equal(t, []int{72, 5}, cleanup.previewed())

	// A sub-floor value is rejected, and the service is NOT called.
	w = fn7x3Do(t, r, http.MethodPost, "/api/resources/cleanup/preview", `{"minAgeHours":0}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body = %s", w.Body.String())
	require.Equal(t, []int{72, 5}, cleanup.previewed(),
		"a sub-floor preview reached the cleanup service anyway")

	// A malformed body is rejected rather than silently served the default.
	w = fn7x3Do(t, r, http.MethodPost, "/api/resources/cleanup/preview", `{"minAgeHours":"nope"}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body = %s", w.Body.String())
	require.Equal(t, []int{72, 5}, cleanup.previewed())
}

// TestDockerCleanupRoutesRefuseWithoutDocker: on a host where Docker was
// unreachable at startup the seams stay nil, and the two Docker-touching cleanup
// routes must refuse with the same 503 every other Docker route gives — not panic,
// and not report success.
//
// The policy and history routes are deliberately NOT in this list: they read the
// database, not Docker, and must keep working so an operator can still see and
// change the policy on a degraded host.
func TestDockerCleanupRoutesRefuseWithoutDocker(t *testing.T) {
	db := fn7x3MemoryDB(t)
	h := NewResourcesHandler(nil, db, nil) // no SetCleanupService: both seams nil
	r := gin.New()
	h.RegisterRoutes(r.Group("/api"))

	for _, path := range []string{"/api/resources/cleanup/preview", "/api/resources/cleanup/run"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		require.NotPanics(t, func() { r.ServeHTTP(w, req) })
		assertUnavailable(t, w)
	}

	// The control: the database-backed routes still answer on the same handler.
	w := fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/policy", "")
	require.Equal(t, http.StatusOK, w.Code,
		"the policy route refused on a Docker-less host, so an operator cannot see or change the policy. body = %s", w.Body.String())
	w = fn7x3Do(t, r, http.MethodGet, "/api/resources/cleanup/history", "")
	require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
}

// TestDockerCleanupPolicyRearmsOnlyOnATickerChange pins the guard in
// updateCleanupPolicy. StartFromPolicy calls Stop() first and is therefore NOT
// idempotent, so re-arming on every accepted PUT discards a mid-flight interval:
// an operator tuning minAgeHours on an already-enabled schedule would silently
// postpone the cleanup that was already counting down, and could postpone it
// indefinitely by tuning it again. handlers/settings.go:749-754 is the house
// precedent -- it restarts the scan scheduler only when the interval really
// changed.
//
// Both arms run on the SAME fixture against the SAME fake, because a lone
// "zero re-arms" assertion is satisfied just as well by a fake that can never
// record one. The second arm is what proves the counter moves here at all.
func TestDockerCleanupPolicyRearmsOnlyOnATickerChange(t *testing.T) {
	seedEnabled := func(t *testing.T, db *database.DB) {
		t.Helper()
		require.NoError(t, db.SetSetting(services.SettingDockerCleanupEnabled, "true"))
		require.NoError(t, db.SetSetting(services.SettingDockerCleanupMinAgeHours, "168"))
		require.NoError(t, db.SetSetting(services.SettingDockerCleanupIntervalHours, "24"))
	}

	t.Run("minAgeHours alone does not re-arm", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		seedEnabled(t, db)
		r, _, armer := fn7x3Router(t, db)

		w := fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy", `{"minAgeHours":200}`)

		require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
		stored, err := db.GetSetting(services.SettingDockerCleanupMinAgeHours)
		require.NoError(t, err, "the age floor was not stored")
		require.Equal(t, "200", stored)
		require.Zero(t, armer.armCalls(),
			"a PUT touching only minAgeHours re-armed the scheduler; StartFromPolicy calls Stop first, so that discards a mid-flight interval and postpones a cleanup already counting down")
	})

	t.Run("control: changing intervalHours DOES re-arm on the same fixture", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		seedEnabled(t, db)
		r, _, armer := fn7x3Router(t, db)

		w := fn7x3Do(t, r, http.MethodPut, "/api/resources/cleanup/policy", `{"intervalHours":48}`)

		require.Equal(t, http.StatusOK, w.Code, "body = %s", w.Body.String())
		require.Equal(t, 1, armer.armCalls(),
			"changing the interval did not re-arm, so the new period would not take effect until the next process restart -- this arm is also the control proving the counter can move in this fixture")
	})
}
