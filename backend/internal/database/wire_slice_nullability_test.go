package database

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestWireNullability_StackContainersFromDB measures what the four stack read
// functions leave in Stack.Containers. There is no containers column, so the
// scan never touches the field; each reader therefore opens the struct with an
// empty slice, and the field marshals to [] rather than null (agent-os-e5pr).
//
// That matters because both /stacks handlers reach the wire with exactly this
// struct whenever the live-status snapshot fails: handlers/stacks.go logs the
// GetStackStatuses error and falls through to c.JSON WITHOUT calling
// applyLiveStatus. A Docker outage used to send `"containers":null`.
//
// The last arm nils the field and marshals the same struct, so the assertion is
// shown to be capable of reporting null and is not simply reporting [] for
// every input.
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

	requireEmptyContainers := func(t *testing.T, reader string, stack models.Stack) {
		t.Helper()
		// assert, not require: every reader reports, not only the first.
		assert.NotNil(t, stack.Containers, "%s left Containers nil", reader)
		raw, err := json.Marshal(stack)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"containers":[]`, reader)
	}

	listed, err := db.ListStacks()
	require.NoError(t, err)
	require.Len(t, listed, 1)
	requireEmptyContainers(t, "ListStacks", listed[0])

	byDir, err := db.ListStacksByDirectory("/srv/s1")
	require.NoError(t, err)
	require.Len(t, byDir, 1)
	requireEmptyContainers(t, "ListStacksByDirectory", byDir[0])

	byName, err := db.GetStackByProjectName("s1")
	require.NoError(t, err)
	requireEmptyContainers(t, "GetStackByProjectName", *byName)

	got, err := db.GetStack("s1")
	require.NoError(t, err)
	requireEmptyContainers(t, "GetStack", *got)

	// The instrument reports null when the field is nil, so the [] above is a
	// property of the read path and not of this assertion.
	got.Containers = nil
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"containers":null`)
}
