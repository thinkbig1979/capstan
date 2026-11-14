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
// compose service, one-offs included.
const serviceFilterLabel = "com.docker.compose.service="

// TestServiceLabelFilterOnlyInHelper_fjd6 keeps every compose service-label
// filter on services.serviceContainers, which excludes `docker compose run`
// one-offs (agent-os-fjd6, agent-os-e04r): a string literal containing
// "com.docker.compose.service=" may appear only inside that helper. Reading
// the label off a container (Labels["com.docker.compose.service"]) has no "="
// and is not matched.
//
// Scope: every non-test file in internal/<pkg>/, read from internalSources
// (update_history_guard_test.go), so a `go test -overlay` mutant reaches it.
// Cannot see: the label assembled from pieces ("...service" + "=" + name), or
// a filter built outside internal/<pkg>/.
func TestServiceLabelFilterOnlyInHelper_fjd6(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	inHelper := 0

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
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !strings.Contains(lit.Value, serviceFilterLabel) {
					return true
				}
				fn, isFunc := decl.(*ast.FuncDecl)
				if isFunc && path == "services/docker_update.go" && fn.Name.Name == "serviceContainers" {
					inHelper++
					return true
				}
				offenders = append(offenders, fset.Position(lit.Pos()).String())
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
	for _, o := range offenders {
		t.Errorf("%s builds a compose service-label filter outside services.serviceContainers, so it matches one-offs too (agent-os-fjd6)", o)
	}
}
