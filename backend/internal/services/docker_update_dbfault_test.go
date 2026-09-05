package services

import (
	"bytes"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-g482. GetStackByProjectName returns the bare Scan error
// (database/stacks.go:127-138), so `err == nil` could not tell "this project
// name is not a stack" — a legitimately standalone container — from "the stacks
// table cannot be read". All four call sites answered a DB fault with "not a
// compose stack", and at UpdateContainer/UpdateContainerStreaming that branch
// calls the STANDALONE apply path, which RECREATES the container: a
// compose-managed container rebuilt with the wrong strategy, silently, on the
// strength of a read that failed.
//
// The instrument is resolveUpdateStrategy, the unexported decision the two apply
// paths now switch on. It is the tightest available unit: DockerService.client
// is a concrete *client.Client (the `client *client.Client` field,
// docker.go:55), not an interface, so
// UpdateContainer and UpdateContainerStreaming cannot be driven without a live
// daemon — their end-to-end coverage is in internal/integrationtest, behind the
// `integration` build tag. Every test here therefore also pins the PREMISE
// (TestGetStackByProjectName_PremiseFaultIsNotErrNoRows) against a real
// *database.DB, so the discrimination is not tested against an assumption about
// what the database layer returns.

const (
	g482ProjectKnown   = "g482-known-compose-project"
	g482ProjectUnknown = "g482-project-that-is-not-a-stack"
	g482StackID        = "g482-stack-id"
	g482StorageKey     = "g482-storage-key-0123456789abcdef"
)

// g482HealthyDB returns a fully migrated database holding exactly one stack,
// whose project name is g482ProjectKnown.
func g482HealthyDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrationsAndEncryptor(t.TempDir(), NewTokenEncryptorOrDefault(g482StorageKey, ""))
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	g482SeedStack(t, db)
	return db
}

// g482SeedStack inserts the one stack both fixtures hold. The directories row
// comes first: stacks.directory is a FOREIGN KEY onto directories(path)
// (migrations.go:151), so a stack cannot be inserted without it.
func g482SeedStack(t *testing.T, db *database.DB) {
	t.Helper()
	dir := t.TempDir()
	if err := db.UpsertDirectory(models.Directory{
		Path:      dir,
		Name:      "g482",
		ScannedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed directory: %v", err)
	}
	if err := db.UpsertStack(models.Stack{
		ID:          g482StackID,
		Directory:   dir,
		ComposeFile: "docker-compose.yml",
		ProjectName: g482ProjectKnown,
		Status:      "running",
	}); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

// g482ClosedDB seeds the same stack and then closes the connection, so every
// later read fails with a driver error rather than sql.ErrNoRows. This is the
// closedDBWithSettings shape (backup_config_dbfault_test.go:57), and unlike the
// closed-database instrument rejected in agent-os-obgr nothing self-protects
// here: GetStackByProjectName has no nil guard and goes straight to QueryRow.
func g482ClosedDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.NewWithMigrationsAndEncryptor(t.TempDir(), NewTokenEncryptorOrDefault(g482StorageKey, ""))
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	g482SeedStack(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close db to induce failure: %v", err)
	}
	return db
}

// g482CaptureLogs redirects the default slog logger — the one docker_update.go
// and docker.go use — into a buffer for the duration of the test.
//
// The tests that use it are deliberately NOT parallel, and every assertion
// counts only lines carrying the message under test, so a concurrent test
// logging into the same buffer cannot change a verdict.
func g482CaptureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func g482CountLines(logs *bytes.Buffer, substr string) int {
	n := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

// TestGetStackByProjectName_PremiseFaultIsNotErrNoRows pins the premise the whole
// fix rests on, two-sided on ONE instrument: on a healthy database an unknown
// project name is sql.ErrNoRows, and on a faulty database the SAME query is a
// different, non-ErrNoRows error — while the pre-fix predicate `err == nil` is
// false in both cases and so cannot separate them.
func TestGetStackByProjectName_PremiseFaultIsNotErrNoRows(t *testing.T) {
	healthy := g482HealthyDB(t)
	faulty := g482ClosedDB(t)

	_, absentErr := healthy.GetStackByProjectName(g482ProjectUnknown)
	if !errors.Is(absentErr, sql.ErrNoRows) {
		t.Fatalf("healthy db, unknown project: want sql.ErrNoRows, got %v", absentErr)
	}

	_, faultErr := faulty.GetStackByProjectName(g482ProjectUnknown)
	if faultErr == nil {
		t.Fatal("closed db: want an error, got nil — the fault instrument does not fault")
	}
	if errors.Is(faultErr, sql.ErrNoRows) {
		t.Fatalf("closed db: fault must NOT be sql.ErrNoRows, got %v", faultErr)
	}

	// The pre-fix predicate, verbatim. It answers both cases the same way, which
	// is the defect.
	if (absentErr == nil) != (faultErr == nil) {
		t.Fatal("premise broken: `err == nil` already separates absence from fault")
	}
}

// TestResolveUpdateStrategy_RefusesWhenStacksTableUnreadable is the failing-first
// arm: a compose-managed container updated behind a faulty DB must NOT take the
// standalone path. Pre-fix this returned updateViaStandalone, which recreates
// the container.
func TestResolveUpdateStrategy_RefusesWhenStacksTableUnreadable(t *testing.T) {
	db := g482ClosedDB(t)

	strategy, stack, err := resolveUpdateStrategy(db, g482ProjectKnown, "web")

	if strategy == updateViaStandalone {
		t.Fatal("a compose-managed container behind an unreadable stacks table took the STANDALONE path — it would be recreated with the wrong strategy (agent-os-g482)")
	}
	if strategy != updateRefused {
		t.Fatalf("want updateRefused, got strategy %d", strategy)
	}
	if stack != nil {
		t.Fatalf("a refusal must carry no stack, got %+v", stack)
	}
	if err == nil {
		t.Fatal("a refusal must carry its cause, got nil error")
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("absence must never be reported as a fault, got %v", err)
	}
	if !strings.Contains(err.Error(), g482ProjectKnown) {
		t.Fatalf("cause must name the project it could not resolve, got %v", err)
	}
}

// TestLogRefusedUpdate_EmitsOneDiscriminatedErrorLine covers the log half of the
// acceptance criterion. logRefusedUpdate is the line both apply paths emit; the
// paths themselves need a live daemon (see the file comment).
func TestLogRefusedUpdate_EmitsOneDiscriminatedErrorLine(t *testing.T) {
	logs := g482CaptureLogs(t)

	_, _, cause := resolveUpdateStrategy(g482ClosedDB(t), g482ProjectKnown, "web")
	if cause == nil {
		t.Fatal("setup: expected a cause to log")
	}
	logRefusedUpdate("container-abc", g482ProjectKnown, "web", cause)

	const msg = "Refusing container update"
	if got := g482CountLines(logs, msg); got != 1 {
		t.Fatalf("want exactly 1 refusal line, got %d\n%s", got, logs.String())
	}
	out := logs.String()
	for _, want := range []string{"level=ERROR", "cause=", g482ProjectKnown, "container-abc"} {
		if !strings.Contains(out, want) {
			t.Fatalf("refusal line missing %q:\n%s", want, out)
		}
	}
}

// TestResolveUpdateStrategy_ControlKnownProjectTakesComposePath is control 1:
// healthy DB, known project name, today's compose path unchanged.
func TestResolveUpdateStrategy_ControlKnownProjectTakesComposePath(t *testing.T) {
	logs := g482CaptureLogs(t)
	db := g482HealthyDB(t)

	strategy, stack, err := resolveUpdateStrategy(db, g482ProjectKnown, "web")

	if err != nil {
		t.Fatalf("healthy db, known project: unexpected error %v", err)
	}
	if strategy != updateViaCompose {
		t.Fatalf("want updateViaCompose, got strategy %d", strategy)
	}
	if stack == nil || stack.ID != g482StackID {
		t.Fatalf("want the seeded stack %q, got %+v", g482StackID, stack)
	}
	if n := g482CountLines(logs, "level=ERROR"); n != 0 {
		t.Fatalf("healthy path must log no ERROR, got %d line(s):\n%s", n, logs.String())
	}
}

// TestResolveUpdateStrategy_ControlUnknownProjectTakesStandalonePath is control
// 2: healthy DB, genuinely unknown project name, today's standalone path
// unchanged and no ERROR line. This is the case the fix must NOT convert into a
// refusal — a container carrying stale compose labels for a stack that no longer
// exists is still updatable.
func TestResolveUpdateStrategy_ControlUnknownProjectTakesStandalonePath(t *testing.T) {
	logs := g482CaptureLogs(t)
	db := g482HealthyDB(t)

	strategy, stack, err := resolveUpdateStrategy(db, g482ProjectUnknown, "web")

	if err != nil {
		t.Fatalf("healthy db, unknown project: want nil error, got %v", err)
	}
	if strategy != updateViaStandalone {
		t.Fatalf("want updateViaStandalone, got strategy %d", strategy)
	}
	if stack != nil {
		t.Fatalf("want no stack, got %+v", stack)
	}
	if n := g482CountLines(logs, "level=ERROR"); n != 0 {
		t.Fatalf("an absent project is not a fault and must log no ERROR, got %d line(s):\n%s", n, logs.String())
	}
}

// TestResolveUpdateStrategy_GuardsUnchanged pins the three pre-existing guards
// the switch replaced: no db, no project label, no service label. Each is
// standalone, with no lookup and no error — byte-for-byte today's behaviour.
func TestResolveUpdateStrategy_GuardsUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		db      DashboardDB
		project string
		service string
	}{
		{"nil db", nil, g482ProjectKnown, "web"},
		{"no compose project label", g482ClosedDB(t), "", "web"},
		{"no compose service label", g482ClosedDB(t), g482ProjectKnown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			strategy, stack, err := resolveUpdateStrategy(tc.db, tc.project, tc.service)
			if err != nil {
				t.Fatalf("want nil error, got %v", err)
			}
			if strategy != updateViaStandalone {
				t.Fatalf("want updateViaStandalone, got strategy %d", strategy)
			}
			if stack != nil {
				t.Fatalf("want no stack, got %+v", stack)
			}
		})
	}
}

// TestLookupStackByProject_AbsenceIsNotAFault covers the read sites
// (CheckForUpdates, GetAllContainersWithDetails), which default rather than
// refuse but must still discriminate: an absent row is (nil, nil), a fault is an
// error carrying its cause.
func TestLookupStackByProject_AbsenceIsNotAFault(t *testing.T) {
	healthy := g482HealthyDB(t)

	stack, err := lookupStackByProject(healthy, g482ProjectUnknown)
	if err != nil || stack != nil {
		t.Fatalf("absent project: want (nil, nil), got (%+v, %v)", stack, err)
	}

	stack, err = lookupStackByProject(healthy, g482ProjectKnown)
	if err != nil || stack == nil || stack.ID != g482StackID {
		t.Fatalf("known project: want the seeded stack, got (%+v, %v)", stack, err)
	}

	stack, err = lookupStackByProject(g482ClosedDB(t), g482ProjectKnown)
	if err == nil {
		t.Fatal("unreadable stacks table: want an error, got nil — this is the softening the bead is about")
	}
	if stack != nil {
		t.Fatalf("a fault must carry no stack, got %+v", stack)
	}

	stack, err = lookupStackByProject(nil, g482ProjectKnown)
	if err != nil || stack != nil {
		t.Fatalf("nil db: want (nil, nil), got (%+v, %v)", stack, err)
	}
}
