package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// diskReadAllowlist names the test functions allowed to parse source from disk,
// each with the reason a disk read is acceptable there. Empty in this package.
var diskReadAllowlist = map[string]string{}

// TestSourceGuardsReadEmbeddedSources keeps every test in this package that
// parses Go source on go:embed instead of the disk (agent-os-qags.14). A
// source guard asserts on code that exposes no seam, and a `go test -overlay`
// mutant is how it is shown to fail; the overlay only reaches compiled and
// embedded content, so parser.ParseFile(fset, path, nil, 0) reads the real file
// at run time and stays green under the very mutant that should turn it red.
// This is the per-package sibling of cmd/server's
// TestSourceGuardsReadEmbeddedMain (agent-os-qags.9).
//
// The *_test.go files are read from disk on purpose: they are not the mutation
// target. Flagged: ParseFile with a nil source, ParseFile whose source is a
// direct os.ReadFile call, and any ParseDir. Not seen: a source variable
// assigned from os.ReadFile on an earlier line (this test's own read has that
// shape), and a package that has no such file yet, because this guard lives
// in the package it guards.
func TestSourceGuardsReadEmbeddedSources(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	var safe int
	used := map[string]bool{}
	for _, name := range files {
		src, err := os.ReadFile(name) //nolint:gosec // name comes from Glob("*_test.go") in this package directory
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		bad, ok := diskSourceParses(file, diskReadAllowlist, used)
		safe += ok
		for _, pos := range bad {
			t.Errorf("%s: parses Go source from disk, so a go test -overlay mutant of the file it guards cannot turn it red; read it from a go:embed variable instead (agent-os-qags.14)",
				fset.Position(pos))
		}
	}

	for fn := range diskReadAllowlist {
		if !used[fn] {
			t.Errorf("diskReadAllowlist names %s but it has no disk-reading parse; remove the stale entry", fn)
		}
	}
	if safe == 0 {
		t.Fatal("found no embedded-source parser.ParseFile call in any *_test.go; this guard can no longer see the source-reading tests and must be updated (agent-os-qags.14)")
	}
}

// TestSourceGuardsReadEmbeddedSources_CheckerSeesTheShapes proves the walk
// reports each way a guard can read the disk and stays quiet on the embedded
// form, so a clean run above is not a checker that sees nothing.
func TestSourceGuardsReadEmbeddedSources_CheckerSeesTheShapes(t *testing.T) {
	const head = "package p\nimport (\"go/parser\"; \"go/token\"; \"os\")\n"
	cases := []struct {
		name    string
		body    string
		allow   map[string]string
		wantBad int
		wantOK  int
	}{
		{"nil source", "func f() { parser.ParseFile(token.NewFileSet(), \"a.go\", nil, 0) }", nil, 1, 0},
		{"direct os.ReadFile", "func f() { parser.ParseFile(token.NewFileSet(), \"a.go\", os.ReadFile(\"a.go\"), 0) }", nil, 1, 0},
		{"ParseDir", "func f() { parser.ParseDir(token.NewFileSet(), \".\", nil, 0) }", nil, 1, 0},
		{"embedded source variable", "func f(src []byte) { parser.ParseFile(token.NewFileSet(), \"a.go\", src, 0) }", nil, 0, 1},
		{"allowlisted function", "func f() { parser.ParseFile(token.NewFileSet(), \"a.go\", nil, 0) }", map[string]string{"f": "x"}, 0, 0},
		{"allowlist does not cover its neighbour", "func f() { parser.ParseFile(token.NewFileSet(), \"a.go\", nil, 0) }\nfunc g() { parser.ParseFile(token.NewFileSet(), \"a.go\", nil, 0) }", map[string]string{"f": "x"}, 1, 0},
		{"package-level initialiser", "var _ = func() { parser.ParseFile(token.NewFileSet(), \"a.go\", nil, 0) }", nil, 1, 0},
		{"other receiver named ParseFile", "func f(x interface{ ParseFile(...any) }) { x.ParseFile(1, 2, nil) }", nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "planted.go", head+tc.body, 0)
			if err != nil {
				t.Fatal(err)
			}
			bad, ok := diskSourceParses(file, tc.allow, map[string]bool{})
			if len(bad) != tc.wantBad || ok != tc.wantOK {
				t.Fatalf("got %d disk parses and %d embedded, want %d and %d", len(bad), ok, tc.wantBad, tc.wantOK)
			}
		})
	}
}

// diskSourceParses returns the position of every parser.ParseFile/ParseDir call
// in file that reads the disk and is not in allow, plus the count of ParseFile
// calls that read a supplied source. Allowlisted hits mark used[funcName].
func diskSourceParses(file *ast.File, allow map[string]string, used map[string]bool) (bad []token.Pos, embedded int) {
	for _, decl := range file.Decls {
		fnName := ""
		if fn, ok := decl.(*ast.FuncDecl); ok {
			fnName = fn.Name.Name
		}
		_, allowed := allow[fnName]
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "parser" {
				return true
			}
			reads := false
			switch sel.Sel.Name {
			case "ParseDir":
				reads = true
			case "ParseFile":
				if len(call.Args) < 3 {
					return true
				}
				reads = readsDisk(call.Args[2])
			default:
				return true
			}
			switch {
			case !reads:
				embedded++
			case allowed:
				used[fnName] = true
			default:
				bad = append(bad, call.Pos())
			}
			return true
		})
	}
	return bad, embedded
}

// readsDisk reports whether a ParseFile source argument is the nil literal
// (parser opens the file) or a direct os.ReadFile call.
func readsDisk(arg ast.Expr) bool {
	switch a := arg.(type) {
	case *ast.Ident:
		return a.Name == "nil"
	case *ast.CallExpr:
		sel, ok := a.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "os" && sel.Sel.Name == "ReadFile"
	}
	return false
}
