package services

import (
	"embed"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// packageSources is this package's own source, embedded rather than read from
// disk so a `go test -overlay` mutant is what the guard sees.
//
//go:embed *.go
var packageSources embed.FS

// readOnlyComposeSubcommands are the only subcommands buildComposeArgs may be
// called with directly: they read and change no container.
var readOnlyComposeSubcommands = map[string]bool{"logs": true, "ps": true}

// TestBuildComposeArgs_OnlyReadOnlyCallersBypassTheSharedNameCheck is
// agent-os-z91e.38's guard. Every compose command that changes containers must
// get its argv from mutatingComposeArgs, which refuses a stack whose project
// name another stack carries. buildComposeArgs has no error return and cannot
// refuse, so a new mutating call site written against it would quietly skip
// the check. This fails on any buildComposeArgs call outside
// mutatingComposeArgs whose subcommand is not a literal "logs" or "ps".
func TestBuildComposeArgs_OnlyReadOnlyCallersBypassTheSharedNameCheck(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	readOnly := 0
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "mutatingComposeArgs" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "buildComposeArgs" {
					return true
				}
				if len(call.Args) >= 2 {
					if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if sub, err := strconv.Unquote(lit.Value); err == nil && readOnlyComposeSubcommands[sub] {
							readOnly++
							return true
						}
					}
				}
				t.Errorf("%s: %s calls buildComposeArgs for a subcommand that is not a literal \"logs\" or \"ps\"; "+
					"a compose command that can change containers must use mutatingComposeArgs (agent-os-z91e.38)",
					fset.Position(call.Pos()), fn.Name.Name)
				return true
			})
		}
	}
	// The read-only callers (Logs, Status) must be seen, or the walk saw nothing.
	if readOnly < 2 {
		t.Fatalf("found %d read-only buildComposeArgs calls, want at least 2 (Logs and Status): the guard is not reading the package", readOnly)
	}
}
