package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_RateLimitersAreStoppedAtShutdown is agent-os-z91e.17's wiring check.
// The rate limiters' cleanup goroutines only end if shutdown calls
// middleware.StopRateLimiters; every unit test calls it directly, so deleting
// the call from main.go would leave them all green (safe-defaults rule 5). It
// asserts the call sits after `<-quit` and before srv.Shutdown. It reads
// mainSource (go:embed, declared in oplock_wiring_test.go), so a
// `go test -overlay` mutant of main.go is what it sees.
func TestMain_RateLimitersAreStoppedAtShutdown(t *testing.T) {
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
	var stops []token.Pos
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
		case recv.Name == "middleware" && sel.Sel.Name == "StopRateLimiters":
			stops = append(stops, call.Pos())
		}
		return true
	})
	if shutdownPos == token.NoPos {
		t.Fatal("main() has no srv.Shutdown after `<-quit`; this test can no longer find the shutdown sequence and must be updated")
	}

	stopped := false
	for _, p := range stops {
		if p < shutdownPos {
			stopped = true
		}
	}
	if !stopped {
		t.Error("middleware.StopRateLimiters() is not called between `<-quit` and srv.Shutdown, so the seven rate-limiter cleanup goroutines outlive shutdown (agent-os-z91e.17)")
	}
}
