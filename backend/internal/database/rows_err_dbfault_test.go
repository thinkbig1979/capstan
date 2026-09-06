package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-chmv. Sixteen `for rows.Next()` loops in this package never called
// rows.Err(), so a driver fault part-way through iteration ended the loop
// exactly the way exhaustion does and the function returned a SHORT slice with
// a nil error — "I could not find out" rendered as "this is everything".
//
// A dropped table cannot reproduce that: it fails at Query, which every one of
// these functions already checks, so such a fixture passes against the UNFIXED
// code and proves nothing — the trap retention_dbfault_test.go:21-26 names. The
// fault has to land BETWEEN rows, which is only reachable at the driver layer,
// hence the wrapper below rather than a SQL-level fixture.

// errInjectedRowsFault is what the wrapped driver returns from Rows.Next once
// the armed row budget is spent. It is deliberately not io.EOF: io.EOF is how a
// driver signals normal exhaustion, and returning it would simulate a short but
// HEALTHY result set, which is not the defect under test.
var errInjectedRowsFault = errors.New("injected driver fault mid-iteration")

// rowsFault is the arming state for the wrapped driver. It is matched on the
// SQL text so exactly one read path faults while every other statement — the
// migrations, the seeding INSERTs, the settings lookups, the neighbouring
// listings — keeps working. That discrimination is the point: a fixture that
// breaks the whole database cannot distinguish "this function refused" from
// "nothing worked". TestRowsFaultFixture_BreaksOnlyTheMatchedQuery is the arm
// that proves the fixture has that property instead of asserting it here.
var rowsFault struct {
	mu    sync.Mutex
	match string // substring of the SQL that should fault; "" means disarmed
	rows  int    // successful Next calls to allow before faulting
}

// armRowsFault makes the next query whose SQL contains match deliver rows rows
// and then fail. It disarms at test cleanup, so a fault can never leak into a
// sibling test.
func armRowsFault(t *testing.T, match string, rows int) {
	t.Helper()
	rowsFault.mu.Lock()
	rowsFault.match, rowsFault.rows = match, rows
	rowsFault.mu.Unlock()
	t.Cleanup(disarmRowsFault)
}

func disarmRowsFault() {
	rowsFault.mu.Lock()
	rowsFault.match, rowsFault.rows = "", 0
	rowsFault.mu.Unlock()
}

// rowsBudgetFor reports how many rows the given query may deliver before
// faulting, or -1 when this query is not the armed one.
func rowsBudgetFor(query string) int {
	rowsFault.mu.Lock()
	defer rowsFault.mu.Unlock()
	if rowsFault.match == "" || !strings.Contains(query, rowsFault.match) {
		return -1
	}
	return rowsFault.rows
}

// faultDriver wraps the real modernc.org/sqlite driver. Every statement is
// prepared through it, so a Tx query (MigrateStackIDsToRootPrefixed uses
// tx.Query) is wrapped exactly like a *sql.DB query.
type faultDriver struct{ inner driver.Driver }

func (d faultDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return &faultConn{Conn: c}, nil
}

// faultConn deliberately does NOT implement driver.QueryerContext or
// driver.ExecerContext. database/sql only takes the direct Queryer path when
// the connection offers it, so omitting them forces every statement through
// PrepareContext below and therefore through faultStmt. Adding them later would
// silently route queries around the injector.
type faultConn struct{ driver.Conn }

func (c *faultConn) Prepare(query string) (driver.Stmt, error) {
	s, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &faultStmt{Stmt: s, query: query}, nil
}

func (c *faultConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	pc, ok := c.Conn.(driver.ConnPrepareContext)
	if !ok {
		return c.Prepare(query)
	}
	s, err := pc.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &faultStmt{Stmt: s, query: query}, nil
}

// BeginTx exists because MigrateStackIDsToRootPrefixed reads through a Tx.
// There is no legacy Begin() fallback here: the deprecated path would trip
// SA1019, and modernc.org/sqlite implements ConnBeginTx — established by
// TestMigrateStackIDsToRootPrefixed_MigratesWhenTheReadIsHealthy passing, which
// cannot open its transaction any other way.
func (c *faultConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	bc, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.New("wrapped connection does not implement driver.ConnBeginTx")
	}
	return bc.BeginTx(ctx, opts)
}

type faultStmt struct {
	driver.Stmt
	query string
}

func (s *faultStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	qc, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		// Not tested against a driver lacking this — modernc.org/sqlite
		// implements it, which the passing tests in this file establish. A
		// future driver swap that does not would surface here rather than
		// silently disarming the injector.
		return nil, errors.New("wrapped statement does not implement driver.StmtQueryContext")
	}
	rows, err := qc.QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	budget := rowsBudgetFor(s.query)
	if budget < 0 {
		return rows, nil
	}
	return &faultRows{Rows: rows, remaining: budget}, nil
}

func (s *faultStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	ec, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, errors.New("wrapped statement does not implement driver.StmtExecContext")
	}
	return ec.ExecContext(ctx, args)
}

type faultRows struct {
	driver.Rows
	remaining int
}

func (r *faultRows) Next(dest []driver.Value) error {
	if r.remaining <= 0 {
		return errInjectedRowsFault
	}
	r.remaining--
	return r.Rows.Next(dest)
}

const faultDriverName = "sqlite_rowsfault"

var registerFaultDriverOnce sync.Once

// newRowsFaultTestDB mirrors NewWithEncryptor's DSN and single-connection
// setting for ":memory:" (database.go:98-116) but opens through faultDriver.
// It cannot call NewWithEncryptor itself, which hardcodes sql.Open("sqlite").
func newRowsFaultTestDB(t *testing.T) *DB {
	t.Helper()
	registerFaultDriverOnce.Do(func() {
		probe, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatalf("open probe handle to reach the real driver: %v", err)
		}
		sql.Register(faultDriverName, faultDriver{inner: probe.Driver()})
		probe.Close()
	})

	sqlDB, err := sql.Open(faultDriverName, ":memory:?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", faultDriverName, err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	d := &DB{db: sqlDB, encryptor: noEncryptor{}}
	t.Cleanup(func() { d.Close() })
	if err := RunMigrations(d); err != nil {
		t.Fatalf("RunMigrations on the fault-driver database: %v", err)
	}
	return d
}

// seedThreeStacks inserts one directory and three stacks under it. Three is the
// smallest count that lets a fault land strictly BETWEEN rows: with a budget of
// 1, row one is delivered and rows two and three are lost, so a short slice and
// a full slice are different values.
func seedThreeStacks(t *testing.T, d *DB) {
	t.Helper()
	if err := d.UpsertDirectory(models.Directory{Path: "/srv/stacks/app", Name: "app", RootDir: "/srv/stacks"}); err != nil {
		t.Fatalf("UpsertDirectory: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := d.UpsertStack(models.Stack{
			ID:          id,
			Directory:   "/srv/stacks/app",
			ComposeFile: id + ".yml",
			ProjectName: id,
			Status:      "unknown",
		}); err != nil {
			t.Fatalf("UpsertStack(%s): %v", id, err)
		}
	}
}

// TestListStacks_RefusesTruncatedResultSet is the bead's core assertion, with
// both sides on the same instrument and the same database: the healthy read
// must return all three rows and no error, and the SAME call under a
// mid-iteration driver fault must return an error. A ListStacks that had simply
// stopped working would fail the first arm, so "returns an error" here cannot
// be satisfied by a broken function.
//
// Before the fix this test failed on its second arm's assertion — not on a
// build error — with ListStacks returning ([1 stack], nil).
func TestListStacks_RefusesTruncatedResultSet(t *testing.T) {
	db := newRowsFaultTestDB(t)
	seedThreeStacks(t, db)

	// Healthy arm.
	stacks, err := db.ListStacks()
	if err != nil {
		t.Fatalf("ListStacks on a healthy database: %v", err)
	}
	if len(stacks) != 3 {
		t.Fatalf("ListStacks returned %d stacks on a healthy database, want 3", len(stacks))
	}

	// Fault arm: the same query, now failing after its first row.
	armRowsFault(t, "FROM stacks ORDER BY project_name", 1)

	stacks, err = db.ListStacks()
	if err == nil {
		t.Fatalf("ListStacks returned (%d stacks, nil) after a mid-iteration driver fault — a truncated listing reported as complete is the defect", len(stacks))
	}
	if !errors.Is(err, errInjectedRowsFault) {
		t.Errorf("ListStacks returned %v, want the injected driver fault wrapped — a different error means the fault is not the thing being reported", err)
	}
	if len(stacks) != 0 {
		t.Errorf("ListStacks returned %d stacks alongside the error; a caller that logs and continues must not receive a partial listing", len(stacks))
	}
}

// TestRowsFaultFixture_BreaksOnlyTheMatchedQuery proves the injector is
// discriminating rather than globally destructive. With the stacks listing
// armed, an unrelated listing on the same connection must still succeed and
// still return its row. Without this arm, the error in the test above would be
// indistinguishable from "the database was too broken to read anything", which
// is the shape that passes against unfixed code.
func TestRowsFaultFixture_BreaksOnlyTheMatchedQuery(t *testing.T) {
	db := newRowsFaultTestDB(t)
	seedThreeStacks(t, db)

	armRowsFault(t, "FROM stacks ORDER BY project_name", 1)

	dirs, err := db.ListDirectories()
	if err != nil {
		t.Fatalf("ListDirectories under a fault armed on a different query: %v — the fixture is breaking more than the query it names", err)
	}
	if len(dirs) != 1 {
		t.Errorf("ListDirectories returned %d directories, want 1 — the fixture is breaking more than the query it names", len(dirs))
	}
	if _, err := db.GetSetting("stack_id_version"); err != nil {
		t.Errorf("GetSetting under a fault armed on a different query: %v — the fixture is breaking more than the query it names", err)
	}
}

// TestMigrateStackIDsToRootPrefixed_RefusesTruncatedStackRead pins the site that
// actually justifies this bead. MigrateStackIDsToRootPrefixed reads every stack
// into `mappings` (migrations.go:753), rewrites each one's ID, then stamps
// stack_id_version='2' (:791) and commits (:795). The guard at :711 short-
// circuits on version=="2", so the migration never runs again.
//
// A driver fault mid-iteration truncated `mappings` and the version stamp
// committed anyway: a silent, irreversible, half-applied schema migration, with
// the unmigrated stacks left on unprefixed IDs forever. This test drives that
// exact fault and requires the whole transaction to be refused.
func TestMigrateStackIDsToRootPrefixed_RefusesTruncatedStackRead(t *testing.T) {
	db := newRowsFaultTestDB(t)
	seedThreeStacks(t, db)

	armRowsFault(t, "SELECT id, directory FROM stacks", 1)

	err := db.MigrateStackIDsToRootPrefixed("/srv/stacks")
	if err == nil {
		t.Fatalf("MigrateStackIDsToRootPrefixed returned nil after reading only 1 of 3 stacks — it half-migrated and stamped the version")
	}
	if !errors.Is(err, errInjectedRowsFault) {
		t.Errorf("MigrateStackIDsToRootPrefixed returned %v, want the injected driver fault wrapped", err)
	}

	disarmRowsFault()

	// The version stamp must NOT have committed, or the migration is now
	// permanently skipped with two stacks left unmigrated.
	version, err := db.GetSetting("stack_id_version")
	if err != nil {
		t.Fatalf("GetSetting(stack_id_version): %v", err)
	}
	if version == "2" {
		t.Errorf("stack_id_version committed as %q after a refused migration — the guard at migrations.go:711 will now skip it forever", version)
	}

	// And no stack may carry a rewritten ID: the transaction rolled back whole.
	stacks, err := db.ListStacks()
	if err != nil {
		t.Fatalf("ListStacks after the refused migration: %v", err)
	}
	if len(stacks) != 3 {
		t.Fatalf("ListStacks returned %d stacks after a refused migration, want 3", len(stacks))
	}
	for _, s := range stacks {
		if strings.Contains(s.ID, "~") {
			t.Errorf("stack %q was migrated despite the refusal — the transaction did not roll back whole", s.ID)
		}
	}
}

// TestMigrateStackIDsToRootPrefixed_MigratesWhenTheReadIsHealthy is the
// must-pass side of the arm above, on the same instrument. Without it,
// "refuses" would be satisfiable by a migration that had simply stopped
// migrating.
func TestMigrateStackIDsToRootPrefixed_MigratesWhenTheReadIsHealthy(t *testing.T) {
	db := newRowsFaultTestDB(t)
	seedThreeStacks(t, db)

	if err := db.MigrateStackIDsToRootPrefixed("/srv/stacks"); err != nil {
		t.Fatalf("MigrateStackIDsToRootPrefixed on a healthy database: %v", err)
	}

	version, err := db.GetSetting("stack_id_version")
	if err != nil {
		t.Fatalf("GetSetting(stack_id_version): %v", err)
	}
	if version != "2" {
		t.Errorf("stack_id_version is %q after a healthy migration, want \"2\"", version)
	}

	stacks, err := db.ListStacks()
	if err != nil {
		t.Fatalf("ListStacks: %v", err)
	}
	if len(stacks) != 3 {
		t.Fatalf("ListStacks returned %d stacks, want 3", len(stacks))
	}
	for _, s := range stacks {
		if !strings.HasPrefix(s.ID, "stacks~") {
			t.Errorf("stack %q was not migrated to the root-prefixed form", s.ID)
		}
	}
}
