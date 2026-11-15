package internal

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"
)

// TestMutatingComposeRefusalIsTyped_B6u8 keeps every refusal from
// mutatingComposeArgs typed (agent-os-b6u8 item 2, the pusq close draft). A
// refused compose ran nothing, so StoreLifecycleStatus must leave the stack's
// stored status alone, and it can only tell a refusal apart by its type
// (composeRefusedError, which refusedCompose wraps). An untyped `return err`
// would let a refused start overwrite the status as if compose had failed.
//
// Rule, per call: the statement right after `args, e := s.mutatingComposeArgs(...)`
// is `if e != nil { ... }`, and that body names refusedCompose(...) or
// composeRefusedError{...} and does not return e itself. It keys on the
// method name, not the receiver or the variable names.
//
// Scope: every non-test file in internal/<pkg>/, read from internalSources
// (update_history_guard_test.go), so a `go test -overlay` mutant reaches it.
// Cannot see: a call through a function value, or a result checked anywhere
// but the very next statement (that shape is reported, not skipped).
func TestMutatingComposeRefusalIsTyped_B6u8(t *testing.T) {
	fset := token.NewFileSet()
	var offenders []string
	calls := 0

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
		ast.Inspect(file, func(n ast.Node) bool {
			var stmts []ast.Stmt
			switch b := n.(type) {
			case *ast.BlockStmt:
				stmts = b.List
			case *ast.CaseClause:
				stmts = b.Body
			case *ast.CommClause:
				stmts = b.Body
			default:
				return true
			}
			for i, st := range stmts {
				errName, ok := mutatingComposeArgsAssign(st)
				if !ok {
					continue
				}
				calls++
				pos := fset.Position(st.Pos()).String()
				if i+1 >= len(stmts) {
					offenders = append(offenders, pos+": no `if err != nil` follows the call")
					continue
				}
				if why := typedRefusalBranch(stmts[i+1], errName); why != "" {
					offenders = append(offenders, pos+": "+why)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Positive arm: the walk sees every call that exists today
	// (docker_lifecycle.go 5, docker_update.go 6 on 7e7691f). A change in the
	// count is a new or removed site to read, not a pass.
	if calls != 11 {
		t.Errorf("saw %d mutatingComposeArgs calls, want 11; read the new or removed site and update this count", calls)
	}
	for _, o := range offenders {
		t.Errorf("untyped compose refusal: %s", o)
	}
}

// mutatingComposeArgsAssign reports the error variable of
// `x, e := <recv>.mutatingComposeArgs(...)` (or `=`).
func mutatingComposeArgsAssign(st ast.Stmt) (string, bool) {
	as, ok := st.(*ast.AssignStmt)
	if !ok || len(as.Rhs) != 1 || len(as.Lhs) != 2 {
		return "", false
	}
	call, ok := as.Rhs[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "mutatingComposeArgs" {
		return "", false
	}
	id, ok := as.Lhs[1].(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

// typedRefusalBranch returns why st is not `if errName != nil { <typed refusal> }`,
// or "" when it is.
func typedRefusalBranch(st ast.Stmt, errName string) string {
	ifs, ok := st.(*ast.IfStmt)
	if !ok {
		return "the next statement is not `if " + errName + " != nil`"
	}
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return "the next if is not `" + errName + " != nil`"
	}
	if id, ok := cond.X.(*ast.Ident); !ok || id.Name != errName {
		return "the next if is not `" + errName + " != nil`"
	}
	typed, bare := false, false
	ast.Inspect(ifs.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "refusedCompose" {
				typed = true
			}
		case *ast.CompositeLit:
			if id, ok := x.Type.(*ast.Ident); ok && id.Name == "composeRefusedError" {
				typed = true
			}
		case *ast.ReturnStmt:
			for _, r := range x.Results {
				if id, ok := r.(*ast.Ident); ok && id.Name == errName {
					bare = true
				}
			}
		}
		return true
	})
	switch {
	case bare:
		return "the branch returns " + errName + " untyped"
	case !typed:
		return "the branch names neither refusedCompose nor composeRefusedError"
	}
	return ""
}
