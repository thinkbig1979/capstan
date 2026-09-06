package services

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"

	_ "modernc.org/sqlite"
)

// One defect class, two sites: "a read that could not answer is merged into a
// legitimate value state, so a fault and a normal condition produce the same
// branch."
//
//   - agent-os-koy9, scheduler.go RunAutoUpdates: a failed settings read and an
//     operator who turned auto-updates off shared one bare return, with no log.
//   - agent-os-91u2, monitor.go stackEventFor: a failed stacks read and a
//     compose project that simply has no stack row shared one DEBUG line and one
//     dropped event.
//
// The third site the class sweep found, scheduler.go loadApplySchedule
// (agent-os-7549), is not an instance of THIS class and is deliberately
// untested here: reaching it requires update_apply_mode == "scheduled", which
// requires migration 14 to have committed, and migration 14 seeds
// update_apply_time and update_apply_days in the same transaction
// (migrations.go:542-544, run under tx.Begin/tx.Commit at
// migrations.go:665-694). The absent-row state it was filed for is unreachable,
// so the `||` there merges two ERRORS and never an error with a value.
// TestMigration14SeedsAllThreeKeysTogether below pins that premise so the
// verdict is anchored to the code rather than to this comment.
//
// What was left standing there — that a genuine read fault resolved to
// immediate and APPLIED updates outside the operator's maintenance window — is
// a different class (fail open on a read fault, agent-os-r1kc's shape) and was
// fixed separately under agent-os-rltu. Its tests live in
// scheduler_readfault_test.go, not here.

// ---------------------------------------------------------------------------
// agent-os-koy9 — RunAutoUpdates
// ---------------------------------------------------------------------------

// koy9FaultLog is the substring that must appear when the setting cannot be
// read. It deliberately includes the CONSEQUENCE, not just the cause: a mutant
// that logs "failed to read auto_update_enabled" and stops there names what
// broke but not what it costs the operator, which is the whole complaint in
// agent-os-koy9. Asserting on the cause alone would accept it.
const koy9FaultLog = "skipping this auto-update run, so no container will be patched"

// koy9Logger returns a scheduler-shaped logger writing into buf at Debug level,
// so nothing is filtered out before an assertion can see it. Unlike monitor.go,
// SchedulerService takes its logger by injection, so no global state is touched
// and these tests do not race with anything else in the package.
func koy9Logger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// koy9HealthyDB returns a fully migrated on-disk database and its data
// directory. The directory is returned because the DDL/DML the tests below need
// (deleting a seeded row, dropping a table) has no exported route through
// *database.DB, so it goes over a second connection to the same file — the
// pattern already used by scanner_dbfault_test.go and
// backup_restore_policy_dbfault_test.go.
func koy9HealthyDB(t *testing.T) (*database.DB, string) {
	t.Helper()
	dataDir := t.TempDir()
	db, err := database.NewWithMigrations(dataDir)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, dataDir
}

// koy9ClosedDB is the fault instrument: a fully migrated database that is then
// closed, so every read returns a real error that is NOT sql.ErrNoRows.
func koy9ClosedDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrations(t.TempDir())
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db to induce failure: %v", err)
	}
	return db
}

// TestKoy9PremiseFaultIsNotErrNoRows pins what the discrimination rests on,
// two-sided on one instrument: on a healthy database an absent settings row is
// sql.ErrNoRows, and on a faulted database the same read is a different error.
// Without this, the fix is tested against an assumption about the database
// layer rather than against the database layer.
func TestKoy9PremiseFaultIsNotErrNoRows(t *testing.T) {
	healthy, _ := koy9HealthyDB(t)
	if _, err := healthy.GetSetting("koy9-key-that-was-never-seeded"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("healthy db, absent key: want sql.ErrNoRows, got %v", err)
	}

	faulty := koy9ClosedDB(t)
	faultErr := func() error {
		_, err := faulty.GetSetting("auto_update_enabled")
		return err
	}()
	if faultErr == nil {
		t.Fatal("closed db: want an error, got nil — the fault instrument does not fault")
	}
	if errors.Is(faultErr, sql.ErrNoRows) {
		t.Fatalf("closed db: the fault must NOT be sql.ErrNoRows, got %v", faultErr)
	}
}

// TestRunAutoUpdatesLogsWhenTheSettingCannotBeRead is the failing-first arm for
// agent-os-koy9. Pre-fix, `if err != nil || autoEnabledStr != "true" { return }`
// returned in silence, so this buffer was empty.
func TestRunAutoUpdatesLogsWhenTheSettingCannotBeRead(t *testing.T) {
	var buf bytes.Buffer
	s := NewSchedulerService(nil, koy9ClosedDB(t), koy9Logger(&buf), nil)

	s.RunAutoUpdates(context.Background(), nil)

	got := buf.String()
	if !strings.Contains(got, koy9FaultLog) {
		t.Fatalf("a settings read fault must be logged with its consequence.\nwant substring: %q\ngot log:\n%s", koy9FaultLog, got)
	}
	if !strings.Contains(got, "level=ERROR") {
		t.Fatalf("the fault must be logged at ERROR, not below it.\ngot log:\n%s", got)
	}
	// The fault must be the reason it stopped: it must not fall through to the
	// policies read and log that instead, which would be a different complaint
	// with the same red/green.
	if strings.Contains(got, "Failed to get auto-update policies") {
		t.Fatalf("a settings read fault must return before the policies read.\ngot log:\n%s", got)
	}
}

// TestRunAutoUpdatesStaysSilentWhenDeliberatelyDisabled is the control side of
// the same instrument: the operator turning auto-updates off is a normal
// condition and must remain exactly as quiet as it was before the fix. Without
// it, "log on the fault" could be satisfied by logging on both.
func TestRunAutoUpdatesStaysSilentWhenDeliberatelyDisabled(t *testing.T) {
	db, _ := koy9HealthyDB(t)
	if err := db.SetSetting("auto_update_enabled", "false"); err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	var buf bytes.Buffer
	s := NewSchedulerService(nil, db, koy9Logger(&buf), nil)

	s.RunAutoUpdates(context.Background(), nil)

	if got := buf.String(); got != "" {
		t.Fatalf("auto-updates deliberately off must log nothing at any level, got:\n%s", got)
	}
}

// TestRunAutoUpdatesStaysSilentWhenTheRowIsAbsent covers the third state. An
// absent row is the pre-migration-3 database (migrations.go:248 seeds the key
// as 'false'), which means the same thing as 'false' and is not worth shouting
// about — the same judgement loadApplySchedule already makes for a missing
// update_apply_mode at scheduler.go:483-489.
func TestRunAutoUpdatesStaysSilentWhenTheRowIsAbsent(t *testing.T) {
	db, dataDir := koy9HealthyDB(t)
	koy9DeleteSetting(t, dataDir, "auto_update_enabled")

	// Pin the premise rather than assuming the delete worked.
	if _, err := db.GetSetting("auto_update_enabled"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("after deleting the row the read must be sql.ErrNoRows, got %v", err)
	}

	var buf bytes.Buffer
	s := NewSchedulerService(nil, db, koy9Logger(&buf), nil)

	s.RunAutoUpdates(context.Background(), nil)

	if got := buf.String(); strings.Contains(got, koy9FaultLog) {
		t.Fatalf("an absent row is not a fault and must not be logged as one, got:\n%s", got)
	}
}

// TestRunAutoUpdatesGateStillOpensAndCloses is the two-sided proof that the
// gate itself still works after being split in two, on ONE instrument: the
// "Failed to get auto-update policies" line, which is only reachable PAST the
// gate. With the auto_update_policies table dropped, the settings read succeeds
// and the policies read fails, so that line is a direct read-out of whether the
// gate let execution through.
func TestRunAutoUpdatesGateStillOpensAndCloses(t *testing.T) {
	const pastTheGate = "Failed to get auto-update policies"

	for _, tc := range []struct {
		name     string
		enabled  string
		wantPast bool
	}{
		{name: "enabled proceeds", enabled: "true", wantPast: true},
		{name: "disabled stops", enabled: "false", wantPast: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, dataDir := koy9HealthyDB(t)
			if err := db.SetSetting("auto_update_enabled", tc.enabled); err != nil {
				t.Fatalf("seed setting: %v", err)
			}
			koy9DropTable(t, dataDir, "auto_update_policies")

			var buf bytes.Buffer
			s := NewSchedulerService(nil, db, koy9Logger(&buf), nil)

			s.RunAutoUpdates(context.Background(), nil)

			if past := strings.Contains(buf.String(), pastTheGate); past != tc.wantPast {
				t.Fatalf("auto_update_enabled=%q: reached the policies read = %v, want %v.\ngot log:\n%s",
					tc.enabled, past, tc.wantPast, buf.String())
			}
		})
	}
}

// TestMigration14SeedsAllThreeKeysTogether pins the premise behind closing
// agent-os-7549 as not-an-instance-of-this-class: the time/days branch of
// loadApplySchedule can only be reached when update_apply_mode reads back as
// "scheduled", and the migration that puts any value in that row puts values in
// the other two as well. So the absent-row state that bead was filed for cannot
// exist while that branch is reachable, and the errors it merges are both
// genuine faults. (Both of them now refuse rather than apply — agent-os-rltu.)
func TestMigration14SeedsAllThreeKeysTogether(t *testing.T) {
	db, _ := koy9HealthyDB(t)
	for _, key := range []string{"update_apply_mode", "update_apply_time", "update_apply_days"} {
		v, err := db.GetSetting(key)
		if err != nil {
			t.Fatalf("%s must be seeded by migration 14, got error %v", key, err)
		}
		if v == "" {
			t.Fatalf("%s is seeded but empty", key)
		}
	}
}

// koy9Raw opens a second connection to the same database file. The DSN mirrors
// database.go minus the connection-scoped pragmas, which are irrelevant to a
// single statement.
func koy9Raw(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "capstan.db"))
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	return raw
}

func koy9DeleteSetting(t *testing.T, dataDir, key string) {
	t.Helper()
	if _, err := koy9Raw(t, dataDir).Exec("DELETE FROM settings WHERE key = ?", key); err != nil {
		t.Fatalf("delete setting %s: %v", key, err)
	}
}

func koy9DropTable(t *testing.T, dataDir, table string) {
	t.Helper()
	if _, err := koy9Raw(t, dataDir).Exec("DROP TABLE " + table); err != nil {
		t.Fatalf("drop table %s: %v", table, err)
	}
}

// ---------------------------------------------------------------------------
// agent-os-91u2 — MonitorService.stackEventFor
// ---------------------------------------------------------------------------

// u2FaultLog carries the consequence, for the same reason koy9FaultLog does: a
// mutant that logs only "failed to read the stack" names the cause and leaves
// the operator no idea that their stack view has stopped receiving this
// container's events.
const u2FaultLog = "so this container's events are no longer attributed to its stack"

const (
	u2Project = "u2-compose-project"
	u2StackID = "u2-stack-id"
	u2Ctr     = "u2containerid00000000000000000000"
)

// u2StubDB drives MonitorService through its Database interface, which is the
// tightest available unit: MonitorService.client is a concrete *client.Client
// (monitor.go:20), so ListenEvents cannot be driven without a live daemon.
// stackEventFor is the whole per-event decision ListenEvents makes.
type u2StubDB struct {
	stack *models.Stack
	err   error
}

func (s u2StubDB) GetStackByProjectName(string) (*models.Stack, error) {
	return s.stack, s.err
}

// u2CaptureLogs redirects the default slog logger, which is the one monitor.go
// writes to. Tests using it are deliberately NOT parallel, and every assertion
// counts only lines carrying the message under test.
func u2CaptureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// u2AllActions is every action ListenEvents forwards (monitor.go's action
// switch). The behaviour-preservation tests below run all of them so a mutant
// cannot pass by getting one representative case right.
var u2AllActions = []string{"start", "stop", "die", "kill", "destroy", "restart", "pause", "unpause", "create", "rename"}

// TestStackEventForEmitsWhenTheProjectHasNoStackRow is the failing-first arm for
// agent-os-91u2. Pre-fix, GetStackByProjectName returning sql.ErrNoRows — a
// compose project started outside Capstan, or one not yet scanned — dropped the
// event and left a DEBUG line, which is off in production.
func TestStackEventForEmitsWhenTheProjectHasNoStackRow(t *testing.T) {
	_ = u2CaptureLogs(t)
	s := &MonitorService{db: u2StubDB{err: sql.ErrNoRows}}

	ts := time.Unix(1700000000, 0)
	for _, action := range []string{"start", "stop", "die", "destroy"} {
		ev, ok := s.stackEventFor(action, u2Ctr, u2Project, ts)
		if !ok {
			t.Fatalf("action %q: a container whose project has no stack row must still be emitted, got dropped", action)
		}
		if ev.Type != "container_event" {
			t.Fatalf("action %q: Type = %q, want %q", action, ev.Type, "container_event")
		}
		if ev.StackID != "" {
			t.Fatalf("action %q: StackID = %q, want empty (there is no stack to associate)", action, ev.StackID)
		}
		if ev.ContainerID != u2Ctr || ev.Event != action || !ev.Timestamp.Equal(ts) {
			t.Fatalf("action %q: event body not carried through: %+v", action, ev)
		}
	}
}

// TestStackEventForDiscriminatesAbsentRowFromReadFault is the two-sided arm, on
// ONE instrument: the log. Both cases now emit the event, so emission alone
// cannot tell them apart — the level and the message are what must, which is
// precisely what agent-os-91u2 asks for.
func TestStackEventForDiscriminatesAbsentRowFromReadFault(t *testing.T) {
	ts := time.Unix(1700000000, 0)

	// Side A: absent row. Not an error, so nothing at ERROR.
	absentBuf := u2CaptureLogs(t)
	absent := &MonitorService{db: u2StubDB{err: sql.ErrNoRows}}
	evAbsent, okAbsent := absent.stackEventFor("start", u2Ctr, u2Project, ts)
	absentLog := absentBuf.String()

	if !okAbsent || evAbsent.StackID != "" {
		t.Fatalf("absent row: want an emitted, unassociated event, got ok=%v ev=%+v", okAbsent, evAbsent)
	}
	if strings.Contains(absentLog, "level=ERROR") {
		t.Fatalf("an absent stack row is a normal condition and must not log at ERROR, got:\n%s", absentLog)
	}
	if strings.Contains(absentLog, u2FaultLog) {
		t.Fatalf("an absent stack row must not use the read-fault message, got:\n%s", absentLog)
	}

	// Side B: a genuine read fault. Same emission, different and louder log.
	faultBuf := u2CaptureLogs(t)
	fault := &MonitorService{db: u2StubDB{err: errors.New("sql: database is closed")}}
	evFault, okFault := fault.stackEventFor("start", u2Ctr, u2Project, ts)
	faultLog := faultBuf.String()

	if !okFault || evFault.StackID != "" {
		t.Fatalf("read fault: want an emitted, unassociated event, got ok=%v ev=%+v", okFault, evFault)
	}
	if !strings.Contains(faultLog, "level=ERROR") {
		t.Fatalf("a stacks read fault must be logged above DEBUG, got:\n%s", faultLog)
	}
	if !strings.Contains(faultLog, u2FaultLog) {
		t.Fatalf("the read-fault log must name its consequence.\nwant substring: %q\ngot log:\n%s", u2FaultLog, faultLog)
	}

	// The two emitted events are indistinguishable, which is why the log has to
	// do the work. Pin that so the assertions above cannot be weakened into
	// "the events differ" later.
	if evAbsent != evFault {
		t.Fatalf("the two emitted events are expected to be identical; if that changes, the log is no longer the only discriminator.\nabsent=%+v\nfault=%+v", evAbsent, evFault)
	}
}

// TestStackEventForKnownStackIsUnchanged is the behaviour-preservation control
// for the path that was already correct: a project WITH a stack row must map to
// exactly what it mapped to before, for every action.
func TestStackEventForKnownStackIsUnchanged(t *testing.T) {
	_ = u2CaptureLogs(t)
	s := &MonitorService{db: u2StubDB{stack: &models.Stack{ID: u2StackID}}}
	ts := time.Unix(1700000000, 0)

	want := map[string]models.StackEvent{
		"start":   {Type: "stack_status", Status: "running", StackID: u2StackID},
		"restart": {Type: "stack_status", Status: "running", StackID: u2StackID},
		"unpause": {Type: "stack_status", Status: "running", StackID: u2StackID},
		"stop":    {Type: "stack_status", Status: "stopped", StackID: u2StackID},
		"die":     {Type: "stack_status", Status: "stopped", StackID: u2StackID},
		"kill":    {Type: "stack_status", Status: "stopped", StackID: u2StackID},
		"pause":   {Type: "stack_status", Status: "paused", StackID: u2StackID},
		"create":  {Type: "container_event", StackID: u2StackID},
		"rename":  {Type: "container_event", StackID: u2StackID},
		"destroy": {Type: "container_event", StackID: u2StackID},
	}

	for _, action := range u2AllActions {
		ev, ok := s.stackEventFor(action, u2Ctr, u2Project, ts)
		if !ok {
			t.Fatalf("action %q: a container with a known stack must always be emitted", action)
		}
		w := want[action]
		w.ContainerID = u2Ctr
		w.Event = action
		w.Timestamp = ts
		if ev != w {
			t.Fatalf("action %q:\n got %+v\nwant %+v", action, ev, w)
		}
	}
}

// TestStackEventForLabellessContainerIsUnchanged is the other
// behaviour-preservation control. A container with no compose project label
// never reached the stack lookup at all, so the fix must not have moved it: the
// four lifecycle actions are emitted unassociated, the other six are dropped.
//
// It also disproves the framing this bead was filed under ("a standalone
// container ... the normal case"). A standalone container has no
// com.docker.compose.project label, so it took this branch and was already
// emitted; the case that was actually being dropped is a container that HAS the
// label but whose project has no stacks row.
func TestStackEventForLabellessContainerIsUnchanged(t *testing.T) {
	_ = u2CaptureLogs(t)
	// db is deliberately non-nil so a mutant that reached the lookup for a
	// label-less container would fault visibly rather than pass.
	s := &MonitorService{db: u2StubDB{err: errors.New("this lookup must never happen")}}
	ts := time.Unix(1700000000, 0)

	emitted := map[string]bool{"start": true, "stop": true, "die": true, "destroy": true}

	for _, action := range u2AllActions {
		ev, ok := s.stackEventFor(action, u2Ctr, "", ts)
		if ok != emitted[action] {
			t.Fatalf("action %q with no compose label: emitted = %v, want %v", action, ok, emitted[action])
		}
		if !ok {
			continue
		}
		want := models.StackEvent{
			Type:        "container_event",
			StackID:     "",
			ContainerID: u2Ctr,
			Event:       action,
			Timestamp:   ts,
		}
		if ev != want {
			t.Fatalf("action %q:\n got %+v\nwant %+v", action, ev, want)
		}
	}
}

// TestStackEventForWithoutADatabaseIsUnchanged pins the last pre-existing path:
// a MonitorService built by NewMonitorService has no db, and reports the compose
// project name as the stack id.
func TestStackEventForWithoutADatabaseIsUnchanged(t *testing.T) {
	_ = u2CaptureLogs(t)
	s := &MonitorService{}
	ts := time.Unix(1700000000, 0)

	ev, ok := s.stackEventFor("start", u2Ctr, u2Project, ts)
	if !ok {
		t.Fatal("no database: the event must still be emitted")
	}
	if ev.StackID != u2Project {
		t.Fatalf("no database: StackID = %q, want the project name %q", ev.StackID, u2Project)
	}
	if ev.Type != "stack_status" || ev.Status != "running" {
		t.Fatalf("no database: want a running stack_status, got %+v", ev)
	}
}
