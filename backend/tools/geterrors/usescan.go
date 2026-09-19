package geterrors

import (
	"go/ast"
	"go/token"
)

// useScan walks a REGION and classifies every use of one variable. It is
// ported from scripts/getter-errors/main.go, which this analyzer replaces,
// with two changes: the nil test is typed rather than spelled, and expr
// unwraps parentheses before matching (see unparen -- without it `(err) == nil`
// is silently counted as a hard use and the site disappears).
//
// THE REGION IS THE POINT. A prototype of this detector classified a candidate
// by walking the entire enclosing function body and calling any later ident of
// the same spelling a hard use, which made it blind exactly where the family's
// commonest spelling -- plain `err` -- lives. Here the region is the statements
// that follow the candidate IN ITS OWN STATEMENT LIST (or, for an if-init,
// that if's cond/body/else), and within the region:
//
//   - an if/for/switch/range whose init or key/value DEFINES the same name is
//     a SHADOW: its RHS is scanned for uses of the outer variable, the rest of
//     it is skipped, and scanning resumes after it.
//   - `e = ...` anywhere, or `e, x := ...` in the region's own list, is a
//     BOUNDARY: the variable's value has been replaced, so nothing after it
//     says anything about the value read here.
//   - `e := ...` in a NESTED list is a shadow for that list only; scanning
//     resumes after the nested block.
type useScan struct {
	name    string
	isNil   func(ast.Expr) bool
	soft    int  // `name == nil`
	hard    int  // anything else at all
	stopped bool // a boundary was reached; nothing later can be a use
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

// definesName reports whether an assignment DECLARES name on its left side.
func definesName(a *ast.AssignStmt, name string) bool {
	if a == nil || a.Tok != token.DEFINE {
		return false
	}
	for _, l := range a.Lhs {
		if isIdent(l, name) {
			return true
		}
	}
	return false
}

// initShadows reports whether a statement's init clause declares name.
func (u *useScan) initShadows(init ast.Stmt) bool {
	a, ok := init.(*ast.AssignStmt)
	return ok && definesName(a, u.name)
}

// scanInitRHS records uses of the outer variable inside a shadowing init's
// right-hand side, which are real uses of the OUTER variable.
func (u *useScan) scanInitRHS(init ast.Stmt) {
	if a, ok := init.(*ast.AssignStmt); ok {
		for _, r := range a.Rhs {
			u.expr(r)
		}
	}
}

// stmts walks a statement list. topLevel is true only for the list that
// physically contains the candidate assignment: there a `:=` of the same name
// re-uses the same variable, while in a nested list it declares a new one.
func (u *useScan) stmts(list []ast.Stmt, topLevel bool) {
	for _, s := range list {
		if u.stopped {
			return
		}
		if u.stmt(s, topLevel) {
			return
		}
	}
}

// stmt returns endList when the REST OF THE ENCLOSING LIST must be skipped
// because the name was shadowed there. u.stopped is the stronger, global form.
func (u *useScan) stmt(s ast.Stmt, topLevel bool) bool {
	switch n := s.(type) {
	case nil:
		return false

	case *ast.AssignStmt:
		for _, r := range n.Rhs {
			u.expr(r)
		}
		hit := false
		for _, l := range n.Lhs {
			if isIdent(l, u.name) {
				hit = true
			} else {
				u.expr(l) // `m[err] = x` is a use
			}
		}
		if !hit {
			return false
		}
		if n.Tok == token.DEFINE && !topLevel {
			return true // shadow: this list is done, the outer var lives on after it
		}
		u.stopped = true // reassignment, or a same-list `:=` of the same variable
		return false

	case *ast.DeclStmt:
		gd, ok := n.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return false
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, v := range vs.Values {
				u.expr(v)
			}
			for _, nm := range vs.Names {
				if nm.Name == u.name {
					if topLevel {
						u.stopped = true
						return false
					}
					return true
				}
			}
		}
		return false

	case *ast.BlockStmt:
		u.stmts(n.List, false)
		return false

	case *ast.IfStmt:
		if u.initShadows(n.Init) {
			u.scanInitRHS(n.Init)
			return false // skip cond/body/else entirely; resume after
		}
		if n.Init != nil && u.stmt(n.Init, topLevel) {
			return true
		}
		if u.stopped {
			return false
		}
		u.expr(n.Cond)
		u.stmts(n.Body.List, false)
		if n.Else != nil {
			u.stmt(n.Else, false)
		}
		return false

	case *ast.ForStmt:
		if u.initShadows(n.Init) {
			u.scanInitRHS(n.Init)
			return false
		}
		if n.Init != nil && u.stmt(n.Init, topLevel) {
			return true
		}
		if n.Cond != nil {
			u.expr(n.Cond)
		}
		if n.Post != nil {
			u.stmt(n.Post, false)
		}
		u.stmts(n.Body.List, false)
		return false

	case *ast.RangeStmt:
		if n.Tok == token.DEFINE && (isIdent(n.Key, u.name) || isIdent(n.Value, u.name)) {
			u.expr(n.X)
			return false
		}
		u.expr(n.X)
		u.stmts(n.Body.List, false)
		return false

	case *ast.SwitchStmt:
		if u.initShadows(n.Init) {
			u.scanInitRHS(n.Init)
			return false
		}
		if n.Init != nil && u.stmt(n.Init, topLevel) {
			return true
		}
		if n.Tag != nil {
			u.expr(n.Tag)
		}
		u.stmts(n.Body.List, false)
		return false

	case *ast.TypeSwitchStmt:
		if a, ok := n.Assign.(*ast.AssignStmt); ok && definesName(a, u.name) {
			for _, r := range a.Rhs {
				u.expr(r)
			}
			return false
		}
		if n.Init != nil && u.stmt(n.Init, topLevel) {
			return true
		}
		u.stmt(n.Assign, false)
		u.stmts(n.Body.List, false)
		return false

	case *ast.CaseClause:
		for _, e := range n.List {
			u.expr(e)
		}
		u.stmts(n.Body, false)
		return false

	case *ast.CommClause:
		if n.Comm != nil {
			u.stmt(n.Comm, false)
		}
		u.stmts(n.Body, false)
		return false

	case *ast.SelectStmt:
		u.stmts(n.Body.List, false)
		return false

	case *ast.LabeledStmt:
		return u.stmt(n.Stmt, topLevel)

	default:
		u.expr(s)
		return false
	}
}

// expr counts uses of the name inside an expression or a simple statement.
// `name == nil` is the one soft shape; every other appearance is hard.
func (u *useScan) expr(n ast.Node) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(x ast.Node) bool {
		switch e := x.(type) {
		case *ast.BinaryExpr:
			if e.Op != token.EQL && e.Op != token.NEQ {
				return true
			}
			// unparen, or `(err) == nil` misses: isIdent sees a ParenExpr,
			// the match falls through, ast.Inspect descends to the bare Ident
			// and counts it HARD -- so the site goes silent with no directive
			// and no reason. OBSERVED silent on the pre-fix binary, and the
			// form is gofmt-stable.
			x, y := unparen(e.X), unparen(e.Y)
			lhsIsName := isIdent(x, u.name) && u.isNil(y)
			rhsIsName := isIdent(y, u.name) && u.isNil(x)
			if !lhsIsName && !rhsIsName {
				return true
			}
			if e.Op == token.EQL {
				u.soft++
			} else {
				u.hard++
			}
			return false
		case *ast.FuncLit:
			for _, fl := range e.Type.Params.List {
				for _, nm := range fl.Names {
					if nm.Name == u.name {
						return false // the closure's own parameter shadows ours
					}
				}
			}
			u.stmts(e.Body.List, false)
			return false
		case *ast.Ident:
			if e.Name == u.name {
				u.hard++
			}
			return false
		}
		return true
	})
}
