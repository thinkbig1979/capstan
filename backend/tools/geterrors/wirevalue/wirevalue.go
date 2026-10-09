// Package wirevalue is a go/analysis Analyzer that keeps the producers of two
// wire value sets on their typed Go constants instead of bare string literals.
//
// WHY. Stack statuses and policy target types each have one Go source,
// services.StackStatus and services.JobTargetType
// (backend/internal/services/wire_types.go), and the frontend's unions are
// asserted Exact against the generated types (agent-os-rc69). That ties the
// CONST SET to the frontend, not the producers: models.Stack.Status is a plain
// string, so a new `stack.Status = "restarting"` compiles, passes the Exact
// assertion, and reaches a TS union that cannot hold it (agent-os-oa26). A
// grep for the words cannot do this job: "running" and "partial" are also
// BackupRun statuses and "error" is a slog key, so only types tell a stack's
// Status from a backup run's.
//
// HOW. A value set is seeded on the Go objects that carry it to the wire
// (struct fields, function results, a parameter; see sets below). Inside the
// package under analysis, the carrier mark spreads along assignment, :=, var
// declarations, composite-literal keys, return statements and call argument
// to parameter of a same-package function. A string literal, or a constant of
// plain string type, whose value is in the set is REPORTED when it is
// assigned, returned or passed to a carrier, or compared (==, !=, switch
// case) with one. string(services.StackStatusRunning) is a conversion of a
// typed constant and is the intended spelling, so it is not reported.
//
// NOT REPORTED: a value that crosses packages through a function or
// parameter that is not a seed (parameters of other packages' functions are
// not followed, so a shared helper such as strings.EqualFold cannot spread
// the mark to every string in the package); a value stored in a map, slice or
// unseeded struct field; a value built by concatenation or fmt. _test.go
// files are skipped: fixtures spell wire values on purpose.
//
// Suppress a site with //wirevalue:ignore <reason> on the site line or alone
// on the line above. The reason is mandatory.
package wirevalue

import (
	"errors"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const Doc = `report bare string literals where a typed wire constant belongs

A stack status ("running", "stopped", ...) or an auto-update policy target type
("container", "stack") spelled as a bare string literal and assigned, returned,
passed to or compared with a field, result or parameter that carries that value
to the wire. Spell it string(services.StackStatusX) or
string(services.JobTargetTypeX). _test.go files are skipped. Suppress a site
with //wirevalue:ignore <reason> on the site line or the line above.`

var Analyzer = &analysis.Analyzer{
	Name: "wirevalue",
	Doc:  Doc,
	Run:  run,
}

const directive = "//wirevalue:ignore"

// valueSet is one wire value set and the objects seeded as its carriers.
type valueSet struct {
	name   string            // the Go type the values belong to
	values map[string]string // value -> the constant to use instead
	fields map[string]bool   // "pkgsuffix.Type.Field"
	funcs  map[string]bool   // "pkgsuffix.Func" or "pkgsuffix.Type.Method": result 0
	params map[string]string // "pkgsuffix.Type.Method" -> parameter name
}

var sets = []*valueSet{
	{
		name: "services.StackStatus",
		values: map[string]string{
			"running": "StackStatusRunning", "stopped": "StackStatusStopped",
			"partial": "StackStatusPartial", "paused": "StackStatusPaused",
			"unknown": "StackStatusUnknown", "error": "StackStatusError",
		},
		fields: map[string]bool{
			"internal/models.Stack.Status":        true,
			"internal/models.StackEvent.Status":   true,
			"internal/services.LiveStatus.Status": true,
		},
		funcs: map[string]bool{
			"internal/services.DockerService.Status": true,
			// BackupService reaches Status through this interface; an
			// interface method is its own object, not DockerService's.
			"internal/services.dockerStopper.Status": true,
			"internal/handlers.lifecycleStatus":      true,
		},
		params: map[string]string{
			"internal/database.DB.UpdateStackStatus": "status",
		},
	},
	{
		name: "services.JobTargetType",
		values: map[string]string{
			"container": "JobTargetTypeContainer", "stack": "JobTargetTypeStack",
		},
		fields: map[string]bool{
			"internal/models.AutoUpdatePolicy.TargetType": true,
			"internal/models.StackEvent.TargetType":       true,
		},
		params: map[string]string{
			"internal/database.DB.GetAutoUpdatePolicy":    "targetType",
			"internal/database.DB.DeleteAutoUpdatePolicy": "targetType",
		},
	},
}

// site is a literal (or plain-string constant) meeting a carrier candidate.
type site struct {
	obj   *types.Var
	value string
	pos   token.Pos
	how   string
}

type analysis_ struct {
	pass  *analysis.Pass
	edges map[*types.Var][]*types.Var
	sites []site
	seeds []map[*types.Var]bool // per set
}

func run(pass *analysis.Pass) (any, error) {
	a := &analysis_{pass: pass, edges: map[*types.Var][]*types.Var{}, seeds: make([]map[*types.Var]bool, len(sets))}
	for i := range a.seeds {
		a.seeds[i] = map[*types.Var]bool{}
	}
	suppressed := map[string]map[int]bool{}
	for _, f := range pass.Files {
		// PositionFor(..., false) so a //line directive cannot rename a file
		// into a skip; the same trap geterrors documents in posOf.
		name := pass.Fset.PositionFor(f.Pos(), false).Filename
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		suppressed[name] = collectDirectives(pass, f)
		a.walkFile(f)
	}

	for i, set := range sets {
		carrier := a.closure(a.seeds[i])
		for _, s := range a.sites {
			use, ok := set.values[s.value]
			if !ok || !carrier[s.obj] {
				continue
			}
			p := pass.Fset.PositionFor(s.pos, false)
			if suppressed[p.Filename][p.Line] {
				continue
			}
			pass.Reportf(s.pos, "bare %q %s a %s carrier (%s): use string(services.%s)",
				s.value, s.how, set.name, s.obj.Name(), use)
		}
	}
	return nil, nil
}

func (a *analysis_) closure(seeds map[*types.Var]bool) map[*types.Var]bool {
	seen := map[*types.Var]bool{}
	var queue []*types.Var
	for v := range seeds {
		seen[v] = true
		queue = append(queue, v)
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range a.edges[v] {
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return seen
}

func (a *analysis_) link(x, y *types.Var) {
	if x == nil || y == nil || x == y {
		return
	}
	a.edges[x] = append(a.edges[x], y)
	a.edges[y] = append(a.edges[y], x)
}

// pair records that the values of two expressions are the same value: an
// edge when both resolve to carrier candidates, a site when one is a literal.
func (a *analysis_) pair(dst types.Object, src ast.Expr, how string) {
	dv, ok := dst.(*types.Var)
	if !ok || dv == nil || !isString(dv.Type()) {
		return
	}
	if val, pos, ok := a.literal(src); ok {
		a.sites = append(a.sites, site{obj: dv, value: val, pos: pos, how: how})
		return
	}
	if sv := a.resolve(src); sv != nil {
		a.link(dv, sv)
	}
}

// compare records x == y, x != y and switch cases: a site, never an edge,
// since comparing two variables does not make them the same value.
func (a *analysis_) compare(x, y ast.Expr) {
	for _, pr := range [2][2]ast.Expr{{x, y}, {y, x}} {
		val, pos, ok := a.literal(pr[1])
		if !ok {
			continue
		}
		if v := a.resolve(pr[0]); v != nil && isString(v.Type()) {
			a.sites = append(a.sites, site{obj: v, value: val, pos: pos, how: "is compared with"})
		}
	}
}

// literal reports a string literal, or a constant of plain (unnamed) string
// type, and its value. A typed constant such as services.StackStatusRunning,
// or a conversion of one, is the intended spelling and is not a literal here.
func (a *analysis_) literal(e ast.Expr) (string, token.Pos, bool) {
	e = ast.Unparen(e)
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", 0, false
		}
	case *ast.Ident:
		c, ok := a.pass.TypesInfo.Uses[x].(*types.Const)
		if !ok {
			return "", 0, false
		}
		if b, ok := c.Type().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
			return "", 0, false
		}
	default:
		return "", 0, false
	}
	tv, ok := a.pass.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", 0, false
	}
	return constant.StringVal(tv.Value), e.Pos(), true
}

// resolve maps an expression to the variable that holds its value: a local,
// parameter or field, or result 0 of a called function. A conversion is
// looked through, so string(spec.TargetType) is spec.TargetType.
func (a *analysis_) resolve(e ast.Expr) *types.Var {
	return a.resolveN(e, 0)
}

func (a *analysis_) resolveN(e ast.Expr, i int) *types.Var {
	info := a.pass.TypesInfo
	e = ast.Unparen(e)
	switch x := e.(type) {
	case *ast.Ident:
		if v, ok := info.Uses[x].(*types.Var); ok {
			return v
		}
		if v, ok := info.Defs[x].(*types.Var); ok {
			return v
		}
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[x]; ok && sel.Kind() == types.FieldVal {
			v := sel.Obj().(*types.Var)
			a.seedField(sel.Recv(), v)
			return v
		}
	case *ast.CallExpr:
		if tv, ok := info.Types[x.Fun]; ok && tv.IsType() {
			if len(x.Args) == 1 && i == 0 {
				return a.resolveN(x.Args[0], 0)
			}
			return nil
		}
		fn := a.callee(x)
		if fn == nil {
			return nil
		}
		res := fn.Type().(*types.Signature).Results()
		if i >= res.Len() {
			return nil
		}
		if fn.Pkg() == a.pass.Pkg || a.seedFunc(fn) {
			return res.At(i)
		}
	}
	return nil
}

func (a *analysis_) callee(call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = f
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.IndexExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			id = x
		}
	}
	if id == nil {
		return nil
	}
	fn, ok := a.pass.TypesInfo.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	return fn.Origin()
}

func pkgSuffix(p *types.Package) string {
	if p == nil {
		return ""
	}
	path := p.Path()
	if i := strings.Index(path, "internal/"); i >= 0 && (i == 0 || path[i-1] == '/') {
		return path[i:]
	}
	return path
}

func (a *analysis_) seedField(recv types.Type, v *types.Var) {
	named, ok := deref(recv).(*types.Named)
	if !ok {
		return
	}
	key := pkgSuffix(named.Obj().Pkg()) + "." + named.Obj().Name() + "." + v.Name()
	for i, s := range sets {
		if s.fields[key] {
			a.seeds[i][v] = true
		}
	}
}

func funcKey(fn *types.Func) string {
	key := pkgSuffix(fn.Pkg()) + "."
	if recv := fn.Type().(*types.Signature).Recv(); recv != nil {
		if named, ok := deref(recv.Type()).(*types.Named); ok {
			key += named.Obj().Name() + "."
		}
	}
	return key + fn.Name()
}

// seedFunc marks fn's result 0 and seeded parameter, and reports whether fn
// is seeded at all (so a cross-package call into it is followed).
func (a *analysis_) seedFunc(fn *types.Func) bool {
	key := funcKey(fn)
	sig := fn.Type().(*types.Signature)
	seeded := false
	for i, s := range sets {
		if s.funcs[key] && sig.Results().Len() > 0 {
			a.seeds[i][sig.Results().At(0)] = true
			seeded = true
		}
		if name, ok := s.params[key]; ok {
			for j := 0; j < sig.Params().Len(); j++ {
				if p := sig.Params().At(j); p.Name() == name {
					a.seeds[i][p] = true
					seeded = true
				}
			}
		}
	}
	return seeded
}

func (a *analysis_) walkFile(f *ast.File) {
	info := a.pass.TypesInfo
	var sigs []*types.Signature
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			fn, _ := info.Defs[x.Name].(*types.Func)
			if fn == nil || x.Body == nil {
				return false
			}
			a.seedFunc(fn)
			sigs = append(sigs, fn.Type().(*types.Signature))
			ast.Inspect(x.Body, visit)
			sigs = sigs[:len(sigs)-1]
			return false
		case *ast.FuncLit:
			sig, _ := info.TypeOf(x).(*types.Signature)
			if sig == nil {
				return false
			}
			sigs = append(sigs, sig)
			ast.Inspect(x.Body, visit)
			sigs = sigs[:len(sigs)-1]
			return false
		case *ast.AssignStmt:
			a.assign(x.Lhs, x.Rhs, "is assigned to")
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(x.Names))
			for i, id := range x.Names {
				lhs[i] = id
			}
			a.assign(lhs, x.Values, "is assigned to")
		case *ast.ReturnStmt:
			if len(sigs) == 0 {
				return true
			}
			res := sigs[len(sigs)-1].Results()
			if len(x.Results) == res.Len() {
				for i, r := range x.Results {
					a.pair(res.At(i), r, "is returned as")
				}
			} else if len(x.Results) == 1 {
				if call, ok := ast.Unparen(x.Results[0]).(*ast.CallExpr); ok {
					for i := 0; i < res.Len(); i++ {
						a.link(res.At(i), a.resolveN(call, i))
					}
				}
			}
		case *ast.CompositeLit:
			t := info.TypeOf(x)
			if t == nil {
				return true
			}
			if _, ok := t.Underlying().(*types.Struct); !ok {
				return true
			}
			for _, el := range x.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if v, ok := info.Uses[key].(*types.Var); ok {
					a.seedField(t, v)
					a.pair(v, kv.Value, "is set as")
				}
			}
		case *ast.CallExpr:
			fn := a.callee(x)
			if fn == nil {
				return true
			}
			seeded := a.seedFunc(fn)
			if fn.Pkg() != a.pass.Pkg && !seeded {
				return true
			}
			params := fn.Type().(*types.Signature).Params()
			for i, arg := range x.Args {
				if i >= params.Len() || (fn.Type().(*types.Signature).Variadic() && i >= params.Len()-1) {
					break
				}
				a.pair(params.At(i), arg, "is passed as")
			}
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				a.compare(x.X, x.Y)
			}
		case *ast.SwitchStmt:
			if x.Tag == nil {
				return true
			}
			for _, st := range x.Body.List {
				if cc, ok := st.(*ast.CaseClause); ok {
					for _, e := range cc.List {
						a.compare(x.Tag, e)
					}
				}
			}
		}
		return true
	}
	ast.Inspect(f, visit)
}

func (a *analysis_) assign(lhs, rhs []ast.Expr, how string) {
	if len(lhs) == len(rhs) {
		for i := range lhs {
			if v := a.resolve(lhs[i]); v != nil {
				a.pair(v, rhs[i], how)
			}
		}
		return
	}
	if len(rhs) != 1 {
		return
	}
	call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
	if !ok {
		return
	}
	for i := range lhs {
		a.link(a.resolve(lhs[i]), a.resolveN(call, i))
	}
}

func isString(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

func deref(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func collectDirectives(pass *analysis.Pass, f *ast.File) map[int]bool {
	lines := map[int]bool{}
	var src []byte
	srcErr := errors.New("pass.ReadFile unavailable")
	if pass.ReadFile != nil {
		src, srcErr = pass.ReadFile(pass.Fset.PositionFor(f.Pos(), false).Filename)
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			pos := pass.Fset.PositionFor(c.Pos(), false)
			text := c.Text
			switch {
			case text == directive:
				pass.Reportf(c.Pos(), "wirevalue:ignore needs a reason: write %s <why this site is not a defect>", directive)
			case strings.HasPrefix(text, directive+" "), strings.HasPrefix(text, directive+"\t"):
				if strings.TrimSpace(text[len(directive):]) == "" {
					pass.Reportf(c.Pos(), "wirevalue:ignore needs a reason: write %s <why this site is not a defect>", directive)
					continue
				}
				lines[pos.Line] = true
				// A directive alone on its line also covers the next line; a
				// trailing one covers only its own, so it cannot silence a new
				// site below it. Unreadable source: cover both.
				start := pos.Offset - (pos.Column - 1)
				if srcErr != nil || (start >= 0 && pos.Offset <= len(src) && strings.TrimSpace(string(src[start:pos.Offset])) == "") {
					lines[pos.Line+1] = true
				}
			case strings.HasPrefix(text, "// wirevalue:ignore"):
				pass.Reportf(c.Pos(), "wirevalue:ignore must have no space after //: write %s <reason>", directive)
			}
		}
	}
	return lines
}
