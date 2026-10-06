package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_StartsSessionSweepOnBootContext is agent-os-n4ca.4's wiring check.
// The session sweep is the only thing that closes a WebSocket whose session
// was revoked outside this process (the CLI `admin reset-password`). Every
// unit test calls RunSessionSweep directly, so dropping the `go` statement
// from main.go would leave them all green while production never sweeps.
// This asserts main.go starts it in a goroutine, over allConnectionManagers
// (both managers), on the boot `ctx` that shutdown cancels, against `db`.
// It reads mainSource (go:embed, declared in oplock_wiring_test.go), so a
// `go test -overlay` mutant of main.go is what it sees.
func TestMain_StartsSessionSweepOnBootContext(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	found := 0
	ast.Inspect(file, func(n ast.Node) bool {
		stmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		sel, ok := stmt.Call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "RunSessionSweep" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "allConnectionManagers" {
			t.Errorf("RunSessionSweep must run over allConnectionManagers, so it reaches every manager; got %v", sel.X)
			return true
		}
		if len(stmt.Call.Args) != 3 {
			t.Errorf("RunSessionSweep call has %d args, want 3", len(stmt.Call.Args))
			return true
		}
		if id, ok := stmt.Call.Args[0].(*ast.Ident); !ok || id.Name != "ctx" {
			t.Errorf("RunSessionSweep must take the boot ctx (cancelled at shutdown); got %v", stmt.Call.Args[0])
		}
		if id, ok := stmt.Call.Args[1].(*ast.Ident); !ok || id.Name != "db" {
			t.Errorf("RunSessionSweep must look sessions up in db; got %v", stmt.Call.Args[1])
		}
		found++
		return true
	})
	if found != 1 {
		t.Fatalf("main.go must start exactly one `go allConnectionManagers.RunSessionSweep(ctx, db, ...)`; found %d", found)
	}
}
