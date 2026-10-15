package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_CleanupServiceGetsTheHostSpaceServiceBeforeTheTickIsArmed is
// agent-os-lr7x's wiring check (safe-defaults rule 5). DockerCleanupService
// removes unused tagged images, containers, volumes and networks only through
// the HostSpaceService and the lock it is given; nil makes such a run refuse,
// which every unit test that builds the service bare relies on. So a missing
// setter call in main.go would leave those categories failing in production
// while every unit test stayed green. This asserts main.go hands the value
// NewHostSpaceService returns, and opLock, to the value
// NewDockerCleanupService returns, both before StartFromPolicy can arm the
// tick: a setter after it would race the first tick. The lock ordering lives
// here rather than in oplock_wiring_test.go, which checks every consumer as a
// set and has no single tick to order against.
func TestMain_CleanupServiceGetsTheHostSpaceServiceBeforeTheTickIsArmed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	builtBy := map[string]string{}
	type call struct {
		recv, arg string
		pos       token.Pos
	}
	var setters, lockSetters []call
	armedAt := token.NoPos
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				c, ok := rhs.(*ast.CallExpr)
				if !ok || i >= len(n.Lhs) {
					continue
				}
				sel, ok := c.Fun.(*ast.SelectorExpr)
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
			switch sel.Sel.Name {
			case "SetHostSpaceService":
				recv, ok := sel.X.(*ast.Ident)
				if ok && len(n.Args) == 1 {
					if arg, ok := n.Args[0].(*ast.Ident); ok {
						setters = append(setters, call{recv: recv.Name, arg: arg.Name, pos: n.Pos()})
					}
				}
			case "SetOperationLock":
				recv, ok := sel.X.(*ast.Ident)
				if ok && len(n.Args) == 1 {
					if arg, ok := n.Args[0].(*ast.Ident); ok {
						lockSetters = append(lockSetters, call{recv: recv.Name, arg: arg.Name, pos: n.Pos()})
					}
				}
			case "StartFromPolicy":
				if recv, ok := sel.X.(*ast.Ident); ok && armedAt == token.NoPos {
					if builtBy[recv.Name] == "NewDockerCleanupScheduler" {
						armedAt = n.Pos()
					}
				}
			}
		}
		return true
	})

	if armedAt == token.NoPos {
		t.Fatal("main.go never calls StartFromPolicy on the value NewDockerCleanupScheduler returns; this test cannot place the setter")
	}
	lockSet := false
	for _, s := range lockSetters {
		if builtBy[s.recv] == "NewDockerCleanupService" && s.arg == "opLock" {
			lockSet = true
			if s.pos > armedAt {
				t.Errorf("main.go installs opLock on the cleanup service at %s, after StartFromPolicy at %s",
					fset.Position(s.pos), fset.Position(armedAt))
			}
		}
	}
	if !lockSet {
		t.Errorf("main.go never calls SetOperationLock(opLock) on the value NewDockerCleanupService returns")
	}

	for _, s := range setters {
		if builtBy[s.recv] == "NewDockerCleanupService" && builtBy[s.arg] == "NewHostSpaceService" {
			if s.pos > armedAt {
				t.Errorf("main.go installs the host space service on the cleanup service at %s, after StartFromPolicy at %s",
					fset.Position(s.pos), fset.Position(armedAt))
			}
			return
		}
	}
	t.Errorf("main.go never calls SetHostSpaceService on the value NewDockerCleanupService returns with the value NewHostSpaceService returns (calls seen: %+v)", setters)
}
