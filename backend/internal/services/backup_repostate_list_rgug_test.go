package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllRepoStatesListsEveryRepoStateConstant keeps AllRepoStates equal to
// the RepoState constants declared in this package (agent-os-rgug): handlers
// checks its dispositions against AllRepoStates, so a constant missing here
// would escape that check. Read through go:embed (packageSources), so a
// `go test -overlay` mutant is what this sees.
func TestAllRepoStatesListsEveryRepoStateConstant(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	declared, listedNames := map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if id, ok := vs.Type.(*ast.Ident); gd.Tok == token.CONST && ok && id.Name == "RepoState" {
					for _, n := range vs.Names {
						declared[n.Name] = true
					}
				}
				if gd.Tok == token.VAR && len(vs.Names) == 1 && vs.Names[0].Name == "AllRepoStates" && len(vs.Values) == 1 {
					lit, ok := vs.Values[0].(*ast.CompositeLit)
					require.True(t, ok, "AllRepoStates must be a slice literal")
					for _, elt := range lit.Elts {
						if id, ok := elt.(*ast.Ident); ok {
							listedNames[id.Name] = true
						}
					}
				}
			}
		}
	}

	for name := range declared {
		assert.True(t, listedNames[name], "%s is a RepoState constant but AllRepoStates does not list it (agent-os-rgug)", name)
	}
	for name := range listedNames {
		assert.True(t, declared[name], "AllRepoStates lists %s, which is not a declared RepoState constant", name)
	}
	seen := map[RepoState]bool{}
	for _, s := range AllRepoStates {
		assert.False(t, seen[s], "AllRepoStates lists %q twice", s)
		seen[s] = true
	}

	// Positive control: the scan must see the known constants, or a match
	// above means it saw nothing.
	for _, want := range []string{"RepoStateOK", "RepoStateUninitialized", "RepoStateLocked"} {
		assert.True(t, declared[want], "the scan did not see %s: its const detection is blind", want)
	}
}
