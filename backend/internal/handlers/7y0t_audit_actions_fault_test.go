package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-7y0t. GetAuditLog checked DistinctActionLogActions' error, then
// replaced the list with [] and did not log anything. On a fault the 200 said
// "no action types exist" beside a page of entries that plainly have action
// types, and the server left no trace of why. The list is now OMITTED on a
// fault (xppj's convention: absent, never wrong) and the error is logged.

// poisonedActionTypesDB returns a DB where DistinctActionLogActions fails and a
// FILTERED ListActionLogsFiltered still succeeds. Both read action_log, so
// hiding the table would fault the entries too and prove nothing. Instead the
// table is rebuilt without NOT NULL on action and given one NULL-action row:
// DISTINCT returns that NULL and its Scan into a string fails, while
// ?action=stack.start's WHERE clause never selects it.
func poisonedActionTypesDB(t *testing.T) *database.DB {
	t.Helper()

	dataDir := newMigratedDBDir(t)
	db, err := database.New(dataDir)
	require.NoError(t, err, "open migrated on-disk db")
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, db.LogAction(models.ActionLog{
		ID:        "log-real",
		UserID:    "test-user-id",
		Action:    "stack.start",
		Detail:    "{}",
		CreatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
	}))

	side, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	require.NoError(t, err, "open side connection")
	t.Cleanup(func() { _ = side.Close() })

	for _, stmt := range []string{
		`ALTER TABLE action_log RENAME TO action_log_orig`,
		`CREATE TABLE action_log (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			stack_id TEXT,
			action TEXT,
			detail TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			request_id TEXT
		)`,
		`INSERT INTO action_log (id, user_id, stack_id, action, detail, created_at, request_id)
			SELECT id, user_id, stack_id, action, detail, created_at, request_id FROM action_log_orig`,
		`DROP TABLE action_log_orig`,
		`INSERT INTO action_log (id, user_id, action, detail) VALUES ('log-poison', 'test-user-id', NULL, '{}')`,
	} {
		_, err := side.Exec(stmt)
		require.NoError(t, err, "poison action_log: %s", stmt)
	}
	return db
}

func getAuditLogRaw(t *testing.T, db *database.DB, query string) (int, map[string]json.RawMessage, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := NewSettingsHandler(db, "/opt/stacks", dbFaultTestSecret, false, nil, &config.Config{})
	router := gin.New()
	router.GET("/settings/audit-log", authContextMiddleware("test-user-id"), h.GetAuditLog)

	req := httptest.NewRequest(http.MethodGet, "/settings/audit-log"+query, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "body: %s", w.Body.String())
	return w.Code, body, w.Body.String()
}

// Two-sided control for the fixture: the action-type read must fault AND the
// filtered entries read must not, or the fault arm below measures something
// else.
func TestPoisonedActionTypesDB_FaultsOnlyTheActionTypes(t *testing.T) {
	db := poisonedActionTypesDB(t)

	_, err := db.DistinctActionLogActions()
	require.Error(t, err, "the fixture did not make DistinctActionLogActions fail")

	entries, total, err := db.ListActionLogsFiltered(50, 0, database.ActionLogFilter{Action: "stack.start"})
	require.NoError(t, err, "the fixture broke the filtered entries read too")
	require.Equal(t, 1, total)
	require.Len(t, entries, 1)
}

// FAULT ARM. The entries read cleanly, so the request still succeeds; only the
// action-type list is unknown, and it must be absent rather than [] and the
// failure must be logged.
func TestGetAuditLog_UnreadableActionTypesAreOmittedAndLogged(t *testing.T) {
	buf := captureHandlerLogs(t)
	db := poisonedActionTypesDB(t)

	code, body, raw := getAuditLogRaw(t, db, "?action=stack.start")

	require.Equal(t, http.StatusOK, code, "the entries read cleanly, so the page must still be served: %s", raw)
	var entries []models.ActionLog
	require.NoError(t, json.Unmarshal(body["entries"], &entries))
	require.Len(t, entries, 1, "precondition: the entries payload must be intact, body = %s", raw)

	actions, present := body["availableActions"]
	require.False(t, present,
		"a DistinctActionLogActions fault is being emitted as a factual list. availableActions = %s — "+
			"indistinguishable from an audit log with no action types, which "+
			"TestGetAuditLog_EmptyLogStillReportsEmptyActionTypes proves this server also sends",
		string(actions))

	logged := buf.String()
	require.True(t, strings.Contains(logged, "Failed to list audit log action types"),
		"the fault was not logged; captured logs:\n%s", logged)
	require.Contains(t, logged, "converting NULL to string",
		"the log line does not carry the underlying error; captured logs:\n%s", logged)
}

// EMPTY ARM, the positive side of the same instrument: a healthy log with no
// entries genuinely has no action types and must still say [] (not omit it,
// and not null).
func TestGetAuditLog_EmptyLogStillReportsEmptyActionTypes(t *testing.T) {
	db, err := database.NewWithMigrations(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	code, body, raw := getAuditLogRaw(t, db, "")

	require.Equal(t, http.StatusOK, code, "body: %s", raw)
	actions, present := body["availableActions"]
	require.True(t, present, "a healthy, empty audit log must still report its action types, body = %s", raw)
	require.JSONEq(t, `[]`, string(actions))
}
