// getter-errors: an identifier-free detector for the discarded / softened
// getter family, plus a reachability reporter for the sites already converted.
//
// WHY THIS EXISTS. Every sweep this defect family has used was anchored on an
// identifier -- a receiver expression (`h.db.`), an error-variable name
// (`err`), or a `Get*` callee prefix -- and every one of those anchors has
// already produced a FALSE ZERO on this tree: sErr / dbErr / pErr evaded the
// name anchor (agent-os-g482, obgr, r1by), `List*` evaded the verb anchor
// (agent-os-l42o, handlers/directories.go:55/:79/:81), and `UserCount` has no
// verb at all (handlers/auth.go:127/:144 at 4b569a6). This program is anchored
// on SHAPE only: the last value of a two-or-more-value assignment whose RHS is
// a single call.
//
// THREE COMMANDS:
//
//	scan <dir>              list every DISCARD / SOFT / MERGE site under <dir>
//	counts <dir>            the same, aggregated per file (the ratchet baseline)
//	reach <profile> <file.go...>
//	                        list converted sites and whether a test drives a
//	                        fault into each -- REACHED / PARTIAL / MISS (layer
//	                        C; needs a coverage profile)
//	reach --self-test       prove that verdict still fires, all four ways
//
// SHAPES.
//
//	DISCARD  x, _ := f()          the error is thrown away outright
//	SOFT     x, e := f()          e is only ever compared `e == nil`; the error
//	         if e == nil { ... }  is softened into an "absent" signal
//	MERGE    if e != nil || v {   the error IS checked, but it is fused by `||`
//	                              to a VALUE test, so "I could not read it" and
//	                              "I read it and the answer is no" take one
//	                              branch and the caller cannot tell them apart
//
// THE MERGE SHAPE, and why it needed its own kind (agent-os-8f2g). DISCARD and
// SOFT both describe an error that is thrown away or weakened at the
// ASSIGNMENT. MERGE describes one that is checked correctly and then merged
// into a legitimate value state at the BRANCH, so neither of the other two
// kinds could ever see it: at 33666e3 none of services/backup_runner.go,
// services/docker_update.go's fetch loop or handlers/auth.go appeared in a
// DISCARD or SOFT row, while all three carried the defect. agent-os-koy9 /
// 91u2 / 89ut / rb6f are four beads of that one class, and it regrew unwatched
// between them because the only ratchet in the repo could not classify it.
//
// FOUR WAYS A GREP FOR THIS SHAPE RETURNS A FALSE ZERO, all four OBSERVED on
// this tree, and all four are why it is detected on the AST and not with a
// pattern:
//
//   - OPERAND ORDER. `if !userExists || cmpErr != nil` (handlers/auth.go:346)
//     is invisible to every arm anchored on the error to the LEFT of the `||`,
//     and both arms in agent-os-koy9's brief were left-anchored.
//   - ERROR NAME. sErr, listErr, timeErr, rerr and cmpErr all evade an
//     `err`-anchored pattern; a SelectorExpr (`resp.Err != nil`) evades an
//     ident-anchored one.
//   - IF-INIT. `if x, rerr := f(); rerr != nil || ...` puts the call and the
//     test on ONE line, so no arm anchored on a line beginning `if <err> !=`
//     can match it. This form hid handlers/compose.go:452 and
//     services/backup_restic.go:351 from every sweep run on this class, and
//     neither site had ever been dispositioned when this kind was added.
//   - COMMENTS. handlers/terminal.go:99 is prose describing the shape, and a
//     text sweep counts it. So did a draft fix that quoted the pre-fix code in
//     a comment, which made the arm report an unchanged count after a real
//     conversion. An AST walk cannot see either.
//
// And a fifth that only an AST detector can have, found by probing this one
// wider than its own verdict rather than by re-running it: a TAGLESS SWITCH.
// `switch { case err != nil || v: }` is the same branch as `if err != nil || v`
// and an IfStmt-only walk cannot see it -- which mattered immediately, because
// that is the shape agent-os-89ut's own fix is written in. See mergeSwitch.
//
// MEMBERSHIP RULE, stated so a later reader can check it rather than infer it:
// an `if` whose condition is a top-level `||` chain with AT LEAST ONE
// error-nil operand AND AT LEAST ONE operand that is not. The second half is
// what the class means -- a fault merged with a VALUE. `if timeErr != nil ||
// daysErr != nil` (services/scheduler.go:497) is two faults sharing a branch,
// which is a different question, owned by agent-os-rltu, and it is deliberately
// NOT a MERGE site. The one identifier this program is forced to anchor on is
// the error's NAME (`err`, or any name ending `err`, on an ident or a selector),
// because without go/types there is nothing else that says an operand is an
// error; that anchor is stated here rather than left to be discovered.
//
// SCOPE RULE, and it is the whole substance of this program. bud5's prototype
// classified a candidate by walking the ENTIRE enclosing function body after
// the assignment and calling any later ident of the same spelling a hard use.
// That is a FIFTH ANCHOR, and the worst one, because it makes the tool blind
// exactly where the family's commonest spelling -- plain `err` -- lives. At
// 4b569a6 it missed services/docker_update.go:204 and handlers/updates.go:179,
// :325 and :812, all textbook members, because each function reuses or shadows
// `err` further down. Here a candidate's REGION is the statements that follow
// it IN ITS OWN STATEMENT LIST (or, for an if-init, that if's cond/body/else),
// and within the region:
//
//   - an if/for/switch/range whose init or key/value DEFINES the same name is
//     a SHADOW: its RHS is scanned for uses of the outer variable, the rest of
//     it is skipped, and scanning resumes after it. That is what makes
//     handlers/updates.go:812 a hit despite the `if err := h.db.Upsert...` at
//     :838.
//   - `e = ...` anywhere, or `e, x := ...` in the region's own list, is a
//     BOUNDARY: the variable's value has been replaced, so nothing after it
//     says anything about the value read here.
//   - `e := ...` in a NESTED list is a shadow for that list only; scanning
//     resumes after the nested block.
//
// A candidate is found by exactly ONE code path -- the statement list that
// contains it, or the if/for/switch that owns it as an init -- so a site is
// reported once. The prototype visited an if-init assignment twice (once as
// the IfStmt, once as the AssignStmt) and printed five of its thirteen SOFT
// rows in duplicate; "13" was never a site count.
//
// WHAT IS DELIBERATELY NOT DONE. No type information: loading go/types needs
// the module's dependencies, and the required check that runs this has a
// checkout and nothing else. So `v, _ := m[k]`-style non-error second values
// would be false positives if their RHS were a call. The type-aware arm is
// layer A -- `errcheck` with `check-blank: true` in backend/.golangci.yml --
// and check-getter-errors.sh --cross-check compares the two instruments'
// DISCARD sets rather than trusting either alone.
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// THE REACH SELF-TEST'S FIXTURES ARE EMBEDDED, NOT READ FROM DISK. The self-
// test is the only exercise `reach` has: scripts/getter-errors holds no
// _test.go file and the repo root has no go.mod, so no `go test` can reach
// this program at all. A fixture loaded by relative path would resolve against
// the CALLER's working directory, and the shape that produces is a self-test
// that passes because it found nothing to disagree with. Embedding makes the
// fixtures part of the binary, so "the fixture was missing" is a compile
// error rather than a green run.
//
//go:embed testdata/reach/reach.go
var fxReachSrc []byte

//go:embed testdata/reach/cov_all.txt
var fxCovAll []byte

//go:embed testdata/reach/cov_partial.txt
var fxCovPartial []byte

//go:embed testdata/reach/reach_branch.go
var fxBranchSrc []byte

//go:embed testdata/reach/cov_branch_all.txt
var fxCovBranchAll []byte

//go:embed testdata/reach/cov_branch_partial.txt
var fxCovBranchPartial []byte

type site struct {
	kind   string // DISCARD | SOFT | MERGE
	file   string // slash path relative to the scanned root
	line   int
	callee string // reporting only -- never a filter in the committed verdict
	fn     string // enclosing function, reporting only
}

// ---------------------------------------------------------------- use scan --

// useScan walks a region and classifies every use of one variable.
type useScan struct {
	name    string
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
			lhsIsName := isIdent(e.X, u.name) && isIdent(e.Y, "nil")
			rhsIsName := isIdent(e.Y, u.name) && isIdent(e.X, "nil")
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

// ------------------------------------------------------------- candidates --

func calleeName(c *ast.CallExpr) string {
	switch f := c.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name
	case *ast.Ident:
		return f.Name
	case *ast.IndexExpr: // generic instantiation
		return calleeName(&ast.CallExpr{Fun: f.X})
	}
	return ""
}

// candidate returns the call and the last-position identifier of an assignment
// of the shape `a, b, ..., last := call()`. Position, not spelling, picks the
// error: Go returns it last, and a name test is exactly the anchor this
// program exists to avoid.
func candidate(a *ast.AssignStmt) (*ast.CallExpr, *ast.Ident, bool) {
	if a == nil || len(a.Lhs) < 2 || len(a.Rhs) != 1 {
		return nil, nil, false
	}
	call, ok := a.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	last, ok := a.Lhs[len(a.Lhs)-1].(*ast.Ident)
	if !ok {
		return nil, nil, false
	}
	return call, last, true
}

type scanner struct {
	fset  *token.FileSet
	rel   string
	fn    string
	sites *[]site
}

func (s *scanner) record(kind string, a *ast.AssignStmt, call *ast.CallExpr) {
	*s.sites = append(*s.sites, site{
		kind:   kind,
		file:   s.rel,
		line:   s.fset.Position(a.Pos()).Line,
		callee: calleeName(call),
		fn:     s.fn,
	})
}

// classify handles one candidate given the region that follows it.
func (s *scanner) classify(a *ast.AssignStmt, region func(*useScan)) {
	call, last, ok := candidate(a)
	if !ok {
		return
	}
	if last.Name == "_" {
		s.record("DISCARD", a, call)
		return
	}
	if a.Tok != token.DEFINE {
		return // `x, err = f()` reuses an existing variable; not this shape
	}
	u := &useScan{name: last.Name}
	region(u)
	if u.soft > 0 && u.hard == 0 {
		s.record("SOFT", a, call)
	}
}

// ------------------------------------------------------------ merge shape --

// errNameLike reports whether e NAMES something that is conventionally an
// error. This is the ONE identifier anchor in this program and it is
// unavoidable: without go/types nothing else distinguishes `err != nil` from
// `conn != nil`. It is deliberately receiver-agnostic (an ident or the
// selected field of a selector, never the receiver expression) and
// name-variation tolerant, because sErr / listErr / dbErr / cmpErr / rerr are
// exactly the spellings that produced false zeros in this family before.
func errNameLike(e ast.Expr) bool {
	var n string
	switch x := e.(type) {
	case *ast.Ident:
		n = x.Name
	case *ast.SelectorExpr:
		n = x.Sel.Name
	default:
		return false
	}
	return strings.HasSuffix(strings.ToLower(n), "err")
}

// isErrNotNil reports whether e is `<error-ish> != nil`, in either operand
// order. Both orders are checked because `nil != err` is legal Go and because
// checking only one is the same category of blindness as anchoring the whole
// sweep on the left of the `||`.
func isErrNotNil(e ast.Expr) bool {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return false
	}
	if isIdent(b.Y, "nil") && errNameLike(b.X) {
		return true
	}
	return isIdent(b.X, "nil") && errNameLike(b.Y)
}

// orOperands flattens a top-level `||` chain. `a || b || c` parses as
// `(a || b) || c`, so a check that looked only at Cond.X and Cond.Y would miss
// the first operand of any three-way condition -- and three-way conditions are
// real here (handlers/settings.go, middleware/ratelimit.go).
func orOperands(e ast.Expr, out *[]ast.Expr) {
	if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.LOR {
		orOperands(b.X, out)
		orOperands(b.Y, out)
		return
	}
	*out = append(*out, e)
}

// mergesErrorWithValue applies the membership rule stated in the header: at
// least one error-nil operand AND at least one operand that is not. Returns
// the callee-slot label used in the scan row.
func mergesErrorWithValue(cond ast.Expr) (string, bool) {
	var ops []ast.Expr
	orOperands(cond, &ops)
	if len(ops) < 2 {
		return "", false
	}
	errs, values := 0, 0
	for _, o := range ops {
		if isErrNotNil(o) {
			errs++
		} else {
			values++
		}
	}
	if errs == 0 || values == 0 {
		return "", false
	}
	return fmt.Sprintf("||(err=%d,value=%d)", errs, values), true
}

// recordAt records a site that is a STATEMENT rather than an assignment, which
// is what MERGE is: the defect lives at the branch, not at the read.
func (s *scanner) recordAt(kind string, pos token.Pos, callee string) {
	*s.sites = append(*s.sites, site{
		kind:   kind,
		file:   s.rel,
		line:   s.fset.Position(pos).Line,
		callee: callee,
		fn:     s.fn,
	})
}

// mergeIf records the MERGE site, if any, carried by one if statement.
func (s *scanner) mergeIf(x *ast.IfStmt) {
	if label, ok := mergesErrorWithValue(x.Cond); ok {
		s.recordAt("MERGE", x.Pos(), label)
	}
}

// mergeSwitch records MERGE sites carried by a TAGLESS switch's case
// expressions. `switch { case err != nil || v: }` is the same branch as
// `if err != nil || v`, written the other way, and an IfStmt-only detector is
// blind to it.
//
// THIS IS NOT HYPOTHETICAL AND IT IS WHY THE ARM EXISTS. The first version of
// this detector handled IfStmt alone. Its verdict was then probed the way
// CLAUDE.md requires -- by reintroducing a known in-class site into a file the
// baseline recorded at MERGE=0 -- and the ratchet stayed GREEN, because the
// reintroduced site was written as `case dbRun.FinishedAt == nil || err != nil`
// inside the very switch that agent-os-89ut's fix had just introduced. So the
// instrument was blind precisely to the shape its own remedy produces, which is
// the worst possible place for a ratchet to be blind: the fix would have been
// free to be undone under a green gate. A control that fires on the sites you
// already know about would never have shown this.
//
// A TAGGED switch is excluded on purpose: in `switch x { case a || b: }` the
// case expression is compared against x, so it is a value, not a branch
// condition, and treating it as one would be a false positive no arm catches.
func (s *scanner) mergeSwitch(x *ast.SwitchStmt) {
	if x.Tag != nil || x.Body == nil {
		return
	}
	for _, st := range x.Body.List {
		cc, ok := st.(*ast.CaseClause)
		if !ok {
			continue
		}
		for _, e := range cc.List {
			if label, ok := mergesErrorWithValue(e); ok {
				s.recordAt("MERGE", e.Pos(), label)
			}
		}
	}
}

// list handles every candidate that sits directly in a statement list.
func (s *scanner) list(stmts []ast.Stmt) {
	for i, st := range stmts {
		a, ok := st.(*ast.AssignStmt)
		if !ok {
			continue
		}
		rest := stmts[i+1:]
		s.classify(a, func(u *useScan) { u.stmts(rest, true) })
	}
}

// initOf handles a candidate that is the init clause of an if/for/switch: its
// region is that statement's own cond/body/else.
func (s *scanner) initOf(init ast.Stmt, region func(*useScan)) {
	a, ok := init.(*ast.AssignStmt)
	if !ok {
		return
	}
	s.classify(a, region)
}

func (s *scanner) file(f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			s.fn = x.Name.Name
		case *ast.BlockStmt:
			s.list(x.List)
		case *ast.CaseClause:
			s.list(x.Body)
		case *ast.CommClause:
			s.list(x.Body)
		case *ast.IfStmt:
			s.mergeIf(x)
			s.initOf(x.Init, func(u *useScan) {
				u.expr(x.Cond)
				u.stmts(x.Body.List, false)
				if x.Else != nil {
					u.stmt(x.Else, false)
				}
			})
		case *ast.ForStmt:
			s.initOf(x.Init, func(u *useScan) {
				if x.Cond != nil {
					u.expr(x.Cond)
				}
				u.stmts(x.Body.List, false)
			})
		case *ast.SwitchStmt:
			s.mergeSwitch(x)
			s.initOf(x.Init, func(u *useScan) {
				if x.Tag != nil {
					u.expr(x.Tag)
				}
				u.stmts(x.Body.List, false)
			})
		}
		return true
	})
}

// -------------------------------------------------------------- traversal --

func goFiles(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(p)
			if base == "vendor" || base == "testdata" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	sort.Strings(out)
	return out, err
}

func scanTree(root string) ([]site, error) {
	files, err := goFiles(root)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var sites []site
	for _, p := range files {
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", p, perr)
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			rel = p
		}
		s := &scanner{fset: fset, rel: filepath.ToSlash(rel), sites: &sites}
		s.file(f)
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		if sites[i].line != sites[j].line {
			return sites[i].line < sites[j].line
		}
		return sites[i].kind < sites[j].kind
	})
	return sites, nil
}

func cmdScan(root string) error {
	sites, err := scanTree(root)
	if err != nil {
		return err
	}
	d, so, m := 0, 0, 0
	for _, h := range sites {
		switch h.kind {
		case "DISCARD":
			d++
		case "MERGE":
			m++
		default:
			so++
		}
		fmt.Printf("%-7s %s:%d %s (%s)\n", h.kind, h.file, h.line, h.callee, h.fn)
	}
	fmt.Printf("TOTAL SITES=%d DISCARD=%d SOFT=%d MERGE=%d\n", len(sites), d, so, m)
	return nil
}

// cmdCounts prints the ratchet baseline: per-file counts, never file:line.
// Line numbers rot -- services/backup.go:1084 at 4b569a6 is :1172 on 5e7bbc5,
// pure code motion -- and a line-keyed baseline reports that as a new site.
func cmdCounts(root string) error {
	sites, err := scanTree(root)
	if err != nil {
		return err
	}
	type triple struct{ d, s, m int }
	per := map[string]*triple{}
	var order []string
	for _, h := range sites {
		p, ok := per[h.file]
		if !ok {
			p = &triple{}
			per[h.file] = p
			order = append(order, h.file)
		}
		switch h.kind {
		case "DISCARD":
			p.d++
		case "MERGE":
			p.m++
		default:
			p.s++
		}
	}
	sort.Strings(order)
	td, ts, tm := 0, 0, 0
	for _, f := range order {
		p := per[f]
		td += p.d
		ts += p.s
		tm += p.m
		fmt.Printf("%s DISCARD=%d SOFT=%d MERGE=%d\n", f, p.d, p.s, p.m)
	}
	fmt.Printf("TOTAL FILES=%d DISCARD=%d SOFT=%d MERGE=%d\n", len(order), td, ts, tm)
	return nil
}

// ----------------------------------------------------------------- layer C --

// converted is a site of the shape this family's fixes produce:
//
//	x, err := call()
//	if err != nil { ... }
//
// Layers A and B answer "is every site converted". This answers the DIFFERENT
// question "does any test drive a fault into each converted site" -- the one
// that read clean at 0 while eight of ten converted sites in backup_config.go
// were pinned by nothing (agent-os-l42o).
type converted struct {
	file           string
	line           int // the assignment
	callee         string
	fn             string
	bodyLo, bodyHi int // line range of the `if err != nil` body
}

// displayPath keys a site by its path under backend/internal, not by its base
// name: middleware/auth.go and handlers/auth.go are different files with the
// same base, and collapsing them merges two functions' verdicts into one row.
func displayPath(p string) string {
	p = filepath.ToSlash(p)
	const marker = "/backend/internal/"
	if i := strings.Index(p, marker); i >= 0 {
		return p[i+len(marker):]
	}
	return filepath.Base(p)
}

func convertedSites(fset *token.FileSet, f *ast.File, rel string) []converted {
	var out []converted
	fn := ""
	guard := func(next ast.Stmt, name string) (*ast.IfStmt, bool) {
		ifs, ok := next.(*ast.IfStmt)
		if !ok || ifs.Init != nil {
			return nil, false
		}
		be, ok := ifs.Cond.(*ast.BinaryExpr)
		if !ok || be.Op != token.NEQ {
			return nil, false
		}
		if (isIdent(be.X, name) && isIdent(be.Y, "nil")) || (isIdent(be.Y, name) && isIdent(be.X, "nil")) {
			return ifs, true
		}
		return nil, false
	}
	scanList := func(list []ast.Stmt) {
		for i, st := range list {
			a, ok := st.(*ast.AssignStmt)
			if !ok || i+1 >= len(list) {
				continue
			}
			call, last, ok := candidate(a)
			if !ok || last.Name == "_" || a.Tok != token.DEFINE {
				continue
			}
			ifs, ok := guard(list[i+1], last.Name)
			if !ok {
				continue
			}
			out = append(out, converted{
				file:   rel,
				line:   fset.Position(a.Pos()).Line,
				callee: calleeName(call),
				fn:     fn,
				bodyLo: fset.Position(ifs.Body.Lbrace).Line,
				bodyHi: fset.Position(ifs.Body.Rbrace).Line,
			})
		}
	}
	// Only inside a FuncDecl: a converted site in a package-level var
	// initializer has no function to name, and an unnamed row in the baseline
	// cannot be acted on.
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		fn = fd.Name.Name
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BlockStmt:
				scanList(x.List)
			case *ast.CaseClause:
				scanList(x.Body)
			case *ast.CommClause:
				scanList(x.Body)
			}
			return true
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].line < out[j].line })
	return out
}

type covBlock struct {
	lo, hi int
	count  int
}

// readProfile parses a Go coverage profile, keyed by the profile's own path
// suffix so it matches regardless of the module's import path.
func readProfile(path string) (map[string][]covBlock, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	return parseProfile(fh)
}

// parseProfile is readProfile's body over any reader, so the self-test can
// feed it an embedded fixture instead of a path.
func parseProfile(r io.Reader) (map[string][]covBlock, error) {
	out := map[string][]covBlock{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		// path:lo.col,hi.col numstmt count
		colon := strings.LastIndex(line, ":")
		if colon < 0 {
			continue
		}
		file := line[:colon]
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			continue
		}
		rangePart := fields[0]
		comma := strings.Index(rangePart, ",")
		if comma < 0 {
			continue
		}
		lo, err1 := strconv.Atoi(strings.SplitN(rangePart[:comma], ".", 2)[0])
		hi, err2 := strconv.Atoi(strings.SplitN(rangePart[comma+1:], ".", 2)[0])
		cnt, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		out[file] = append(out[file], covBlock{lo: lo, hi: hi, count: cnt})
	}
	return out, sc.Err()
}

func blocksFor(prof map[string][]covBlock, rel string) []covBlock {
	for k, v := range prof {
		if strings.HasSuffix(k, "/"+rel) || k == rel {
			return v
		}
	}
	return nil
}

// reachInput is one source file to analyse. src nil means "read the file at
// name"; src non-nil means "this is the content, and name is only a label" --
// which is what lets the self-test run against embedded fixtures.
type reachInput struct {
	name string
	src  []byte
}

// THE VERDICT IS THREE-VALUED, and that is the whole point of it.
//
// A binary REACHED/MISS reports per SITE while the coverage underneath is per
// BLOCK, so an error body holding several blocks reads REACHED as soon as ONE
// of them runs. That is a truthful answer to "did a fault ARRIVE here" and a
// misleading answer to the question this tool actually exists for, which is
// "would a mutation at this site survive". Measured on the tree
// (agent-os-hsj7): services/git.go:101 and :726 both read REACHED while their
// own fall-through `return nil, fmt.Errorf(...)` never executed --
//
//	git.go:101.3,101.60 1 0
//	git.go:726.3,726.55 1 0
//
// -- because each body also holds a nested dispatch (the 404 branch at :97,
// the gitFailure branch at :722) that DID run. A mutant on either dead line
// survives, and the instrument said REACHED.
//
//	REACHED  every block wholly inside the body ran
//	PARTIAL  some did and some did not; the dead blocks' first lines are named
//	MISS     none did, so no fault reaches this site at all
//
// MISS keeps exactly its old meaning, so anything keyed on MISS is unaffected;
// PARTIAL is carved out of what used to be reported as REACHED.
func reachReport(w io.Writer, prof map[string][]covBlock, profName string, files []reachInput) error {
	fset := token.NewFileSet()
	total, reached, partial := 0, 0, 0
	var misses []string
	for _, in := range files {
		var srcArg any
		if in.src != nil {
			srcArg = in.src
		}
		f, perr := parser.ParseFile(fset, in.name, srcArg, 0)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", in.name, perr)
		}
		rel := displayPath(in.name)
		blocks := blocksFor(prof, filepath.ToSlash(filepath.Base(in.name)))
		if blocks == nil {
			blocks = blocksFor(prof, filepath.ToSlash(in.name))
		}
		if blocks == nil {
			return fmt.Errorf("no coverage blocks for %s in %s -- the profile does not cover this file, which is a blind instrument, not a clean result", rel, profName)
		}
		for _, c := range convertedSites(fset, f, rel) {
			total++
			// MEMBERSHIP RULE, stated because a proximity window that does not
			// stop borrows the NEXT construct's evidence. A block counts only
			// when it lies WHOLLY inside the `if err != nil` body: lo >= the
			// brace line AND hi <= the closing brace line. The lo-only form is
			// wrong -- Go's cover profile starts the block AFTER the body at
			// the closing brace's own line, so `lo <= bodyHi` alone reports a
			// never-executed error branch as covered whenever the statement
			// following it ran. testdata/reach/cov_partial.txt pins that: the
			// second site's branch is 0 while the block immediately after it
			// is 1, and the self-test requires the answer to be MISS.
			inBody, ran := 0, 0
			var dead []int
			for _, b := range blocks {
				if b.lo < c.bodyLo || b.hi > c.bodyHi {
					continue
				}
				inBody++
				if b.count > 0 {
					ran++
				} else {
					dead = append(dead, b.lo)
				}
			}
			row := fmt.Sprintf("%s:%d %s (%s)", c.file, c.line, c.callee, c.fn)
			switch {
			case ran == 0:
				// inBody == 0 lands here too: a body the profile says nothing
				// about is no evidence, not good evidence.
				misses = append(misses, row)
				fmt.Fprintf(w, "MISS    %s\n", row)
			case ran == inBody:
				reached++
				fmt.Fprintf(w, "REACHED %s\n", row)
			default:
				partial++
				parts := make([]string, len(dead))
				for i, d := range dead {
					parts[i] = strconv.Itoa(d)
				}
				fmt.Fprintf(w, "PARTIAL %s dead=%s\n", row, strings.Join(parts, ","))
			}
		}
	}
	fmt.Fprintf(w, "TOTAL CONVERTED=%d REACHED=%d PARTIAL=%d MISS=%d\n", total, reached, partial, total-reached-partial)
	for _, m := range misses {
		fmt.Fprintf(w, "MISSLINE %s\n", m)
	}
	return nil
}

// cmdReach reports, for every converted site in the named source files,
// whether the coverage profile shows its error branch executed.
func cmdReach(profile string, files []string) error {
	prof, err := readProfile(profile)
	if err != nil {
		return err
	}
	inputs := make([]reachInput, len(files))
	for i, f := range files {
		inputs[i] = reachInput{name: f}
	}
	return reachReport(os.Stdout, prof, profile, inputs)
}

// selfTestReach proves the reach verdict still fires, in every direction it
// can be wrong in. Arms 1 and 2 were carried over verbatim from
// scripts/check-getter-fault-reach.sh when that script was deleted
// (agent-os-1hig); arms 3 and 4 are new, because a self-test in which PARTIAL
// never appears cannot show that PARTIAL fires -- and one in which it always
// appears cannot show that it stops.
func selfTestReach() error {
	arms := []struct {
		name string
		cov  []byte
		file string
		src  []byte
		want string
	}{
		{
			// MUST REPORT CLEAN: every error branch covered.
			name: "fully covered",
			cov:  fxCovAll, file: "reach.go", src: fxReachSrc,
			want: `REACHED reach.go:19 GetThing (firstRead)
REACHED reach.go:27 GetThing (secondRead)
TOTAL CONVERTED=2 REACHED=2 PARTIAL=0 MISS=0
`,
		},
		{
			// MUST REPORT THE MISS: the second site's own branch is 0 while
			// the block immediately after it is 1. A window that does not stop
			// at the closing brace reports this as REACHED, which is the
			// borrowed-guard failure that made handlers/directories.go:219
			// read GUARDED in three separate documents (agent-os-3h9x).
			name: "unreached branch is not rescued by the block after it",
			cov:  fxCovPartial, file: "reach.go", src: fxReachSrc,
			want: `REACHED reach.go:19 GetThing (firstRead)
MISS    reach.go:27 GetThing (secondRead)
TOTAL CONVERTED=2 REACHED=1 PARTIAL=0 MISS=1
MISSLINE reach.go:27 GetThing (secondRead)
`,
		},
		{
			// MUST REPORT PARTIAL AND NAME THE DEAD LINE. Two of the three
			// blocks inside this body ran; `return "", err` at :35 did not.
			// The binary verdict called this REACHED.
			name: "multi-block body with one dead exit",
			cov:  fxCovBranchPartial, file: "reach_branch.go", src: fxBranchSrc,
			want: `PARTIAL reach_branch.go:30 GetThing (branchRead) dead=35
TOTAL CONVERTED=1 REACHED=0 PARTIAL=1 MISS=0
`,
		},
		{
			// MUST NOT REPORT PARTIAL: same source, same body, every in-body
			// block now 1. Without this arm an instrument that always said
			// PARTIAL would pass arm 3. Note this profile still carries
			// `37.2,37.22 1 0` -- the success return, OUTSIDE the body -- so
			// it also re-proves the bound excludes the following block even
			// when that block is dead.
			name: "multi-block body with every exit exercised",
			cov:  fxCovBranchAll, file: "reach_branch.go", src: fxBranchSrc,
			want: `REACHED reach_branch.go:30 GetThing (branchRead)
TOTAL CONVERTED=1 REACHED=1 PARTIAL=0 MISS=0
`,
		},
	}

	failed := 0
	for _, a := range arms {
		prof, err := parseProfile(bytes.NewReader(a.cov))
		if err != nil {
			return fmt.Errorf("self-test %q: %w", a.name, err)
		}
		var buf bytes.Buffer
		if err := reachReport(&buf, prof, "<embedded "+a.name+">", []reachInput{{name: a.file, src: a.src}}); err != nil {
			return fmt.Errorf("self-test %q: %w", a.name, err)
		}
		if buf.String() != a.want {
			failed++
			fmt.Printf("reach self-test: FAIL - %s\n  want:\n", a.name)
			for _, l := range strings.Split(strings.TrimRight(a.want, "\n"), "\n") {
				fmt.Printf("    %s\n", l)
			}
			fmt.Printf("  got:\n")
			for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
				fmt.Printf("    %s\n", l)
			}
		}
	}
	if failed != 0 {
		return fmt.Errorf("%d of %d reach self-test arms failed", failed, len(arms))
	}
	fmt.Printf("reach self-test: %d arms pass - a fully covered body reports REACHED, an unreached branch reports MISS and is not rescued by the block after it, a body with one dead exit reports PARTIAL naming line 35, and the same body with every exit exercised reports REACHED\n", len(arms))
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: getter-errors scan|counts <dir> | reach <profile> <file.go...> | reach --self-test")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "scan":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: getter-errors scan <dir>")
			os.Exit(2)
		}
		err = cmdScan(os.Args[2])
	case "counts":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: getter-errors counts <dir>")
			os.Exit(2)
		}
		err = cmdCounts(os.Args[2])
	case "reach":
		if len(os.Args) == 3 && os.Args[2] == "--self-test" {
			err = selfTestReach()
			break
		}
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: getter-errors reach <profile> <file.go...> | reach --self-test")
			os.Exit(2)
		}
		err = cmdReach(os.Args[2], os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
