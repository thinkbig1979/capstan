package main

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// mainSource is embedded, not read from disk, so a `go test -overlay` mutant of
// main.go is what this test sees.
//
//go:embed main.go
var mainSource string

// TestMain_EveryOperationLockConsumerGetsTheSameLock is agent-os-a1ye.4's
// wiring check. The update handlers, scheduler auto-apply, git pull, and the
// compose and env writes take the per-stack lock only when it is injected
// (nil means no locking, which is what lets their unit tests build them bare),
// so a missing setter call in main.go would silently drop the lock in
// production while every unit test stayed green. This asserts that main.go
// hands the one `opLock` to every consumer: the three constructors that take it
// as an argument and the five components that take it by setter, each setter
// receiver bound to the constructor that built it.
func TestMain_EveryOperationLockConsumerGetsTheSameLock(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	// variable name -> the constructor (selector name) it was assigned from.
	builtBy := map[string]string{}
	var setterReceivers, ctorsWithLock []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(n.Lhs) {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				id, ok2 := n.Lhs[i].(*ast.Ident)
				if ok && ok2 {
					builtBy[id.Name] = sel.Sel.Name
				}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			passesLock := false
			for _, a := range n.Args {
				if id, ok := a.(*ast.Ident); ok && id.Name == "opLock" {
					passesLock = true
				}
			}
			if !passesLock {
				return true
			}
			if sel.Sel.Name == "SetOperationLock" {
				if recv, ok := sel.X.(*ast.Ident); ok {
					setterReceivers = append(setterReceivers, recv.Name)
				}
			} else {
				ctorsWithLock = append(ctorsWithLock, sel.Sel.Name)
			}
		}
		return true
	})

	gotSetters := map[string]bool{}
	for _, r := range setterReceivers {
		gotSetters[builtBy[r]] = true
	}
	for _, want := range []string{
		"NewSchedulerService",
		"NewEnvHandler",
		"NewComposeHandler",
		"NewGitService",
		"NewResourcesHandlerWithJobManager",
	} {
		if !gotSetters[want] {
			t.Errorf("main.go never calls SetOperationLock(opLock) on the value %s returns", want)
		}
	}

	sort.Strings(ctorsWithLock)
	gotCtors := map[string]bool{}
	for _, c := range ctorsWithLock {
		gotCtors[c] = true
	}
	for _, want := range []string{"NewStacksHandler", "NewOperationsHandler", "NewBackupService"} {
		if !gotCtors[want] {
			t.Errorf("main.go does not pass opLock to %s (calls passing it: %v)", want, ctorsWithLock)
		}
	}
}
