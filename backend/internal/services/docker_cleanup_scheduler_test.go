package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-fn7x.3 — the cleanup scheduler's behavioural arms.
//
// WHY A FAKE RUNNER AND NOT DockerCleanupService. The property under test is the
// POLICY DECISION, not the prune: does the tick call Execute at all, with which
// floor, and does it refuse when the policy cannot be read. A fake that records
// its calls is the only way to observe "was Execute called zero times", which is
// the entire content of AC1.
//
// WHY THE INTERFACE HAS ONE METHOD (D1). The rejected alternative reached the
// pruner off SchedulerService by type assertion, which compiles but which every
// existing test fake fails — so the tick would silently no-op and the arm below
// would pass whether or not the policy check existed. A one-method interface
// cannot be accidentally unsatisfied, so a passing "disabled does nothing" arm
// means the policy check ran rather than that the seam was missing.

type fn7x3ExecuteCall struct {
	trigger     string
	minAgeHours int
}

type fn7x3FakeCleanupRunner struct {
	mu    sync.Mutex
	calls []fn7x3ExecuteCall
	err   error
}

func (f *fn7x3FakeCleanupRunner) Execute(_ context.Context, trigger string, minAgeHours int) (*models.DockerCleanupRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fn7x3ExecuteCall{trigger: trigger, minAgeHours: minAgeHours})
	if f.err != nil {
		return nil, f.err
	}
	return &models.DockerCleanupRun{ID: "fn7x3-run", Status: "success", MinAgeHours: minAgeHours}, nil
}

func (f *fn7x3FakeCleanupRunner) recorded() []fn7x3ExecuteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fn7x3ExecuteCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// fn7x3Scheduler wires a scheduler to the given db and a buffered logger, so a
// test can assert on the log as well as on the calls. The buffer is returned
// rather than the logger: every assertion here is about what was written.
func fn7x3Scheduler(t *testing.T, db *database.DB, runner dockerCleanupRunner) (*DockerCleanupSchedulerService, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewDockerCleanupScheduler(runner, db, logger), buf
}

func fn7x3MemoryDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestDockerCleanupDisabledDoesNothing is the bead's AC1 arm, and it is
// TWO-SIDED IN ONE TEST on purpose.
//
// The disabled half alone is worthless as evidence: a tick that never calls
// Execute for ANY reason — a broken seam, a nil runner, a scheduler that never
// resolves the policy — passes it. Only the enabled half, on the same fake and
// the same code path, shows that Execute is reachable at all, which is what makes
// the zero in the first half mean "the policy check refused" rather than "nothing
// works".
//
// WHICH ARM DIES IF THE POLICY CHECK IS DELETED: the disabled one. Deleting
// `if !policy.Enabled { return }` from runCycle leaves the enabled arm green and
// turns the disabled arm red, which is exactly the discrimination AC1 asks for.
func TestDockerCleanupDisabledDoesNothing(t *testing.T) {
	t.Run("disabled by default performs no prune", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		runner := &fn7x3FakeCleanupRunner{}
		s, buf := fn7x3Scheduler(t, db, runner)

		// No setting written at all: absence IS the disabled state (FR7).
		_, err := db.GetSetting(SettingDockerCleanupEnabled)
		require.ErrorIs(t, err, sql.ErrNoRows,
			"the fixture must start with NO stored policy, or this arm is not testing the default")

		s.runCycle(context.Background())

		require.Empty(t, runner.recorded(),
			"the tick pruned while cleanup was disabled: %+v", runner.recorded())
		// An absent row is not a fault and must not log. Otherwise every fresh
		// install prints an ERROR on every interval.
		require.NotContains(t, buf.String(), "level=ERROR",
			"an ABSENT policy logged an error; only an UNREADABLE one may. log: %s", buf.String())
	})

	t.Run("enabled performs the prune", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
		runner := &fn7x3FakeCleanupRunner{}
		s, _ := fn7x3Scheduler(t, db, runner)

		s.runCycle(context.Background())

		calls := runner.recorded()
		require.Len(t, calls, 1, "the tick did not prune while cleanup was ENABLED; the disabled arm above proves nothing without this")
		require.Equal(t, TriggerScheduled, calls[0].trigger)
		require.Equal(t, DefaultCleanupMinAgeHours, calls[0].minAgeHours,
			"an enabled policy with no stored floor must run at the 168h default, not at the 1h server floor")
	})

	t.Run("enabled honours a stored floor", func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
		require.NoError(t, db.SetSetting(SettingDockerCleanupMinAgeHours, "3"))
		runner := &fn7x3FakeCleanupRunner{}
		s, _ := fn7x3Scheduler(t, db, runner)

		s.runCycle(context.Background())

		calls := runner.recorded()
		require.Len(t, calls, 1)
		require.Equal(t, 3, calls[0].minAgeHours,
			"the tick ignored the stored age floor and used %d", calls[0].minAgeHours)
	})
}

// TestDockerCleanupUnreadablePolicySkipsAndLogs is the rltu/r1kc arm: a settings
// READ FAULT must not read as "the operator turned it off".
//
// Two-sided on the same fixture: hidden -> refuse and log, restored -> prune and
// stay quiet. Without the restored half, a refusal is equally consistent with a
// scheduler that never prunes and a fixture that broke the whole database.
//
// COVERAGE LIMIT, stated rather than implied. The only settings-fault fixture
// this repo has is TABLE-level (hiddenSettingsDB renames `settings` wholesale)
// and a per-key NULL row is rejected by NOT NULL (see hiddenSettingsDB's own
// docblock, measured under agent-os-l42o). So a fault that hits
// docker_cleanup_min_age_hours WHILE docker_cleanup_enabled still answers true
// cannot be constructed here. ResolveDockerCleanupPolicy reads enabled first and
// returns on the first error, so a real table-wide fault surfaces as the enabled
// fault and the pass is skipped either way — which is the behaviour required. The
// min-age-only variant is NOT separately covered.
func TestDockerCleanupUnreadablePolicySkipsAndLogs(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"),
		"seed the opt-in this test is about: the fault must not look like a deliberate opt-out")
	runner := &fn7x3FakeCleanupRunner{}
	s, buf := fn7x3Scheduler(t, db, runner)

	hide()
	s.runCycle(context.Background())

	require.Empty(t, runner.recorded(),
		"the tick pruned on the strength of a database fault, under a floor nobody could be read to have chosen")
	log := buf.String()
	require.Contains(t, log, "level=ERROR",
		"an unreadable policy was skipped SILENTLY; the operator who opted in now has cleanup off with no signal, and the only other symptom is the disk filling. log: %s", log)
	require.Contains(t, log, "no such table",
		"the ERROR line does not carry the underlying cause, so the fault is undiagnosable: %s", log)

	restore()
	buf.Reset()
	s.runCycle(context.Background())

	require.Len(t, runner.recorded(), 1,
		"the same scheduler did not prune once the settings table answered again, so the refusal above was not about the fault")
	require.NotContains(t, buf.String(), "level=ERROR",
		"a healthy read still logged an error: %s", buf.String())
}

// TestDockerCleanupStartFromPolicyArmsOnlyWhenEnabled covers the arming decision,
// which is a different question from the tick's: the tick re-reads the policy, so
// disabling takes effect without a restart, but ENABLING needs the ticker armed.
func TestDockerCleanupStartFromPolicyArmsOnlyWhenEnabled(t *testing.T) {
	db := fn7x3MemoryDB(t)
	s, buf := fn7x3Scheduler(t, db, &fn7x3FakeCleanupRunner{})
	t.Cleanup(s.Stop)

	s.StartFromPolicy()
	require.False(t, s.IsRunning(), "the scheduler armed a ticker while cleanup was disabled")
	require.NotContains(t, buf.String(), "level=ERROR", "the disabled default logged an error: %s", buf.String())

	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
	s.StartFromPolicy()
	require.True(t, s.IsRunning(), "the scheduler did not arm after an operator opted in, so enabling cleanup would need a process restart")

	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "false"))
	s.StartFromPolicy()
	require.False(t, s.IsRunning(), "the scheduler stayed armed after an operator opted back out")
}

// TestDockerCleanupStartFromPolicyRefusesOnAnUnreadablePolicy: the arming half of
// the rltu/r1kc rule. Arming at a default here would start pruning on a host
// whose opt-in could not be read.
func TestDockerCleanupStartFromPolicyRefusesOnAnUnreadablePolicy(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
	s, buf := fn7x3Scheduler(t, db, &fn7x3FakeCleanupRunner{})
	t.Cleanup(s.Stop)

	hide()
	s.StartFromPolicy()
	require.False(t, s.IsRunning(), "armed a ticker from a policy that could not be read")
	require.Contains(t, buf.String(), "level=ERROR", "refused silently: %s", buf.String())

	restore()
	buf.Reset()
	s.StartFromPolicy()
	require.True(t, s.IsRunning(),
		"the same scheduler did not arm once settings answered again, so the refusal above was not about the fault")
}

// TestDockerCleanupSchedulerTickerFiresTheCycle proves the TICKER path reaches
// runCycle. Every other test in this file calls runCycle directly, which is
// deterministic but says nothing about whether anything ever calls it — the
// silent-no-op failure mode D1 exists to avoid.
//
// synctest, not a real sleep: the interval is an hour, and the arms below are
// about virtual time.
func TestDockerCleanupSchedulerTickerFiresTheCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))
		require.NoError(t, db.SetSetting(SettingDockerCleanupIntervalHours, "1"))
		runner := &fn7x3FakeCleanupRunner{}
		s, _ := fn7x3Scheduler(t, db, runner)

		s.StartFromPolicy()
		require.True(t, s.IsRunning())

		// Before the first interval elapses nothing has fired, which is the arm
		// that would go red if Start armed a ticker that fired immediately.
		time.Sleep(30 * time.Minute)
		synctest.Wait()
		require.Empty(t, runner.recorded(), "the tick fired before its interval elapsed")

		time.Sleep(45 * time.Minute)
		synctest.Wait()
		require.Len(t, runner.recorded(), 1, "the ticker never reached runCycle, so the scheduler is a silent no-op")

		s.Stop()
		require.False(t, s.IsRunning())
	})
}

// TestDockerCleanupSchedulerLifecycle mirrors the backup scheduler's lifecycle
// arms: Start/Stop/Restart leave IsRunning honest and Stop is idempotent.
func TestDockerCleanupSchedulerLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := fn7x3MemoryDB(t)
		s, _ := fn7x3Scheduler(t, db, &fn7x3FakeCleanupRunner{})

		s.Start(time.Hour)
		require.True(t, s.IsRunning())
		s.Stop()
		require.False(t, s.IsRunning())
		s.Stop() // idempotent: a double Stop must not panic on a closed channel
		require.False(t, s.IsRunning())

		s.Start(time.Hour)
		require.True(t, s.IsRunning())
		s.Stop()
		require.False(t, s.IsRunning())
	})
}

// TestDockerCleanupTickLogsAFailedRun: a cleanup that fails is logged, not
// swallowed. Two-sided against the success arm so a green here is not just "the
// logger writes something for everything".
func TestDockerCleanupTickLogsAFailedRun(t *testing.T) {
	db := fn7x3MemoryDB(t)
	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))

	runner := &fn7x3FakeCleanupRunner{err: errors.New("fn7x3-prune-exploded")}
	s, buf := fn7x3Scheduler(t, db, runner)
	s.runCycle(context.Background())
	require.Len(t, runner.recorded(), 1)
	require.Contains(t, buf.String(), "fn7x3-prune-exploded",
		"a failed cleanup cycle did not name its cause: %s", buf.String())

	okRunner := &fn7x3FakeCleanupRunner{}
	okSvc, okBuf := fn7x3Scheduler(t, db, okRunner)
	okSvc.runCycle(context.Background())
	require.NotContains(t, okBuf.String(), "level=ERROR",
		"a successful cycle logged an error: %s", okBuf.String())
	require.Contains(t, okBuf.String(), "Scheduled Docker cleanup completed")
}

// TestClampCleanupIntervalHours pins the ticker floor. time.NewTicker PANICS on a
// non-positive duration and the stored interval is operator-supplied, so the
// boundary is asserted rather than assumed.
func TestClampCleanupIntervalHours(t *testing.T) {
	require.Equal(t, MinCleanupIntervalHours, clampCleanupIntervalHours(0))
	require.Equal(t, MinCleanupIntervalHours, clampCleanupIntervalHours(-24))
	require.Equal(t, MinCleanupIntervalHours, clampCleanupIntervalHours(MinCleanupIntervalHours))
	require.Equal(t, 24, clampCleanupIntervalHours(24), "a legal interval must pass through unchanged")
}

// TestResolveDockerCleanupPolicyDefaults: absent rows give the documented
// defaults, and an unparseable stored value is a malformed setting rather than a
// fault — it falls back, it does not refuse. That split is resolveIntSetting's,
// and this pins that cleanup inherits it.
func TestResolveDockerCleanupPolicyDefaults(t *testing.T) {
	db := fn7x3MemoryDB(t)

	p, err := ResolveDockerCleanupPolicy(db)
	require.NoError(t, err)
	require.False(t, p.Enabled, "cleanup must be DISABLED by default (FR7)")
	require.Equal(t, DefaultCleanupMinAgeHours, p.MinAgeHours)
	require.Equal(t, DefaultCleanupIntervalHours, p.IntervalHours)

	require.NoError(t, db.SetSetting(SettingDockerCleanupMinAgeHours, "not-a-number"))
	p, err = ResolveDockerCleanupPolicy(db)
	require.NoError(t, err, "an unparseable value is a malformed setting, not a database fault")
	require.Equal(t, DefaultCleanupMinAgeHours, p.MinAgeHours)
}

// TestResolveDockerCleanupPolicyRefusesOnAFault is the resolver's own two-sided
// control, separate from the scheduler's: the error has to originate here for the
// tick's refusal to be about the policy read at all.
func TestResolveDockerCleanupPolicyRefusesOnAFault(t *testing.T) {
	db, hide, restore := hiddenSettingsDB(t)
	require.NoError(t, db.SetSetting(SettingDockerCleanupEnabled, "true"))

	p, err := ResolveDockerCleanupPolicy(db)
	require.NoError(t, err, "the healthy control did not fire")
	require.True(t, p.Enabled)

	hide()
	_, err = ResolveDockerCleanupPolicy(db)
	require.Error(t, err, "an unreadable settings table resolved to a policy anyway")
	require.False(t, errors.Is(err, sql.ErrNoRows),
		"the fault arrived as sql.ErrNoRows, the predicate that MUST stay a default; this fixture cannot discriminate the branch under test: %v", err)
	require.True(t, strings.Contains(err.Error(), "no such table"), "unexpected error: %v", err)

	restore()
	_, err = ResolveDockerCleanupPolicy(db)
	require.NoError(t, err)
}
