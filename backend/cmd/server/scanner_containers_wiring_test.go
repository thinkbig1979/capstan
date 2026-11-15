package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_ScannerGetsTheContainerLister is agent-os-epc5's wiring check. A
// rescan deletes a stack row whose compose file is gone only when a container
// lister is installed and reports no containers for its project; with none
// (nil) the scanner keeps every such row, which lets unit tests build it bare.
// So a dropped SetContainerLister call in main.go would silently stop that
// cleanup while every unit test stayed green. It reads mainSource, the embedded
// copy of main.go that oplock_wiring_test.go declares, so a `go test -overlay`
// mutant of main.go is what it sees.
func TestMain_ScannerGetsTheContainerLister(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	builtBy := map[string]string{}
	var receivers, args []string
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
			if !ok || sel.Sel.Name != "SetContainerLister" || len(n.Args) != 1 {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			arg, ok2 := n.Args[0].(*ast.Ident)
			if ok && ok2 {
				receivers = append(receivers, recv.Name)
				args = append(args, arg.Name)
			}
		}
		return true
	})

	for i, r := range receivers {
		// The receiver must be the scanner main.go built and the argument the
		// DockerService it built, not some other value.
		if builtBy[r] == "NewScannerService" && builtBy[args[i]] == "NewDockerService" {
			return
		}
	}
	t.Errorf("main.go never calls SetContainerLister with the value NewDockerService returns on the value NewScannerService returns (receivers %v, args %v)", receivers, args)
}
