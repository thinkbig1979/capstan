package services

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// agent-os-g482 — A STRUCTURAL TEST, DELIBERATELY. Read this before deleting it.
//
// WHY IT IS NOT A BEHAVIOURAL TEST. UpdateContainer and UpdateContainerStreaming
// cannot be driven from this package: both call s.client.ContainerInspect before
// reaching the strategy switch, and DockerService.client is a concrete
// *client.Client (docker.go:55, the `client *client.Client` field), not an
// interface. Their only executable coverage is internal/integrationtest, behind
// the `integration` build tag, which `go test ./...` does not run.
//
// WHY IT EXISTS ANYWAY. resolveUpdateStrategy and logRefusedUpdate are both unit
// tested (docker_update_dbfault_test.go), but a mutation that leaves BOTH of
// them correct and only rewires the call site — renaming `case updateRefused:`
// so the refusal falls through to `default:` — reinstates the P2 defect with
// every one of those tests still green. That mutation was run by the
// orchestrator against the first version of this fix and SURVIVED. This file is
// the arm that kills it: the tested unit and the shipped decision are only the
// same thing if the switch actually routes updateRefused to a return.
//
// DELETE THIS FILE the day DockerService.client becomes an interface. At that
// point the two apply paths become drivable and a behavioural test replaces
// this one, which is strictly better: this file pins the SHAPE of the code, so
// it will object to an equivalent rewrite that is perfectly correct.

// docker_update.go is embedded rather than read from disk so that the assertion
// runs against the source the BUILD used. A test that read the file with
// os.ReadFile would be blind to `go test -overlay` mutations — it would keep
// passing against a mutant, which is the exact failure this file exists to
// prevent, and the repo's merge gate mutates via -overlay.
//
//go:embed docker_update.go
var g482DockerUpdateSource string

// g482ApplyPaths is the two call sites and, for each, the apply calls that must
// be unreachable from its refusal branch.
var g482ApplyPaths = []struct {
	fn         string
	applyFns   []string
	reinstates string
}{
	{
		fn:         "UpdateContainer",
		applyFns:   []string{"updateStandaloneContainer", "updateComposeContainer"},
		reinstates: "a compose-managed container would be RECREATED down the standalone path whenever the stacks table cannot be read",
	},
	{
		fn:         "UpdateContainerStreaming",
		applyFns:   []string{"updateStandaloneContainerStreaming", "updateComposeContainerStreaming"},
		reinstates: "a compose-managed container would be RECREATED down the standalone path whenever the stacks table cannot be read (the site agent-os-g482 was filed on)",
	},
}

func TestRefusalIsWiredIntoBothApplyPaths(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "docker_update.go", g482DockerUpdateSource, 0)
	if err != nil {
		t.Fatalf("parse docker_update.go: %v", err)
	}

	for _, path := range g482ApplyPaths {
		t.Run(path.fn, func(t *testing.T) {
			fn := g482FindFunc(file, path.fn)
			if fn == nil || fn.Body == nil {
				t.Fatalf("%s: function not found in docker_update.go — this guard has stopped measuring anything", path.fn)
			}

			// 1. The switch must be driven by resolveUpdateStrategy's return value.
			strategyVar := g482StrategyVar(fn.Body)
			if strategyVar == "" {
				t.Fatalf("%s: no assignment from resolveUpdateStrategy(...) — the strategy decision has been inlined or bypassed, so %s", path.fn, path.reinstates)
			}

			sw := g482SwitchOn(fn.Body, strategyVar)
			if sw == nil {
				t.Fatalf("%s: no switch on %q, the value resolveUpdateStrategy returned — so %s", path.fn, strategyVar, path.reinstates)
			}

			// 2. POSITIVE CONTROL, so a passing run proves the parser found the
			// REAL decision switch and not some unrelated one: that switch must
			// still contain the standalone apply call in one of its other arms.
			if !g482CallsAny(sw.Body.List, path.applyFns[:1]) {
				t.Fatalf("%s: the switch on %q never calls %s — this guard is pointed at the wrong switch and is no longer measuring anything", path.fn, strategyVar, path.applyFns[0])
			}

			// 3. A case clause must name updateRefused.
			clause := g482CaseNaming(sw, "updateRefused")
			if clause == nil {
				t.Fatalf("%s: the switch on %q has no `case updateRefused:` — the refusal falls through to the default arm, so %s", path.fn, strategyVar, path.reinstates)
			}

			// 4. That clause must RETURN, and must reach no apply call.
			if len(clause.Body) == 0 {
				t.Fatalf("%s: `case updateRefused:` is empty, so it falls out of the switch and %s", path.fn, path.reinstates)
			}
			if _, ok := clause.Body[len(clause.Body)-1].(*ast.ReturnStmt); !ok {
				t.Fatalf("%s: `case updateRefused:` does not end in a return, so execution continues past the switch and %s", path.fn, path.reinstates)
			}
			if g482CallsAny(clause.Body, path.applyFns) {
				t.Fatalf("%s: `case updateRefused:` reaches an apply call (%s) — a refusal must write nothing, and %s", path.fn, strings.Join(path.applyFns, " or "), path.reinstates)
			}
		})
	}
}

func g482FindFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

// g482StrategyVar returns the name of the first variable assigned from a
// resolveUpdateStrategy call, or "" if there is none.
func g482StrategyVar(body *ast.BlockStmt) string {
	name := ""
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || name != "" {
			return name == ""
		}
		for _, rhs := range assign.Rhs {
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				continue
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "resolveUpdateStrategy" {
				continue
			}
			if len(assign.Lhs) > 0 {
				if lhs, ok := assign.Lhs[0].(*ast.Ident); ok {
					name = lhs.Name
				}
			}
		}
		return name == ""
	})
	return name
}

func g482SwitchOn(body *ast.BlockStmt, varName string) *ast.SwitchStmt {
	var found *ast.SwitchStmt
	ast.Inspect(body, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || found != nil {
			return found == nil
		}
		if tag, ok := sw.Tag.(*ast.Ident); ok && tag.Name == varName {
			found = sw
		}
		return found == nil
	})
	return found
}

// g482CaseNaming returns the case clause listing ident as one of its values.
// It matches the bare identifier only: a conversion such as
// composeUpdateStrategy(99) is deliberately NOT a match, because that is exactly
// the mutation this guard exists to catch.
func g482CaseNaming(sw *ast.SwitchStmt, ident string) *ast.CaseClause {
	for _, stmt := range sw.Body.List {
		clause, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, expr := range clause.List {
			if id, ok := expr.(*ast.Ident); ok && id.Name == ident {
				return clause
			}
		}
	}
	return nil
}

// g482CallsAny reports whether any of names is called anywhere under nodes,
// as a bare call or as a method on any receiver.
func g482CallsAny(nodes []ast.Stmt, names []string) bool {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	found := false
	for _, stmt := range nodes {
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || found {
				return !found
			}
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				if want[fn.Name] {
					found = true
				}
			case *ast.SelectorExpr:
				if fn.Sel != nil && want[fn.Sel.Name] {
					found = true
				}
			}
			return !found
		})
	}
	return found
}
