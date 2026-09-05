package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// plantStrayServerFaultLine models the hazard agent-os-nho7 pins for every
// ABSENCE assertion in this package: a goroutine this test never started and
// never joined (a leak from an earlier test, net/http teardown, a sibling
// connection's handler) logging an ERROR through the shared default sink
// while this test's capture buffer IS that sink. captureHandlerLogs
// (respond_test.go) swaps the PROCESS-GLOBAL slog default, so an undiscriminated
// NotContains on the bare level=ERROR token is red the moment any such line
// lands — a logical race the race detector can never report. (Written without
// the call parentheses on purpose: the class sweep in agent-os-nho7's close
// reason greps for that call shape, and a comment quoting it verbatim would
// show up as a site to triage.)
//
// It logs logServerFault's exact line shape (respond.go: msg "request
// failed", with request_id/status/code/error attrs) under a request_id that
// is NOT this test's — the WS-flavoured plantStrayUpgradeFailureLine
// (ws_upgrade_failure_log_test.go, agent-os-737f) is the same idea at the
// four upgrade sites; none of nho7's eight sites is a WS upgrade.
//
// Same construction and the same safety argument as that one: the goroutine
// starts after captureHandlerLogs swapped the sink, and the caller receives
// from the returned channel before reading the buffer, so the read is
// ordered after the write however the scheduler interleaves it. slog's
// TextHandler serialises concurrent Handle calls under its own mutex and
// syncLogBuffer holds a mutex of its own, so the two writers do not race
// (OBSERVED: the -count=3 -race gate on all eight tests stays clean with the
// plant in place).
// strayPlantMarker identifies the planted line specifically. The guards below
// look for THIS text rather than for a bare level=ERROR token, so that a plant
// which never ran cannot be masked by some other goroutine's ERROR line
// happening to be in the buffer. Shared between the planter and the guards on
// purpose: the two cannot drift apart.
const strayPlantMarker = "planted by agent-os-nho7"

func plantStrayServerFaultLine(t *testing.T) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		slog.Default().Error("request failed",
			"request_id", uuid.NewString(),
			"status", 500,
			"code", "INTERNAL_ERROR",
			"error", strayPlantMarker+": a stray line from a goroutine this test did not synchronise with")
	}()
	return done
}

// requirePlantLanded fails unless the planted stray is in the capture window,
// on a level=ERROR line.
//
// It is not decoration, and it is not the proof either: it is the tripwire that
// keeps the proof honest. An absence assertion passes when the instrument is
// dead exactly as it passes when the behaviour is correct, so the plant has to
// be shown present before any absence check below it means anything.
//
// It matches the plant's OWN marker, not a bare level=ERROR token. Counting
// bare tokens would let ANY other ERROR line stand in for a plant that never
// logged — the two are indistinguishable to a count, which is the very
// substitution this whole bead is about.
//
// At the nine sites whose handler is silent on the path under test, ordering
// alone would have been enough (captureHandlerLogs runs before the plant, and
// the caller receives from the plant's channel before reading the buffer), so
// there the marker is a tripwire against a later reordering. At the tenth,
// TestDashboardMetricsWS_UpgradeFailureLogsTheCause, it is not a tripwire but
// the actual fix: serveWS legitimately logs its own ERROR line for that
// request, so a bare count is satisfied whether or not the plant ever ran.
// OBSERVED, two-sided, on one instrument: with the plant disarmed, a bare
// level=ERROR count guard at that site stays GREEN while this marker guard
// goes RED.
//
// countErrorLines, so the match is required to be ON a level=ERROR line. That
// is what carries the second half of the claim: the buffer demonstrably holds
// an ERROR line here, so the undiscriminated assertion this replaced would
// have been RED on this very run, and any site that reaches the discriminated
// check below is a site where the old form was failing.
func requirePlantLanded(t *testing.T, captured string) {
	t.Helper()
	if n := countErrorLines(captured, strayPlantMarker); n != 1 {
		t.Fatalf("the planted stray line is not in the capture window as a level=ERROR line (found %d carrying %q, want 1): "+
			"the undiscriminated assertion this replaced would still PASS here, so the discriminated check that follows proves nothing. captured = %q",
			n, strayPlantMarker, captured)
	}
}

// requireNoOwnErrorLines is agent-os-nho7's discriminated replacement for the
// eight sites that asserted assert.NotContains over the bare level=ERROR
// token: it asserts that no ERROR line in the shared sink carries THIS
// request's sentinel, which is the claim each of those sites actually wanted
// to make.
//
// errorLinesFor returns the captured level=ERROR lines carrying mustContain —
// countErrorLines' selection, handed back instead of tallied.
//
// A count alone pins how MANY lines a request produced, never WHICH. That gap
// is reachable by a single double mutation: demote the line under test below
// ERROR and let a different ERROR line for the same request take its place,
// and the count is unchanged while the thing being asserted is gone. Callers
// that care which line they got assert on the line itself.
func errorLinesFor(captured, mustContain string) []string {
	var lines []string
	for _, line := range strings.Split(captured, "\n") {
		if strings.Contains(line, "level=ERROR") && strings.Contains(line, mustContain) {
			lines = append(lines, line)
		}
	}
	return lines
}

// requirePlantLanded runs first, for the reasons stated on it.
func requireNoOwnErrorLines(t *testing.T, captured, sentinel, what string) {
	t.Helper()
	requirePlantLanded(t, captured)
	if n := countErrorLines(captured, sentinel); n != 0 {
		t.Fatalf("%s produced %d ERROR line(s) carrying %s, want 0. captured = %q", what, n, sentinel, captured)
	}
}

// TestCheckUpdates_CacheFetchFailureLogsCause pins agent-os-2mhb's inline-site
// half (Wave 2, worker w2c): checkUpdates' non-refresh branch already routed
// its GetCachedUpdates failure through handleError, and this specific site
// ALREADY had an adjacent slog.Error("Failed to get cached updates", "error",
// err) beside the call — one of the 10 of 26 the 2mhb bead's own census flags
// as "already diagnosable today". So a bare Contains(buf, "database is
// closed") is NOT a valid discriminator here: that adjacent line logs the raw
// driver error either way and would pass before AND after the fix, which is
// exactly the "check that could only have come out one way" trap (COMMON
// BLOCK clause 5). The real discriminator is logServerFault's own "cause"
// attribute (respond.go), which it appends ONLY when AppError.Cause != nil —
// never true before this conversion, always true after — so this asserts on
// "cause=" specifically, not on the error text the adjacent slog already
// carried regardless of this change.
//
// Two-part assertion on one instrument: status/code/message are unchanged
// (this was never a silent-response defect) AND logServerFault's own line now
// carries the cause. VERIFIED seen failing first via `go test -overlay`
// against pre-fix updates.go (models.NewAppError, no cause): status/code/
// message assertions passed unchanged, and only the "cause=" assertion below
// failed (logServerFault had no Cause to attach).
func TestCheckUpdates_CacheFetchFailureLogsCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db := faultyDB(t)
	handler := NewResourcesHandler(nil, db, nil)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "INTERNAL_ERROR", body["code"], "status/code must survive the cause-attach conversion unchanged")
	assert.Equal(t, "Failed to get cached updates", body["message"])

	assert.Contains(t, buf.String(), "cause=",
		"logServerFault must carry an explicit cause attribute now that the AppError is minted WithCause; captured=%q", buf.String())
	assert.Contains(t, buf.String(), "database is closed",
		"the faultyDB driver error text must appear (via the adjacent slog and/or the new cause attr); captured=%q", buf.String())
	assert.NotContains(t, w.Body.String(), "database is closed",
		"the cause must never leak into the response body")
}

// TestCheckUpdates_HealthyEmptyCacheStaysQuiet is the control for the test
// above on the SAME db-backed instrument: a healthy DB with nothing cached is
// not an error at all, so it must return 200 and log nothing at ERROR level —
// proving the fix didn't turn every checkUpdates call into a logged fault.
//
// The "logs nothing at ERROR" half is discriminated on this request's own id
// (agent-os-nho7): RequestID() puts the sentinel on the context and
// logServerFault stamps it on any line it writes, so the assertion is about
// THIS request rather than about whatever else happened to reach the shared
// sink. See requireNoOwnErrorLines above.
func TestCheckUpdates_HealthyEmptyCacheStaysQuiet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	handler := newTestResourcesHandler(t)
	router := gin.New()
	router.Use(middleware.RequestID())
	handler.RegisterRoutes(router.Group("/api"))

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates", nil)
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusOK, w.Code)
	requireNoOwnErrorLines(t, buf.String(), sentinel, "a healthy checkUpdates with an empty cache")
}

// TestGitGetStatus_DBFaultReturns500WithLoggedCause pins agent-os-7lg1's
// representative site (git.go's resolvePathFromStack, git.go ~48-60): before
// the fix, ANY db.GetStack error — not just a missing row — was mapped to a
// silent 404 with no log line at all, indistinguishable from an operator's
// point of view from a genuinely absent stack.
//
// Seen failing first against pre-fix code: the response was 404
// NOT_FOUND/"Stack not found" (this test's assertions on status/code would
// fail outright) and nothing was logged (the log assertion would also fail).
// Both assertions in this test are the new behaviour; TestGitGetStatus_
// UnknownStackID404sQuietly below is the two-sided control proving the
// genuine-missing-row 404 path is untouched.
func TestGitGetStatus_DBFaultReturns500WithLoggedCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	db := faultyDB(t)
	cfg := &config.Config{StacksDir: t.TempDir()}
	handler := NewGitHandler(services.NewGitService(cfg, db), nil, db, cfg)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/git"))

	req := httptest.NewRequest(http.MethodGet, "/api/git?stackId=whatever", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a genuine DB fault behind db.GetStack must surface as 500, not the generic 404 every error used to collapse to")

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "INTERNAL_ERROR", body["code"])

	// resolvePathFromStack has no adjacent slog of its own (unlike the
	// updates.go site above), so pre-fix this path logged NOTHING at all — the
	// 404 branch made handleError see a 4xx, and logServerFault is silent
	// below 500. Both "cause=" and the raw driver text are valid
	// discriminators here.
	assert.Contains(t, buf.String(), "cause=",
		"logServerFault must carry an explicit cause attribute; captured=%q", buf.String())
	assert.Contains(t, buf.String(), "database is closed",
		"a database fault behind db.GetStack must be logged with its cause; captured=%q", buf.String())
	assert.NotContains(t, w.Body.String(), "database is closed",
		"the cause must never leak into the response body")
}

// TestGitGetStatus_UnknownStackID404sQuietly is the two-sided control: a
// genuinely missing stack against a HEALTHY db must still 404 exactly as
// before (byte-for-byte preserved branch) and must stay silent — logServerFault
// never fires below 500, so no ERROR line should appear either.
func TestGitGetStatus_UnknownStackID404sQuietly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	healthy, err := database.New(newMigratedDBDir(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = healthy.Close() })

	cfg := &config.Config{StacksDir: t.TempDir()}
	handler := NewGitHandler(services.NewGitService(cfg, healthy), nil, healthy, cfg)
	router := gin.New()
	router.Use(middleware.RequestID())
	handler.RegisterRoutes(router.Group("/api/git"))

	plantDone := plantStrayServerFaultLine(t)
	reqID, sentinel := requestIDSentinel()

	req := httptest.NewRequest(http.MethodGet, "/api/git?stackId=does-not-exist", nil)
	req.Header.Set(middleware.RequestIDHeader, reqID)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	<-plantDone

	require.Equal(t, http.StatusNotFound, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["code"])
	assert.Equal(t, "Stack not found", body["message"])

	requireNoOwnErrorLines(t, buf.String(), sentinel, "a plain missing-stack 404")
}
