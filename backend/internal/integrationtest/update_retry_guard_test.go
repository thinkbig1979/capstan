package integrationtest

// No build tag, on purpose: the rest of this package is behind
// //go:build integration and runs only where Docker is, so this guard would
// run only there too. Untagged, it runs in the plain unit job, reading the
// tagged sources as text (agent-os-18uw).

import (
	"embed"
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

// Embedded rather than read with os.ReadFile, so an -overlay mutant of a
// source file is what the guard reads.
//
//go:embed *.go
var packageSources embed.FS

// updateCalls are the update paths whose registry pull is not retried by the
// code under test: a slow registry in CI fails them with nothing wrong.
var updateCalls = map[string]bool{
	"UpdateContainer":               true,
	"UpdateContainerStreaming":      true,
	"UpdateComposeServiceStreaming": true,
}

// unretriedUpdateAllowed lists the calls that may skip RetryUpdateAtPull, by
// file and enclosing t.Run name, with the reason. Each must still match.
var unretriedUpdateAllowed = map[string]string{
	// The update is refused before any pull, so a retry has nothing to retry
	// and the test asserts the failure.
	"docker_update_autoremove_64me_test.go/refused": "refused before the pull; expects failed",
}

// unretriedUpdates returns "file/subtest:line method" for every update call
// in src that is not inside a func literal passed to RetryUpdateAtPull.
func unretriedUpdates(t *testing.T, name, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	require.NoError(t, err, name)

	var found []string
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !updateCalls[sel.Sel.Name] {
			return true
		}
		if !insideRetry(stack) {
			found = append(found, name+"/"+subtestName(stack)+":"+
				strconv.Itoa(fset.Position(call.Pos()).Line)+" "+sel.Sel.Name)
		}
		return true
	})
	return found
}

// insideRetry reports whether the innermost func literal around the call
// (the last node on stack) is an argument of a RetryUpdateAtPull call.
func insideRetry(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		lit, ok := stack[i].(*ast.FuncLit)
		if !ok {
			continue
		}
		if i == 0 {
			return false
		}
		call, ok := stack[i-1].(*ast.CallExpr)
		if !ok {
			return false
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "RetryUpdateAtPull" {
			return false
		}
		for _, arg := range call.Args {
			if arg == lit {
				return true
			}
		}
		return false
	}
	return false
}

// subtestName returns the name of the innermost t.Run around the call, "-"
// when there is none.
func subtestName(stack []ast.Node) string {
	for i := len(stack) - 1; i >= 0; i-- {
		call, ok := stack[i].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" || len(call.Args) == 0 {
			continue
		}
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				return s
			}
		}
	}
	return "-"
}

func TestUpdateCallsRetryAtPull_18uw(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)

	var bad []string
	used := map[string]bool{}
	scanned, calls := 0, 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || name == "update_retry_guard_test.go" {
			continue
		}
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		scanned++
		calls += strings.Count(string(src), ".UpdateContainer(") +
			strings.Count(string(src), ".UpdateContainerStreaming(") +
			strings.Count(string(src), ".UpdateComposeServiceStreaming(")
		for _, site := range unretriedUpdates(t, name, string(src)) {
			key := site[:strings.LastIndex(site, ":")]
			if _, ok := unretriedUpdateAllowed[key]; ok {
				used[key] = true
				continue
			}
			bad = append(bad, site)
		}
	}
	sort.Strings(bad)
	// Positive side: the guard read the package and saw update calls.
	require.Greater(t, scanned, 10, "files scanned")
	require.Greater(t, calls, 0, "update calls seen")
	t.Logf("%d files, %d update calls", scanned, calls)

	assert.Empty(t, bad, "update calls outside RetryUpdateAtPull: a slow registry fails them (wrap as docker_update_apply_test.go does, or allowlist with a reason)")
	for key := range unretriedUpdateAllowed {
		assert.True(t, used[key], "allowlist entry matches nothing (remove it): %s", key)
	}
}

// The guard fires on the shapes it exists for and stays quiet on a wrapped call.
func TestUpdateCallsRetryAtPull_18uw_Detects(t *testing.T) {
	src := `package x
func a() {
	_, ar := svc.UpdateContainer(ctx, id, nil)
	t.Run("sub", func(t *testing.T) {
		svc.UpdateComposeServiceStreaming(ctx, s, "web", e, st)
	})
	// A func literal that is not RetryUpdateAtPull's argument is no cover.
	go func() { svc.UpdateContainerStreaming(ctx, id, db, e, st) }()
	ar = RetryUpdateAtPull(ctx, t, func() (truth.ActionResult, bool, string) {
		_, ar := svc.UpdateContainer(ctx, id, nil)
		return ar, false, ""
	})
}
`
	assert.Equal(t, []string{
		"p.go/-:3 UpdateContainer",
		"p.go/sub:5 UpdateComposeServiceStreaming",
		"p.go/-:8 UpdateContainerStreaming",
	}, unretriedUpdates(t, "p.go", src))
}
