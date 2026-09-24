package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestMigration18_CachedUpdatesGainStackContext pins migration 18
// (agent-os-zt0h). A row cached before it must read back as "lookup not known
// to have failed, no path recorded", which the UI words honestly, and a row
// written after it must round-trip all three new values.
//
// The pre-migration arm is what makes this discriminating: the columns must be
// ABSENT at v17, or the post-migration reads would pass with migration 18
// deleted.
func TestMigration18_CachedUpdatesGainStackContext(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 17)

	_, err = db.db.Exec(`SELECT stack_lookup_failed FROM cached_updates`)
	require.Error(t, err, "stack_lookup_failed must not exist before migration 18")

	_, err = db.db.Exec(`INSERT INTO cached_updates (id, container_id, container_name, image, image_ref, state,
	                      project_name, service_name, is_compose, local_digest, remote_digest, scanned_at)
	                      VALUES ('old', 'c-old', 'old', 'app:1', 'app:1', 'running', 'proj', 'web', TRUE,
	                      'sha256:a', 'sha256:b', '2026-09-01T00:00:00Z')`)
	require.NoError(t, err)

	require.NoError(t, RunMigrations(db))

	got, err := db.GetCachedUpdates()
	require.NoError(t, err)
	require.Len(t, got, 1, "the v17 row must survive migration 18")
	assert.Equal(t, "proj", got[0].ProjectName)
	assert.False(t, got[0].StackLookupFailed)
	assert.Empty(t, got[0].ComposeWorkingDir)
	assert.Empty(t, got[0].ComposeConfigFiles)

	written := models.CachedUpdate{
		ID: "new", ContainerID: "c-new", ContainerName: "new", Image: "app:1", ImageRef: "app:1",
		State: "running", ProjectName: "proj", ServiceName: "web", IsCompose: true,
		StackLookupFailed:  true,
		ComposeWorkingDir:  "/home/op/proj",
		ComposeConfigFiles: "/home/op/proj/a.yml,/home/op/proj/b.yml",
		LocalDigest:        "sha256:a", RemoteDigest: "sha256:b", ScannedAt: "2026-09-02T00:00:00Z",
	}
	require.NoError(t, db.SetCachedUpdates([]models.CachedUpdate{written}))
	got, err = db.GetCachedUpdates()
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, written, got[0])
}
