package handlers

import (
	"encoding/json"
	"fmt"
	"math"
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
	zero, _ := getAuditLogPage(t, router, "?page=4611686018427387905&pageSize=4")
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
