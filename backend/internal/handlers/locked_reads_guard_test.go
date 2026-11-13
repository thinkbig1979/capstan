package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-w6wx guard: every handler that serves stack file content or a
// diff reads the env-unlock verdict, so a new route serving either cannot
// forget the masking. A function is in scope when it builds a ComposeResponse
// or calls a git diff getter (GetChangeDiff, GetDiff and their Masked forms);
// it must call envUnlocked( itself. It reads the sources through go:embed
// (handlerSources4hxa, atomic_write_refusal_4hxa_test.go), so an -overlay
// mutant of a handler file is what it parses.
func TestLockedReadsGuard(t *testing.T) {
	entries, err := handlerSources4hxa.ReadDir(".")
	require.NoError(t, err)

	diffGetters := map[string]bool{"GetChangeDiff": true, "GetDiff": true, "GetChangeDiffMasked": true, "GetDiffMasked": true}
	var inScope []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := handlerSources4hxa.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err, name)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			serves, gated := false, false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					if id, ok := x.Type.(*ast.Ident); ok && id.Name == "ComposeResponse" {
						serves = true
					}
				case *ast.CallExpr:
					switch f := x.Fun.(type) {
					case *ast.Ident:
						if f.Name == "envUnlocked" {
							gated = true
						}
					case *ast.SelectorExpr:
						if diffGetters[f.Sel.Name] {
							serves = true
						}
					}
				}
				return true
			})
			if !serves {
				continue
			}
			inScope = append(inScope, name+":"+fn.Name.Name)
			assert.True(t, gated, "%s %s serves compose content or a diff without reading envUnlocked(c): a locked session would get secrets the env views hide", name, fn.Name.Name)
		}
	}
	sort.Strings(inScope)
	// Not vacuous: the three handlers this bead gated are found.
	assert.Equal(t, []string{"compose.go:Get", "git.go:GetChangeDiff", "git.go:GetDiff"}, inScope)
}
