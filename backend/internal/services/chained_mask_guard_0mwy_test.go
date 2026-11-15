package services

import (
	"embed"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The package's own source, embedded rather than read from disk so a
// go test -overlay mutant is what the guard reads.
//
//go:embed *.go
var servicesSource0mwy embed.FS

// secretSliceName0mwy is a range expression that holds secrets to mask.
var secretSliceName0mwy = regexp.MustCompile(`(?i)secret|needle`)

// agent-os-0mwy guard: masking a list of secrets one strings.Replace at a
// time lets a masked secret break the match of another that overlapped it,
// leaving part of that one shown (agent-os-buwq). Every such list goes
// through maskNeedleUnion (or its run finder, needleUnionRuns). This flags a
// strings.Replace or strings.ReplaceAll inside a range over anything named
// like secrets or needles, in the package's non-test files.
func TestChainedSecretReplaceGuard_0mwy(t *testing.T) {
	entries, err := servicesSource0mwy.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var sites []string
	parsed := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := servicesSource0mwy.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		parsed[name] = true
		ast.Inspect(f, func(n ast.Node) bool {
			r, ok := n.(*ast.RangeStmt)
			if !ok || !secretSliceName0mwy.MatchString(rangeName0mwy(r.X)) {
				return true
			}
			ast.Inspect(r.Body, func(m ast.Node) bool {
				if call, ok := m.(*ast.CallExpr); ok && isStringsReplace0mwy(call) {
					sites = append(sites, fset.Position(call.Pos()).String())
				}
				return true
			})
			return true
		})
	}
	require.True(t, parsed["redact_compose.go"] && parsed["backup_rclone_redact.go"], "the embedded source must be the package's (%d files)", len(parsed))
	require.Empty(t, sites, "chained masking over a secret list: use maskNeedleUnion (agent-os-0mwy)")
}

// rangeName0mwy is the name a range expression is read by: secrets,
// r.secrets, secrets[1:], secretNeedles(secrets).
func rangeName0mwy(x ast.Expr) string {
	switch v := x.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	case *ast.SliceExpr:
		return rangeName0mwy(v.X)
	case *ast.IndexExpr:
		return rangeName0mwy(v.X)
	case *ast.ParenExpr:
		return rangeName0mwy(v.X)
	case *ast.CallExpr:
		return rangeName0mwy(v.Fun)
	}
	return ""
}

func isStringsReplace0mwy(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "strings" && (sel.Sel.Name == "ReplaceAll" || sel.Sel.Name == "Replace")
}
