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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// --- agent-os-95dk: retention after a partial snapshot keeps the stack's
// newest complete snapshot. restic's keep-* policy cannot tell partial from
// complete, so when the unreadable set grows every bucket fills with partial
// snapshots and the last complete one ages out. ---

const dk95Stack = "dk95-app"

// dk95Runner plays restic over an in-memory snapshot store. Each backup takes
// the next entry of unreadable (0 = complete) and writes a snapshot. The
// retention policy it simulates keeps only the newest snapshot, so any
// policy forget drops every older one. forget with explicit ids drops those.
type dk95Runner struct {
	mu         sync.Mutex
	unreadable []int
	store      []string // full snapshot ids, oldest first
	n          int
	forgets    [][]string
	dryRuns    int
	dryRunJSON string // when set, the dry-run answers this instead
}

func dk95ID(n int) string { return fmt.Sprintf("%08x", 0xa0000000+n) + strings.Repeat("0", 56) }

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
		r.store = append(r.store, id)
		for i := range bad {
			item := fmt.Sprintf("/opt/stacks/%s/f%04d", dk95Stack, i)
			out <- StreamLine{Type: "data", Line: fmt.Sprintf(`{"message_type":"error","error":{"message":"denied"},"during":"archival","item":%q}`, item)}
		}
		out <- StreamLine{Type: "data", Line: fmt.Sprintf(`{"message_type":"summary","total_files_processed":1,"snapshot_id":%q}`, id)}
		if bad > 0 {
			return fmt.Errorf("restic exited: %w", fakeExitError{code: resticExitSourceUnreadable})
		}
	case args[0] == "forget" && slices.Contains(args, DatabaseBackupTag):
	case args[0] == "forget":
		r.forgets = append(r.forgets, slices.Clone(args))
		if i := slices.Index(args, "--"); i >= 0 {
			for _, id := range args[i+1:] {
				r.store = slices.DeleteFunc(r.store, func(s string) bool { return s == id })
			}
			return nil
		}
		if len(r.store) > 1 {
			r.store = r.store[len(r.store)-1:]
		}
	}
	return nil
}

func (r *dk95Runner) Output(_ context.Context, _ string, args []string, _ []string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if args[0] != "forget" {
		return []byte("[]"), nil
	}
	r.dryRuns++
	if r.dryRunJSON != "" {
		return []byte(r.dryRunJSON), nil
	}
	type snap struct {
		ID      string `json:"id"`
		ShortID string `json:"short_id"`
	}
	group := struct {
		Keep   []snap `json:"keep"`
		Remove []snap `json:"remove"`
	}{}
	for i, id := range r.store {
		if i == len(r.store)-1 {
			group.Keep = append(group.Keep, snap{id, id[:8]})
		} else {
			group.Remove = append(group.Remove, snap{id, id[:8]})
		}
	}
	raw, err := json.Marshal([]any{group})
	return raw, err
}

func (r *dk95Runner) has(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.store, id)
}

// dk95Runs runs one backup per entry of unreadable on one service and
// returns each run's item.
func dk95Runs(t *testing.T, r *dk95Runner) []models.BackupRunItem {
	t.Helper()
	db := newBackupTestDB(t)
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, r, r)
	seedStack(t, db, dk95Stack, "hot")
	var items []models.BackupRunItem
	for range r.unreadable {
		run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
		require.NoError(t, err)
		_, runItems := readRun(t, db, run.ID)
		require.Len(t, runItems, 1)
		items = append(items, runItems[0])
	}
	return items
}

// The bead's case: one complete night, then 1 unreadable file, then 500. The
// complete snapshot survives both partial nights' retention.
func TestBackup95dk_GrowingUnreadableSetKeepsNewestCompleteSnapshot(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 1, 500}}
	items := dk95Runs(t, r)

	complete := dk95ID(1)
	assert.True(t, r.has(complete), "the newest complete snapshot must survive retention; store %v, forgets %v", r.store, r.forgets)
	assert.Nil(t, items[0].UnreadableCount, "a complete snapshot has no count")
	require.NotNil(t, items[1].UnreadableCount)
	assert.Equal(t, 1, *items[1].UnreadableCount)
	require.NotNil(t, items[2].UnreadableCount)
	assert.Equal(t, 500, *items[2].UnreadableCount)
	assert.Contains(t, items[2].ErrorMessage, "kept snapshot "+complete[:8], "the item says retention held the complete snapshot")
	assert.False(t, r.has(dk95ID(2)), "the older partial snapshot is still forgotten: %v", r.store)
	assert.True(t, r.has(dk95ID(3)), "the newest snapshot is kept by the policy")
}

// Fail closed: a dry-run answer that cannot be read skips retention for the
// stack and says so. It never falls back to a blind policy forget.
func TestBackup95dk_UnreadableDryRunSkipsRetention(t *testing.T) {
	t.Parallel()
	r := &dk95Runner{unreadable: []int{0, 3}, dryRunJSON: "not json"}
	items := dk95Runs(t, r)

	assert.True(t, r.has(dk95ID(1)), "store %v", r.store)
	assert.Len(t, r.forgets, 1, "only the complete night's policy forget ran: %v", r.forgets)
	assert.Contains(t, items[1].ErrorMessage, "retention skipped")
}

// Controls: a complete night runs the plain policy forget with no dry-run,
// and a partial night with no complete snapshot on record has nothing to
// keep, so it runs the plain policy forget too.
func TestBackup95dk_PlainPolicyWhenNothingToProtect(t *testing.T) {
	t.Parallel()
	t.Run("complete nights", func(t *testing.T) {
		t.Parallel()
		r := &dk95Runner{unreadable: []int{0, 0}}
		dk95Runs(t, r)
		assert.Equal(t, 0, r.dryRuns)
		require.Len(t, r.forgets, 2)
		for _, f := range r.forgets {
			assert.Contains(t, f, "--keep-daily")
		}
		assert.Equal(t, []string{dk95ID(2)}, r.store)
	})
	t.Run("partial with no complete on record", func(t *testing.T) {
		t.Parallel()
		r := &dk95Runner{unreadable: []int{2, 2}}
		dk95Runs(t, r)
		require.Len(t, r.forgets, 2)
		for _, f := range r.forgets {
			assert.Contains(t, f, "--keep-daily")
		}
	})
}

// Fail closed on the database side too: when the newest complete snapshot
// cannot be read, a partial night skips retention instead of running the
// policy that could forget it.
func TestBackup95dk_UnreadableHistorySkipsRetention(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	r := &dk95Runner{unreadable: []int{0, 1}}
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, r, r)
	seedStack(t, db, dk95Stack, "hot")

	_, err = svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)
	// The lookup's column goes away through a second connection to the same
	// file: its query now fails, as a faulted database would.
	raw, err := sql.Open("sqlite", dataDir+"/capstan.db")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.Exec(`ALTER TABLE backup_run_items RENAME COLUMN unreadable_count TO unreadable_count_gone`)
	require.NoError(t, err)
	_, armErr := db.GetNewestCompleteSnapshotID(dk95Stack)
	require.Error(t, armErr, "fixture is unarmed")
	require.NotErrorIs(t, armErr, errdefs.ErrNotFound, "fixture is unarmed: a not-found reads as nothing to keep")

	run, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
	require.NoError(t, err)

	assert.Len(t, r.forgets, 1, "only the complete night's forget ran: %v", r.forgets)
	assert.Equal(t, 0, r.dryRuns)
	assert.True(t, r.has(dk95ID(1)))
	stored, err := db.GetBackupRunByID(run.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.ErrorMessage, dk95Stack, "the run names the stack whose retention did not run: %q", stored.ErrorMessage)
}
