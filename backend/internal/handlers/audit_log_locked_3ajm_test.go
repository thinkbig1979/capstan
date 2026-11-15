package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestAuditLog_LockedHidesDetails_3ajm (agent-os-3ajm): the action log served
// every row's free-text detail to a locked session, a git_commit row's file
// names and any other detail included. Locked, every entry's detail is empty
// and the reply says detailsHidden; a search, which matches detail text, is
// refused with ENV_LOCKED. Unlocked, the rows and the search are unchanged.
func TestAuditLog_LockedHidesDetails_3ajm(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newTestSettingsHandler(t)
	detail := `{"commit":"abc","files":["note-` + secret3ajm + `.txt"],"path":"/stacks/app"}`
	require.NoError(t, handler.db.LogAction(models.ActionLog{
		ID: "log-commit", UserID: "test-user-id", StackID: "s-app",
		Action: "git_commit", Detail: detail, CreatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
	}))

	locked := gin.New()
	locked.GET("/settings/audit-log", authContextMiddleware("test-user-id"), handler.GetAuditLog)
	unlocked := gin.New()
	unlocked.GET("/settings/audit-log", authContextMiddleware("test-user-id"), envUnlockedMiddleware(), handler.GetAuditLog)

	get := func(r *gin.Engine, q string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/settings/audit-log"+q, nil))
		return w
	}

	w := get(locked, "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), secret3ajm, "a locked audit log carries a detail")
	var body struct {
		Entries       []models.ActionLog `json:"entries"`
		DetailsHidden bool               `json:"detailsHidden"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Entries, 1)
	assert.Equal(t, "git_commit", body.Entries[0].Action, "the row itself stays")
	assert.Empty(t, body.Entries[0].Detail)
	assert.True(t, body.DetailsHidden)

	// A search reads the details, so a locked one is refused, hit or miss.
	for _, q := range []string{"?search=" + secret3ajm, "?search=nothing-matches"} {
		w = get(locked, q)
		assert.Equal(t, http.StatusForbidden, w.Code, "%s: %s", q, w.Body.String())
		assert.Contains(t, w.Body.String(), models.ErrEnvLocked)
	}

	w = get(unlocked, "?search="+secret3ajm)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), secret3ajm, "unlocked rows are unchanged")
	assert.NotContains(t, w.Body.String(), "detailsHidden")
}
