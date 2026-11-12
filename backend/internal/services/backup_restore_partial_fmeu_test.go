package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// --- agent-os-fmeu: restoring a partial snapshot (restic backup exit 3) with
// --delete removed from the live stack every file the backup could not read.
// The run item now records those paths, and the restore keeps --delete off
// them, or drops --delete when it cannot name them all. ---

const fmeuSnap = "abc12345"

// fmeuSetup seeds a stack with a real directory and, when item is non-nil,
// the run item that recorded the snapshot.
func fmeuSetup(t *testing.T, dataDir string, item *models.BackupRunItem) (*BackupService, *database.DB, *fakeRunner, *fakeDocker, string) {
	t.Helper()
	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	stack := filepath.Join(t.TempDir(), "myapp")
	require.NoError(t, os.MkdirAll(stack, 0o755))
	seedStackAt(t, db, "myapp", stack)
	if item != nil {
		require.NoError(t, db.CreateBackupRun(&models.BackupRun{ID: "r1", Kind: "backup", Trigger: "manual", Status: "partial", StartedAt: "2026-11-01T00:00:00Z"}))
		item.ID, item.RunID, item.StackID, item.Status, item.SnapshotID = "i1", "r1", "myapp", "success", fmeuSnap
		for i, p := range item.UnreadablePaths {
			item.UnreadablePaths[i] = strings.ReplaceAll(p, "$STACK", stack)
		}
		require.NoError(t, db.AddBackupRunItem(item))
	}
	docker := &fakeDocker{statusStr: "running"}
	runner := &fakeRunner{outputData: snapshotJSONAt(fmeuSnap, "myapp", stack)}
	return buildSvc(t, db, docker, runner, runner), db, runner, docker, stack
}

// snapshotJSONAt is snapshotJSON with the snapshot's stored path set and any
// extra tags.
func snapshotJSONAt(id, stackID, path string, extraTags ...string) []byte {
	tags, _ := json.Marshal(append([]string{stackID}, extraTags...)) //nolint:errcheck // strings always marshal
	return []byte(fmt.Sprintf(`[{"id":%q,"short_id":%q,"time":"2026-11-01T00:00:00Z","hostname":"h","tags":%s,"paths":[%q]}]`,
		id, id, tags, path))
}

// fmeuRestore runs the restore and returns its error and every stream line.
func fmeuRestore(t *testing.T, svc *BackupService) (string, error) {
	t.Helper()
	out := make(chan StreamLine, 256)
	var lines []string
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for l := range out {
			lines = append(lines, l.Line)
		}
	}()
	err := svc.RunRestore(context.Background(), "myapp", fmeuSnap, out)
	close(out)
	wg.Wait()
	return strings.Join(lines, "\n"), err
}

func restoreCall(t *testing.T, r *fakeRunner) []string {
	t.Helper()
	for _, c := range r.calls {
		if len(c.Args) > 0 && c.Args[0] == "restore" {
			return c.Args
		}
	}
	t.Fatalf("restic restore never ran: %v", r.calls)
	return nil
}

func intp(n int) *int { return &n }

// The bead's case: every unreadable path is recorded, so the restore keeps
// --delete and excludes them, and says why.
func TestFmeu_PartialSnapshotRestoreKeepsUnreadablePaths(t *testing.T) {
	t.Parallel()
	svc, _, runner, _, _ := fmeuSetup(t, t.TempDir(), &models.BackupRunItem{
		UnreadableCount: intp(2), UnreadablePaths: []string{"$STACK/data/db", "$STACK/locked.txt"}})

	stream, err := fmeuRestore(t, svc)
	require.NoError(t, err)
	args := restoreCall(t, runner)
	assert.Contains(t, args, "--delete")
	assert.True(t, argPairContains(args, "--exclude", "/data/db"), "%v", args)
	assert.True(t, argPairContains(args, "--exclude", "/locked.txt"), "%v", args)
	assert.Contains(t, stream, "partial snapshot")
}

// Restic named more paths than the item kept: the restore cannot exclude
// them all, so it runs without --delete.
func TestFmeu_UnlistedUnreadablePathsDropDelete(t *testing.T) {
	t.Parallel()
	svc, _, runner, _, _ := fmeuSetup(t, t.TempDir(), &models.BackupRunItem{
		UnreadableCount: intp(3), UnreadablePaths: []string{"$STACK/a", "$STACK/b"}})

	stream, err := fmeuRestore(t, svc)
	require.NoError(t, err)
	args := restoreCall(t, runner)
	assert.NotContains(t, args, "--delete", "%v", args)
	assert.Contains(t, stream, "without --delete")
}

// A path the item recorded outside the snapshot's source cannot be turned
// into an exclude, so the restore drops --delete rather than guess.
func TestFmeu_PathOutsideSourceDropsDelete(t *testing.T) {
	t.Parallel()
	svc, _, runner, _, _ := fmeuSetup(t, t.TempDir(), &models.BackupRunItem{
		UnreadableCount: intp(1), UnreadablePaths: []string{"/elsewhere/x"}})

	_, err := fmeuRestore(t, svc)
	require.NoError(t, err)
	assert.NotContains(t, restoreCall(t, runner), "--delete")
}

// Controls on the same instrument: a complete snapshot, and one no item
// recorded, restore with --delete and no exclude beyond the protected scan.
func TestFmeu_CompleteSnapshotRestoreDeletes(t *testing.T) {
	t.Parallel()
	for name, item := range map[string]*models.BackupRunItem{
		"complete item": {},
		"no item":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc, _, runner, _, _ := fmeuSetup(t, t.TempDir(), item)
			stream, err := fmeuRestore(t, svc)
			require.NoError(t, err)
			args := restoreCall(t, runner)
			assert.Contains(t, args, "--delete")
			assert.NotContains(t, args, "--exclude")
			assert.NotContains(t, stream, "partial snapshot")
		})
	}
}

// Fail closed: when the run item cannot be read, nobody can say whether the
// snapshot is partial, so the restore is refused before the stack stops.
func TestFmeu_UnreadableItemRefusesBeforeStopping(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	svc, db, runner, docker, _ := fmeuSetup(t, dataDir, nil)
	raw, err := sql.Open("sqlite", dataDir+"/capstan.db")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.Exec(`ALTER TABLE backup_run_items RENAME COLUMN unreadable_paths TO unreadable_paths_gone`)
	require.NoError(t, err)
	_, armErr := db.GetRunItemForSnapshot("myapp", fmeuSnap)
	require.Error(t, armErr, "fixture is unarmed")

	_, err = fmeuRestore(t, svc)
	require.Error(t, err)
	assert.Equal(t, 0, docker.stopped(), "the stack must not be stopped for a refused restore")
	for _, c := range runner.calls {
		assert.NotEqual(t, "restore", c.Args[0])
	}
}

// The backup records every unreadable path up to the stored cap, while the
// note still lists only the first 20.
func TestFmeu_BackupRecordsUnreadablePaths(t *testing.T) {
	t.Parallel()
	errs := sufnFiles(25)
	r := sufnRun(t, &sufnRunner{exitCode: 3, errs: errs})

	require.NotNil(t, r.item.UnreadableCount)
	assert.Equal(t, 25, *r.item.UnreadableCount)
	require.Len(t, r.item.UnreadablePaths, 25)
	for i, e := range errs {
		assert.Equal(t, e.item, r.item.UnreadablePaths[i])
	}
	assert.Contains(t, r.item.ErrorMessage, "and 5 more", "the note keeps its 20-name cap")
}

// unreadableRestoreExcludes maps recorded paths to excludes relative to the
// snapshot's source, and reports false for any path it cannot place under it:
// a Rel result of "..", one starting with "../", the source itself, or a path
// Rel cannot relate at all (a relative input, which would otherwise come back
// unanchored).
func TestFmeu_UnreadableRestoreExcludes(t *testing.T) {
	t.Parallel()
	const src = "/old/app"
	ok := func(paths ...string) {
		t.Helper()
		_, fine := unreadableRestoreExcludes(src, paths)
		assert.False(t, fine, "%v must drop --delete", paths)
	}
	got, fine := unreadableRestoreExcludes(src, []string{"/old/app/data/db", "/old/app/a, b.txt"})
	require.True(t, fine)
	assert.Equal(t, []string{"data/db", "a, b.txt"}, got)

	ok("/old")              // Rel == ".."
	ok("/old/other/x")      // Rel starts with "../"
	ok("/old/app/../x")     // cleans to "../x"
	ok("/old/app")          // the source itself: "."
	ok("data/db")           // relative: Rel errors, never an unanchored exclude
	ok("/old/app/ok", "/x") // one bad path spoils the set
}

// Review MUST-1: the snapshot is marked partial in the repository, and its run
// item is gone (history pruned, or a DR to a new database). Nothing names its
// unreadable paths, so the restore runs without --delete.
func TestFmeu_TaggedPartialWithoutItemDropsDelete(t *testing.T) {
	t.Parallel()
	svc, _, runner, _, stack := fmeuSetup(t, t.TempDir(), nil)
	runner.outputData = snapshotJSONAt(fmeuSnap, "myapp", stack, PartialSnapshotTag)

	stream, err := fmeuRestore(t, svc)
	require.NoError(t, err)
	assert.NotContains(t, restoreCall(t, runner), "--delete")
	assert.Contains(t, stream, "no longer on record")
}

// Tagged, with the item and every path on record: the paths are excluded and
// --delete stays, as for an untagged partial item.
func TestFmeu_TaggedPartialWithItemExcludesPaths(t *testing.T) {
	t.Parallel()
	svc, _, runner, _, stack := fmeuSetup(t, t.TempDir(), &models.BackupRunItem{
		UnreadableCount: intp(1), UnreadablePaths: []string{"$STACK/data/db"}})
	runner.outputData = snapshotJSONAt(fmeuSnap, "myapp", stack, PartialSnapshotTag)

	_, err := fmeuRestore(t, svc)
	require.NoError(t, err)
	args := restoreCall(t, runner)
	assert.Contains(t, args, "--delete")
	assert.True(t, argPairContains(args, "--exclude", "/data/db"), "%v", args)
}

// Tagged, but its item records no count (an inconsistent pair): the tag wins
// and --delete is dropped.
func TestFmeu_TaggedSnapshotWithCompleteItemDropsDelete(t *testing.T) {
	t.Parallel()
	svc, _, runner, _, stack := fmeuSetup(t, t.TempDir(), &models.BackupRunItem{})
	runner.outputData = snapshotJSONAt(fmeuSnap, "myapp", stack, PartialSnapshotTag)

	_, err := fmeuRestore(t, svc)
	require.NoError(t, err)
	assert.NotContains(t, restoreCall(t, runner), "--delete")
}
