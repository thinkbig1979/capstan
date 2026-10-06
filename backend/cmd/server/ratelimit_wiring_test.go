package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestMain_LoginRoutesSitBehindRateLimitAuth pins the one password-checking
// route whose limiter is attached here rather than at its registration:
// AuthHandler.RegisterRoutes (/setup, /login) adds no limiter itself, and
// main.go puts middleware.RateLimitAuth on the /auth group first. Gin applies a
// group's Use only to routes registered after it, so the order matters as much
// as the call. handlers.TestEveryPasswordCheckingRouteCarriesALimiter defers
// Login to this test (agent-os-n4ca.3). Reads the embedded mainSource, so an
// -overlay mutant of main.go is what it sees.
func TestMain_LoginRoutesSitBehindRateLimitAuth(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}

	// group variable -> position of its .Use(middleware.RateLimitAuth()) call.
	limitedAt := map[string]token.Pos{}
	var registrations int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Use":
			group, ok := sel.X.(*ast.Ident)
			if !ok || len(call.Args) != 1 {
				return true
			}
			inner, ok := call.Args[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			if fn, ok := inner.Fun.(*ast.SelectorExpr); ok && fn.Sel.Name == "RateLimitAuth" {
				limitedAt[group.Name] = call.Pos()
			}
		case "RegisterRoutes":
			recv, ok := sel.X.(*ast.Ident)
			if !ok || recv.Name != "authHandler" || len(call.Args) != 1 {
				return true
			}
			registrations++
			group, ok := call.Args[0].(*ast.Ident)
			if !ok {
				t.Errorf("%s: authHandler.RegisterRoutes is not given a plain group variable", fset.Position(call.Pos()))
				return true
			}
			pos, limited := limitedAt[group.Name]
			if !limited || pos > call.Pos() {
				t.Errorf("%s: authHandler.RegisterRoutes(%s) registers /setup and /login, which check a password, "+
					"but %s.Use(middleware.RateLimitAuth()) does not precede it (agent-os-n4ca.3)",
					fset.Position(call.Pos()), group.Name, group.Name)
			}
		}
		return true
	})

	if registrations == 0 {
		t.Fatal("found no authHandler.RegisterRoutes call in main.go; this test can no longer guard the login limiter and must follow the wiring")
	}
}
