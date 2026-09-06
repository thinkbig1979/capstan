package services

import (
	"bytes"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"

	_ "modernc.org/sqlite"
)

// agent-os-38ct. NextRunAt's interval estimator collapsed THREE outcomes of the
// same `if err == nil && len(runs) > 0 && runs[0].FinishedAt != nil` into one
// silent `base = time.Now()`:
//
//	(a) GetBackupRuns FAILED                       -- a fault
//	(b) a stored FinishedAt is UNPARSEABLE         -- a fault
//	(c) no runs, or the newest has a NULL finish   -- legitimate
//
// (c) genuinely should fall back to now, which is exactly why (a) and (b)
// hiding inside it were invisible. These tests pin (a) and (b) as separately
// observable and (c) as still silent.
//
// THE RETURN VALUE IS DELIBERATELY UNCHANGED in all three cases: NextRunAt is a
// read-only estimator for one JSON field (handlers/backup.go:727), so the fix
// reports the fault, it does not act on it.

// nextRunAtSvc builds a BackupService over an ON-DISK database wired to a
// captured logger, with the scheduler active and a 30-minute interval.
//
// On-disk rather than ":memory:" is load-bearing: the read-fault arm needs a
// SECOND connection to the same database file, and NewWithEncryptor pins a
// ":memory:" DB to a single connection whose database no other handle can see
// (database.go:104-108).
func nextRunAtSvc(t *testing.T, dataDir string) (*BackupService, *bytes.Buffer) {
	t.Helper()

	db, err := database.NewWithMigrations(dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, db.SetSetting("backup_schedule_interval", "30"))

	var logBuf bytes.Buffer
	svc := &BackupService{
		cfg: &config.Config{
			DataDir:      dataDir,
			StacksDir:    "/opt/stacks",
			AuthDisabled: true,
			JWTSecret:    "test-secret-32-chars-padding-here",
		},
		db:      db,
		docker:  &fakeDocker{},
		opLock:  NewOperationLock(),
		actions: NewActionLogger(db),
		logger:  slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	svc.schedulerActive.Store(true)
	return svc, &logBuf
}

// dropBackupRuns opens a SECOND connection to the same on-disk database and
// drops backup_runs, so the service's own GetBackupRuns fails with a real
// driver error while every settings read still succeeds.
//
// A CLOSED database cannot be used for this, which is why the ready-made
// closedDBWithSettings helper (backup_config_dbfault_test.go:57) is not the
// instrument here: on a closed DB resolveBackupConfig fails first and NextRunAt
// returns nil at backup.go:381, ~25 lines before the site under test. The fault
// would never reach the code it is supposed to exercise.
func dropBackupRuns(t *testing.T, dataDir string) {
	t.Helper()
	raw, err := sql.Open("sqlite", dataDir+"/capstan.db?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	defer func() { _ = raw.Close() }()
	_, err = raw.Exec("DROP TABLE backup_runs")
	require.NoError(t, err, "the read-fault arm is only armed if the table is really gone")
}

// seedRun inserts a backup run whose FinishedAt is exactly finishedAt (nil for
// a run that has not finished).
func seedRun(t *testing.T, svc *BackupService, id string, finishedAt *string) {
	t.Helper()
	require.NoError(t, svc.db.CreateBackupRun(&models.BackupRun{
		ID:         id,
		Kind:       "backup",
		Trigger:    "scheduled",
		Status:     "success",
		StartedAt:  time.Now().UTC().Add(-16 * time.Minute).Format(time.RFC3339),
		FinishedAt: finishedAt,
	}))
}

// TestNextRunAt_ReadFaultIsVisibleAndLoggedOnce is arm (a). Pre-fix this fails
// on the Contains assertion, not on a build error: every symbol it names
// already existed.
func TestNextRunAt_ReadFaultIsVisibleAndLoggedOnce(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	svc, logBuf := nextRunAtSvc(t, dataDir)
	dropBackupRuns(t, dataDir)

	next := svc.NextRunAt()
	require.NotNil(t, next, "a read fault must not change WHAT NextRunAt returns")
	diff := time.Until(*next)
	assert.True(t, diff > 29*time.Minute && diff < 31*time.Minute,
		"the fallback estimate is still now+interval, got %v", diff)

	got := logBuf.String()
	assert.Contains(t, got, "Could not read the last backup run",
		"a GetBackupRuns fault must be distinguishable from 'no runs yet'")
	assert.Contains(t, got, "no such table",
		"the driver error itself must be carried, not just the fact of a fault")

	// NO FLOOD: NextRunAt is polled once per dashboard status request. The same
	// persistent fault must log once per episode, not once per poll.
	first := strings.Count(got, "Could not read the last backup run")
	svc.NextRunAt()
	svc.NextRunAt()
	svc.NextRunAt()
	assert.Equal(t, 1, first, "the first poll logs exactly one line")
	assert.Equal(t, 1, strings.Count(logBuf.String(), "Could not read the last backup run"),
		"three further polls of the SAME fault must add no further lines")
}

// TestNextRunAt_ParseFaultIsVisibleAndLoggedOnce is arm (b).
func TestNextRunAt_ParseFaultIsVisibleAndLoggedOnce(t *testing.T) {
	t.Parallel()

	svc, logBuf := nextRunAtSvc(t, t.TempDir())
	corrupt := "not-an-rfc3339-timestamp"
	seedRun(t, svc, "run-38ct-parse", &corrupt)

	next := svc.NextRunAt()
	require.NotNil(t, next, "a parse fault must not change WHAT NextRunAt returns")
	diff := time.Until(*next)
	assert.True(t, diff > 29*time.Minute && diff < 31*time.Minute,
		"the fallback estimate is still now+interval, got %v", diff)

	got := logBuf.String()
	assert.Contains(t, got, "finish time is unparseable",
		"an unparseable FinishedAt must be distinguishable from a NULL one")
	assert.Contains(t, got, "run-38ct-parse",
		"the offending run must be identified")

	first := strings.Count(got, "finish time is unparseable")
	svc.NextRunAt()
	svc.NextRunAt()
	assert.Equal(t, 1, first, "the first poll logs exactly one line")
	assert.Equal(t, 1, strings.Count(logBuf.String(), "finish time is unparseable"),
		"two further polls of the SAME fault must add no further lines")
}

// TestNextRunAt_LegitimateAbsenceStaysSilent is the must-NOT-fire arm: case (c)
// and the healthy path. A fix that logged here would flood the status poll for
// the ORDINARY state, which is precisely what backup.go:375 and :387-389 refuse
// to do. Without this arm, "logs on a fault" is satisfied by "logs always".
func TestNextRunAt_LegitimateAbsenceStaysSilent(t *testing.T) {
	t.Parallel()

	t.Run("no runs at all", func(t *testing.T) {
		t.Parallel()
		svc, logBuf := nextRunAtSvc(t, t.TempDir())
		require.NotNil(t, svc.NextRunAt())
		assert.Empty(t, logBuf.String(), "an empty history is normal and must stay silent")
	})

	t.Run("newest run has a NULL FinishedAt", func(t *testing.T) {
		t.Parallel()
		svc, logBuf := nextRunAtSvc(t, t.TempDir())
		seedRun(t, svc, "run-38ct-running", nil)
		require.NotNil(t, svc.NextRunAt())
		assert.Empty(t, logBuf.String(), "an unfinished run is normal and must stay silent")
	})

	t.Run("newest run has a valid FinishedAt", func(t *testing.T) {
		t.Parallel()
		svc, logBuf := nextRunAtSvc(t, t.TempDir())
		finished := time.Now().UTC().Add(-15 * time.Minute).Format(time.RFC3339)
		seedRun(t, svc, "run-38ct-ok", &finished)
		next := svc.NextRunAt()
		require.NotNil(t, next)
		diff := time.Until(*next)
		assert.True(t, diff > 10*time.Minute && diff < 20*time.Minute,
			"the healthy path still anchors on the last finish, got %v", diff)
		assert.Empty(t, logBuf.String(), "the healthy path must stay silent")
	})
}
