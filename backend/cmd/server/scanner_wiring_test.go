package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_StacksHandlerGetsTheScanner is agent-os-z91e.23's wiring check.
// StacksHandler.Delete serialises its file and row removal with the scanner
// through ScannerService.WithLock, and WithLock on a nil scanner runs unlocked
// (that is what lets handler unit tests build the handler bare). So a main.go
// that passed nil, or anything but the scanner it built, would silently bring
// back the ghost-stack race while every unit test stayed green. This asserts
// NewStacksHandler's scanner argument is a variable assigned from
// NewScannerService. It reads the embedded mainSource (oplock_wiring_test.go),
// so a `go test -overlay` mutant of main.go is what it sees.
func TestMain_StacksHandlerGetsTheScanner(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	builtBy := map[string]string{}
	var scannerArgs []ast.Expr
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
			if ok && sel.Sel.Name == "NewStacksHandler" && len(n.Args) > 1 {
				scannerArgs = append(scannerArgs, n.Args[1])
			}
		}
		return true
	})

	if len(scannerArgs) != 1 {
		t.Fatalf("expected exactly one NewStacksHandler call in main.go, found %d", len(scannerArgs))
	}
	id, ok := scannerArgs[0].(*ast.Ident)
	if !ok || builtBy[id.Name] != "NewScannerService" {
		t.Fatalf("main.go passes %s as NewStacksHandler's scanner, not a variable built by NewScannerService: Delete would run unlocked against the scanner",
			nodeString(fset, scannerArgs[0]))
	}
}

func nodeString(fset *token.FileSet, e ast.Expr) string {
	pos := fset.Position(e.Pos())
	if id, ok := e.(*ast.Ident); ok {
		return id.Name + " (" + pos.String() + ")"
	}
	return "an expression at " + pos.String()
}
