package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestEveryConnectionManagerIsInTheRevocationSet parses main.go and asserts
// that every handlers.NewConnectionManager(...) constructed there is also an
// element of the handlers.ConnectionManagers{...} literal passed to
// authHandler.SetConnectionManagers / settingsHandler.SetConnectionManagers
// (agent-os-teop).
//
// Why a structural test rather than a behavioural one. The failure mode this
// guards against is a THIRD manager (or a renamed/refactored existing one)
// that gets constructed in main.go but never added to the slice logout and
// password-change revocation close through — main() wires everything inline
// with no seam a behavioural test could exercise, and the whole point of
// agent-os-teop's design (an explicit slice built once, next to where the
// managers are constructed, rather than self-registration) was that the
// omission must be loud, not silent. A test that only calls
// ConnectionManagers.CloseForSession/CloseForUser (ws_test.go) proves the
// mechanism works on whatever managers happen to be in the slice — it cannot
// see a manager that was never added to it in the first place. Only reading
// main.go itself can.
//
// Follows the precedent already set by auth_wiring_test.go's
// TestAuthMiddlewareIsWiredToTheAuthDisabledAllowlist for the same reason:
// main() exposes no seam, so the honest options were to refactor purely for
// testability or assert on the source.
//
// THREE construction forms are recognised, because the first version of this
// test inspected only *ast.AssignStmt and an adversarial review found two ways
// to add an unrevocable manager that it passed green (agent-os-teop):
//
//	cm := handlers.NewConnectionManager(10)          // *ast.AssignStmt
//	var cm = handlers.NewConnectionManager(10)       // *ast.ValueSpec — was MISSED
//	handlers.NewFooHandler(handlers.NewConnectionManager(10))
//	                                                 // unnamed — was MISSED
//
// The third form cannot be fixed by naming it in the slice, because slice
// elements are identifiers and an inline call has no identifier: it is
// reported directly at its construction site. A guard with a silent blind
// spot is worse than no guard, since it reads as coverage that does not exist.
//
// If this test fails because a new ConnectionManager was added and correctly
// wired into the slice by a DIFFERENT variable name than this test expects,
// update the expectations — do not delete the test. If it fails because a
// manager was left out of the slice, that is the defect: fix main.go, not
// this test.
func TestEveryConnectionManagerIsInTheRevocationSet(t *testing.T) {
	const (
		constructorCall = "NewConnectionManager"
		sliceType       = "ConnectionManagers"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("failed to parse main.go: %v", err)
	}

	// managerCall reports whether e is a handlers.NewConnectionManager(...)
	// call, whatever syntax it is embedded in.
	managerCall := func(e ast.Expr) (*ast.CallExpr, bool) {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return nil, false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != constructorCall {
			return nil, false
		}
		return call, true
	}

	// Pass 1: every construction site in the file, in source order, regardless
	// of how it is written. This is the denominator — anything the named-form
	// passes below do not account for is an unnamed construction.
	var allSites []token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if call, ok := managerCall(e); ok {
			allSites = append(allSites, call.Pos())
		}
		return true
	})

	// A rename or refactor that removes every call would otherwise make this
	// test vacuously green — the exact "check that cannot fail" shape the
	// auth_wiring_test.go precedent already warns about. Anchored on the call
	// sites rather than on the named ones, so a file whose managers are ALL
	// constructed inline fails loudly instead of reporting nothing to check.
	if len(allSites) == 0 {
		t.Fatalf("found no %s(...) call in main.go; this test can no longer guard "+
			"manager coverage and must be updated to follow whatever replaced it (agent-os-teop)", constructorCall)
	}

	// Pass 2: the construction sites that bind a name, via either
	// `cm := ...` (*ast.AssignStmt) or `var cm = ...` (*ast.ValueSpec).
	type namedManager struct {
		name string
		pos  token.Pos
	}
	var constructed []namedManager
	named := map[token.Pos]bool{}

	ast.Inspect(file, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range d.Rhs {
				call, ok := managerCall(rhs)
				if !ok || i >= len(d.Lhs) {
					continue
				}
				if ident, ok := d.Lhs[i].(*ast.Ident); ok {
					constructed = append(constructed, namedManager{ident.Name, call.Pos()})
					named[call.Pos()] = true
				}
			}
		case *ast.ValueSpec:
			for i, v := range d.Values {
				call, ok := managerCall(v)
				if !ok || i >= len(d.Names) {
					continue
				}
				constructed = append(constructed, namedManager{d.Names[i].Name, call.Pos()})
				named[call.Pos()] = true
			}
		}
		return true
	})

	// An inline construction can never appear in the slice, because the slice's
	// elements are identifiers. Report it at its own position: there is no name
	// to look up and nothing to add.
	for _, pos := range allSites {
		if !named[pos] {
			t.Errorf("%s: a %s(...) is constructed inline with no variable name, so it can never be "+
				"an element of the handlers.%s{...} literal — its live connections will never be closed "+
				"on logout or password change (agent-os-teop). Assign it to a variable and add that "+
				"variable to the slice.",
				fset.Position(pos), constructorCall, sliceType)
		}
	}

	// covered collects every identifier named as an element of a
	// handlers.ConnectionManagers{...} composite literal.
	covered := map[string]bool{}
	var sliceLiteralsFound int

	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != sliceType {
			return true
		}
		sliceLiteralsFound++
		for _, elt := range lit.Elts {
			if ident, ok := elt.(*ast.Ident); ok {
				covered[ident.Name] = true
			}
		}
		return true
	})

	if sliceLiteralsFound == 0 {
		t.Fatalf("found no handlers.%s{...} literal in main.go; every ConnectionManager constructed "+
			"there is then unreachable from logout/password-change revocation (agent-os-teop)", sliceType)
	}

	for _, m := range constructed {
		if !covered[m.name] {
			t.Errorf("%s: %s is constructed via %s(...) but is NOT an element of the handlers.%s{...} "+
				"literal — its live connections will never be closed on logout or password change "+
				"(agent-os-teop). Add it to the slice.",
				fset.Position(m.pos), m.name, constructorCall, sliceType)
		}
	}
}
