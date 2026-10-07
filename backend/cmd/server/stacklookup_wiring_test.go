package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestMain_DockerServiceGetsTheStackLookup is agent-os-z91e.38's wiring check.
// DockerService refuses a mutating compose command on a stack whose project
// name another stack carries only when a stack lookup is installed (nil turns
// the check off, which lets unit tests build it bare), so a dropped
// SetStackLookup call in main.go would silently remove the refusal from every
// non-HTTP path (backup, git redeploy, create-and-deploy) while every unit test
// stayed green. It reads mainSource, the go:embed copy oplock_wiring_test.go
// declares, so a `go test -overlay` mutant of main.go is what it sees.
func TestMain_DockerServiceGetsTheStackLookup(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	builtBy := map[string]string{}
	var lookupArgs []string
	var receivers []string
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
			if !ok || sel.Sel.Name != "SetStackLookup" || len(n.Args) != 1 {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			arg, ok2 := n.Args[0].(*ast.Ident)
			if ok && ok2 {
				receivers = append(receivers, recv.Name)
				lookupArgs = append(lookupArgs, arg.Name)
			}
		}
		return true
	})

	for i, r := range receivers {
		// The argument must be the database main.go opened, not some other value.
		if builtBy[r] == "NewDockerService" && strings.HasPrefix(builtBy[lookupArgs[i]], "NewWithMigrations") {
			return
		}
	}
	t.Errorf("main.go never calls SetStackLookup with the opened database on the value NewDockerService returns (receivers %v, args %v)", receivers, lookupArgs)
}
