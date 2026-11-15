package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// projectFilterLabel is the label filter that matches every container of a
// compose project, `docker compose run` one-offs included.
const projectFilterLabel = "com.docker.compose.project="

// countingListCallers are the functions allowed to call GetContainerList or
// GetContainerListCtx, the project lists that KEEP one-offs, with the reason.
// Every other consumer is stack-scoped and must read GetServiceContainerList
// (agent-os-febs). An entry that no longer matches a call fails as stale.
var countingListCallers = map[string]string{
	"services/docker.go|GetContainerList":             "the bounded wrapper of GetContainerListCtx itself",
	"services/scanner.go|containersKeepRow":           "counts one-offs: dropping the row would hide them too (fail closed)",
	"handlers/stack_crud.go|deleteWithoutComposeFile": "counts one-offs: Delete refuses to hide them",
	"handlers/terminal.go|assertContainerInStack":     "PENDING follow-up (agent-os-febs report): must move to GetServiceContainerList, a one-off is not in the stack",
	"handlers/monitoring.go|getStackContainers":       "PENDING follow-up (agent-os-febs report): must move to GetServiceContainerList, a one-off is not in the stack",
}

// TestProjectContainerListsLeaveOneOffsOut_febs keeps one-offs out of every
// stack-scoped container list. Two arms:
//
//   - PRODUCER: a function holding a "com.docker.compose.project=" filter
//     literal also holds "com.docker.compose.oneoff=False" or calls
//     isOneOffContainer, so the list it builds can leave one-offs out.
//   - CONSUMER: a call to GetContainerList or GetContainerListCtx, which keep
//     one-offs, sits only in a function on countingListCallers.
//
// Scope: every non-test file in internal/<pkg>/, read from internalSources
// (update_history_guard_test.go), so a `go test -overlay` mutant reaches it.
// Cannot see: the filter built from smaller pieces or with fmt.Sprintf, a
// one-off check that is called but whose result is ignored, a counting list
// reached through a func value, or code outside internal/<pkg>/.
func TestProjectContainerListsLeaveOneOffsOut_febs(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	producers := 0
	seenCallers := map[string]bool{}

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
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := path + "|" + fn.Name.Name
			var filterAt token.Pos
			excludes := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if n.Kind == token.STRING && strings.Contains(n.Value, projectFilterLabel) && filterAt == token.NoPos {
						filterAt = n.Pos()
					}
					if n.Kind == token.STRING && strings.Contains(n.Value, "com.docker.compose.oneoff=False") {
						excludes = true
					}
				case *ast.CallExpr:
					name := ""
					switch f := n.Fun.(type) {
					case *ast.Ident:
						name = f.Name
					case *ast.SelectorExpr:
						name = f.Sel.Name
					}
					switch name {
					case "isOneOffContainer":
						excludes = true
					case "GetContainerList", "GetContainerListCtx":
						if _, allowed := countingListCallers[key]; allowed {
							seenCallers[key] = true
						} else {
							offenders = append(offenders, fset.Position(n.Pos()).String()+" in "+fn.Name.Name+" calls "+name+", which keeps `docker compose run` one-offs: a stack-scoped consumer reads GetServiceContainerList")
						}
					}
				}
				return true
			})
			if filterAt != token.NoPos {
				producers++
				if !excludes {
					offenders = append(offenders, fset.Position(filterAt).String()+" in "+fn.Name.Name+" lists a compose project's containers with no one-off exclusion (oneoff=False filter or isOneOffContainer)")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Positive arms: the walk must see the known producers (GetContainerList's
	// helper, GetContainersForStack, serviceContainers) and every allowlisted
	// caller, or a clean result means it saw nothing.
	if producers < 3 {
		t.Errorf("the walk found %d functions holding %q, want at least 3; it no longer covers the project lists", producers, projectFilterLabel)
	}
	var stale []string
	for k := range countingListCallers {
		if !seenCallers[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	for _, k := range stale {
		t.Errorf("countingListCallers entry %q matches no call any more: remove it", k)
	}
	for _, o := range offenders {
		t.Errorf("%s (agent-os-febs)", o)
	}
}
