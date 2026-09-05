package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

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
func TestCheckUpdates_HealthyEmptyCacheStaysQuiet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := captureHandlerLogs(t)

	handler := newTestResourcesHandler(t)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api"))

	req := httptest.NewRequest(http.MethodGet, "/api/resources/updates", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, buf.String(), "level=ERROR")
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
	handler.RegisterRoutes(router.Group("/api/git"))

	req := httptest.NewRequest(http.MethodGet, "/api/git?stackId=does-not-exist", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "NOT_FOUND", body["code"])
	assert.Equal(t, "Stack not found", body["message"])

	assert.NotContains(t, buf.String(), "level=ERROR",
		"a plain missing-stack 404 must stay silent; captured=%q", buf.String())
}
