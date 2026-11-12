package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// --- agent-os-95dk: retention after a partial snapshot keeps the stack's
// newest complete snapshot. restic's keep-* policy cannot tell partial from
// complete, so when the unreadable set grows every bucket fills with partial
// snapshots and the last complete one ages out. Partial snapshots are marked
// in the repository (PartialSnapshotTag), so the protection does not depend
// on run history that pruning deletes (review MUST-1). ---

const dk95Stack = "dk95-app"

type dk95Snap struct {
	id, original string
	at           time.Time
	tags         []string
}

// dk95Runner plays restic over an in-memory snapshot store. Each backup takes
// the next entry of unreadable (0 = complete) and writes a snapshot an hour
// after the last. `tag` rewrites a snapshot under a new id, as restic does.
// The policy it simulates keeps only the newest snapshot. A forget with ids
// removes exactly those.
type dk95Runner struct {
	mu         sync.Mutex
	unreadable []int
	store      []dk95Snap
	n          int
	forgets    [][]string
	dryRuns    int
	tags       int
	tagFail    bool
	dryRunJSON string // when set, the dry-run answers this instead
}

var dk95Epoch = time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)

func dk95ID(n int) string { return fmt.Sprintf("%08x", 0xa0000000+n) + strings.Repeat("0", 56) }

// dk95Tagged is the id dk95Runner's tag gives snapshot n.
func dk95Tagged(n int) string { return "f" + dk95ID(n)[1:] }

func (r *dk95Runner) Run(_ context.Context, _ string, args []string, _ []string, out chan<- StreamLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case args[0] == "backup" && slices.Contains(args, DatabaseBackupTag):
		out <- StreamLine{Type: "data", Line: `{"message_type":"summary","total_files_processed":1,"snapshot_id":"dddddddd44444444"}`}
	case args[0] == "backup":
		bad := 0
		if r.n < len(r.unreadable) {
			bad = r.unreadable[r.n]
		}
		r.n++
		id := dk95ID(r.n)
		r.store = append(r.store, dk95Snap{id: id, at: dk95Epoch.Add(time.Duration(r.n) * time.Hour), tags: []string{dk95Stack}})
		for i := range bad {
			item := fmt.Sprintf("/opt/stacks/%s/f%04d", dk95Stack, i)
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(`{"message_type":"error","error":{"message":"denied"},"during":"archival","item":%q}`, item)}
		}
		out <- StreamLine{Type: "data", Line: fmt.Sprintf(`{"message_type":"summary","total_files_processed":1,"snapshot_id":%q}`, id)}
		if bad > 0 {
			return fmt.Errorf("restic exited: %w", fakeExitError{code: resticExitSourceUnreadable})
		}
	case args[0] == "tag":
		r.tags++
		if r.tagFail {
			return fmt.Errorf("restic exited: %w", fakeExitError{code: 1})
		}
		old := args[len(args)-1]
		for i, s := range r.store {
			if s.id == old {
				r.store[i] = dk95Snap{id: "f" + old[1:], original: old, at: s.at, tags: append(slices.Clone(s.tags), args[2])}
			}
		}
	case args[0] == "forget" && slices.Contains(args, DatabaseBackupTag):
	case args[0] == "forget":
		r.forgets = append(r.forgets, slices.Clone(args))
		if i := slices.Index(args, "--"); i >= 0 {
			for _, id := range args[i+1:] {
				r.store = slices.DeleteFunc(r.store, func(s dk95Snap) bool { return s.id == id })
			}
			return nil
		}
		if len(r.store) > 1 {
			r.store = r.store[len(r.store)-1:]
		}
	}
	return nil
}

type dk95JSON struct {
	ID       string   `json:"id"`
	ShortID  string   `json:"short_id"`
	Time     string   `json:"time"`
	Tags     []string `json:"tags"`
	Original string   `json:"original,omitempty"`
}

func (s dk95Snap) json() dk95JSON {
	return dk95JSON{s.id, s.id[:8], s.at.Format(time.RFC3339Nano), s.tags, s.original}
}

func (r *dk95Runner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch args[0] {
	case "snapshots":
		all := []dk95JSON{}
		for _, s := range r.store {
			all = append(all, s.json())
		}
		return json.Marshal(all)
	case "forget":
		r.dryRuns++
		if r.dryRunJSON != "" {
			return []byte(r.dryRunJSON), nil
		}
		group := struct {
			Keep   []dk95JSON `json:"keep"`
			Remove []dk95JSON `json:"remove"`
		}{}
		for i, s := range r.store {
			if i == len(r.store)-1 {
				group.Keep = append(group.Keep, s.json())
			} else {
				group.Remove = append(group.Remove, s.json())
			}
		}
		return json.Marshal([]any{group})
	}
	return []byte("[]"), nil
}

func (r *dk95Runner) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.store, func(s dk95Snap) bool { return s.id == id })
}

// dk95Svc builds a service over a file-backed database (so a second
// connection can fault or age it) with the dk95 stack seeded.
func dk95Svc(t *testing.T, r *dk95Runner) (*BackupService, *database.DB, *sql.DB) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	raw, err := sql.Open("sqlite", dataDir+"/capstan.db")
	require.NoError(t, err)
	t.Cleanup(func() { _ = raw.Close() })
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, r, r)
	seedStack(t, db, dk95Stack, "hot")
	return svc, db, raw
}

// dk95Run runs one backup and returns its stored run and item.
func dk95Run(t *testing.T, svc *BackupService, db *database.DB) (*models.BackupRun, models.BackupRunItem) {
	t.Helper()
	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)
	stored, items := readRun(t, db, run.ID)
	require.Len(t, items, 1)
	return stored, items[0]
}

// The bead's case: one complete night, then 1 unreadable file, then 500. The
// complete snapshot survives both partial nights' retention.
func TestBackup95dk_GrowingUnreadableSetKeepsNewestCompleteSnapshot(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 1, 500}}
	svc, db, _ := dk95Svc(t, r)
	_, i1 := dk95Run(t, svc, db)
	_, i2 := dk95Run(t, svc, db)
	_, i3 := dk95Run(t, svc, db)

	complete := dk95ID(1)
	assert.True(t, r.has(complete), "the newest complete snapshot must survive retention; store %v, forgets %v", r.store, r.forgets)
	assert.Nil(t, i1.UnreadableCount, "a complete snapshot has no count")
	require.NotNil(t, i3.UnreadableCount)
	assert.Equal(t, 500, *i3.UnreadableCount)
	assert.Equal(t, dk95Tagged(3)[:8], i3.SnapshotID, "the item records the id the tag gave the snapshot")
	assert.Equal(t, dk95Tagged(2)[:8], i2.SnapshotID)
	assert.Contains(t, i3.ErrorMessage, "kept snapshot "+complete[:8])
	assert.False(t, r.has(dk95Tagged(2)), "the older partial snapshot is still forgotten: %v", r.store)
	assert.True(t, r.has(dk95Tagged(3)), "the newest snapshot is kept by the policy")
	for _, f := range r.forgets[1:] {
		assert.Contains(t, f, "--", "a partial night forgets the dry-run's ids, never the policy again: %v", f)
	}
}

// Review MUST-1: backup history pruning deletes the run items. The mark in the
// repository still keeps the complete snapshot.
func TestBackup95dk_HistoryPruningDoesNotExposeCompleteSnapshot(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 1, 1}}
	svc, db, raw := dk95Svc(t, r)
	dk95Run(t, svc, db)
	dk95Run(t, svc, db)

	_, err := raw.Exec(`UPDATE backup_runs SET started_at = '2025-01-01T00:00:00Z'`)
	require.NoError(t, err)
	pruned, err := db.DeleteOldBackupRuns(90)
	require.NoError(t, err)
	require.Equal(t, 2, pruned, "fixture is unarmed: the history was not pruned")
	left, err := db.GetPartialSnapshotIDs(dk95Stack)
	require.NoError(t, err)
	require.Empty(t, left, "fixture is unarmed: the partial items survived pruning")

	dk95Run(t, svc, db)
	assert.True(t, r.has(dk95ID(1)), "store %v, forgets %v", r.store, r.forgets)
}

// Run items of partial snapshots written before the mark existed still count
// as partial (the union rule): such a snapshot is never taken for the newest
// complete one.
func TestBackup95dk_UnmarkedPartialOnRecordIsNotComplete(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 1}}
	svc, db, _ := dk95Svc(t, r)
	dk95Run(t, svc, db)
	// A sufn-era partial snapshot: in the repository with no mark, newer than
	// the complete one, and on record as partial.
	sufn := dk95ID(50)
	r.mu.Lock()
	r.store = append(r.store, dk95Snap{id: sufn, at: dk95Epoch.Add(90 * time.Minute), tags: []string{dk95Stack}})
	r.mu.Unlock()
	require.NoError(t, db.CreateBackupRun(&models.BackupRun{ID: "sufn-run", Kind: "backup", Trigger: "manual", Status: "partial", StartedAt: "2026-11-01T03:30:00Z"}))
	one := 1
	require.NoError(t, db.AddBackupRunItem(&models.BackupRunItem{ID: "sufn-item", RunID: "sufn-run", StackID: dk95Stack, Status: "success", SnapshotID: sufn[:8], UnreadableCount: &one}))

	dk95Run(t, svc, db)
	assert.True(t, r.has(dk95ID(1)), "the complete snapshot is kept, not the unmarked partial: %v", r.store)
	assert.False(t, r.has(sufn), "the unmarked partial on record is forgotten: %v", r.store)
}

// Fail closed on the repository's answer: unparseable, or well-formed but of
// another shape (the just-written snapshot is not listed), forgets nothing.
func TestBackup95dk_UntrustworthyDryRunSkipsRetention(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]string{
		"not json":      "not json",
		"other shape":   `[{"kept":[],"dropped":[]}]`,
		"empty":         `[]`,
		"id missing":    `[{"keep":[{"short_id":"abc"}],"remove":[]}]`,
		"time unparsed": `[{"keep":[{"id":"` + dk95Tagged(2) + `","short_id":"x","time":"yesterday"}],"remove":[]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := &dk95Runner{unreadable: []int{0, 3}}
			svc, db, _ := dk95Svc(t, r)
			dk95Run(t, svc, db)
			r.mu.Lock()
			r.dryRunJSON = answer
			r.mu.Unlock()
			_, item := dk95Run(t, svc, db)

			assert.True(t, r.has(dk95ID(1)), "store %v", r.store)
			assert.Len(t, r.forgets, 1, "only the complete night's policy forget ran: %v", r.forgets)
			assert.Contains(t, item.ErrorMessage, "retention skipped")
		})
	}
}

// A partial snapshot that could not be marked skips retention: unmarked, it
// would read as the newest complete one.
func TestBackup95dk_TagFailureSkipsRetention(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 1}}
	svc, db, _ := dk95Svc(t, r)
	dk95Run(t, svc, db)
	r.mu.Lock()
	r.tagFail = true
	r.mu.Unlock()
	stored, item := dk95Run(t, svc, db)

	assert.Len(t, r.forgets, 1, "%v", r.forgets)
	assert.Equal(t, 0, r.dryRuns)
	assert.Contains(t, item.ErrorMessage, "could not be marked "+PartialSnapshotTag)
	assert.Equal(t, dk95ID(2)[:8], item.SnapshotID, "unmarked, the snapshot keeps its id")
	assert.Contains(t, stored.ErrorMessage, dk95Stack)
}

// Fail closed on the database: the run items that mark sufn-era partials
// cannot be read, so retention is skipped.
func TestBackup95dk_UnreadableHistorySkipsRetention(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 1}}
	svc, db, raw := dk95Svc(t, r)
	dk95Run(t, svc, db)
	_, err := raw.Exec(`ALTER TABLE backup_run_items RENAME COLUMN unreadable_count TO unreadable_count_gone`)
	require.NoError(t, err)
	_, armErr := db.GetPartialSnapshotIDs(dk95Stack)
	require.Error(t, armErr, "fixture is unarmed")
	require.NotErrorIs(t, armErr, errdefs.ErrNotFound)

	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)
	assert.Len(t, r.forgets, 1, "%v", r.forgets)
	assert.Equal(t, 0, r.dryRuns)
	assert.True(t, r.has(dk95ID(1)))
	stored, err := db.GetBackupRunByID(run.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.ErrorMessage, dk95Stack)
}

// Controls: complete nights run the plain policy with no tag and no dry-run;
// a stack with no complete snapshot anywhere forgets exactly the dry-run's
// remove set by id.
func TestBackup95dk_NothingToProtect(t *testing.T) {
	t.Parallel()
	t.Run("complete nights", func(t *testing.T) {
		t.Parallel()
		r := &dk95Runner{unreadable: []int{0, 0}}
		svc, db, _ := dk95Svc(t, r)
		dk95Run(t, svc, db)
		dk95Run(t, svc, db)
		assert.Equal(t, 0, r.dryRuns)
		assert.Equal(t, 0, r.tags)
		require.Len(t, r.forgets, 2)
		for _, f := range r.forgets {
			assert.Contains(t, f, "--keep-daily")
		}
	})
	t.Run("partial with no complete anywhere", func(t *testing.T) {
		t.Parallel()
		r := &dk95Runner{unreadable: []int{2, 2}}
		svc, db, _ := dk95Svc(t, r)
		dk95Run(t, svc, db)
		_, item := dk95Run(t, svc, db)
		require.Len(t, r.forgets, 1, "the first night's remove set is empty: %v", r.forgets)
		assert.Equal(t, []string{"forget", "--verbose", "--prune", "--", dk95Tagged(1)}, r.forgets[0])
		assert.NotContains(t, item.ErrorMessage, "kept snapshot")
	})
}
