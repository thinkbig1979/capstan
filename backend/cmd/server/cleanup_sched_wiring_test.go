package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_CleanupSchedulerIsStoppedAtShutdown is agent-os-z91e.4's wiring
// check. The Docker cleanup scheduler runs on its own context, so main's
// cancel() does not reach it: unless shutdown calls its Stop(), a tick can
// start a prune during the shutdown window and an in-flight one is neither
// cancelled nor awaited. Every unit test drives Stop() directly, so a missing
// call in main.go would leave them all green. This asserts that every variable
// main() assigns from NewDockerCleanupScheduler lives in main()'s own scope
// (a block-local one cannot be reached by the shutdown sequence) and has a
// .Stop() call after `<-quit` and before srv.Shutdown. It reads mainSource
// (go:embed, declared in oplock_wiring_test.go), so a `go test -overlay`
// mutant of main.go is what it sees.
func TestMain_CleanupSchedulerIsStoppedAtShutdown(t *testing.T) {
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

	// Names declared directly in main()'s body, not in a nested block.
	topLevel := map[string]bool{}
	var quitPos, shutdownPos token.Pos
	for _, stmt := range mainFn.Body.List {
		switch s := stmt.(type) {
		case *ast.DeclStmt:
			if gd, ok := s.Decl.(*ast.GenDecl); ok {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, n := range vs.Names {
							topLevel[n.Name] = true
						}
					}
				}
			}
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, l := range s.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						topLevel[id.Name] = true
					}
				}
			}
		case *ast.ExprStmt:
			if u, ok := s.X.(*ast.UnaryExpr); ok && u.Op == token.ARROW {
				if id, ok := u.X.(*ast.Ident); ok && id.Name == "quit" {
					quitPos = s.Pos()
				}
			}
		}
	}
	if quitPos == token.NoPos {
		t.Fatal("main() has no top-level `<-quit`; this test can no longer find the shutdown sequence and must be updated")
	}

	var schedVars []string
	stopsAt := map[string][]token.Pos{}
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(n.Lhs) {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				id, ok2 := n.Lhs[i].(*ast.Ident)
				if ok && ok2 && sel.Sel.Name == "NewDockerCleanupScheduler" {
					schedVars = append(schedVars, id.Name)
				}
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if sel.Sel.Name == "Stop" {
				stopsAt[recv.Name] = append(stopsAt[recv.Name], n.Pos())
			}
			if recv.Name == "srv" && sel.Sel.Name == "Shutdown" && n.Pos() > quitPos {
				shutdownPos = n.Pos()
			}
		}
		return true
	})
	if len(schedVars) == 0 {
		t.Fatal("main.go never assigns NewDockerCleanupScheduler to a variable; this test can no longer see the scheduler and must be updated")
	}
	if shutdownPos == token.NoPos {
		t.Fatal("main() has no srv.Shutdown after `<-quit`; this test can no longer find the shutdown sequence and must be updated")
	}

	for _, v := range schedVars {
		if !topLevel[v] {
			t.Errorf("%s (from NewDockerCleanupScheduler) is declared inside a nested block, so the shutdown sequence cannot stop it; declare it in main()'s scope", v)
		}
		stopped := false
		for _, p := range stopsAt[v] {
			if p > quitPos && p < shutdownPos {
				stopped = true
			}
		}
		if !stopped {
			t.Errorf("%s (from NewDockerCleanupScheduler) is never Stop()ped between `<-quit` and srv.Shutdown, so a scheduled prune can start or be cut off during shutdown (agent-os-z91e.4)", v)
		}
	}
}

// TestMain_UpdateSchedulerIsClosedAtShutdown is agent-os-z91e.5's wiring
// check. The scheduler's Close latch only protects shutdown if main.go calls
// Close, not Stop: Stop leaves Start/Restart free, so a settings save still
// being served before srv.Shutdown would re-arm auto-apply. Every unit test
// calls Close directly, so swapping the call back to Stop would leave them
// all green. This asserts a schedulerService.Close() call between `<-quit` and
// srv.Shutdown, and no schedulerService.Stop() there. It reads mainSource, so
// a `go test -overlay` mutant of main.go is what it sees.
func TestMain_UpdateSchedulerIsClosedAtShutdown(t *testing.T) {
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
	var closes, stops []token.Pos
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
		case recv.Name == "schedulerService" && sel.Sel.Name == "Close":
			closes = append(closes, call.Pos())
		case recv.Name == "schedulerService" && sel.Sel.Name == "Stop":
			stops = append(stops, call.Pos())
		}
		return true
	})
	if shutdownPos == token.NoPos {
		t.Fatal("main() has no srv.Shutdown after `<-quit`; this test can no longer find the shutdown sequence and must be updated")
	}

	closed := false
	for _, p := range closes {
		if p < shutdownPos {
			closed = true
		}
	}
	if !closed {
		t.Error("schedulerService.Close() is not called between `<-quit` and srv.Shutdown, so a late settings save can Restart the update scheduler during shutdown (agent-os-z91e.5)")
	}
	for _, p := range stops {
		t.Errorf("%s: schedulerService.Stop() after `<-quit` does not latch the scheduler; call Close() (agent-os-z91e.5)", fset.Position(p))
	}
}
