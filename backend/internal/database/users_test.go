package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestGetUserByUsername_CaseInsensitive pins migration 13's DB-layer fix
// (agent-os-tmo): a user created as "Alice" must be found by any casing of
// the same username, and the stored casing (not the casing the caller
// looked up with) must be what's returned.
func TestGetUserByUsername_CaseInsensitive(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.CreateUser(models.User{
		ID: "u-alice", Username: "Alice", Password: "hash",
	}))

	for _, lookup := range []string{"Alice", "alice", "ALICE", "aLiCe"} {
		user, err := db.GetUserByUsername(lookup)
		require.NoError(t, err, "lookup %q should find the user", lookup)
		assert.Equal(t, "u-alice", user.ID, "lookup %q returned the wrong user", lookup)
		assert.Equal(t, "Alice", user.Username, "stored casing must be preserved, not normalized to the lookup's casing")
	}
}

// TestCreateUser_RejectsCaseOnlyDuplicate pins the other half of migration
// 13: the NOCASE unique index must reject a second account whose username
// differs from an existing one only by case, at INSERT time, not just at
// lookup time.
func TestCreateUser_RejectsCaseOnlyDuplicate(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.CreateUser(models.User{
		ID: "u-1", Username: "Admin", Password: "hash",
	}))

	err := db.CreateUser(models.User{
		ID: "u-2", Username: "admin", Password: "hash2",
	})
	require.Error(t, err, "creating a case-only duplicate of an existing username must fail")
}
