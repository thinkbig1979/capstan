package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// A malformed dateFrom/dateTo is a client error. ListActionLogsFiltered
// rejects it (agent-os-6exk turned the date into an instant range), so the
// handler must answer 400 itself rather than let that surface as a 500.
func TestGetAuditLog_MalformedDateIsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newTestSettingsHandler(t)
	seedAuditLogPage(t, handler.db)
	router := gin.New()
	router.GET("/settings/audit-log", authContextMiddleware("test-user-id"), handler.GetAuditLog)

	get := func(query string) int {
		req := httptest.NewRequest(http.MethodGet, "/settings/audit-log"+query, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	for _, q := range []string{"?dateFrom=yesterday", "?dateTo=2026-13-01", "?dateFrom=2026-5-31", "?dateFrom=2026-05-31&dateTo=31-05-2026"} {
		assert.Equal(t, http.StatusBadRequest, get(q), q)
	}
	// Control: well-formed dates, and no dates, still succeed.
	for _, q := range []string{"?dateFrom=2026-05-31&dateTo=2026-05-31", "?dateFrom=2026-05-31", ""} {
		assert.Equal(t, http.StatusOK, get(q), q)
	}
}
