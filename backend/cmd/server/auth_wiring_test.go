package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestAuthMiddlewareIsWiredToTheAuthDisabledAllowlist pins agent-os-0s4's
// PRIMARY vector structurally, by parsing main.go and inspecting which config
// field reaches AuthMiddleware.
//
// Why a structural test rather than a behavioural one. The defect was never a
// bad conditional: AuthMiddleware and IsTrustedIP always matched correctly
// against whatever list string they were handed. The bug was main()'s CHOICE
// of which field to hand over — it passed cfg.TrustedNetworks, Gin's
// trusted-proxy list, so every host an operator added there for correct
// client-IP attribution was silently allow-listed for the AUTH_DISABLED admin
// bypass too, with no header spoofing required. A middleware-level test cannot
// see that: it passes against the pre-fix code, because the pre-fix middleware
// was correct in isolation. (Measured 2026-08-04: such a test was written
// first and passed against unfixed source, which is why this one exists.)
//
// main() wires the DB, Docker client, and every route inline and exposes no
// seam, so the honest options were to refactor main() purely for testability
// or to assert on the source. This follows the precedent already set by
// handlers/stack_crud_concurrent_test.go (agent-os-zpg), which pins a
// lock-ordering requirement the same way.
//
// If this test fails, do not relax it: reverting this argument reopens a path
// where anything on the reverse proxy's subnet reaches an anonymous admin
// session whenever AUTH_DISABLED is on.
func TestAuthMiddlewareIsWiredToTheAuthDisabledAllowlist(t *testing.T) {
	const (
		wantArg    = "AuthDisabledAllowedNetworks"
		forbidden  = "TrustedNetworks"
		targetCall = "AuthMiddleware"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("failed to parse main.go: %v", err)
	}

	var found int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != targetCall {
			return true
		}
		found++

		// AuthMiddleware(db, jwtSecret, authDisabled, authAllowedNetworks)
		if len(call.Args) != 4 {
			t.Errorf("%s: expected 4 arguments to %s, got %d",
				fset.Position(call.Pos()), targetCall, len(call.Args))
			return true
		}

		argSel, ok := call.Args[3].(*ast.SelectorExpr)
		if !ok {
			t.Errorf("%s: 4th argument to %s is not a config field selector; expected cfg.%s",
				fset.Position(call.Pos()), targetCall, wantArg)
			return true
		}

		switch argSel.Sel.Name {
		case wantArg:
			// correct
		case forbidden:
			t.Errorf("%s: %s is wired to cfg.%s — that is Gin's trusted-proxy list, and reusing it here "+
				"re-opens agent-os-0s4: every host in TRUSTED_NETWORKS gets the AUTH_DISABLED admin bypass "+
				"with no spoofing required. It must be cfg.%s.",
				fset.Position(call.Pos()), targetCall, forbidden, wantArg)
		default:
			t.Errorf("%s: %s's allowlist argument is cfg.%s; expected cfg.%s. If a new field is intended, "+
				"confirm it is NOT the trusted-proxy list before updating this test (agent-os-0s4).",
				fset.Position(call.Pos()), targetCall, argSel.Sel.Name, wantArg)
		}
		return true
	})

	// A rename or refactor that removes the call entirely would otherwise make
	// this test vacuously green — the exact "check that cannot fail" shape it
	// is meant to prevent.
	if found == 0 {
		t.Fatalf("found no call to %s in main.go; this test can no longer guard the wiring "+
			"and must be updated to follow it (agent-os-0s4)", targetCall)
	}
}
