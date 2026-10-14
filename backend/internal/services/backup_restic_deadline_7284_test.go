package services

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- agent-os-7284: every restic child process runs under an operation
// deadline and reports a timeout as one (safe-defaults rule 1). ---

// childRunHelpers start a child process for their caller and take the
// caller's context as is: the deadline belongs to the caller, so the guard
// checks their callers instead of them.
var childRunHelpers = map[string]bool{"runLocking": true, "runDrained": true}

// deadlinePending names functions that start a child process without
// withCommandDeadline + timeoutError and are tracked for conversion by a
// follow-up bead (the rclone layer). The guard fails when an entry is
// converted or removed, so the list only shrinks.
var deadlinePending = map[string]bool{
	"TestConnectivity":   true,
	"probeRestoreSource": true,
	"remoteHasSnapshots": true,
	"Sync":               true,
	"RestoreRepo":        true,
}

// startsChild reports whether call runs a child process through the
// command runner: <anything>.runner.Run/Output or runner.Run/Output (the
// receiver is not pinned, agent-os-iz9w), or one of childRunHelpers.
func startsChild(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return childRunHelpers[fn.Name]
	case *ast.SelectorExpr:
		if childRunHelpers[fn.Sel.Name] {
			return true
		}
		if fn.Sel.Name != "Run" && fn.Sel.Name != "Output" {
			return false
		}
		switch x := fn.X.(type) {
		case *ast.Ident:
			return x.Name == "runner"
		case *ast.SelectorExpr:
			return x.Sel.Name == "runner"
		}
	}
	return false
}

func calledName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// TestChildProcessCallsRunUnderACommandDeadline keeps every function in this
// package that starts a child process through the command runner on
// withCommandDeadline and timeoutError. Read through go:embed
// (packageSources), so a `go test -overlay` mutant is what this sees.
func TestChildProcessCallsRunUnderACommandDeadline(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	found := map[string]bool{}
	var offHelper []string
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
			if !ok || fn.Body == nil || childRunHelpers[fn.Name.Name] {
				continue
			}
			starts, deadline, reported := false, false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if startsChild(call) {
					starts = true
				}
				switch calledName(call) {
				case "withCommandDeadline", "commandWithDeadline":
					deadline = true
				case "timeoutError":
					reported = true
				}
				return true
			})
			if !starts {
				continue
			}
			found[fn.Name.Name] = true
			compliant := deadline && reported
			switch {
			case deadlinePending[fn.Name.Name] && compliant:
				t.Errorf("%s: %s is on withCommandDeadline + timeoutError now; remove it from deadlinePending",
					fset.Position(fn.Pos()), fn.Name.Name)
			case deadlinePending[fn.Name.Name]:
			case !compliant:
				offHelper = append(offHelper, fn.Name.Name)
				t.Errorf("%s: %s starts a child process without withCommandDeadline (%v) and timeoutError (%v) (agent-os-7284, safe-defaults rule 1)",
					fset.Position(fn.Pos()), fn.Name.Name, deadline, reported)
			}
		}
	}
	sort.Strings(offHelper)
	t.Logf("off the helper: %v", offHelper)

	for name := range deadlinePending {
		assert.True(t, found[name], "deadlinePending names %s but the guard did not see it start a child; remove the stale entry", name)
	}
	// Positive control: the guard must see every known restic site, or a
	// clean run above means it matched nothing.
	for _, want := range []string{
		"CheckRepository", "EnsureRepository", "Backup", "Restore", "ListSnapshots", "RestorePreview", "Stats",
		"Verify", "VerifyRepositoryData", "ApplyRetention", "Prune", "unlockStale",
	} {
		assert.True(t, found[want], "guard did not see %s start a child: its call detection is blind", want)
	}
}

// hangingOutputRunner blocks every call until its context ends, like a
// restic stuck on a network repository.
type hangingOutputRunner struct{}

func (hangingOutputRunner) Run(ctx context.Context, _ string, _ []string, _ []string, _ chan<- StreamLine) error {
	<-ctx.Done()
	return fmt.Errorf("restic exited: %w", errors.New("signal: killed"))
}

func (hangingOutputRunner) Output(ctx context.Context, _ string, _ []string, _ []string) ([]byte, error) {
	<-ctx.Done()
	return nil, fmt.Errorf("restic exited: %w", errors.New("signal: killed"))
}

func TestResticManager_HungCallTimesOutAndSaysSo(t *testing.T) {
	t.Parallel()

	ops := []struct {
		what string
		run  func(ctx context.Context, m *ResticManager) error
	}{
		{"restic backup", func(ctx context.Context, m *ResticManager) error {
			_, err := m.Backup(ctx, "/srv/app", []string{"app"}, nil)
			return err
		}},
		{"restic restore", func(ctx context.Context, m *ResticManager) error {
			return m.Restore(ctx, "abc123", "/srv/app", "/srv/app", nil, nil)
		}},
		{"restic snapshots", func(ctx context.Context, m *ResticManager) error {
			_, err := m.ListSnapshots(ctx, "", 0)
			return err
		}},
		{"restic ls", func(ctx context.Context, m *ResticManager) error { return m.RestorePreview(ctx, "abc123", nil) }},
		{"restic stats", func(ctx context.Context, m *ResticManager) error {
			_, err := m.Stats(ctx)
			return err
		}},
		{"restic snapshots", func(ctx context.Context, m *ResticManager) error { return m.CheckRepository(ctx) }},
	}
	for _, op := range ops {
		t.Run(op.what, func(t *testing.T) {
			t.Parallel()
			m := newResticManagerWithRunner(testBackupConfig(), hangingOutputRunner{}, nil)
			m.testDeadlineCap = 200 * time.Millisecond
			// The outer bound only stops a call with no deadline of its own
			// from hanging the test; it is a plain parent, not a timeout.
			outer, cancelOuter := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelOuter()
			err := op.run(outer, m)
			require.Error(t, err)
			assert.Contains(t, err.Error(), op.what+" timed out after 200ms")

			// Control: a parent cancellation is not reported as a timeout.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err = op.run(ctx, m)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "timed out")
		})
	}
}

// Backup and Restore hold the global backup lock; their bound is the long one
// already chosen for reading pack data.
func TestResticManager_BackupAndRestoreRunUnderTheLongDeadline(t *testing.T) {
	t.Parallel()
	ops := []struct {
		name string
		run  func(m *ResticManager) error
		want time.Duration
	}{
		{"backup", func(m *ResticManager) error {
			_, err := m.Backup(context.Background(), "/srv/app", []string{"app"}, nil)
			return err
		}, backupTimeout},
		{"restore", func(m *ResticManager) error {
			return m.Restore(context.Background(), "abc123", "/srv/app", "/srv/app", nil, nil)
		}, restoreTimeout},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()
			runner := &ctxRecordingRunner{}
			m := newResticManagerWithRunner(testBackupConfig(), runner, nil)
			before := time.Now()
			require.NoError(t, op.run(m))
			require.True(t, runner.hasDeadline, "restic ran with no deadline")
			assert.WithinDuration(t, before.Add(op.want), runner.deadline, time.Minute)
		})
	}
}
