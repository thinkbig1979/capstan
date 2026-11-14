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

// updateVerbs are the calls that update a container in place: each changes
// what a stack runs, so each caller stores the stack's re-read status.
var updateVerbs = map[string]bool{
	"UpdateContainer": true, "UpdateContainerStreaming": true, "UpdateComposeServiceStreaming": true,
}

// updateStatusStores are the calls that store it. storeStackStatusAfterUpdate
// is the scheduler's wrapper; the test checks that it calls
// StoreObservedStackStatus itself.
var updateStatusStores = map[string]bool{
	"StoreObservedStackStatus": true, "storeStackStatusAfterUpdate": true,
}

// TestUpdateCallsStoreObservedStatus_Txqx keeps every update entry point on
// services.StoreObservedStackStatus (agent-os-txqx): in each function, the
// update calls are matched by at least as many store calls, closures and
// defers included. It keys on the method name, not the receiver, as
// TestLifecycleCallsStoreStatus_Vush does.
//
// Scope: every non-test file in internal/<pkg>/, read from internalSources,
// so a `go test -overlay` mutant reaches it. Cannot see: a call through a
// function value or a method expression, a store that is unreachable on
// some exit path (the count is per function, not per path), and callers
// outside internal/<pkg>/.
func TestUpdateCallsStoreObservedStatus_Txqx(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	seen := map[string]int{}
	wrapperStores := -1

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
			verbs, stores, direct := countUpdateCalls(fn.Body)
			if path == "services/scheduler.go" && fn.Name.Name == "storeStackStatusAfterUpdate" {
				wrapperStores = direct
			}
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

	// Positive arm: the walk must see both callers' files, or a clean result
	// below means nothing.
	for _, want := range []string{"handlers/updates.go", "services/scheduler.go"} {
		if seen[want] == 0 {
			t.Errorf("the walk saw no update call in %s; it no longer covers the callers (seen: %v)", want, seen)
		}
	}
	if wrapperStores < 1 {
		t.Errorf("services/scheduler.go storeStackStatusAfterUpdate does not call StoreObservedStackStatus (found %d), so counting it as a store hides a missing one", wrapperStores)
	}

	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("%s updates a container without storing the stack's status through services.StoreObservedStackStatus for each call (agent-os-txqx)", o)
	}
}

// countUpdateCalls counts the update calls, the store calls (wrapper
// included) and the direct StoreObservedStackStatus calls in body.
func countUpdateCalls(body ast.Node) (verbs, stores, direct int) {
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
		if updateVerbs[name] {
			verbs++
		}
		if updateStatusStores[name] {
			stores++
		}
		if name == "StoreObservedStackStatus" {
			direct++
		}
		return true
	})
	return verbs, stores, direct
}
