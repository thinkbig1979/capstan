package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_BackupSchedulerVerifierIsWired is agent-os-ffaj's wiring check
// (safe-defaults rule 5). The backup scheduler runs its weekly repository
// check only when SetVerifier gave it a launcher (nil means off, which is what
// lets the scheduler unit tests build it bare), so deleting the call from
// main.go would stop every scheduled check while all unit tests stayed green.
// It asserts main() calls backupSched.SetVerifier(backupHandler.VerifyLauncher()),
// each receiver bound to the constructor that built it, and before the
// scheduler is started. It reads mainSource (go:embed, declared in
// oplock_wiring_test.go), so a `go test -overlay` mutant of main.go is what it
// sees.
func TestMain_BackupSchedulerVerifierIsWired(t *testing.T) {
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

	builtBy := map[string]string{}
	var wiredAt, startAt token.Pos
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				call, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(n.Lhs) {
					continue
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				lhs, ok2 := n.Lhs[i].(*ast.Ident)
				if ok && ok2 {
					builtBy[lhs.Name] = sel.Sel.Name
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
			switch {
			case recv.Name == "backupSvc" && sel.Sel.Name == "StartScheduler":
				startAt = n.Pos()
			case sel.Sel.Name == "SetVerifier" && len(n.Args) == 1:
				arg, ok := n.Args[0].(*ast.CallExpr)
				if !ok {
					return true
				}
				argSel, ok := arg.Fun.(*ast.SelectorExpr)
				if !ok || argSel.Sel.Name != "VerifyLauncher" {
					return true
				}
				argRecv, ok := argSel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if builtBy[recv.Name] == "NewBackupScheduler" && builtBy[argRecv.Name] == "NewBackupHandler" {
					wiredAt = n.Pos()
				}
			}
		}
		return true
	})

	if wiredAt == token.NoPos {
		t.Fatal("main() never calls SetVerifier(<NewBackupHandler result>.VerifyLauncher()) on the NewBackupScheduler result, so the weekly repository check never runs (agent-os-ffaj)")
	}
	if startAt == token.NoPos {
		t.Fatal("main() has no backupSvc.StartScheduler(); this test can no longer find the scheduler start and must be updated")
	}
	if wiredAt > startAt {
		t.Error("SetVerifier runs after backupSvc.StartScheduler(), so a cycle can fire before the check is wired")
	}
}
