package database

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Five rows one second apart, newest first under GetUpdateHistory's
// `ORDER BY started_at DESC`. Five (not four) is deliberate: it makes
// {Page: 3, Limit: 2} a SHORT last page of exactly one row, which an
// over-rejecting guard turns empty and a correct guard leaves alone.
var seededUpdateIDs = []string{"upd-e", "upd-d", "upd-c", "upd-b", "upd-a"}

func seedUpdateHistoryPage(t *testing.T, db *DB) {
	t.Helper()
	rows := []struct{ id, startedAt string }{
		{"upd-a", "2026-03-01T10:00:00Z"},
		{"upd-b", "2026-03-01T10:00:01Z"},
		{"upd-c", "2026-03-01T10:00:02Z"},
		{"upd-d", "2026-03-01T10:00:03Z"},
		{"upd-e", "2026-03-01T10:00:04Z"},
	}
	for _, r := range rows {
		require.NoError(t, db.InsertUpdateHistory(&models.UpdateHistoryEntry{
			ID:            r.id,
			ContainerID:   "c1",
			ContainerName: "web",
			Image:         "nginx:latest",
			Status:        "success",
			Trigger:       "manual",
			StartedAt:     r.startedAt,
		}))
	}
}

func updateEntryIDs(entries []models.UpdateHistoryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// GetUpdateHistory computes its SQL OFFSET as (page-1)*limit on an int, and
// int wraps. When the product goes negative SQLite treats the OFFSET as absent
// — so the call comes back holding page ONE's rows while the caller believes
// it asked for a page far past the end. That is a wrong answer served as a
// correct one, which is worse than an error.
//
// This site has TWO routes into the wrap, unlike backup history's single one:
// getUpdateHistory in handlers/updates.go clamps limit with `v > 0` and sets no
// maximum, so a SMALL page overflows whenever limit is large enough.
//
// Three arms, each with a different job:
//   - the defect arm: an overflowing page must be EMPTY;
//   - the control arm: page one over the same seed is NON-EMPTY, so "empty"
//     above cannot merely mean the table is empty;
//   - the over-rejection arm: an in-range page landing on real data still
//     returns it, which is what separates a guard from a blanket rejection.
func TestGetUpdateHistory_OffsetOverflowDoesNotWrapToPageOne(t *testing.T) {
	db := newTestDB(t)
	seedUpdateHistoryPage(t, db)

	// Control arm: page one over the same seed is populated.
	first, firstTotal, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Page: 1, Limit: 50})
	require.NoError(t, err)
	require.Equal(t, seededUpdateIDs, updateEntryIDs(first), "page one is non-empty, so empty below means something")
	require.Equal(t, len(seededUpdateIDs), firstTotal)

	// Defect arm 1 — the huge-page route.
	got, total, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Page: math.MaxInt, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, updateEntryIDs(got), "a page past the end must be empty, never page one")
	assert.Equal(t, len(seededUpdateIDs), total, "total still describes the whole match set")

	// Defect arm 2 — the small-page/large-limit route, unique to this site.
	// page-1 is only 2, but MaxInt/limit is 1, so the product still wraps.
	small, smallTotal, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Page: 3, Limit: math.MaxInt})
	require.NoError(t, err)
	assert.Empty(t, updateEntryIDs(small), "a small page with a huge limit overflows too, and must be empty")
	assert.Equal(t, len(seededUpdateIDs), smallTotal, "total still describes the whole match set")

	// Defect arm 3 — the wrap-to-ZERO route. (1<<62+1-1)*4 wraps to exactly 0,
	// which is not negative, so a guard that multiplied first and then tested
	// the sign of the product would miss this and serve page one again. Only a
	// guard on the OPERANDS catches it.
	zero, zeroTotal, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Page: 1<<62 + 1, Limit: 4})
	require.NoError(t, err)
	assert.Empty(t, updateEntryIDs(zero), "a page whose offset wraps to exactly zero must be empty, never page one")
	assert.Equal(t, len(seededUpdateIDs), zeroTotal, "total still describes the whole match set")

	// Over-rejection arm — the real discriminator. Page 3 at limit 2 is the
	// short LAST page: one row, not zero. A guard that over-rejects empties
	// this; a correct one leaves it untouched.
	last, lastTotal, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Page: 3, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, []string{"upd-a"}, updateEntryIDs(last), "the last page is short, not empty")
	assert.Equal(t, len(seededUpdateIDs), lastTotal)

	// Regression control, NOT a discriminator: a merely large page (no
	// overflow) already returned empty before the guard existed, so it cannot
	// be seen failing first and a blanket large-page rejection predicts the
	// same output. It is here so that behaviour does not break.
	big, bigTotal, err := db.GetUpdateHistory(models.UpdateHistoryFilters{Page: 999999999, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, updateEntryIDs(big), "an in-range page past the end is empty too")
	assert.Equal(t, len(seededUpdateIDs), bigTotal)
}
