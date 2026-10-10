package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_BackupRunsAreDrainedAtShutdown is agent-os-wp9n's wiring check.
// Durable backup/restore/sync/prune runs are detached goroutines, and nothing
// but backupHandler.StopWithTimeout joins them at shutdown. The backup unit
// tests use the unbounded Stop() (the test-only variant, deliberately kept), so
// deleting the StopWithTimeout call from main.go would leave them all green
// (safe-defaults rule 5). It asserts the call sits after `<-quit` and after
// srv.Shutdown: draining first would reject in-flight requests with 503 while
// the server is still meant to be serving them. It reads mainSource (go:embed,
// declared in oplock_wiring_test.go), so a `go test -overlay` mutant of main.go
// is what it sees.
func TestMain_BackupRunsAreDrainedAtShutdown(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	var mainFn *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
			mainFn = fn
		}
	}
	if mainFn == nil {
		t.Fatal("main.go has no func main")
	}

	var quitPos token.Pos
	for _, stmt := range mainFn.Body.List {
		if es, ok := stmt.(*ast.ExprStmt); ok {
			if u, ok := es.X.(*ast.UnaryExpr); ok && u.Op == token.ARROW {
				if id, ok := u.X.(*ast.Ident); ok && id.Name == "quit" {
					quitPos = es.Pos()
				}
			}
		}
	}
	if quitPos == token.NoPos {
		t.Fatal("main() has no top-level `<-quit`; this test can no longer find the shutdown sequence and must be updated")
	}

	var shutdownPos token.Pos
	var drains []token.Pos
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Pos() < quitPos {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case recv.Name == "srv" && sel.Sel.Name == "Shutdown":
			shutdownPos = call.Pos()
		case recv.Name == "backupHandler" && sel.Sel.Name == "StopWithTimeout":
			drains = append(drains, call.Pos())
		}
		return true
	})
	if shutdownPos == token.NoPos {
		t.Fatal("main() has no srv.Shutdown after `<-quit`; this test can no longer find the shutdown sequence and must be updated")
	}

	if len(drains) == 0 {
		t.Error("backupHandler.StopWithTimeout(...) is not called after `<-quit`, so in-flight backup runs are not drained at shutdown and are cut off mid-run (agent-os-wp9n)")
		return
	}
	drained := false
	for _, p := range drains {
		if p > shutdownPos {
			drained = true
		}
	}
	if !drained {
		t.Error("backupHandler.StopWithTimeout(...) runs before srv.Shutdown, so the drain happens while HTTP still accepts the requests that start new backup runs and in-flight requests get 503; it must follow srv.Shutdown (agent-os-wp9n)")
	}
}
