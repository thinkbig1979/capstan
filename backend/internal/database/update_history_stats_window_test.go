package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// GetUpdateStats' 7/30-day counts compare started_at -- stored as RFC3339,
// "2026-09-11T00:00:00Z" -- against a cutoff, and SQLite compares TEXT. A
// cutoff in datetime()'s shape, "2026-09-11 08:23:11", is therefore decided at
// the separator rather than at the time: 'T' (0x54) outranks ' ' (0x20), so
// every row sharing the cutoff's calendar date counted as inside the window
// whatever time of day it held, and the dashboard over-counted by up to a day.
//
// The fixtures below sit ON the cutoff's own UTC date on purpose. A row a few
// days either side of the boundary is decided by the date prefix alone, so it
// answers the same on broken and fixed code and proves nothing about this bug.

func startOfDayUTC(ts time.Time) time.Time {
	return time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.UTC)
}

func timeOfDayUTC(ts time.Time) time.Duration {
	return ts.Sub(startOfDayUTC(ts))
}

// olderSameDate is midnight UTC on the cutoff's own calendar date: strictly
// older than the `days`-ago cutoff while sharing its date, which is the only
// case the separator mismatch decides. The margin is the cutoff's whole time
// of day, up to 24h, so drift between Go's clock and SQLite's cannot flip it.
//
// DO NOT simplify this to the obvious "N days and an hour ago". That
// construction is correct 23 hours in 24 and silently wrong in the 24th:
// between 00:00 and 01:00 UTC, now-7d-1h lands on the calendar date BEFORE the
// cutoff's, the date prefix then decides the comparison correctly, and the
// UNFIXED code returns the right answer -- so the arm passes against the very
// bug it exists to catch, once a day, forever. Every local run outside that
// hour agrees with the simplification, which is what makes it so easy to make.
// Midnight of the cutoff's own date is on that date by construction rather
// than by arithmetic against the wall clock, so only the separator is left to
// decide the row.
func olderSameDate(t *testing.T, days int) string {
	t.Helper()
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	if timeOfDayUTC(cutoff) < 2*time.Second {
		// Just past midnight there is no earlier instant left on the cutoff's
		// own date, so the fixture would not discriminate. Wait the window out
		// instead of t.Skip: a skipped regression test is indistinguishable
		// from a passing one.
		t.Logf("cutoff %s is within 2s of midnight UTC; waiting it out so the "+
			"fixture has room earlier on the cutoff's own date. This sleep is "+
			"load-bearing -- do not delete it.", cutoff.Format(time.RFC3339))
		time.Sleep(2 * time.Second)
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}
	return startOfDayUTC(cutoff).Format(time.RFC3339)
}

// newerSameDate is the last whole second of the cutoff's own calendar date:
// strictly newer than the cutoff, still sharing its date. It is the pass side
// of the same instrument -- a "fix" that compared only the date prefix, or one
// that excluded the boundary date wholesale, would drop this row and still
// satisfy olderSameDate.
func newerSameDate(t *testing.T, days int) string {
	t.Helper()
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	if timeOfDayUTC(cutoff) > 24*time.Hour-2*time.Second {
		// Mirror of the guard above, at the other end of the day: sleeping
		// past midnight leaves the whole day's room ahead of the new cutoff.
		t.Logf("cutoff %s is within 2s of the end of its UTC date; waiting "+
			"past midnight so the fixture has room later on the cutoff's own "+
			"date. This sleep is load-bearing -- do not delete it.",
			cutoff.Format(time.RFC3339))
		time.Sleep(3 * time.Second)
		cutoff = time.Now().UTC().AddDate(0, 0, -days)
	}
	return startOfDayUTC(cutoff).Add(24*time.Hour - time.Second).Format(time.RFC3339)
}

// The regression: a success row on the cutoff's date but older than the cutoff
// must not be counted.
func TestGetUpdateStats_ExcludesSameDateRowOlderThanCutoff(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("older7", olderSameDate(t, 7), nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("older30", olderSameDate(t, 30), nil)))

	_, last7Days, last30Days, err := db.GetUpdateStats()
	require.NoError(t, err)
	require.Equal(t, 0, last7Days,
		"a row on the 7-day cutoff's date but older than the cutoff is outside the window")
	require.Equal(t, 1, last30Days,
		"only older7 is inside 30 days; older30 is on the 30-day cutoff's date but older than it")
}

// The pass side of the same instrument, green before and after the fix: a row
// on the cutoff's date but newer than the cutoff IS counted.
func TestGetUpdateStats_CountsSameDateRowNewerThanCutoff(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.InsertUpdateHistory(newEntry("newer7", newerSameDate(t, 7), nil)))
	require.NoError(t, db.InsertUpdateHistory(newEntry("newer30", newerSameDate(t, 30), nil)))

	_, last7Days, last30Days, err := db.GetUpdateStats()
	require.NoError(t, err)
	require.Equal(t, 1, last7Days, "newer7 is inside the 7-day window")
	require.Equal(t, 2, last30Days, "both rows are inside the 30-day window")
}

// The blunt control, also green on both sides: without it a fix that simply
// counted nothing would satisfy the regression above.
func TestGetUpdateStats_CountsOnlyRowsInsideEachWindow(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	for id, days := range map[string]int{"in7": 6, "out7": 8, "in30": 29, "out30": 31} {
		started := now.AddDate(0, 0, -days).Format(time.RFC3339)
		require.NoError(t, db.InsertUpdateHistory(newEntry(id, started, nil)))
	}

	_, last7Days, last30Days, err := db.GetUpdateStats()
	require.NoError(t, err)
	require.Equal(t, 1, last7Days, "only the 6-day-old row is inside 7 days")
	require.Equal(t, 3, last30Days, "every row but the 31-day-old one is inside 30 days")
}
