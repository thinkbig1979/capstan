package services

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// z91e19SharedProjectDB holds two stacks whose compose project is "shared"
// (as the scanner stores alpha/ and beta/ whose files both say
// `name: shared`) and one, "solo", that is unique.
func z91e19SharedProjectDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	for _, s := range []struct{ id, project, dir string }{
		{"s-alpha", "shared", "/srv/alpha"},
		{"s-beta", "shared", "/srv/beta"},
		{"s-solo", "solo", "/srv/solo"},
	} {
		require.NoError(t, db.UpsertDirectory(models.Directory{Path: s.dir, Name: s.id, ScannedAt: time.Now()}))
		require.NoError(t, db.UpsertStack(models.Stack{ID: s.id, Directory: s.dir, ComposeFile: "compose.yaml", ProjectName: s.project, Status: "unknown"}))
	}
	return db
}

// TestResolveUpdateStrategy_SharedProjectNameIsRefused is agent-os-z91e.19
// (D25) for the update apply paths: a container labelled with a project two
// stacks share must not be updated through either stack's compose file.
// Before the fix the lookup answered the first row, so this was
// updateViaCompose against s-alpha.
func TestResolveUpdateStrategy_SharedProjectNameIsRefused(t *testing.T) {
	db := z91e19SharedProjectDB(t)

	strategy, stack, err := resolveUpdateStrategy(db, "shared", "web")
	assert.Equal(t, updateRefused, strategy)
	assert.Nil(t, stack)
	require.ErrorIs(t, err, errdefs.ErrAmbiguous)
	assert.Contains(t, err.Error(), "s-alpha (/srv/alpha)")
	assert.Contains(t, err.Error(), "s-beta (/srv/beta)")

	// Other side, same database: a unique name still goes through compose.
	strategy, stack, err = resolveUpdateStrategy(db, "solo", "web")
	require.NoError(t, err)
	assert.Equal(t, updateViaCompose, strategy)
	assert.Equal(t, "s-solo", stack.ID)
}

// TestStackEventFor_SharedProjectNameIsUnassociatedAtWarn: an event for a
// shared name belongs to neither stack, and the database is healthy, so it
// must not use the read-fault ERROR line that sends an operator looking at the
// database.
func TestStackEventFor_SharedProjectNameIsUnassociatedAtWarn(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	buf := u2CaptureLogs(t)
	s := &MonitorService{db: z91e19SharedProjectDB(t)}

	ev, ok := s.stackEventFor("start", u2Ctr, "shared", ts)
	log := buf.String()

	require.True(t, ok)
	assert.Empty(t, ev.StackID, "an event for a shared project name must not be attributed to either stack")
	assert.False(t, strings.Contains(log, "level=ERROR"), "a shared name is not a database fault, got:\n%s", log)
	assert.Contains(t, log, "level=WARN")
	assert.Contains(t, log, "shared by more than one stack")

	// Other side: a unique name is attributed.
	ev, ok = s.stackEventFor("start", u2Ctr, "solo", ts)
	require.True(t, ok)
	assert.Equal(t, "s-solo", ev.StackID)
}
