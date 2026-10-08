package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-6exk: sessions.expires_at and action_log.created_at are compared
// and ORDERed as text. The driver used to store a bound time.Time as
// t.String() in its own zone, so across a DST fall-back hour the later instant
// could carry the smaller text ("01:10 -0500 EST" < "01:50 -0400 EDT").
//
// These tests set time.Local, a process global, so none of them may call
// t.Parallel(). Go runs every sequential top-level test, cleanup included,
// before it releases the parallel ones.

// useLocal sets time.Local for the test and restores it afterwards.
func useLocal(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })
	return loc
}

// The 2026 fall-back in New York is 2026-11-01 06:00Z: 01:00-02:00 local runs
// twice, first as EDT (-04:00), then as EST (-05:00).
var (
	beforeFallBack = time.Date(2026, 11, 1, 5, 50, 0, 0, time.UTC) // 01:50 EDT
	afterFallBack  = time.Date(2026, 11, 1, 6, 10, 0, 0, time.UTC) // 01:10 EST, 20 minutes LATER
	expiredInstant = time.Date(2026, 11, 1, 5, 40, 0, 0, time.UTC) // 01:40 EDT
)

func seedSessionUser(t *testing.T, db *DB) {
	t.Helper()
	_, err := db.db.Exec(`INSERT INTO users (id, username, password) VALUES ('u1', 'u1', 'x')`)
	require.NoError(t, err)
}

func sessionIDs(t *testing.T, db *DB) []string {
	t.Helper()
	rows, err := db.db.Query(`SELECT id FROM sessions ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

func actionIDs(actions []models.ActionLog) []string {
	ids := make([]string, 0, len(actions))
	for _, a := range actions {
		ids = append(ids, a.ID)
	}
	return ids
}

func TestDeleteExpiredSessions_KeepsUnexpiredSessionAcrossFallBack(t *testing.T) {
	loc := useLocal(t, "America/New_York")
	db := newTestDB(t)
	seedSessionUser(t, db)

	created := expiredInstant.Add(-time.Hour).In(loc)
	require.NoError(t, db.CreateSession(models.Session{ID: "unexpired", UserID: "u1", ExpiresAt: afterFallBack.In(loc), CreatedAt: created}))
	require.NoError(t, db.CreateSession(models.Session{ID: "expired", UserID: "u1", ExpiresAt: expiredInstant.In(loc), CreatedAt: created}))

	require.NoError(t, db.deleteExpiredSessionsAt(beforeFallBack.In(loc)))
	require.Equal(t, []string{"unexpired"}, sessionIDs(t, db),
		"now is 01:50 EDT: the session expiring at 01:10 EST (20 minutes later) must survive, the one at 01:40 EDT must go")

	got, err := db.GetSession("unexpired")
	require.NoError(t, err)
	require.True(t, got.ExpiresAt.Equal(afterFallBack), "expires_at must read back as the same instant, got %v", got.ExpiresAt)
}

func TestGetRecentActions_OrdersByInstantAcrossFallBack(t *testing.T) {
	loc := useLocal(t, "America/New_York")
	db := newTestDB(t)

	require.NoError(t, db.LogAction(models.ActionLog{ID: "first", UserID: "u1", StackID: "s1", Action: "a", CreatedAt: beforeFallBack.In(loc)}))
	require.NoError(t, db.LogAction(models.ActionLog{ID: "second", UserID: "u1", StackID: "s1", Action: "a", CreatedAt: afterFallBack.In(loc)}))

	recent, err := db.GetRecentActions(10)
	require.NoError(t, err)
	require.Equal(t, []string{"second", "first"}, actionIDs(recent), "newest instant first")

	byStack, err := db.GetActionsByStack("s1", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"second", "first"}, actionIDs(byStack), "newest instant first")

	page, _, err := db.ListActionLogsPaginated(10, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"second", "first"}, actionIDs(page), "newest instant first")

	require.True(t, recent[0].CreatedAt.Equal(afterFallBack), "created_at must read back as the same instant, got %v", recent[0].CreatedAt)
}

// DeleteOldActionLogs compares a cutoff against created_at as text too. A row
// one hour inside the window, written in a different spelling than the
// cutoff, must survive.
func TestDeleteOldActionLogs_KeepsRowInsideWindowAcrossZones(t *testing.T) {
	useLocal(t, "America/New_York")
	db := newTestDB(t)
	inside := time.Now().AddDate(0, 0, -MinRetentionDays).Add(time.Hour)
	// Spelled in a zone far west of Local, so its wall clock reads earlier
	// than the cutoff's while the instant is later.
	far := time.FixedZone("UTC-11", -11*3600)
	require.NoError(t, db.LogAction(models.ActionLog{ID: "inside", UserID: "u1", Action: "a", CreatedAt: inside.In(far)}))

	require.NoError(t, db.DeleteOldActionLogs(MinRetentionDays))
	require.Equal(t, 1, countRows(t, db, "action_log"), "a row one hour inside the retention window was deleted")
}

// The date filter keeps its documented meaning, the server-local date, now
// that created_at is stored in UTC. 00:30 on 2026-05-31 in Amsterdam is
// 2026-05-30T22:30Z: a UTC-date filter would put it on the wrong day.
func TestListActionLogsFiltered_DateRangeUsesServerLocalDate(t *testing.T) {
	loc := useLocal(t, "Europe/Amsterdam")
	db := newTestDB(t)
	require.NoError(t, db.LogAction(models.ActionLog{ID: "early", UserID: "u1", Action: "a", CreatedAt: time.Date(2026, 5, 31, 0, 30, 0, 0, loc)}))
	require.NoError(t, db.LogAction(models.ActionLog{ID: "late", UserID: "u1", Action: "a", CreatedAt: time.Date(2026, 5, 31, 23, 59, 30, 0, loc)}))

	for _, tc := range []struct {
		from, to string
		want     []string
	}{
		{"2026-05-31", "2026-05-31", []string{"late", "early"}},
		{"2026-05-30", "2026-05-30", []string{}},
		{"2026-06-01", "2026-06-01", []string{}},
		{"2026-05-31", "", []string{"late", "early"}},
		{"", "2026-05-30", []string{}},
	} {
		rows, total, err := db.ListActionLogsFiltered(50, 0, ActionLogFilter{DateFrom: tc.from, DateTo: tc.to})
		require.NoError(t, err)
		require.Equal(t, tc.want, actionIDs(rows), "from=%q to=%q", tc.from, tc.to)
		require.Equal(t, len(tc.want), total, "from=%q to=%q", tc.from, tc.to)
	}
}

func rawText(t *testing.T, db *DB, query, id string) string {
	t.Helper()
	var v sql.NullString
	require.NoError(t, db.db.QueryRow(query, id).Scan(&v))
	require.True(t, v.Valid, "value for %s must not be NULL", id)
	return v.String
}

// Rows written before the fix hold the driver's t.String() spelling. Migration
// 23 rewrites them; this seeds that spelling directly and re-runs it.
func TestMigration23_RewritesOldSpellingWithoutDestroyingAnything(t *testing.T) {
	loc := useLocal(t, "America/New_York")
	db := newTestDB(t)
	seedSessionUser(t, db)

	insertSession := func(id, expires, created string) {
		_, err := db.db.Exec(`INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, 'u1', ?, ?)`, id, expires, created)
		require.NoError(t, err)
	}
	insertAction := func(id, created string) {
		_, err := db.db.Exec(`INSERT INTO action_log (id, user_id, action, created_at) VALUES (?, 'u1', 'a', ?)`, id, created)
		require.NoError(t, err)
	}
	// Old spelling, exactly what the driver wrote: t.String() with a
	// monotonic suffix on values from time.Now().
	insertSession("unexpired", "2026-11-01 01:10:00 -0500 EST", "2026-11-01 00:10:00.5 -0400 EDT m=+12.5")
	insertSession("expired", "2026-11-01 01:40:00 -0400 EDT", "2026-11-01 00:10:00 -0400 EDT")
	insertAction("first", "2026-11-01 01:50:00.123456789 -0400 EDT m=+3.000000001")
	insertAction("second", "2026-11-01 01:10:00 -0500 EST")
	// Already in the new spelling: must be left alone.
	insertAction("new", "2026-11-01T07:00:00.000Z")
	// Not a time at all: must survive byte-identical.
	insertAction("garbage", "not-a-date")

	rerun := func() {
		t.Helper()
		_, err := db.db.Exec("DELETE FROM schema_migrations WHERE version = 23")
		require.NoError(t, err)
		require.NoError(t, RunMigrations(db))
	}
	sessionCol := func(col, id string) string {
		return rawText(t, db, `SELECT CAST(`+col+` AS TEXT) FROM sessions WHERE id = ?`, id)
	}
	actionCreated := func(id string) string {
		return rawText(t, db, `SELECT CAST(created_at AS TEXT) FROM action_log WHERE id = ?`, id)
	}
	assertAll := func(when string) {
		t.Helper()
		require.Equal(t, "2026-11-01T06:10:00.000Z", sessionCol("expires_at", "unexpired"), when)
		require.Equal(t, "2026-11-01T04:10:00.500Z", sessionCol("created_at", "unexpired"), when)
		require.Equal(t, "2026-11-01T05:40:00.000Z", sessionCol("expires_at", "expired"), when)
		require.Equal(t, "2026-11-01T05:50:00.123Z", actionCreated("first"), when)
		require.Equal(t, "2026-11-01T06:10:00.000Z", actionCreated("second"), when)
		require.Equal(t, "2026-11-01T07:00:00.000Z", actionCreated("new"), when)
		require.Equal(t, "not-a-date", actionCreated("garbage"), when)
	}
	rerun()
	assertAll("first run")
	rerun()
	assertAll("second run")

	// The migrated rows now behave: the unexpired session survives a sweep at
	// 01:50 EDT, and the actions order by instant.
	require.NoError(t, db.deleteExpiredSessionsAt(beforeFallBack.In(loc)))
	require.Equal(t, []string{"unexpired"}, sessionIDs(t, db))

	_, err := db.db.Exec(`DELETE FROM action_log WHERE id = 'garbage'`)
	require.NoError(t, err)
	recent, err := db.GetRecentActions(10)
	require.NoError(t, err)
	require.Equal(t, []string{"new", "second", "first"}, actionIDs(recent))
}
