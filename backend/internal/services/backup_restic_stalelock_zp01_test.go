package services

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// staleLockRunner answers restic calls from a script, one result per call in
// order, and records each call's argv and whether its context had a deadline.
// It wraps a result the way execRunner.Run does ("restic exited: %w"), so the
// exit code reaches isExitCode through the same chain as in production.
type staleLockRunner struct {
	mu        sync.Mutex
	results   []error
	calls     [][]string
	deadlines []time.Duration // remaining time at the call; 0 = no deadline
}

func (r *staleLockRunner) Run(ctx context.Context, name string, args []string, _ []string, _ chan<- StreamLine) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	var left time.Duration
	if d, ok := ctx.Deadline(); ok {
		left = time.Until(d)
	}
	r.deadlines = append(r.deadlines, left)
	if len(r.results) == 0 {
		return nil
	}
	res := r.results[0]
	r.results = r.results[1:]
	if res == nil {
		return nil
	}
	return fmt.Errorf("%s exited: %w", name, res)
}

func (r *staleLockRunner) Output(context.Context, string, []string, []string) ([]byte, error) {
	return []byte("null"), nil
}

func (r *staleLockRunner) subcommands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c[0]
	}
	return out
}

// lockOps are the restic calls that fail with exit 11 on a stale lock and run
// under the global backup lock: forget, prune and check need the exclusive
// lock, and backup is refused by a stale exclusive one.
var lockOps = []struct {
	sub string
	run func(m *ResticManager) error
}{
	{"forget", func(m *ResticManager) error { return m.ApplyRetention(context.Background(), "app", nil) }},
	{"prune", func(m *ResticManager) error { return m.Prune(context.Background(), false, nil) }},
	{"check", func(m *ResticManager) error { return m.VerifyRepositoryData(context.Background(), "", nil) }},
	{"backup", func(m *ResticManager) error {
		out := make(chan StreamLine, 64)
		_, err := m.Backup(context.Background(), "/srv/app", []string{"app"}, out)
		return err
	}},
	// restore takes a non-exclusive lock, which a stale exclusive one refuses
	// with exit 11 (OBSERVED, restic 0.18.0); runRestore holds the global lock
	// (agent-os-hxuz).
	{"restore", func(m *ResticManager) error {
		return m.Restore(context.Background(), "abc123", "/srv/app", "/srv/app", nil, nil)
	}},
}

// TestResticLockOps_StaleLockIsUnlockedAndRetriedOnce pins agent-os-zp01: a
// stale lock left by an interrupted restic made forget exit 11 on every
// backup, and retention silently never ran. On exit 11 the op now runs
// `restic unlock` (which removes only locks restic judges stale) and retries
// once.
func TestResticLockOps_StaleLockIsUnlockedAndRetriedOnce(t *testing.T) {
	t.Parallel()
	for _, op := range lockOps {
		t.Run(op.sub, func(t *testing.T) {
			t.Parallel()
			r := &staleLockRunner{results: []error{fakeExitError{code: 11}, nil, nil}}
			m := newResticManagerWithRunner(testBackupConfig(), r, nil)

			require.NoError(t, op.run(m))
			assert.Equal(t, []string{op.sub, "unlock", op.sub}, r.subcommands())
			require.Len(t, r.calls, 3)
			assert.Equal(t, []string{"unlock"}, r.calls[1],
				"plain unlock only: --remove-all would also remove a live process's lock")
		})
	}
}

// TestResticLockOps_LiveLockIsReportedNotLooped: a lock held by a live process
// survives `restic unlock`, so the retry fails with 11 again. That is reported
// as a lock held by another process, after exactly one retry.
func TestResticLockOps_LiveLockIsReportedNotLooped(t *testing.T) {
	t.Parallel()
	for _, op := range lockOps {
		t.Run(op.sub, func(t *testing.T) {
			t.Parallel()
			r := &staleLockRunner{results: []error{fakeExitError{code: 11}, nil, fakeExitError{code: 11}}}
			m := newResticManagerWithRunner(testBackupConfig(), r, nil)

			err := op.run(m)
			require.Error(t, err)
			assert.True(t, isExitCode(err, 11), "the exit code stays in the chain: %v", err)
			assert.Contains(t, err.Error(), "locked by another restic process")
			assert.Equal(t, []string{op.sub, "unlock", op.sub}, r.subcommands(), "one retry, never a loop")
		})
	}
}

// TestResticLockOps_OtherFailuresAreNotRetried: only exit 11 means "locked";
// any other failure is returned as it was, with no unlock.
func TestResticLockOps_OtherFailuresAreNotRetried(t *testing.T) {
	t.Parallel()
	for _, op := range lockOps {
		t.Run(op.sub, func(t *testing.T) {
			t.Parallel()
			r := &staleLockRunner{results: []error{fakeExitError{code: 1}}}
			m := newResticManagerWithRunner(testBackupConfig(), r, nil)

			err := op.run(m)
			require.Error(t, err)
			assert.True(t, isExitCode(err, 1))
			assert.Equal(t, []string{op.sub}, r.subcommands())
		})
	}
}

// TestResticLockOps_UnlockIsBoundedAndItsFailureReported: the unlock runs under
// its own short deadline (safe-defaults rule 1), and if it fails the op is not
// retried and both errors are reported.
func TestResticLockOps_UnlockIsBoundedAndItsFailureReported(t *testing.T) {
	t.Parallel()
	unlockErr := errors.New("permission denied")
	r := &staleLockRunner{results: []error{fakeExitError{code: 11}, unlockErr}}
	m := newResticManagerWithRunner(testBackupConfig(), r, nil)

	err := m.ApplyRetention(context.Background(), "app", nil)
	require.Error(t, err)
	assert.Equal(t, []string{"forget", "unlock"}, r.subcommands())
	assert.True(t, isExitCode(err, 11), "the original lock failure stays in the chain: %v", err)
	assert.ErrorIs(t, err, unlockErr)

	require.Len(t, r.deadlines, 2)
	assert.Greater(t, r.deadlines[1], time.Duration(0), "unlock must run with a deadline")
	assert.LessOrEqual(t, r.deadlines[1], 2*time.Minute, "unlock deadline is the short unlock bound")
}

// lockingSubcommands are the restic subcommands that fail with exit 11 on a
// stale lock while the global backup lock is held, so their calls must go
// through runLocking.
var lockingSubcommands = map[string]bool{"forget": true, "prune": true, "check": true, "backup": true, "restore": true}

// TestResticLockingCallsGoThroughRunLocking keeps every restic call that
// builds a forget/prune/check/backup/restore argv on runLocking (agent-os-zp01,
// agent-os-hxuz): a function that builds one must call runLocking and must not
// call the runner directly. Read through go:embed (packageSources), so a `go test -overlay`
// mutant of backup_restic.go is what this sees.
func TestResticLockingCallsGoThroughRunLocking(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			buildsLockingArgv, callsRunLocking := false, false
			var directRuns []token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					if len(x.Elts) > 0 {
						if lit, ok := x.Elts[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							if sub, err := strconv.Unquote(lit.Value); err == nil && lockingSubcommands[sub] {
								buildsLockingArgv = true
							}
						}
					}
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if sel.Sel.Name == "runLocking" {
						callsRunLocking = true
					}
					if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "runner" &&
						(sel.Sel.Name == "Run" || sel.Sel.Name == "Output") {
						directRuns = append(directRuns, x.Pos())
					}
				}
				return true
			})
			if !buildsLockingArgv {
				continue
			}
			found[fn.Name.Name] = true
			if !callsRunLocking {
				t.Errorf("%s: %s builds a restic forget/prune/check/backup/restore argv but does not call runLocking (agent-os-zp01)",
					fset.Position(fn.Pos()), fn.Name.Name)
			}
			for _, pos := range directRuns {
				t.Errorf("%s: %s calls the runner directly; route the restic call through runLocking (agent-os-zp01)",
					fset.Position(pos), fn.Name.Name)
			}
		}
	}

	// Positive control: the guard must see the five known sites, or a zero
	// above means it matched nothing.
	for _, want := range []string{"ApplyRetention", "Prune", "VerifyRepositoryData", "Backup", "Restore"} {
		assert.True(t, found[want], "guard did not see %s: its argv detection is blind", want)
	}
}

// TestRunBackup_StaleLockRetryWithNilOutDoesNotHang: RunBackup accepts a nil
// out (stream drops lines), and Backup forwards everything sent on its raw
// channel to out with a blocking send. A notice written to raw by the retry
// path therefore hung the run forever on a nil out. The database snapshot is
// the first restic backup of a run, so its exit 11 takes that path.
func TestRunBackup_StaleLockRetryWithNilOutDoesNotHang(t *testing.T) {
	t.Parallel()
	db := newBackupTestDB(t)
	r := &staleLockRunner{results: []error{fakeExitError{code: 11}}} // the database backup, then all succeed
	svc := buildSvc(t, db, &fakeDocker{statusStr: "running"}, r, r)
	seedStack(t, db, "app", "hot")

	done := make(chan error, 1)
	go func() {
		_, err := svc.RunBackup(context.Background(), nil, false, "manual", nil)
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("RunBackup with a nil out hung on the stale-lock retry")
	}
	subs := r.subcommands()
	require.GreaterOrEqual(t, len(subs), 3)
	assert.Equal(t, []string{"backup", "unlock", "backup"}, subs[:3])
}
