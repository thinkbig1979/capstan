package services

import (
	"embed"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
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
// through maskNeedleUnion (or its run finder, needleUnionRuns). In the
// package's non-test files this flags (agent-os-75hd item 3, widened after
// rv-mask2 F5 passed four variants):
//   - a strings.Replace or strings.ReplaceAll inside a range over anything
//     named like secrets or needles, or inside a for loop that indexes one;
//   - inside such a loop, a call to a package function whose own body calls
//     strings.Replace* (one level of helper);
//   - a strings.Replace* inside any range whose old-string argument is the
//     range's value or key variable, whatever the range is named;
//   - any strings.NewReplacer: it applies its pairs leftmost-first, the same
//     overlap break.
func TestChainedSecretReplaceGuard_0mwy(t *testing.T) {
	files := parseServicesSource0mwy(t)
	require.Empty(t, chainedReplaceSites0mwy(files), "chained masking over a secret list: use maskNeedleUnion (agent-os-0mwy)")
}

// parseServicesSource0mwy parses the embedded non-test files of the package.
func parseServicesSource0mwy(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := servicesSource0mwy.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := servicesSource0mwy.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		files[name] = f
	}
	require.True(t, files["redact_compose.go"] != nil && files["backup_rclone_redact.go"] != nil, "the embedded source must be the package's (%d files)", len(files))
	return files
}

// chainedReplaceSites0mwy is every site the guard flags, as "file: func: what".
func chainedReplaceSites0mwy(files map[string]*ast.File) []string {
	// The package functions whose body calls strings.Replace*.
	replacers := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil && fd.Recv == nil && containsReplace0mwy(fd.Body) {
				replacers[fd.Name.Name] = true
			}
		}
	}
	var sites []string
	for name, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			at := name + ": " + fd.Name.Name + ": "
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				var body *ast.BlockStmt
				secretLoop := false
				switch v := n.(type) {
				case *ast.CallExpr:
					if isStringsCall0mwy(v, "NewReplacer") {
						sites = append(sites, at+"strings.NewReplacer")
					}
					return true
				case *ast.RangeStmt:
					body = v.Body
					secretLoop = secretSliceName0mwy.MatchString(rangeName0mwy(v.X))
					loopVars := map[string]bool{}
					for _, e := range []ast.Expr{v.Key, v.Value} {
						if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
							loopVars[id.Name] = true
						}
					}
					ast.Inspect(body, func(m ast.Node) bool {
						if call, ok := m.(*ast.CallExpr); ok && isStringsReplace0mwy(call) && len(call.Args) >= 2 {
							if id, ok := call.Args[1].(*ast.Ident); ok && loopVars[id.Name] {
								sites = append(sites, at+"Replace of the range variable "+id.Name)
							}
						}
						return true
					})
				case *ast.ForStmt:
					body = v.Body
					ast.Inspect(body, func(m ast.Node) bool {
						if ix, ok := m.(*ast.IndexExpr); ok && secretSliceName0mwy.MatchString(rangeName0mwy(ix.X)) {
							secretLoop = true
						}
						return true
					})
				default:
					return true
				}
				if !secretLoop {
					return true
				}
				ast.Inspect(body, func(m ast.Node) bool {
					call, ok := m.(*ast.CallExpr)
					if !ok {
						return true
					}
					if isStringsReplace0mwy(call) {
						sites = append(sites, at+"strings.Replace* in a secret loop")
					} else if id, ok := call.Fun.(*ast.Ident); ok && replacers[id.Name] {
						sites = append(sites, at+"helper "+id.Name+" (calls strings.Replace*) in a secret loop")
					}
					return true
				})
				return true
			})
		}
	}
	sort.Strings(sites)
	return sites
}

func containsReplace0mwy(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if call, ok := m.(*ast.CallExpr); ok && isStringsReplace0mwy(call) {
			found = true
		}
		return !found
	})
	return found
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
	return isStringsCall0mwy(call, "ReplaceAll") || isStringsCall0mwy(call, "Replace")
}

func isStringsCall0mwy(call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "strings" && sel.Sel.Name == name
}
