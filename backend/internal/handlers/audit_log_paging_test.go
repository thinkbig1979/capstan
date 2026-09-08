package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// auditLogResponse is the body GetAuditLog writes. Only the fields these
// assertions read are declared.
type auditLogResponse struct {
	Entries  []models.ActionLog `json:"entries"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}

// Five rows one second apart, newest first under ListActionLogsFiltered's
// `ORDER BY created_at DESC`. Five (not four) makes ?page=3&pageSize=2 a SHORT
// last page of exactly one row, which an over-rejecting guard empties.
var seededAuditIDs = []string{"log-e", "log-d", "log-c", "log-b", "log-a"}

// wrapToZeroPage is the page at which (page-1)*4 wraps to EXACTLY zero on THIS
// platform: 2^62+1 where int is 64-bit, 2^30+1 where it is 32-bit. Derived from
// bits.UintSize rather than hardcoded into the query string, because a 64-bit
// literal Atoi-clamps to page=1 on a 32-bit build — the arm would still compile
// there and would then assert the wrong thing entirely. The URL is built from
// this same constant with Sprintf so the value and the request cannot drift.
const wrapToZeroPage = 1<<(bits.UintSize-2) + 1

func seedAuditLogPage(t *testing.T, db *database.DB) {
	t.Helper()
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"log-a", "log-b", "log-c", "log-d", "log-e"} {
		require.NoError(t, db.LogAction(models.ActionLog{
			ID:        id,
			UserID:    "test-user-id",
			Action:    "test_action",
			Detail:    "{}",
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		}))
	}
}

func auditLogIDs(entries []models.ActionLog) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// getAuditLogPage drives GetAuditLog through its REAL route — the
// client-supplied page is the whole point of the bug, so calling the
// arithmetic in isolation would test the wrong thing. Returns the decoded body
// and the raw body bytes; the raw bytes are what prove the JSON shape.
func getAuditLogPage(t *testing.T, router *gin.Engine, query string) (auditLogResponse, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/settings/audit-log"+query, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var body auditLogResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body, w.Body.String()
}

// GetAuditLog computes its SQL OFFSET as (page-1)*pageSize on an int, and int
// wraps. When the product goes negative SQLite treats the OFFSET as absent — so
// ?page=<MaxInt> comes back holding page ONE's entries while echoing the huge
// page number the client asked for. That is a wrong answer served as a correct
// one, which is worse than an error.
//
// pageSize is clamped to [1,100] here, so unlike update history this site has
// only the huge-page route into the wrap; the small-page/large-limit route does
// not exist through this handler.
//
// Three arms, each with a different job:
//   - the defect arm: an overflowing page must return EMPTY entries and the
//     TRUE total, because reporting total:0 would be a second wrong answer
//     served as a correct one by the fix for the first;
//   - the control arm: page one over the same seed is NON-EMPTY, so "empty"
//     above cannot merely mean the table is empty;
//   - the over-rejection arm: an in-range page landing on real data still
//     returns it, which is what separates a guard from a blanket rejection.
func TestGetAuditLog_OffsetOverflowDoesNotWrapToPageOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newTestSettingsHandler(t)
	seedAuditLogPage(t, handler.db)

	// GetAuditLog is not mounted by setupSettingsFullRouter, so mount the one
	// route under test, following audit_logging_test.go's pattern.
	router := gin.New()
	router.GET("/settings/audit-log", authContextMiddleware("test-user-id"), handler.GetAuditLog)

	// Control arm: page one over the same seed is populated.
	first, _ := getAuditLogPage(t, router, "?page=1&pageSize=50")
	require.Equal(t, seededAuditIDs, auditLogIDs(first.Entries), "page one is non-empty, so empty below means something")
	require.Equal(t, len(seededAuditIDs), first.Total)

	// Defect arm 1 — the huge-page route. (MaxInt-1)*50 wraps negative.
	huge, hugeRaw := getAuditLogPage(t, router, fmt.Sprintf("?page=%d&pageSize=50", math.MaxInt))
	assert.Empty(t, auditLogIDs(huge.Entries), "a page past the end must be empty, never page one")
	assert.Equal(t, len(seededAuditIDs), huge.Total, "total must stay the true match-set size, never 0")
	assert.Contains(t, hugeRaw, `"entries":[]`, "empty entries serialize as [], the same shape an ordinary past-the-end page returns")

	// Defect arm 2 — the wrap-to-ZERO route. (1<<62+1-1)*4 wraps to exactly 0,
	// which is not negative, so a guard that multiplied first and then tested
	// the sign of the product would miss this and serve page one again. Only a
	// guard on the OPERANDS catches it.
	zero, _ := getAuditLogPage(t, router, fmt.Sprintf("?page=%d&pageSize=4", wrapToZeroPage))
	assert.Empty(t, auditLogIDs(zero.Entries), "a page whose offset wraps to exactly zero must be empty, never page one")
	assert.Equal(t, len(seededAuditIDs), zero.Total, "total must stay the true match-set size, never 0")

	// Over-rejection arm — the real discriminator. Page 3 at pageSize 2 is the
	// short LAST page: one row, not zero. A guard that over-rejects empties
	// this; a correct one leaves it untouched.
	last, _ := getAuditLogPage(t, router, "?page=3&pageSize=2")
	assert.Equal(t, []string{"log-a"}, auditLogIDs(last.Entries), "the last page is short, not empty")
	assert.Equal(t, len(seededAuditIDs), last.Total)

	// Regression control, NOT a discriminator: a merely large page (no
	// overflow) already returned empty before the guard existed, so it cannot
	// be seen failing first and a blanket large-page rejection predicts the
	// same output. It is here so that behaviour does not break.
	big, _ := getAuditLogPage(t, router, "?page=999999999&pageSize=50")
	assert.Empty(t, auditLogIDs(big.Entries), "an in-range page past the end is empty too")
	assert.Equal(t, len(seededAuditIDs), big.Total)
}

// The overflow guard divides by pageSize, which the handler did not do before
// the guard existed — back then pageSize was only ever MULTIPLIED, so
// pageSize = 0 gave offset = 0 and was merely useless. It is now a DIVISOR,
// which makes the clamp rejecting a pageSize below 1 load-bearing for
// panic-safety rather than only for defaults. Nothing pinned that coupling, so
// a later cleanup could weaken the clamp with no idea it was holding back a
// divide-by-zero. This test drives the real route, because the clamp reads a
// client-supplied query parameter and that is the whole exposure.
//
// The router is gin.New() with no recovery middleware, deliberately: an
// unclamped 0 panics inside the guard and the panic surfaces as a test
// failure here rather than being converted into a quiet 500.
func TestGetAuditLog_ZeroPageSizeDoesNotDivideByZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newTestSettingsHandler(t)
	seedAuditLogPage(t, handler.db)

	router := gin.New()
	router.GET("/settings/audit-log", authContextMiddleware("test-user-id"), handler.GetAuditLog)

	// pageSize 0 reaches the clamp, which substitutes the default page size.
	// The assertion is that this RESPONDS at all — an unclamped 0 panics with
	// "integer divide by zero" inside the guard and never reaches the 200.
	zeroSize, _ := getAuditLogPage(t, router, "?pageSize=0")
	assert.Equal(t, seededAuditIDs, auditLogIDs(zeroSize.Entries), "pageSize 0 falls back to the default page size")
	assert.Equal(t, 50, zeroSize.PageSize, "the clamp substitutes the default, it does not pass 0 through")
	assert.Equal(t, len(seededAuditIDs), zeroSize.Total)

	// Page 2 as well: page-1 is non-zero here, so the division is reached by a
	// different route through the comparison than the page-1 case above.
	secondPage, _ := getAuditLogPage(t, router, "?page=2&pageSize=0")
	assert.Empty(t, auditLogIDs(secondPage.Entries), "page two at the default page size is past the end of a five-row seed")
	assert.Equal(t, len(seededAuditIDs), secondPage.Total)

	// A negative pageSize takes the same clamp branch and must behave the same.
	negative, _ := getAuditLogPage(t, router, "?pageSize=-1")
	assert.Equal(t, seededAuditIDs, auditLogIDs(negative.Entries), "a negative pageSize falls back to the default too")
	assert.Equal(t, 50, negative.PageSize)

	// Control arm: an explicit in-range pageSize still pages normally.
	explicit, _ := getAuditLogPage(t, router, "?page=1&pageSize=2")
	assert.Equal(t, []string{"log-e", "log-d"}, auditLogIDs(explicit.Entries), "an explicit pageSize still applies")
	assert.Equal(t, 2, explicit.PageSize)
}
