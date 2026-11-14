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

// lifecycleVerbs are the verified calls that change what a stack runs. Pull
// is not one: it changes images, not the stack's state.
var lifecycleVerbs = map[string]bool{
	"StartVerified": true, "StopVerified": true, "RestartVerified": true, "DeleteVerified": true,
}

// TestLifecycleCallsStoreStatus_Vush keeps every lifecycle call on
// services.StoreLifecycleStatus (agent-os-vush): in each function, the
// verified lifecycle calls are matched by at least as many
// StoreLifecycleStatus calls. It keys on the method name, not the receiver,
// so h.dockerSvc(), s.dockerSvc() and a plain docker value all count.
// The one exemption is RestartVerified in services/docker_lifecycle.go: it
// composes Stop and Start with no store to write to, and its callers store
// its result. Any other function in that file is checked.
//
// Scope: every non-test file in internal/<pkg>/, read from internalSources
// (update_history_guard_test.go), so a `go test -overlay` mutant reaches it.
// Cannot see: a call through a function value or a method expression, and a
// function that stores once for two calls that need two stores but has a
// third, unrelated store (the count is per function, not per call).
func TestLifecycleCallsStoreStatus_Vush(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	seen := map[string]int{}

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
			if path == "services/docker_lifecycle.go" && fn.Name.Name == "RestartVerified" {
				// It composes Stop and Start and has no store to write to;
				// its callers store its result.
				continue
			}
			verbs, stores := countLifecycleCalls(fn.Body)
			if verbs == 0 {
				continue
			}
			seen[path] += verbs
			if stores < verbs {
				offenders = append(offenders, fset.Position(fn.Pos()).String()+" "+fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Positive arm: the walk must see the callers that exist today, one per
	// receiver shape, or a clean result below means nothing.
	for _, want := range []string{
		"handlers/stack_lifecycle.go", // h.dockerSvc()
		"handlers/stack_crud.go",
		"services/backup.go", // s.dockerSvc()
		"services/git.go",    // docker.
	} {
		if seen[want] == 0 {
			t.Errorf("the walk saw no lifecycle call in %s; it no longer covers the callers (seen: %v)", want, seen)
		}
	}

	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("%s calls a *Verified lifecycle method without services.StoreLifecycleStatus for each call (agent-os-vush)", o)
	}
}

// countLifecycleCalls counts the lifecycle verb calls and the
// StoreLifecycleStatus calls in body, closures included.
func countLifecycleCalls(body ast.Node) (verbs, stores int) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := ""
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			name = f.Sel.Name
		case *ast.Ident:
			name = f.Name
		}
		if lifecycleVerbs[name] {
			verbs++
		}
		if name == "StoreLifecycleStatus" {
			stores++
		}
		return true
	})
	return verbs, stores
}
