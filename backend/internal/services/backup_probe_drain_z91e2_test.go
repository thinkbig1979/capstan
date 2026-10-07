package services

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"
)

// drainSettledCount polls until the goroutine count stops being above limit or
// the 2s budget ends, and returns the last count. A fixed sleep would either
// flake or hide a slow exit; the loop returns as soon as the count is low.
func drainSettledCount(limit int) int {
	deadline := time.Now().Add(2 * time.Second)
	n := runtime.NumGoroutine()
	for n > limit && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		n = runtime.NumGoroutine()
	}
	return n
}

// drainStableCount returns the goroutine count once two reads 10ms apart agree
// (bounded at 2s), so the baseline is not taken mid-exit of an earlier test's
// goroutines.
func drainStableCount() int {
	deadline := time.Now().Add(2 * time.Second)
	prev := runtime.NumGoroutine()
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	return prev
}

// TestFailedProbe_DoesNotStrandItsDrainGoroutine is agent-os-z91e.2's
// regression test. CheckRepository, EnsureRepository and TestConnectivity each
// start `for range out {}` and used to close out only after Run succeeded, so
// every failed probe left one goroutine blocked on out for good (EnsureRepository
// two: its check and its init). A repository that stays misconfigured leaked one
// per status read.
//
// Not parallel: runtime.NumGoroutine counts the whole process, and a parallel
// test's goroutines would move the baseline. Each probe is its own subtest, so a
// mutant that restores the leak at one site reddens exactly that subtest.
func TestFailedProbe_DoesNotStrandItsDrainGoroutine(t *testing.T) {
	quietLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
	const calls = 50
	// Slack for the runtime's own housekeeping goroutines. A leak is +50 or more.
	const slack = 5

	probes := []struct {
		name string
		run  func(runErr error)
	}{
		{"CheckRepository", func(runErr error) {
			m := newResticManagerWithRunner(testBackupConfig(), &fakeRunner{runErr: runErr}, quietLogger)
			_ = m.CheckRepository(context.Background()) //nolint:errcheck // the error path is what is under test; the leak, not the error, is asserted
		}},
		{"EnsureRepository", func(runErr error) {
			m := newResticManagerWithRunner(testBackupConfig(), &fakeRunner{runErr: runErr}, quietLogger)
			_ = m.EnsureRepository(context.Background()) //nolint:errcheck // same: the leak is asserted, not the error
		}},
		{"TestConnectivity", func(runErr error) {
			m := newRcloneManagerWithRunner(testBackupConfig(), &fakeRunner{runErr: runErr}, quietLogger)
			_ = m.TestConnectivity(context.Background(), "") //nolint:errcheck // same: the leak, not the error, is asserted
		}},
	}

	for _, p := range probes {
		for _, tc := range []struct {
			name   string
			runErr error
		}{
			{"failing run", errors.New("wrong password")},
			// Positive control: the same loop with a succeeding runner must also
			// end at baseline, so a counter that never settles cannot pass a
			// subtest by looking like a failure here and a leak there.
			{"succeeding run", nil},
		} {
			t.Run(p.name+"/"+tc.name, func(t *testing.T) {
				baseline := drainStableCount()
				for range calls {
					p.run(tc.runErr)
				}
				got := drainSettledCount(baseline + slack)
				t.Logf("goroutines: baseline=%d after %d calls=%d", baseline, calls, got)
				if got > baseline+slack {
					t.Errorf("goroutines = %d after %d %s calls, baseline %d: %d drain goroutines are still blocked on their channel",
						got, calls, tc.name, baseline, got-baseline)
				}
			})
		}
	}
}

// TestProbeDrain_EveryDrainGoesThroughRunDrained is agent-os-z91e.2's guard. A
// hand-written `go func() { for range out {} }()` has to remember to close out
// on every exit path, and three copies did not. runDrained is the one place that
// does. This fails on any empty `for range out` loop outside it.
//
// The scan's positive side is explicit: it must find runDrained's own drain
// exactly once, so a scan that matches nothing (a renamed channel, a changed
// shape) fails instead of passing vacuously. The sources come from the embedded
// packageSources, not from disk, so a `go test -overlay` mutant is what is
// scanned.
func TestProbeDrain_EveryDrainGoesThroughRunDrained(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	inHelper := 0
	var outside []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				rs, ok := n.(*ast.RangeStmt)
				if !ok || len(rs.Body.List) != 0 {
					return true
				}
				if id, ok := rs.X.(*ast.Ident); !ok || id.Name != "out" {
					return true
				}
				if fn.Name.Name == "runDrained" {
					inHelper++
				} else {
					outside = append(outside, fset.Position(rs.Pos()).String()+" in "+fn.Name.Name)
				}
				return true
			})
		}
	}
	if inHelper != 1 {
		t.Errorf("found %d empty `for range out` drains inside runDrained, want exactly 1 (a scan that cannot see the helper's own drain proves nothing)", inHelper)
	}
	for _, o := range outside {
		t.Errorf("hand-written drain outside runDrained: %s; route it through runDrained so out is closed on every path", o)
	}
}
