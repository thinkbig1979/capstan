package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// routeRegistrars are the calls in main() that attach handlers to the router.
// gin's Use only reaches routes registered AFTER it (NoRoute excepted), so the
// Host check must precede every one of these.
var routeRegistrars = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
	"HEAD": true, "OPTIONS": true, "Any": true, "Handle": true,
	"Static": true, "StaticFile": true, "StaticFS": true, "NoRoute": true,
	"Group": true, "wireStacksGroup": true,
}

func isRouteRegistrar(name string) bool {
	return routeRegistrars[name] || strings.HasPrefix(name, "Register") || strings.HasPrefix(name, "register")
}

// TestMain_AllowedHostsIsGlobalAndPrecedesEveryRoute is agent-os-n4ca.1's
// wiring check. middleware.AllowedHosts is what keeps a DNS-rebinding page off
// the AUTH_DISABLED bypass, and its own tests build their own router, so they
// stay green if main.go drops the registration, puts it on a group instead of
// the engine, feeds it the wrong config, or registers it after some routes.
// This pins all four against main.go's source (mainSource is go:embed'ed in
// oplock_wiring_test.go, so a `go test -overlay` mutant is what it reads).
func TestMain_AllowedHostsIsGlobalAndPrecedesEveryRoute(t *testing.T) {
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
		t.Fatal("no func main in main.go")
	}

	// The engine variable: the one assigned from gin.New().
	var engine string
	ast.Inspect(mainFn, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "New" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "gin" {
					engine = as.Lhs[0].(*ast.Ident).Name
				}
			}
		}
		return true
	})
	if engine == "" {
		t.Fatal("no `x := gin.New()` in main(); this test must follow the engine")
	}

	var hostCheck token.Pos
	var registrars []token.Pos
	ast.Inspect(mainFn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		if isRouteRegistrar(name) {
			registrars = append(registrars, call.Pos())
		}

		// engine.Use(middleware.AllowedHosts(cfg.AuthDisabled, cfg.AllowedHosts))
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Use" || len(call.Args) != 1 {
			return true
		}
		inner, ok := call.Args[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		innerSel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || innerSel.Sel.Name != "AllowedHosts" {
			return true
		}
		if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != engine {
			t.Errorf("%s: AllowedHosts is registered on %s, not on the engine %q; a group leaves the "+
				"routes outside it (health, static, SPA index) open to DNS rebinding",
				fset.Position(call.Pos()), exprName(sel.X), engine)
			return true
		}
		wantArgs := []string{"AuthDisabled", "AllowedHosts"}
		if len(inner.Args) != len(wantArgs) {
			t.Errorf("%s: AllowedHosts takes %d args, want %d", fset.Position(inner.Pos()), len(inner.Args), len(wantArgs))
			return true
		}
		for i, want := range wantArgs {
			if got := exprName(inner.Args[i]); got != "cfg."+want {
				t.Errorf("%s: AllowedHosts arg %d is %s, want cfg.%s", fset.Position(inner.Pos()), i, got, want)
			}
		}
		hostCheck = call.Pos()
		return true
	})

	if hostCheck == token.NoPos {
		t.Fatalf("main() never calls %s.Use(middleware.AllowedHosts(...)): with AUTH_DISABLED every route "+
			"and WebSocket is reachable from a DNS-rebinding page (agent-os-n4ca.1)", engine)
	}
	if len(registrars) == 0 {
		t.Fatal("found no route registration in main(); the ordering half of this test can no longer fail")
	}
	for _, p := range registrars {
		if p < hostCheck {
			t.Errorf("%s registers routes before AllowedHosts (%s); gin's Use does not reach them",
				fset.Position(p), fset.Position(hostCheck))
		}
	}
}

func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprName(v.X) + "." + v.Sel.Name
	}
	return "<expr>"
}
