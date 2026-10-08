package handlers

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

// containerReadOnlyMethods are the containerActionDocker methods that change
// nothing. Every other method on the interface counts as a container mutation,
// so a method added later is guarded until someone decides, here, that it only
// reads. That is the fail-closed direction: a new read method goes red once,
// a new mutation never slips through unlocked.
var containerReadOnlyMethods = map[string]bool{"InspectContainer": true}

const containerLockHelper = "lockContainerStack"

// containerLockViolations parses sources (file name -> content), derives the
// container mutation set from the containerActionDocker interface, and returns
// one line per use of a mutation method in non-test code that has no
// lockContainerStack call before it. sites is how many uses it checked, and
// mutations the derived method set.
//
// A use is any selector naming a mutation method, call or method value
// (`f := h.containerOps.StopContainer`), on any receiver, so a direct
// h.docker.StopContainer that skips containerOps is caught too. The enclosing
// function is the top-level FuncDecl; a closure counts as part of it. "Before"
// means a lockContainerStack call that
//   - is positioned earlier,
//   - sits in a statement of a block (or case clause) that also encloses the
//     use, so `if x { lock }; mutate` and a lock in an earlier closure both
//     fail, and
//   - does not itself contain the use in its arguments.
//
// Blind spots, stated so nobody reads a pass as more than it is: the lock's ok
// result being ignored, a missing `defer release()`, an early return between
// the lock and the use, a lock taken for a different container id, and a
// mutation reached through a helper that is not itself in the set. A type
// other than the docker one that happens to define a method of the same name
// is flagged too, which fails closed.
func containerLockViolations(t *testing.T, sources map[string]string) (violations, mutations []string, sites int) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, sources[name], 0)
		require.NoErrorf(t, err, "parse %s", name)
		files[name] = f
	}

	set := map[string]bool{}
	foundInterface, sawReadOnly := false, map[string]bool{}
	for _, name := range names {
		for _, decl := range files[name].Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != "containerActionDocker" {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				require.Truef(t, ok, "containerActionDocker in %s is no longer an interface type", name)
				foundInterface = true
				for _, m := range it.Methods.List {
					for _, n := range m.Names {
						if containerReadOnlyMethods[n.Name] {
							sawReadOnly[n.Name] = true
							continue
						}
						set[n.Name] = true
					}
				}
			}
		}
	}
	require.True(t, foundInterface, "type containerActionDocker not found in the handler sources; the guard has nothing to derive the mutation set from")
	for m := range containerReadOnlyMethods {
		require.Truef(t, sawReadOnly[m], "containerReadOnlyMethods lists %s but containerActionDocker has no such method; drop the stale entry", m)
	}
	for m := range set {
		mutations = append(mutations, m)
	}
	sort.Strings(mutations)

	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range files[name].Decls {
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

			var uses, locks []containerLock
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if set[n.Sel.Name] {
						uses = append(uses, containerLock{n, append([]ast.Node(nil), stack...)})
					}
				case *ast.CallExpr:
					var callee string
					switch f := n.Fun.(type) {
					case *ast.SelectorExpr:
						callee = f.Sel.Name
					case *ast.Ident:
						callee = f.Name
					}
					if callee == containerLockHelper {
						locks = append(locks, containerLock{n, append([]ast.Node(nil), stack...)})
					}
				}
				return true
			})

			for _, use := range uses {
				sites++
				if !containerLockPrecedes(locks, use.node, use.stack) {
					sel := use.node.(*ast.SelectorExpr)
					violations = append(violations, fmt.Sprintf("%s: %s at %s has no %s call before it in an enclosing block",
						label, sel.Sel.Name, fset.Position(sel.Pos()), containerLockHelper))
				}
			}
		}
	}
	return violations, mutations, sites
}

// containerLock is a node with its ancestor chain, outermost first.
type containerLock struct {
	node  ast.Node
	stack []ast.Node
}

// containerLockPrecedes reports whether any lock call qualifies as "before" the
// use, per the rule on containerLockViolations.
func containerLockPrecedes(locks []containerLock, use ast.Node, useStack []ast.Node) bool {
	for _, l := range locks {
		if l.node.Pos() >= use.Pos() {
			continue
		}
		// The statement holding the lock call, and the block whose statement
		// list it is a member of.
		var stmt, scope ast.Node
		for i := len(l.stack) - 1; i > 0; i-- {
			if listsStatements(l.stack[i-1]) {
				stmt, scope = l.stack[i], l.stack[i-1]
				break
			}
		}
		if stmt == nil || stmt.End() > use.Pos() {
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

// TestContainerLockGuard_EveryContainerMutationIsLockedFirst is
// agent-os-qags.13's guard. agent-os-w1cj put the single-container actions
// behind lockContainerStack, so a start, stop, restart or delete cannot
// interleave with a backup or restore of the stack that owns the container.
// Nothing made the next route do the same: a new kill, pause or bulk action
// that called the service directly would skip the lock and only a behavioural
// test written for that route would notice (safe-defaults rule 4).
//
// The sources come from go:embed (handlerSources), not a path on disk, so a
// `go test -overlay` mutant of a handler file is what this reads.
func TestContainerLockGuard_EveryContainerMutationIsLockedFirst(t *testing.T) {
	entries, err := handlerSources.ReadDir(".")
	require.NoError(t, err)
	sources := map[string]string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := handlerSources.ReadFile(e.Name())
		require.NoError(t, err)
		sources[e.Name()] = string(b)
	}

	violations, mutations, sites := containerLockViolations(t, sources)
	require.NotEmpty(t, mutations, "the containerActionDocker mutation set came out empty")
	// A guard that finds nothing to check passes on any tree, so the real
	// handlers must show it at least one mutation site.
	require.Positive(t, sites, "no use of %v found in the handler sources; the guard is blind", mutations)
	t.Logf("checked %d use(s) of %v", sites, mutations)
	require.Emptyf(t, violations, "container mutation without the stack lock (agent-os-qags.13, safe-defaults rule 4):\n  %s",
		strings.Join(violations, "\n  "))
}

// TestContainerLockGuard_CheckerSeesTheShapes runs the checker on small
// sources, so its discrimination is shown without a mutant: each row that must
// fail does, and the rows that must pass do, on the same instrument.
func TestContainerLockGuard_CheckerSeesTheShapes(t *testing.T) {
	const iface = `package handlers
type containerActionDocker interface {
	InspectContainer(id string) error
	StopContainer(id string) error
	PauseContainer(id string) error
}
`
	cases := []struct {
		name string
		body string
		want []string // substrings of the violations, in order; nil = clean
	}{
		{"locked first", `func (h *H) stop(c int) {
	release, ok := h.lockContainerStack(c)
	if !ok { return }
	defer release()
	h.containerOps.StopContainer("x")
}`, nil},
		{"lock in an if-init", `func (h *H) stop(c int) {
	if release, ok := h.lockContainerStack(c); !ok { return } else { defer release() }
	h.containerOps.StopContainer("x")
}`, nil},
		{"lock in a case clause, use in the same clause", `func (h *H) stop(c int) {
	switch c {
	case 1:
		release, _ := h.lockContainerStack(c)
		defer release()
		h.containerOps.StopContainer("x")
	}
}`, nil},
		{"use inside a closure after the lock", `func (h *H) stop(c int) {
	release, _ := h.lockContainerStack(c)
	defer release()
	func() { h.containerOps.StopContainer("x") }()
}`, nil},
		{"no lock", `func (h *H) stop(c int) {
	h.containerOps.StopContainer("x")
}`, []string{"(*H).stop: StopContainer"}},
		{"lock after the use", `func (h *H) stop(c int) {
	h.containerOps.StopContainer("x")
	release, _ := h.lockContainerStack(c)
	defer release()
}`, []string{"(*H).stop: StopContainer"}},
		{"conditional lock", `func (h *H) stop(c int) {
	if c > 0 {
		release, _ := h.lockContainerStack(c)
		defer release()
	}
	h.containerOps.StopContainer("x")
}`, []string{"(*H).stop: StopContainer"}},
		{"lock only in an earlier closure", `func (h *H) stop(c int) {
	func() { release, _ := h.lockContainerStack(c); defer release() }()
	h.containerOps.StopContainer("x")
}`, []string{"(*H).stop: StopContainer"}},
		{"use inside the lock call's own arguments", `func (h *H) stop(c int) {
	h.lockContainerStack(c, h.containerOps.StopContainer)
}`, []string{"(*H).stop: StopContainer"}},
		{"method value", `func (h *H) stop(c int) {
	f := h.containerOps.StopContainer
	f("x")
}`, []string{"(*H).stop: StopContainer"}},
		{"direct docker call bypassing containerOps", `func (h *H) stop(c int) {
	h.docker.StopContainer("x")
}`, []string{"(*H).stop: StopContainer"}},
		{"plain function, not a method", `func stopIt(h *H) {
	h.containerOps.StopContainer("x")
}`, []string{"stopIt: StopContainer"}},
		{"a method added to the interface later is picked up", `func (h *H) pause(c int) {
	h.containerOps.PauseContainer("x")
}`, []string{"(*H).pause: PauseContainer"}},
		{"the read-only method needs no lock", `func (h *H) look(c int) {
	h.containerOps.InspectContainer("x")
}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, sites := containerLockViolations(t, map[string]string{
				"iface.go": iface,
				"h.go":     "package handlers\n" + tc.body + "\n",
			})
			require.Len(t, got, len(tc.want), "violations: %v (sites %d)", got, sites)
			for i, w := range tc.want {
				require.Contains(t, got[i], w)
			}
		})
	}
}

// exclusiveGateHelper is the handler-side call a container prune must make
// first (agent-os-qags.27). acquireStackLock does not count: it takes one
// stack's turn, and a prune removes containers of every stack.
const exclusiveGateHelper = "acquireExclusiveLock"

// exclusiveGateViolations is containerLockViolations for the cross-stack
// container prune: the method set comes from the resourcePruner interface
// (every method on it removes containers of any stack, so there is no read-only
// list), and a use is violated unless an acquireExclusiveLock call precedes it,
// by the same ancestor-block rule (containerLockPrecedes). The same blind spots
// apply, and one more: it sees handler code only. A caller outside the handlers
// package would not be seen, and today there is none
// (`command grep -rn PruneContainers backend/internal backend/cmd` outside
// _test.go: the handler and the DockerService definition).
func exclusiveGateViolations(t *testing.T, sources map[string]string) (violations, pruneMethods []string, sites int) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f, err := parser.ParseFile(fset, name, sources[name], 0)
		require.NoErrorf(t, err, "parse %s", name)
		files[name] = f
	}

	set := map[string]bool{}
	foundInterface := false
	for _, name := range names {
		for _, decl := range files[name].Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != "resourcePruner" {
					continue
				}
				it, ok := ts.Type.(*ast.InterfaceType)
				require.Truef(t, ok, "resourcePruner in %s is no longer an interface type", name)
				foundInterface = true
				for _, m := range it.Methods.List {
					for _, n := range m.Names {
						set[n.Name] = true
					}
				}
			}
		}
	}
	require.True(t, foundInterface, "type resourcePruner not found in the handler sources; the guard has nothing to derive the prune set from")
	for m := range set {
		pruneMethods = append(pruneMethods, m)
	}
	sort.Strings(pruneMethods)

	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		for _, decl := range files[name].Decls {
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

			var uses, gates []containerLock
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if set[n.Sel.Name] {
						uses = append(uses, containerLock{n, append([]ast.Node(nil), stack...)})
					}
				case *ast.CallExpr:
					var callee string
					switch f := n.Fun.(type) {
					case *ast.SelectorExpr:
						callee = f.Sel.Name
					case *ast.Ident:
						callee = f.Name
					}
					if callee == exclusiveGateHelper {
						gates = append(gates, containerLock{n, append([]ast.Node(nil), stack...)})
					}
				}
				return true
			})

			for _, use := range uses {
				sites++
				if !containerLockPrecedes(gates, use.node, use.stack) {
					sel := use.node.(*ast.SelectorExpr)
					violations = append(violations, fmt.Sprintf("%s: %s at %s has no %s call before it in an enclosing block",
						label, sel.Sel.Name, fset.Position(sel.Pos()), exclusiveGateHelper))
				}
			}
		}
	}
	return violations, pruneMethods, sites
}

// TestContainerLockGuard_EveryContainerPruneTakesTheExclusiveTurnFirst is
// agent-os-qags.27's guard. A container prune removes `created` containers, and
// a stack operation holds some in `created` while it waits (compose up on a
// health check), so a prune during one fails it with "No such container"
// (probed against a real daemon, 3 of 3). pruneContainers takes the exclusive
// turn before Docker is touched; this keeps the next prune route, or a second
// call in the same handler, from skipping it. Sources come from go:embed, as in
// the guard above, so a `go test -overlay` mutant of a handler file is what it
// reads.
func TestContainerLockGuard_EveryContainerPruneTakesTheExclusiveTurnFirst(t *testing.T) {
	entries, err := handlerSources.ReadDir(".")
	require.NoError(t, err)
	sources := map[string]string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := handlerSources.ReadFile(e.Name())
		require.NoError(t, err)
		sources[e.Name()] = string(b)
	}

	violations, methods, sites := exclusiveGateViolations(t, sources)
	require.NotEmpty(t, methods, "the resourcePruner method set came out empty")
	require.Positive(t, sites, "no use of %v found in the handler sources; the guard is blind", methods)
	t.Logf("checked %d use(s) of %v", sites, methods)
	require.Emptyf(t, violations, "container prune without the exclusive stack turn (agent-os-qags.27):\n  %s",
		strings.Join(violations, "\n  "))
}

// TestContainerLockGuard_ExclusiveGateCheckerSeesTheShapes runs the checker on
// small sources: each row that must fail does, and the rows that must pass do,
// on the same instrument.
func TestContainerLockGuard_ExclusiveGateCheckerSeesTheShapes(t *testing.T) {
	const iface = `package handlers
type resourcePruner interface {
	PruneContainers(id string) error
}
`
	cases := []struct {
		name string
		body string
		want []string // substrings of the violations, in order; nil = clean
	}{
		{"gated first", `func (h *H) prune(c int) {
	release, ok := acquireExclusiveLock(c, h.opLock, "container prune")
	if !ok { return }
	defer release()
	h.pruner.PruneContainers("x")
}`, nil},
		{"gate in an if-init", `func (h *H) prune(c int) {
	if release, ok := acquireExclusiveLock(c, h.opLock, "k"); !ok { return } else { defer release() }
	h.pruner.PruneContainers("x")
}`, nil},
		{"use inside a closure after the gate", `func (h *H) prune(c int) {
	release, _ := acquireExclusiveLock(c, h.opLock, "k")
	defer release()
	func() { h.pruner.PruneContainers("x") }()
}`, nil},
		{"no gate", `func (h *H) prune(c int) {
	h.pruner.PruneContainers("x")
}`, []string{"(*H).prune: PruneContainers"}},
		{"a single-stack lock is not the exclusive turn", `func (h *H) prune(c int) {
	release, _ := acquireStackLock(c, h.opLock, "s1", "k")
	defer release()
	h.pruner.PruneContainers("x")
}`, []string{"(*H).prune: PruneContainers"}},
		{"gate after the use", `func (h *H) prune(c int) {
	h.pruner.PruneContainers("x")
	release, _ := acquireExclusiveLock(c, h.opLock, "k")
	defer release()
}`, []string{"(*H).prune: PruneContainers"}},
		{"conditional gate", `func (h *H) prune(c int) {
	if c > 0 {
		release, _ := acquireExclusiveLock(c, h.opLock, "k")
		defer release()
	}
	h.pruner.PruneContainers("x")
}`, []string{"(*H).prune: PruneContainers"}},
		{"gate only in an earlier closure", `func (h *H) prune(c int) {
	func() { release, _ := acquireExclusiveLock(c, h.opLock, "k"); defer release() }()
	h.pruner.PruneContainers("x")
}`, []string{"(*H).prune: PruneContainers"}},
		{"use inside the gate call's own arguments", `func (h *H) prune(c int) {
	acquireExclusiveLock(c, h.opLock, h.pruner.PruneContainers)
}`, []string{"(*H).prune: PruneContainers"}},
		{"a second prune in the same handler, gate before only the first", `func (h *H) prune(c int) {
	if c > 0 {
		release, _ := acquireExclusiveLock(c, h.opLock, "k")
		defer release()
		h.pruner.PruneContainers("x")
	}
	h.docker.PruneContainers("y")
}`, []string{"(*H).prune: PruneContainers"}},
		{"method value", `func (h *H) prune(c int) {
	f := h.pruner.PruneContainers
	f("x")
}`, []string{"(*H).prune: PruneContainers"}},
		{"direct docker call bypassing pruner", `func (h *H) prune(c int) {
	h.docker.PruneContainers("x")
}`, []string{"(*H).prune: PruneContainers"}},
		{"plain function, not a method", `func pruneIt(h *H) {
	h.pruner.PruneContainers("x")
}`, []string{"pruneIt: PruneContainers"}},
		{"another prune needs no gate", `func (h *H) prune(c int) {
	h.docker.PruneVolumes("x")
}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, sites := exclusiveGateViolations(t, map[string]string{
				"iface.go": iface,
				"h.go":     "package handlers\n" + tc.body + "\n",
			})
			require.Len(t, got, len(tc.want), "violations: %v (sites %d)", got, sites)
			for i, w := range tc.want {
				require.Contains(t, got[i], w)
			}
		})
	}
}
