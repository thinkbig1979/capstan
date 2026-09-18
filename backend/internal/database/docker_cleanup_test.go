package database

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestMigration17_CreatesCleanupRunsTableAndPreservesData pins migration 17
// (agent-os-fn7x): docker_cleanup_runs is a NEW table nobody seeded, so the
// pre-v16 hand-built-schema recipe in backup_runs_verify_migration_test.go
// gives it nothing to preserve. It checkpoints at v16 with
// applyMigrationsThrough instead, seeds a row into a table that already
// exists there, and only then runs the rest of the migrations.
//
// The arm that makes this discriminating is the pre-migration one: without
// asserting the table is ABSENT at v16, every post-migration assertion below
// would hold just as well against a tree where some other migration -- or the
// test itself -- had created the table, so the test would pass with migration
// 17 deleted.
func TestMigration17_CreatesCleanupRunsTableAndPreservesData(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	applyMigrationsThrough(t, db, 16)

	// Precondition, both halves. Nothing has created docker_cleanup_runs at
	// schema version 16 ...
	var tablesAtV16 int
	require.NoError(t, db.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'docker_cleanup_runs'`,
	).Scan(&tablesAtV16))
	require.Equal(t, 0, tablesAtV16, "docker_cleanup_runs must not exist before migration 17")

	// ... and SQLite agrees, so the assertion above is reading the schema it
	// claims to and not an empty sqlite_master.
	_, err = db.db.Exec(`INSERT INTO docker_cleanup_runs (id, trigger, status, started_at, min_age_hours) VALUES ('pre', 'manual', 'success', '2026-01-01T00:00:00Z', 24)`)
	require.Error(t, err, "an INSERT must fail before migration 17 creates the table")

	// A row in a table that DOES exist at v16, to prove migration 17 adds
	// rather than rebuilds.
	_, err = db.db.Exec(`INSERT INTO backup_runs (id, kind, trigger, status, started_at, finished_at, stacks_total, stacks_ok, stacks_failed)
	                      VALUES ('run-legacy', 'backup', 'manual', 'success', '2026-01-01T00:00:00Z', '2026-01-01T00:05:00Z', 2, 2, 0)`)
	require.NoError(t, err)

	require.NoError(t, RunMigrations(db))

	// Migration 17 is the LAST element of the slice, not merely present in it:
	// RunMigrations reads migrations[len(migrations)-1].Version as the version
	// this binary understands, so an entry spliced in mid-slice would leave the
	// forward-version guard comparing against 16 while 17 applies.
	assert.Equal(t, 17, migrations[len(migrations)-1].Version, "migration 17 must be appended at the end of the migrations slice")

	var stamped int
	require.NoError(t, db.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&stamped))
	assert.Equal(t, 17, stamped)

	// The v16 row survived.
	var legacyStatus, legacyStartedAt string
	require.NoError(t, db.db.QueryRow(`SELECT status, started_at FROM backup_runs WHERE id = 'run-legacy'`).Scan(&legacyStatus, &legacyStartedAt))
	assert.Equal(t, "success", legacyStatus)
	assert.Equal(t, "2026-01-01T00:00:00Z", legacyStartedAt)

	// The table and its index now exist.
	var indexes int
	require.NoError(t, db.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_docker_cleanup_runs_started_at'`,
	).Scan(&indexes))
	assert.Equal(t, 1, indexes, "migration 17 must create idx_docker_cleanup_runs_started_at")

	// The INSERT that failed above now succeeds -- same statement, so the only
	// thing that changed is the migration.
	_, err = db.db.Exec(`INSERT INTO docker_cleanup_runs (id, trigger, status, started_at, min_age_hours) VALUES ('pre', 'manual', 'success', '2026-01-01T00:00:00Z', 24)`)
	require.NoError(t, err, "the same INSERT must succeed after migration 17")

	// The three counters default to 0 rather than NULL, which is what lets a
	// row written before a run finishes still read as numbers.
	var imagesDeleted, bytesReclaimed, cacheBytes int64
	require.NoError(t, db.db.QueryRow(
		`SELECT images_deleted, bytes_reclaimed, cache_bytes_reclaimed FROM docker_cleanup_runs WHERE id = 'pre'`,
	).Scan(&imagesDeleted, &bytesReclaimed, &cacheBytes))
	assert.Equal(t, int64(0), imagesDeleted)
	assert.Equal(t, int64(0), bytesReclaimed)
	assert.Equal(t, int64(0), cacheBytes)

	// min_age_hours is NOT NULL: a run whose floor was not recorded is a
	// history entry nobody can interpret, which is the whole reason the column
	// is stored per row.
	_, err = db.db.Exec(`INSERT INTO docker_cleanup_runs (id, trigger, status, started_at, min_age_hours) VALUES ('no-floor', 'manual', 'success', '2026-01-01T00:00:00Z', NULL)`)
	require.Error(t, err, "min_age_hours must be NOT NULL")

	// finished_at, by contrast, is nullable -- a failed run may never finish.
	_, err = db.db.Exec(`INSERT INTO docker_cleanup_runs (id, trigger, status, started_at, finished_at, min_age_hours) VALUES ('unfinished', 'scheduled', 'failed', '2026-01-01T00:00:00Z', NULL, 24)`)
	require.NoError(t, err, "finished_at must stay nullable")
}

// TestDockerCleanupRunRecorded covers the database layer either side of the
// wire: a run written by CreateDockerCleanupRun comes back out of
// GetDockerCleanupRuns with every field intact, newest first.
func TestDockerCleanupRunRecorded(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	finished := "2026-03-01T10:05:00Z"
	newest := &models.DockerCleanupRun{
		ID:                  "run-newest",
		Trigger:             "scheduled",
		Status:              "success",
		StartedAt:           "2026-03-01T10:00:00Z",
		FinishedAt:          &finished,
		ImagesDeleted:       7,
		BytesReclaimed:      1234567890,
		CacheBytesReclaimed: 987654321,
		MinAgeHours:         168,
		ErrorMessage:        "",
	}
	oldest := &models.DockerCleanupRun{
		ID:           "run-oldest",
		Trigger:      "manual",
		Status:       "failed",
		StartedAt:    "2026-02-01T09:00:00Z",
		MinAgeHours:  24,
		ErrorMessage: "docker daemon unreachable",
	}

	// Inserted oldest-last so the ordering assertion below cannot be satisfied
	// by insertion order.
	require.NoError(t, db.CreateDockerCleanupRun(newest))
	require.NoError(t, db.CreateDockerCleanupRun(oldest))

	runs, err := db.GetDockerCleanupRuns(10)
	require.NoError(t, err)
	require.Len(t, runs, 2, "both runs must be listed")

	// Every column, so a wrong bind order or a shifted Scan shows up as a
	// swapped value rather than passing silently -- images_deleted,
	// bytes_reclaimed, cache_bytes_reclaimed and min_age_hours are four
	// adjacent integers and all four hold distinct values here for that reason.
	assert.Equal(t, *newest, runs[0], "newest run must round-trip verbatim and sort first")
	require.NotNil(t, runs[0].FinishedAt)
	assert.Equal(t, finished, *runs[0].FinishedAt)

	assert.Equal(t, *oldest, runs[1])
	assert.Nil(t, runs[1].FinishedAt, "a run that never finished must read back as nil, not as the zero time")

	// The list is not trivially "everything": the limit is honoured, and what
	// survives it is the newest row.
	page, err := db.GetDockerCleanupRuns(1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	assert.Equal(t, "run-newest", page[0].ID)

	// The id is the primary key, so a service that retried a write cannot
	// silently double-count a run. This is the arm that makes the Len(2) above
	// meaningful: without it, "2 rows" would also be consistent with a table
	// that accepts any number of copies of the same run.
	dup := *newest
	require.Error(t, db.CreateDockerCleanupRun(&dup), "a duplicate id must be rejected")

	after, err := db.GetDockerCleanupRuns(10)
	require.NoError(t, err)
	assert.Len(t, after, 2, "the rejected duplicate must not have been stored")

	// A row whose error_message is NULL rather than '' -- reachable from a
	// hand-written row or a future writer that omits the column -- lists
	// without failing the scan.
	_, err = db.db.Exec(`INSERT INTO docker_cleanup_runs (id, trigger, status, started_at, min_age_hours, error_message) VALUES ('run-null-error', 'manual', 'success', '2026-04-01T00:00:00Z', 24, NULL)`)
	require.NoError(t, err)
	withNull, err := db.GetDockerCleanupRuns(10)
	require.NoError(t, err, "a NULL error_message must not fail the scan")
	require.Len(t, withNull, 3)
	assert.Equal(t, "run-null-error", withNull[0].ID)
	assert.Equal(t, "", withNull[0].ErrorMessage)
}
