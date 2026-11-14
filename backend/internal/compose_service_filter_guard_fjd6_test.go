package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// serviceFilterLabel is the label filter that matches every container of a
// compose service, one-offs included; serviceLabelKey is its key alone, and
// oneOffExclusion is the filter that keeps one-offs out.
const (
	serviceFilterLabel = "com.docker.compose.service="
	serviceLabelKey    = `"com.docker.compose.service"`
	oneOffExclusion    = "com.docker.compose.oneoff=False"
)

// TestServiceLabelFilterOnlyInHelper_fjd6 keeps every compose service-label
// filter on services.serviceContainers, which excludes `docker compose run`
// one-offs (agent-os-fjd6, agent-os-e04r). Three arms:
//
//   - a string literal containing "com.docker.compose.service=" appears only
//     inside that helper;
//   - the bare key "com.docker.compose.service" is never concatenated (+) or
//     bound to a const or var outside the helper, the two ways to build the
//     filter without spelling its "=". Reading the label off a container,
//     Labels["com.docker.compose.service"], is neither and stays allowed;
//   - the helper itself still holds the "com.docker.compose.oneoff=False"
//     literal, so it cannot lose the exclusion while every caller stays on it.
//
// Scope: every non-test file in internal/<pkg>/, read from internalSources
// (update_history_guard_test.go), so a `go test -overlay` mutant reaches it.
// Cannot see: the key built from smaller pieces ("com.docker.compose." +
// "service"), fmt.Sprintf, or a filter built outside internal/<pkg>/.
func TestServiceLabelFilterOnlyInHelper_fjd6(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	inHelper, exclusionInHelper := 0, 0

	err := fs.WalkDir(internalSources, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := internalSources.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			helper := isFunc && path == "services/docker_update.go" && fn.Name.Name == "serviceContainers"
			var parents []ast.Node
			ast.Inspect(decl, func(n ast.Node) bool {
				if n == nil {
					parents = parents[:len(parents)-1]
					return true
				}
				parent := ast.Node(nil)
				if len(parents) > 0 {
					parent = parents[len(parents)-1]
				}
				parents = append(parents, n)

				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				switch {
				case strings.Contains(lit.Value, oneOffExclusion) && helper:
					exclusionInHelper++
				case strings.Contains(lit.Value, serviceFilterLabel):
					if helper {
						inHelper++
					} else {
						offenders = append(offenders, fset.Position(lit.Pos()).String()+" builds a compose service-label filter")
					}
				case lit.Value == serviceLabelKey && !helper:
					switch parent.(type) {
					case *ast.BinaryExpr:
						offenders = append(offenders, fset.Position(lit.Pos()).String()+" concatenates the compose service label key")
					case *ast.ValueSpec:
						offenders = append(offenders, fset.Position(lit.Pos()).String()+" binds the compose service label key to a name")
					}
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Positive arm: the walk must find the helper's own filter, or a clean
	// result below means the walk saw nothing.
	if inHelper == 0 {
		t.Errorf("the walk found no %q literal in services.serviceContainers; it no longer covers the helper", serviceFilterLabel)
	}
	if exclusionInHelper == 0 {
		t.Errorf("services.serviceContainers no longer holds %q, so every service lookup matches one-offs again (agent-os-fjd6)", oneOffExclusion)
	}
	for _, o := range offenders {
		t.Errorf("%s outside services.serviceContainers, so it matches one-offs too (agent-os-fjd6)", o)
	}
}
