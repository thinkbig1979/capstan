package database

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// SetAutoUpdatePolicyEnabled is the operator's writer (agent-os-nlll). It
// owns the enabled column; the failure count and the pause belong to the
// auto-update pass and change here only on a disable or an explicit resume.
func TestSetAutoUpdatePolicyEnabled_S7_LeavesCountersAlone(t *testing.T) {
	const created = "2020-01-01T00:00:00Z"
	const now = "2020-01-02T00:00:00Z"

	seed := func(t *testing.T, failures int, paused bool) *DB {
		t.Helper()
		db, err := NewWithMigrations(":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		require.NoError(t, db.UpsertAutoUpdatePolicy(&models.AutoUpdatePolicy{
			ID: "row-1", TargetType: "container", TargetID: "c1", Enabled: true,
			ConsecutiveFailures: failures, Paused: paused, CreatedAt: created, UpdatedAt: created,
		}))
		return db
	}
	stored := func(t *testing.T, db *DB) models.AutoUpdatePolicy {
		t.Helper()
		p, err := db.GetAutoUpdatePolicy("container", "c1")
		require.NoError(t, err)
		return *p
	}

	t.Run("plain enable keeps the counters and the pause", func(t *testing.T) {
		db := seed(t, 3, true)
		got, err := db.SetAutoUpdatePolicyEnabled("unused-id", "container", "c1", true, "", now)
		require.NoError(t, err)
		want := models.AutoUpdatePolicy{ID: "row-1", TargetType: "container", TargetID: "c1", Enabled: true,
			ConsecutiveFailures: 3, Paused: true, CreatedAt: created, UpdatedAt: now}
		require.Equal(t, want, *got)
		require.Equal(t, want, stored(t, db))
	})

	t.Run("resume of another row id leaves this pause standing", func(t *testing.T) {
		db := seed(t, 3, true)
		_, err := db.SetAutoUpdatePolicyEnabled("unused-id", "container", "c1", true, "some-other-row", now)
		require.NoError(t, err)
		got := stored(t, db)
		require.True(t, got.Paused)
		require.Equal(t, 3, got.ConsecutiveFailures)
	})

	t.Run("resume of this row clears the pause and the count", func(t *testing.T) {
		db := seed(t, 3, true)
		_, err := db.SetAutoUpdatePolicyEnabled("unused-id", "container", "c1", true, "row-1", now)
		require.NoError(t, err)
		got := stored(t, db)
		require.False(t, got.Paused)
		require.Equal(t, 0, got.ConsecutiveFailures)
		require.True(t, got.Enabled)
	})

	t.Run("disable clears the pause and the count", func(t *testing.T) {
		db := seed(t, 2, false)
		_, err := db.SetAutoUpdatePolicyEnabled("unused-id", "container", "c1", false, "", now)
		require.NoError(t, err)
		got := stored(t, db)
		require.False(t, got.Enabled)
		require.False(t, got.Paused)
		require.Equal(t, 0, got.ConsecutiveFailures)
		require.Equal(t, "row-1", got.ID)
	})

	t.Run("absent row is created with the new id", func(t *testing.T) {
		db, err := NewWithMigrations(":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		got, err := db.SetAutoUpdatePolicyEnabled("new-id", "stack", "s1", true, "", now)
		require.NoError(t, err)
		want := models.AutoUpdatePolicy{ID: "new-id", TargetType: "stack", TargetID: "s1", Enabled: true,
			ConsecutiveFailures: 0, Paused: false, CreatedAt: now, UpdatedAt: now}
		require.Equal(t, want, *got)
	})
}
