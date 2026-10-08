package internal

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// standaloneUpdateEntryPoints are the DockerService methods that, for a
// container no managed stack owns, stop and remove the old container and then
// ContainerCreate -> ContainerStart the new one (docker_update.go,
// updateStandaloneContainer / updateStandaloneContainerStreaming). A container
// prune takes AcquireExclusive, which only waits for keys that are HELD, so a
// caller that reaches these without holding a key lets a prune remove the
// created container and the service is gone (agent-os-qags.30).
var standaloneUpdateEntryPoints = map[string]bool{
	"UpdateContainer":          true,
	"UpdateContainerStreaming": true,
}

// updateLockHelpers are the calls that take the key. acquireStackLock and
// lockStackForUpdate are the handler helpers; Acquire counts only on a
// receiver whose last name is opLock (the services.OperationLock a component
// holds), so an unrelated mu.Acquire() is not mistaken for the turn.
var updateLockHelpers = map[string]bool{
	"lockStackForUpdate": true,
	"acquireStackLock":   true,
	"Acquire":            true,
}

// updateLockViolations parses sources (file name -> content, non-test) and
// returns one line per use of UpdateContainer / UpdateContainerStreaming that
// has no lock call before it, and how many uses it checked.
//
// A use is any selector naming an entry point, call or method value, on any
// receiver. The window is one top-level FuncDecl (a closure counts as part of
// it): a lock in another function never covers a use, and neither does a lock
// in a block that does not enclose the use. "Before" means a lock call that
//   - is positioned earlier,
//   - sits in a statement of a block (or case clause) that also encloses the
//     use, so `if x { lock }; use` and a lock in an earlier closure both fail,
//   - does not itself contain the use in its arguments.
//
// One shape is accepted beyond that: the optional-dependency form
// `if <x>.opLock != nil { ...lock... }` (scheduler.go's RunAutoUpdates). The
// condition must be exactly a `!= nil` test of a selector ending in opLock, the
// lock must be in the if's Body (not its else), and the if statement stands in
// for the lock statement: it must end before the use, in a block enclosing the
// use. Any other condition (`if debug`, `if x.opLock == nil`, `if x.mu != nil`)
// leaves the lock conditional and the use unguarded.
//
// Blind spots, stated so nobody reads a pass as more than it is:
//   - a nil opLock means no lock at all. That is the optional guard dependency
//     of safe-defaults rule 5, proved by cmd/server/oplock_wiring_test.go, not
//     by this guard;
//   - the lock's ok/err result being ignored, a missing release, an early
//     return between the lock and the use, and a lock taken for a different
//     key than the container's own;
//   - a use reached through a wrapper that is not itself named in
//     standaloneUpdateEntryPoints (a wrapper that CALLS them is itself a use
//     and is flagged);
//   - a lock-named call that is not the operation lock but is spelled like it
//     (lockStackForUpdate / acquireStackLock by name).
//
// The block/ancestor rule is containerLockPrecedes in
// handlers/container_lock_guard_test.go (agent-os-qags.13), copied here because
// test code cannot be imported across packages. The nil-check hoist is the one
// difference.
func updateLockViolations(t *testing.T, sources map[string]string) (violations []string, sites int) {
	t.Helper()
	fset := token.NewFileSet()
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, sources[name], 0)
		require.NoErrorf(t, err, "parse %s", name)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			label := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				recv := fn.Recv.List[0].Type
				ptr := ""
				if star, ok := recv.(*ast.StarExpr); ok {
					recv, ptr = star.X, "*"
				}
				if id, ok := recv.(*ast.Ident); ok {
					label = fmt.Sprintf("(%s%s).%s", ptr, id.Name, fn.Name.Name)
				}
			}

			var uses, locks []updateLockNode
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if standaloneUpdateEntryPoints[n.Sel.Name] {
						uses = append(uses, updateLockNode{n, append([]ast.Node(nil), stack...)})
					}
				case *ast.CallExpr:
					if isUpdateLockCall(n) {
						locks = append(locks, updateLockNode{n, append([]ast.Node(nil), stack...)})
					}
				}
				return true
			})

			for _, use := range uses {
				sites++
				if !updateLockPrecedes(locks, use.node, use.stack) {
					sel := use.node.(*ast.SelectorExpr)
					violations = append(violations, fmt.Sprintf("%s: %s at %s has no lock call (lockStackForUpdate / acquireStackLock / opLock.Acquire) before it in an enclosing block",
						label, sel.Sel.Name, fset.Position(sel.Pos())))
				}
			}
		}
	}
	return violations, sites
}

// updateLockNode is a node with its ancestor chain, outermost first.
type updateLockNode struct {
	node  ast.Node
	stack []ast.Node
}

// isUpdateLockCall reports whether call takes an operation-lock key.
func isUpdateLockCall(call *ast.CallExpr) bool {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return updateLockHelpers[f.Name] && f.Name != "Acquire"
	case *ast.SelectorExpr:
		if !updateLockHelpers[f.Sel.Name] {
			return false
		}
		if f.Sel.Name != "Acquire" {
			return true
		}
		return lastName(f.X) == "opLock"
	}
	return false
}

// lastName is the final identifier of an expression: x for x, b for a.b.
func lastName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

// isOpLockNilCheck reports whether cond is exactly `<selector or ident
// ending in opLock> != nil`.
func isOpLockNilCheck(cond ast.Expr) bool {
	b, ok := cond.(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return false
	}
	if id, ok := b.Y.(*ast.Ident); !ok || id.Name != "nil" {
		return false
	}
	return lastName(b.X) == "opLock"
}

// updateLockPrecedes reports whether any lock call qualifies as "before" the
// use, per the rule on updateLockViolations.
func updateLockPrecedes(locks []updateLockNode, use ast.Node, useStack []ast.Node) bool {
	for _, l := range locks {
		if l.node.Pos() >= use.Pos() {
			continue
		}
		// j indexes the statement holding the lock call; stack[j-1] is the block
		// whose statement list it is a member of.
		j := -1
		for i := len(l.stack) - 1; i > 0; i-- {
			if listsStatements(l.stack[i-1]) {
				j = i
				break
			}
		}
		if j < 0 {
			continue
		}
		// Hoist out of `if <x>.opLock != nil { ... }` bodies: the if statement
		// stands in for the lock statement.
		for j >= 3 {
			ifStmt, ok := l.stack[j-2].(*ast.IfStmt)
			if !ok || ifStmt.Body != l.stack[j-1] || !isOpLockNilCheck(ifStmt.Cond) || !listsStatements(l.stack[j-3]) {
				break
			}
			j -= 2
		}
		stmt, scope := l.stack[j], l.stack[j-1]
		if stmt.End() > use.Pos() {
			continue
		}
		for _, anc := range useStack {
			if anc == scope {
				return true
			}
		}
	}
	return false
}

func listsStatements(n ast.Node) bool {
	switch n.(type) {
	case *ast.BlockStmt, *ast.CaseClause, *ast.CommClause:
		return true
	}
	return false
}

// TestStandaloneUpdateCallersHoldALock is agent-os-qags.32's guard for the
// class fixed in agent-os-qags.30: a caller of the standalone container update
// that has not first taken an operation-lock key (the stack ID, or the
// container ID when no stack owns it). Both entry points hold one today
// (handlers/updates.go updateContainer, services/scheduler.go RunAutoUpdates);
// nothing stopped a third caller from skipping it. The guard spans two packages,
// so it lives here and reads the sources from the go:embed in
// update_history_guard_test.go, which is what a `go test -overlay` mutant of
// scheduler.go or updates.go reaches.
func TestStandaloneUpdateCallersHoldALock(t *testing.T) {
	entries, err := internalSources.ReadDir(".")
	require.NoError(t, err)
	sources := map[string]string{}
	for _, dir := range entries {
		if !dir.IsDir() {
			continue
		}
		files, err := internalSources.ReadDir(dir.Name())
		require.NoError(t, err)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), "_test.go") {
				continue
			}
			path := dir.Name() + "/" + f.Name()
			b, err := internalSources.ReadFile(path)
			require.NoError(t, err)
			sources[path] = string(b)
		}
	}

	violations, sites := updateLockViolations(t, sources)
	// A guard that finds nothing to check passes on any tree. The tree has two
	// entry points; a floor of 2 means a rename of either method fails here
	// instead of silently emptying the guard.
	require.GreaterOrEqual(t, sites, 2, "found %d use(s) of %v in internal/*/*.go; the guard is blind (agent-os-qags.32)",
		sites, []string{"UpdateContainer", "UpdateContainerStreaming"})
	t.Logf("checked %d use(s) of UpdateContainer / UpdateContainerStreaming in %d files", sites, len(sources))
	require.Emptyf(t, violations, "standalone container update without an operation-lock key (agent-os-qags.32, agent-os-qags.30):\n  %s",
		strings.Join(violations, "\n  "))
}

// TestStandaloneUpdateCallersHoldALock_CheckerSeesTheShapes runs the checker on
// small sources, so its discrimination is shown without a mutant: every row
// that must fail does, and the rows that must pass do, on the same instrument.
func TestStandaloneUpdateCallersHoldALock_CheckerSeesTheShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string // substrings of the violations, in order; nil = clean
	}{
		// --- must pass ---
		{"handler shape: helper in the body, use in a closure after it", `func (h *H) up(c int) {
	release, ok := h.lockStackForUpdate(c, "k")
	if !ok { return }
	run := func() { defer release(); h.docker.UpdateContainerStreaming("x") }
	run()
}`, nil},
		{"acquireStackLock", `func (h *H) up(c int) {
	release, ok := acquireStackLock(c, h.opLock, "k", "u")
	if !ok { return }
	defer release()
	h.docker.UpdateContainer("x")
}`, nil},
		{"scheduler shape: Acquire inside if opLock != nil", `func (s *S) pass() {
	if s.opLock != nil {
		token, err := s.opLock.Acquire("k", "u")
		if err != nil { return }
		_ = token
	}
	s.docker.UpdateContainer("x")
}`, nil},
		{"scheduler shape inside a loop, use after the if", `func (s *S) pass(items []int) {
	for range items {
		if s.opLock != nil {
			if _, err := s.opLock.Acquire("k", "u"); err != nil { continue }
		}
		s.docker.UpdateContainer("x")
	}
}`, nil},
		{"lock in an if-init", `func (h *H) up(c int) {
	if release, ok := h.lockStackForUpdate(c, "k"); !ok { return } else { defer release() }
	h.docker.UpdateContainer("x")
}`, nil},
		{"a method that merely starts with the name is not an entry point", `func (h *H) up() {
	h.docker.UpdateContainerLabels("x")
}`, nil},

		// --- must fail ---
		{"no lock at all (the planted third caller)", `func (h *H) up() {
	h.docker.UpdateContainer("x")
}`, []string{"(*H).up: UpdateContainer"}},
		{"streaming variant, no lock", `func (h *H) up() {
	h.docker.UpdateContainerStreaming("x")
}`, []string{"(*H).up: UpdateContainerStreaming"}},
		{"lock after the use", `func (h *H) up(c int) {
	h.docker.UpdateContainer("x")
	release, _ := h.lockStackForUpdate(c, "k")
	defer release()
}`, []string{"(*H).up: UpdateContainer"}},
		{"lock inside an if with a DIFFERENT condition", `func (s *S) pass(debug bool) {
	if debug {
		s.opLock.Acquire("k", "u")
	}
	s.docker.UpdateContainer("x")
}`, []string{"(*S).pass: UpdateContainer"}},
		{"lock inside if opLock == nil", `func (s *S) pass() {
	if s.opLock == nil {
		s.opLock.Acquire("k", "u")
	}
	s.docker.UpdateContainer("x")
}`, []string{"(*S).pass: UpdateContainer"}},
		{"lock inside the nil check of another field", `func (s *S) pass() {
	if s.mu != nil {
		s.opLock.Acquire("k", "u")
	}
	s.docker.UpdateContainer("x")
}`, []string{"(*S).pass: UpdateContainer"}},
		{"lock in the else of if opLock != nil", `func (s *S) pass() {
	if s.opLock != nil {
	} else {
		s.opLock.Acquire("k", "u")
	}
	s.docker.UpdateContainer("x")
}`, []string{"(*S).pass: UpdateContainer"}},
		{"if opLock != nil with an extra condition", `func (s *S) pass(on bool) {
	if s.opLock != nil && on {
		s.opLock.Acquire("k", "u")
	}
	s.docker.UpdateContainer("x")
}`, []string{"(*S).pass: UpdateContainer"}},
		{"Acquire on a receiver that is not opLock", `func (s *S) pass() {
	s.mu.Acquire("k")
	s.docker.UpdateContainer("x")
}`, []string{"(*S).pass: UpdateContainer"}},
		{"lock only in an earlier closure", `func (h *H) up(c int) {
	func() { release, _ := h.lockStackForUpdate(c, "k"); defer release() }()
	h.docker.UpdateContainer("x")
}`, []string{"(*H).up: UpdateContainer"}},
		{"use inside the lock call's own arguments", `func (h *H) up(c int) {
	h.lockStackForUpdate(c, h.docker.UpdateContainer)
}`, []string{"(*H).up: UpdateContainer"}},
		{"method value", `func (h *H) up() {
	f := h.docker.UpdateContainerStreaming
	f("x")
}`, []string{"(*H).up: UpdateContainerStreaming"}},
		{"plain function, not a method", `func updateIt(d D) {
	d.UpdateContainer("x")
}`, []string{"updateIt: UpdateContainer"}},

		// --- the window stops where it claims ---
		{"window: a lock in function f does not cover function g", `func f(h *H, c int) {
	release, _ := h.lockStackForUpdate(c, "k")
	defer release()
}
func g(h *H) {
	h.docker.UpdateContainer("x")
}`, []string{"g: UpdateContainer"}},
		{"window: the lock covers the later use only, not the earlier one", `func (h *H) up(c int) {
	h.docker.UpdateContainer("a")
	release, _ := h.lockStackForUpdate(c, "k")
	defer release()
	h.docker.UpdateContainer("b")
}`, []string{"(*H).up: UpdateContainer"}},
		{"window: a lock in an if block covers a use inside it, not the one after it", `func (h *H) up(c int, on bool) {
	if on {
		release, _ := h.lockStackForUpdate(c, "k")
		defer release()
		h.docker.UpdateContainer("inside")
	}
	h.docker.UpdateContainer("after")
}`, []string{"(*H).up: UpdateContainer"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, sites := updateLockViolations(t, map[string]string{
				"h.go": "package p\n" + tc.body + "\n",
			})
			require.Len(t, got, len(tc.want), "violations: %v (sites %d)", got, sites)
			for i, w := range tc.want {
				require.Contains(t, got[i], w)
			}
		})
	}
}
