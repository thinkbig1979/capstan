package database

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-z91e.19: two stacks can share a compose project name (the scanner
// stores alpha/ and beta/ both as "shared" when their compose files say
// `name: shared`). The lookup used QueryRow, so the first row won silently and
// the second stack was never returned: a container labelled "shared" locked
// or updated alpha while beta stayed unlocked.
func TestGetStackByProjectName_SharedNameIsAmbiguous(t *testing.T) {
	db := newTestDB(t)
	for _, d := range []string{"/srv/alpha", "/srv/beta"} {
		require.NoError(t, db.UpsertDirectory(models.Directory{Path: d, Name: d, ScannedAt: time.Now()}))
	}
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-beta", Directory: "/srv/beta", ComposeFile: "compose.yaml", ProjectName: "shared", Status: "unknown"}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-alpha", Directory: "/srv/alpha", ComposeFile: "compose.yaml", ProjectName: "shared", Status: "unknown"}))
	require.NoError(t, db.UpsertStack(models.Stack{ID: "s-solo", Directory: "/srv/alpha", ComposeFile: "compose.solo.yaml", ProjectName: "solo", Status: "unknown"}))

	got, err := db.GetStackByProjectName("shared")
	require.ErrorIs(t, err, errdefs.ErrAmbiguous, "two stacks share the name, so no single stack may be returned (got %+v)", got)
	assert.Nil(t, got)
	assert.False(t, errors.Is(err, errdefs.ErrNotFound), "ambiguous is not absent")
	var amb *AmbiguousProjectNameError
	require.True(t, errors.As(err, &amb))
	require.Len(t, amb.Stacks, 2)
	assert.Equal(t, "s-alpha", amb.Stacks[0].ID)
	assert.Equal(t, "s-beta", amb.Stacks[1].ID)
	assert.Contains(t, err.Error(), "s-alpha (/srv/alpha)")
	assert.Contains(t, err.Error(), "s-beta (/srv/beta)")

	// A unique name still resolves, and an unknown one is still not-found.
	solo, err := db.GetStackByProjectName("solo")
	require.NoError(t, err)
	assert.Equal(t, "s-solo", solo.ID)
	assert.Equal(t, "compose.solo.yaml", solo.ComposeFile)
	_, err = db.GetStackByProjectName("nope")
	assert.ErrorIs(t, err, errdefs.ErrNotFound)
}
