package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// agent-os-lcx7: a stack's ComposeFile and EnvFile are relative to its
// folder; git's changed names are relative to the repository top. A raw
// compare of the two is the agent-os-4d60 defect: place the stack's names
// with repoRel/repoJoin (git_history_diff.go) first, then compare.

// isStackFileField is x.ComposeFile or x.EnvFile.
func isStackFileField(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && (sel.Sel.Name == "ComposeFile" || sel.Sel.Name == "EnvFile")
}

func isEmptyString(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && (lit.Value == `""` || lit.Value == "``")
}

// stringCompareFuncs are the strings functions that compare their two
// arguments as paths could be compared.
var stringCompareFuncs = map[string]bool{
	"HasSuffix": true, "HasPrefix": true, "EqualFold": true, "Contains": true, "Compare": true,
	"TrimSuffix": true, "TrimPrefix": true, "CutSuffix": true, "CutPrefix": true,
}

// stackFileCompares lists, as "file:line", every == / != compare and every
// switch case in src that sets a ComposeFile/EnvFile field against anything
// other than "" or another such field.
func stackFileCompares(t *testing.T, fset *token.FileSet, name string, src []byte) []string {
	t.Helper()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A local alias (ef := stack.EnvFile) carries the field into a compare
	// one assignment away (review rv-dm-gitpull F2). Keyed by name per file:
	// an over-approximation, which can only add hits.
	aliases := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == len(as.Rhs) {
			for i, r := range as.Rhs {
				if id, ok := as.Lhs[i].(*ast.Ident); ok && isStackFileField(r) {
					aliases[id.Name] = true
				}
			}
		}
		return true
	})
	field := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return isStackFileField(e) || (ok && aliases[id.Name])
	}
	compareOK := func(a, b ast.Expr) bool {
		if !field(a) && !field(b) {
			return true
		}
		return isEmptyString(a) || isEmptyString(b) || (field(a) && field(b))
	}
	var bad []string
	at := func(n ast.Node) { bad = append(bad, fset.Position(n.Pos()).String()) }
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if (n.Op == token.EQL || n.Op == token.NEQ) && !compareOK(n.X, n.Y) {
				at(n)
			}
		case *ast.CallExpr:
			// strings.HasSuffix(f, stack.ComposeFile) and friends compare too.
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || len(n.Args) != 2 || !stringCompareFuncs[sel.Sel.Name] || !isIdent(sel.X, "strings") {
				return true
			}
			if !compareOK(n.Args[0], n.Args[1]) {
				at(n)
			}
		case *ast.SwitchStmt:
			// switch f { case stack.ComposeFile: } is the same compare.
			if n.Tag == nil {
				return true
			}
			for _, c := range n.Body.List {
				for _, e := range c.(*ast.CaseClause).List {
					if !compareOK(n.Tag, e) {
						at(e)
					}
				}
			}
		}
		return true
	})
	return bad
}

// TestStackFileCompareGuard: no non-test file of package services compares a
// stack's ComposeFile/EnvFile raw. Read through go:embed (packageSources), so
// a `go test -overlay` mutant is what this sees.
func TestStackFileCompareGuard(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var bad []string
	read, sawGit := 0, false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		read++
		sawGit = sawGit || name == "git.go"
		bad = append(bad, stackFileCompares(t, fset, name, src)...)
	}
	if read == 0 || !sawGit {
		t.Fatalf("read %d files, want the package's sources including git.go", read)
	}
	if len(bad) > 0 {
		t.Fatalf("raw compare of a stack's ComposeFile/EnvFile (folder-relative) against another path: %v; place it with repoRel/repoJoin first (agent-os-4d60)", bad)
	}
}

// The checker fires on the pre-4d60 shape (17dfcb8 git.go:940) and the
// switch route, and stays quiet on the compares the package makes today.
func TestStackFileCompareGuardSeesTheShapes(t *testing.T) {
	const head = "package services\nfunc f() {\n"
	cases := map[string]int{
		`if f == stack.ComposeFile || f == stack.EnvFile { return }`: 2, // pre-4d60
		`if stack.EnvFile != name { return }`:                        1,
		`switch f { case stack.ComposeFile, stack.EnvFile: }`:        2,
		`switch st.ComposeFile { case "", other.ComposeFile: }`:      0,
		`if st.EnvFile != "" && st.ComposeFile == "" { return }`:     0,
		`if a.EnvFile == b.EnvFile { return }`:                       0,
		`if want[p] { return }`:                                      0,
		`if ef := stack.EnvFile; f == ef { return }`:                 1, // review lB
		`if strings.HasSuffix(f, stack.ComposeFile) { return }`:      1, // review lA
		`if strings.EqualFold(stack.EnvFile, name) { return }`:       1,
		`if strings.HasSuffix(stack.EnvFile, "") { return }`:         0,
		`name := st.EnvFile; if name == "" { return }`:               0,
	}
	for body, want := range cases {
		got := stackFileCompares(t, token.NewFileSet(), "x.go", []byte(head+body+"\n}\n"))
		if len(got) != want {
			t.Errorf("%s: flagged %v, want %d", body, got, want)
		}
	}
}
