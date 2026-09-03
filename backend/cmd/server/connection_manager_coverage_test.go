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

	// constructed maps each variable name assigned from
	// handlers.NewConnectionManager(...) to the position it was constructed at,
	// so a failure can point at the exact line.
	constructed := map[string]token.Pos{}

	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != constructorCall {
				continue
			}
			if i >= len(assign.Lhs) {
				continue
			}
			ident, ok := assign.Lhs[i].(*ast.Ident)
			if !ok {
				continue
			}
			constructed[ident.Name] = call.Pos()
		}
		return true
	})

	// A rename or refactor that removes every call would otherwise make this
	// test vacuously green — the exact "check that cannot fail" shape the
	// auth_wiring_test.go precedent already warns about.
	if len(constructed) == 0 {
		t.Fatalf("found no %s(...) assignment in main.go; this test can no longer guard "+
			"manager coverage and must be updated to follow whatever replaced it (agent-os-teop)", constructorCall)
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

	for name, pos := range constructed {
		if !covered[name] {
			t.Errorf("%s: %s is constructed via %s(...) but is NOT an element of the handlers.%s{...} "+
				"literal — its live connections will never be closed on logout or password change "+
				"(agent-os-teop). Add it to the slice.",
				fset.Position(pos), name, constructorCall, sliceType)
		}
	}
}
