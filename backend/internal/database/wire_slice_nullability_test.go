package database

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestWireNullability_StackContainersFromDB measures what ListStacks and
// GetStack leave in Stack.Containers. There is no containers column and the
// scan never touches the field, so it keeps its nil zero value and marshals to
// JSON null.
//
// That matters because both /stacks handlers reach the wire with exactly this
// struct whenever the live-status snapshot fails: handlers/stacks.go logs the
// GetStackStatuses error and falls through to c.JSON WITHOUT calling
// applyLiveStatus, which is the only thing that would have substituted
// []models.Container{}. A Docker outage therefore sends `"containers":null`.
//
// The second arm assigns an empty slice and marshals the same struct, so the
// assertion is shown to be capable of reporting [] and is not simply reporting
// null for every input.
func TestWireNullability_StackContainersFromDB(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// stacks.directory carries a foreign key onto directories.path.
	require.NoError(t, db.UpsertDirectory(models.Directory{Path: "/srv/s1", Name: "s1"}))
	require.NoError(t, db.UpsertStack(models.Stack{
		ID:          "s1",
		Directory:   "/srv/s1",
		ComposeFile: "docker-compose.yml",
		ProjectName: "s1",
		Status:      "running",
	}))

	listed, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Nil(t, listed[0].Containers, "ListStacks never scans a containers column")

	raw, err := json.Marshal(listed[0])
	require.NoError(t, err)
	require.Contains(t, string(raw), `"containers":null`)

	got, err := db.GetStack("s1")
	require.NoError(t, err)
	require.Nil(t, got.Containers, "GetStack never scans a containers column")

	raw, err = json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"containers":null`)

	// The instrument reports [] when the field is populated, so the two nulls
	// above are a property of the read path and not of this assertion.
	got.Containers = []models.Container{}
	raw, err = json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"containers":[]`)
}
