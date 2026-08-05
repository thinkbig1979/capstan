package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestTrustedProxyProtoGateIsWired pins agent-os-ab9's wiring in main.go: a
// single deleted line — middleware.InitTrustedProxyNetworks(trustedProxies)
// — silently degrades the X-Forwarded-Proto trust gate to loopback-only in
// production, with the full test suite staying green (VERIFIED 2026-08-05 by
// the orchestrator: `sed -i '/InitTrustedProxyNetworks/d' main.go && go test
// ./...` exits 0). No middleware-level test can see this: IsSecureRequest and
// isTrustedProxyPeer are both correct in isolation regardless of whether
// InitTrustedProxyNetworks was ever called from main() at all — see
// TestIsTrustedProxyPeer_DefaultsToLoopbackOnly in
// internal/middleware/secure_request_trust_test.go, which asserts that
// exact "never called" state IS the correct standalone default. Follows the
// same AST-parsing precedent as
// TestAuthMiddlewareIsWiredToTheAuthDisabledAllowlist in auth_wiring_test.go
// (agent-os-0s4), for the identical reason: main() wires everything inline
// with no seam a behavioural test could use instead.
//
// This asserts more than "the call exists": InitTrustedProxyNetworks must be
// handed the SAME identifier that r.SetTrustedProxies(...) receives, so
// gin's own trusted-proxy resolution and the XFP gate cannot be wired to two
// different variables that drift apart. (It does NOT, and cannot, catch the
// two functions disagreeing on how they PARSE that identical list once
// malformed entries are in it — see the comment on trustedProxyNetworks in
// internal/middleware/proxytrust.go for that separate, acknowledged gap.)
//
// If this test fails, do not relax it: reverting either the call or the
// shared identifier reopens agent-os-ab9 in production while every other
// gate in this repo stays green.
func TestTrustedProxyProtoGateIsWired(t *testing.T) {
	const (
		setCall  = "SetTrustedProxies"
		initCall = "InitTrustedProxyNetworks"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("failed to parse main.go: %v", err)
	}

	var setArg, initArg string
	var foundSet, foundInit int

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		var argOut *string
		switch sel.Sel.Name {
		case setCall:
			foundSet++
			argOut = &setArg
		case initCall:
			foundInit++
			argOut = &initArg
		default:
			return true
		}

		if len(call.Args) != 1 {
			t.Errorf("%s: expected 1 argument to %s, got %d", fset.Position(call.Pos()), sel.Sel.Name, len(call.Args))
			return true
		}
		ident, ok := call.Args[0].(*ast.Ident)
		if !ok {
			t.Errorf("%s: %s argument is not a plain identifier; this test cannot compare it structurally to the other call",
				fset.Position(call.Pos()), sel.Sel.Name)
			return true
		}
		*argOut = ident.Name
		return true
	})

	if foundSet == 0 {
		t.Fatalf("found no call to %s in main.go; this test can no longer see gin's trusted-proxy wiring "+
			"and must be updated to follow it (agent-os-ab9)", setCall)
	}
	if foundInit == 0 {
		t.Fatalf("found no call to middleware.%s in main.go — the X-Forwarded-Proto trust gate is unwired "+
			"and silently degrades to loopback-only in production, with the rest of the test suite staying "+
			"green (agent-os-ab9)", initCall)
	}
	if setArg != initArg {
		t.Errorf("%s is called with %q but middleware.%s is called with %q — gin's trusted-proxy resolution "+
			"and the X-Forwarded-Proto gate must be fed the SAME computed list, or they can silently diverge "+
			"onto two different variables (agent-os-ab9)", setCall, setArg, initCall, initArg)
	}
}
