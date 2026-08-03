package database

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForeignKeysEnforced_PoolWide proves that foreign_keys=ON and
// busy_timeout apply to every pooled connection, not just whichever
// connection happened to run the historical one-shot db.Exec("PRAGMA ...").
// Regression test for agent-os-94t: PRAGMA foreign_keys=ON / busy_timeout=5000
// were previously applied only to a single connection out of the pool of 25,
// so most connections silently allowed FK-violating inserts.
//
// The test uses a start barrier so all N goroutines hold a distinct
// connection (via an open transaction) at the same instant, which is
// verified via db.Stats() before checking the pragma on each one.
func TestForeignKeysEnforced_PoolWide(t *testing.T) {
	dir := t.TempDir()
	db, err := NewWithMigrations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	const n = 8
	ready := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup

	fkResults := make([]int, n)
	fkErrs := make([]error, n)
	insertErrs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tx, err := db.db.Begin()
			require.NoError(t, err)
			// No-op once Commit succeeds; safety net for early returns only.
			defer func() { _ = tx.Rollback() }()

			ready <- struct{}{}
			<-release

			var fkOn int
			fkErrs[i] = tx.QueryRow("PRAGMA foreign_keys").Scan(&fkOn)
			fkResults[i] = fkOn

			// Also directly prove enforcement on this exact connection: a
			// session referencing a non-existent user_id must be rejected.
			// (action_log intentionally has no FKs as of migration v9 — see
			// agent-os-z4v — so sessions.user_id -> users.id is used here
			// instead as the enforcement probe.)
			//nolint:gosec // i ranges over const n = 8 (see above); nowhere near rune overflow
			_, insertErrs[i] = tx.Exec(
				`INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
				"fkprobe-"+string(rune('a'+i)), "does-not-exist-user",
			)
		}(i)
	}

	// Wait for all n goroutines to have opened a transaction (and therefore a
	// connection) before releasing them, so db.Stats() below reflects true
	// concurrency rather than serialized reuse of one connection.
	for i := 0; i < n; i++ {
		<-ready
	}
	stats := db.db.Stats()
	require.GreaterOrEqualf(t, stats.OpenConnections, 2,
		"expected multiple concurrently-open connections, got %d (InUse=%d) — pool concurrency not actually exercised",
		stats.OpenConnections, stats.InUse)

	close(release)
	wg.Wait()

	for i := 0; i < n; i++ {
		require.NoError(t, fkErrs[i], "connection %d: PRAGMA foreign_keys query failed", i)
		assert.Equal(t, 1, fkResults[i], "connection %d: foreign_keys pragma was not ON for this pooled connection", i)

		require.Error(t, insertErrs[i], "connection %d: FK-violating insert should have failed", i)
		assert.Contains(t, insertErrs[i].Error(), "FOREIGN KEY constraint failed")
	}
}

// TestMemoryDSN_PragmasApply proves the ":memory:" DSN form (with the
// _pragma query params appended) still opens correctly and applies the
// pragmas, matching the file-backed behavior.
func TestMemoryDSN_PragmasApply(t *testing.T) {
	db, err := NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	var fkOn int
	require.NoError(t, db.db.QueryRow("PRAGMA foreign_keys").Scan(&fkOn))
	assert.Equal(t, 1, fkOn)

	var busyTimeout int
	require.NoError(t, db.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout))
	assert.Equal(t, 5000, busyTimeout)
}

// TestFileBackedDSN_UsableAsPlainPath proves the file-backed DSN (path +
// "?_pragma=...") still resolves to the expected on-disk file rather than
// being misinterpreted (e.g. as a URI or a literal filename containing '?').
func TestFileBackedDSN_UsableAsPlainPath(t *testing.T) {
	dir := t.TempDir()
	db, err := NewWithMigrations(dir)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	expected := filepath.Join(dir, "capstan.db")
	_, statErr := os.Stat(expected)
	require.NoError(t, statErr, "expected sqlite file at %s", expected)
}
