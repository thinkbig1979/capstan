package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestZ0gl_GitChildrenOnlyFromGitCmdWithCreds keeps every git child on the
// one constructor that applies the credentials (agent-os-z0gl): a git child
// built anywhere else gets neither the SSH key nor the HTTPS helper nor
// ojdu's hardening prefix. It finds every call to a process constructor
// whose arguments include the string literal "git" in this package's
// non-test files, read through go:embed (packageSources) so a `go test
// -overlay` mutant reaches it. A program name held in a variable or constant
// is not seen.
func TestZ0gl_GitChildrenOnlyFromGitCmdWithCreds(t *testing.T) {
	constructors := map[string]bool{
		"boundCommand": true, "commandWithDeadline": true,
		"execCommand": true, "execCommandContext": true,
		"exec.Command": true, "exec.CommandContext": true, "execx.Command": true,
	}
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var sites []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !constructors[calleeName(call.Fun)] {
					return true
				}
				for _, a := range call.Args {
					if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if v, err := strconv.Unquote(lit.Value); err == nil && v == "git" {
							sites = append(sites, fn.Name.Name+" "+fset.Position(call.Pos()).String())
						}
					}
				}
				return true
			})
		}
	}
	sort.Strings(sites)
	require.NotEmpty(t, sites, "the scan found no git child at all: it is blind, not clean")
	for _, s := range sites {
		assert.True(t, strings.HasPrefix(s, "gitCmdWithCreds "), "a git child built outside gitCmdWithCreds: %s", s)
	}
	t.Logf("git children: %v", sites)
}

func calleeName(e ast.Expr) string {
	switch f := e.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			return x.Name + "." + f.Sel.Name
		}
	}
	return ""
}
