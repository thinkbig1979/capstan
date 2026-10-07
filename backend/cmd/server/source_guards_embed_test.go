package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSourceGuardsReadEmbeddedMain keeps every test that parses main.go on
// mainSource (go:embed, declared in oplock_wiring_test.go) instead of the
// disk (agent-os-qags.9). main() exposes no seam, so these tests assert on its
// source; a `go test -overlay` mutant of main.go is how each one is shown to
// fail, and the overlay only reaches go:embed. parser.ParseFile("main.go", nil)
// reads the real file at run time, so a guard written that way stays green under
// the very mutant that should turn it red. Three of them did until this fix.
//
// This reads the *_test.go files from disk on purpose: they are not the
// mutation target. It flags any ParseFile call on "main.go" whose source
// argument is not the identifier mainSource. A file named through a variable or
// constant is not seen; the positive control below only proves the literal form
// is.
func TestSourceGuardsReadEmbeddedMain(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	var mainParses int
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "ParseFile" {
				return true
			}
			lit, ok := call.Args[1].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if path, err := strconv.Unquote(lit.Value); err != nil || strings.TrimPrefix(path, "./") != "main.go" {
				return true
			}
			mainParses++
			if id, ok := call.Args[2].(*ast.Ident); !ok || id.Name != "mainSource" {
				t.Errorf("%s: parses main.go from disk, so a go test -overlay mutant of main.go cannot turn it red; pass mainSource as the source argument (agent-os-qags.9)",
					fset.Position(call.Pos()))
			}
			return true
		})
	}

	if mainParses == 0 {
		t.Fatal("found no parser.ParseFile(..., \"main.go\", ...) call in any *_test.go; this guard can no longer see the source-reading tests and must be updated (agent-os-qags.9)")
	}
}
