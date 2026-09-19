// Package geterrors is a go/analysis Analyzer for the softened / merged getter
// error family: a call's error return that is weakened into an "absent" signal
// at the ASSIGNMENT, or fused by `||` to a value test at the BRANCH.
//
// WHY IT EXISTS, AND WHAT IT REPLACES. Nine beads filed across 2026-09-04/05
// are one defect family, and every sweep their close reasons cited was
// anchored on an identifier, so every one of them returned a FALSE ZERO on
// this tree: the error NAME (sErr, dbErr, pErr evaded every `err`-anchored
// regex -- agent-os-g482, obgr, r1by), the callee VERB (`List*` evaded every
// `\.Get[A-Z]` sweep -- agent-os-l42o), and the SHAPE itself. The predecessor
// instrument, scripts/getter-errors/main.go driven by
// scripts/check-getter-errors.sh, fixed the anchoring by walking the AST, but
// it enforced a per-file COUNT BASELINE rather than a gate. Its own header
// stated the cost: deleting one site and adding another in the SAME file nets
// zero and passes. This analyzer reports ZERO on the tree, so any new site is
// a hard failure in a required job instead of a number that has to move.
//
// THE TWO SHAPES.
//
//	SOFT   x, e := f()            e is ONLY ever compared `e == nil`. The
//	       if e == nil { ... }    error is softened into an "absent" signal
//	                              and "I could not read it" becomes
//	                              "there is nothing to read".
//
//	MERGE  if e != nil || v {     the error IS checked, but it is fused by
//	                              `||` to a VALUE test, so "I could not read
//	                              it" and "I read it and the answer is no"
//	                              take one branch and the caller cannot tell
//	                              them apart.
//
// DISCARD (`x, _ := f()`) was the third kind of the predecessor and is
// DELIBERATELY NOT HERE. errcheck with `check-blank: true` owns it as of
// agent-os-qyg7.1, in the required job "Lint (golangci-lint)", and it owns it
// with type information. Two instruments reporting one class is how the
// predecessor's --cross-check came to exist; one owner is better.
//
// MEMBERSHIP RULE for MERGE, stated so a later reader can check it rather than
// infer it: an `if` (or a TAGLESS `switch` case) whose condition is a
// top-level `||` chain with AT LEAST ONE error-nil operand AND AT LEAST ONE
// operand that is not. The second half is what the class means -- a fault
// merged with a VALUE. `if timeErr != nil || daysErr != nil` is two faults
// sharing a branch, a different question owned by agent-os-rltu, and it is
// deliberately NOT a member.
//
// WHAT go/types CHANGED, AND WHAT IT DID NOT. The predecessor had no type
// information by design (the required check that ran it had a checkout and
// nothing else), so it was forced to anchor MERGE membership on the error's
// NAME -- any ident or selector ending "err". That is the one identifier
// anchor its own header says the program was written to avoid, and it is gone
// here: an operand is an error because its STATIC TYPE implements `error`.
//
// Do not read that as a reduction of the current set, because it is not one.
// MEASURED on 6c0819c: all 63 SOFT+MERGE sites the predecessor reported carry
// values whose static type is already `error`, and a typed MERGE arm with the
// name anchor removed entirely finds the same 13. The value of typing here is
// PROSPECTIVE: a future second value that is nillable but is not an error --
// a `*Config`, a `[]byte`, a channel -- cannot be reported, and under the name
// anchor a `*cfgErr` would have been.
//
// Be precise about `strings.Cut`, because it is easy to credit typing with a
// cleanup it did not do. The predecessor's three standing false positives
// there (exec_env.go:81, redact_url.go:246 and :412 on 6c0819c) are gone
// because all three are DISCARD -- `_` in the last position -- and this
// analyzer no longer owns that kind at all. Typing is what WOULD decline them
// had they been written as softened rather than discarded, since Cut's LAST
// value, the one this analyzer looks at, is a bool.
//
// SCOPE RULE, ported verbatim from the predecessor because it is the substance
// of the detector and not an implementation detail. An earlier prototype
// classified a candidate by walking the ENTIRE enclosing function body and
// calling any later ident of the same spelling a hard use. That is a fifth
// anchor, and the worst one, because it makes the tool blind exactly where the
// family's commonest spelling -- plain `err` -- lives: at 4b569a6 it missed
// services/docker_update.go:204 and handlers/updates.go:179, :325 and :812,
// all textbook members, because each function reuses or shadows `err` further
// down. Here a candidate's REGION is the statements that follow it IN ITS OWN
// STATEMENT LIST (or, for an if-init, that if's cond/body/else). See usescan.go.
//
// TEST FILES ARE SKIPPED, and that is a decision rather than an omission.
// go vet analyses _test.go files and has no flag to stop it, so the skip lives
// inside the analyzer. The predecessor skipped them at main.go:701. MEASURED
// on 6c0819c, by building this analyzer with the skip disabled and running the
// same `go vet -vettool ./...`: the census is 86 (SOFT=67, MERGE=19), not 63 --
// 23 further in-class sites across 14 test files. They are mostly CORRECT
// combined assertions of the form
// `if ok, err := IsContained(root, root); err != nil || !ok { t.Fatalf(...) }`,
// where merging the two states is exactly what the assertion wants. That
// measurement doubles as the proof that the skip is a skip and not blindness:
// the same analyzer, one line changed, fires 23 times inside _test.go files.
// Whether test files should be covered at all is a separate question for a
// later bead.
//
// SUPPRESSION is `//geterrors:ignore <reason>`, on the site line or the line
// immediately above. go vet does not honour `//nolint`, so a custom directive
// is forced; `//nolint:geterrors` was rejected because geterrors is not a
// golangci-lint linter, so enabling nolintlint later would turn every one of
// those comments into an unknown-linter failure in a required job. THE REASON
// IS MANDATORY: a bare directive is itself reported, because a suppression
// with no stated reason is indistinguishable from one added to make a build
// green.
package geterrors

import (
	"errors"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const Doc = `report errors softened to nil or merged into a value test

SOFT   x, e := f() where e is only ever compared e == nil: the error is
       softened into an "absent" signal.
MERGE  if e != nil || v: the error is checked, then fused by || to a value
       test, so "I could not read it" and "the answer is no" take one branch.

Only values whose static type implements error are reported, and _test.go
files are skipped. Suppress a site with //geterrors:ignore <reason> on the
site line or the line above; the reason is mandatory.`

// Analyzer is the go/analysis entry point. It is driven by go vet through
// cmd/geterrors, and directly by analysistest.
var Analyzer = &analysis.Analyzer{
	Name: "geterrors",
	Doc:  Doc,
	Run:  run,
}

const directive = "//geterrors:ignore"

var errorIface = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

func run(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		// THE TEST-FILE SKIP. go vet hands us _test.go files and offers no
		// flag to stop it, so the filter is here. See the package doc.
		if strings.HasSuffix(pass.Fset.Position(f.Pos()).Filename, "_test.go") {
			continue
		}
		s := &scanner{pass: pass}
		s.collectDirectives(f)
		s.file(f)
	}
	return nil, nil
}

type scanner struct {
	pass *analysis.Pass
	// suppressed holds the lines a valid directive covers. A comment ON ITS
	// OWN LINE covers that line and the next, so it can sit above the site; a
	// TRAILING comment covers only its own line. The distinction matters:
	// under a blanket line+1 rule a trailing suppression would also silence a
	// genuine new site on the following line, which is the shape of a gate
	// that goes quiet without anyone deciding it should.
	suppressed map[int]bool
}

// collectDirectives records every //geterrors:ignore in the file and reports
// the malformed ones. A directive that suppresses nothing is not itself an
// error -- code moves -- but one that CANNOT work, or one with no reason, is:
// both read as suppression and neither suppresses anything.
func (s *scanner) collectDirectives(f *ast.File) {
	s.suppressed = map[int]bool{}
	src, srcErr := s.readFile(f)
	for _, grp := range f.Comments {
		for _, c := range grp.List {
			pos := s.pass.Fset.Position(c.Pos())
			text := c.Text
			switch {
			case text == directive:
				s.pass.Reportf(c.Pos(), "geterrors:ignore needs a reason: write %s <why this site is not a defect>", directive)
			case strings.HasPrefix(text, directive+" "), strings.HasPrefix(text, directive+"\t"):
				if strings.TrimSpace(text[len(directive):]) == "" {
					s.pass.Reportf(c.Pos(), "geterrors:ignore needs a reason: write %s <why this site is not a defect>", directive)
					continue
				}
				s.suppressed[pos.Line] = true
				// srcErr != nil means we cannot tell own-line from trailing;
				// cover both, which is what the directive means in the common
				// case, rather than silently suppressing nothing.
				if srcErr != nil || onlyBlankBefore(src, pos.Offset-(pos.Column-1), pos.Offset) {
					s.suppressed[pos.Line+1] = true
				}
			case strings.HasPrefix(text, "// geterrors:ignore"):
				// A space after // makes it an ordinary comment, so it would
				// suppress nothing while reading exactly like a suppression.
				s.pass.Reportf(c.Pos(), "geterrors:ignore must have no space after //: write %s <reason>", directive)
			}
		}
	}
}

func (s *scanner) readFile(f *ast.File) ([]byte, error) {
	name := s.pass.Fset.Position(f.Pos()).Filename
	if s.pass.ReadFile != nil {
		return s.pass.ReadFile(name)
	}
	return nil, errNoReadFile
}

var errNoReadFile = errors.New("pass.ReadFile unavailable")

// onlyBlankBefore reports whether src[start:end] is all whitespace, i.e. the
// comment at end is the first thing on its line.
func onlyBlankBefore(src []byte, start, end int) bool {
	if start < 0 || end > len(src) || start > end {
		return false
	}
	return strings.TrimSpace(string(src[start:end])) == ""
}

func (s *scanner) report(pos token.Pos, format string, args ...any) {
	if s.suppressed[s.pass.Fset.Position(pos).Line] {
		return
	}
	s.pass.Reportf(pos, format, args...)
}

// isError reports whether e's static type implements error. This is the
// membership test the predecessor could not make; see the package doc.
func (s *scanner) isError(e ast.Expr) bool {
	t := s.pass.TypesInfo.TypeOf(e)
	if t == nil || t == types.Typ[types.Invalid] {
		return false
	}
	return types.Implements(t, errorIface)
}

// isNil reports whether e is the predeclared nil. Typed rather than spelled,
// because `nil` is shadowable and a name test is the habit this analyzer
// exists to break.
func (s *scanner) isNil(e ast.Expr) bool {
	tv, ok := s.pass.TypesInfo.Types[e]
	return ok && tv.Type == types.Typ[types.UntypedNil]
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
// of the shape `a, b, ..., last := call()`. POSITION, not spelling, picks the
// candidate: Go returns the error last, and a name test is exactly the anchor
// this analyzer exists to avoid. The static type then decides membership.
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

// classify handles one SOFT candidate given the region that follows it.
func (s *scanner) classify(a *ast.AssignStmt, region func(*useScan)) {
	call, last, ok := candidate(a)
	if !ok {
		return
	}
	if last.Name == "_" {
		return // DISCARD belongs to errcheck check-blank; see the package doc.
	}
	if a.Tok != token.DEFINE {
		return // `x, err = f()` reuses an existing variable; not this shape.
	}
	if !s.isError(last) {
		return
	}
	u := &useScan{name: last.Name, isNil: s.isNil}
	region(u)
	if u.soft > 0 && u.hard == 0 {
		s.report(a.Pos(), "error from %s is softened: %s is only ever compared to nil, so a failed call is indistinguishable from an absent value", calleeName(call), last.Name)
	}
}

// ------------------------------------------------------------ merge shape --

// isErrNotNil reports whether e is `<error> != nil`, in either operand order.
// Both orders are checked because `nil != err` is legal Go and because
// checking only one is the same category of blindness as anchoring the whole
// sweep on the left of the `||` (agent-os-koy9 was blind that way).
func (s *scanner) isErrNotNil(e ast.Expr) bool {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return false
	}
	if s.isNil(b.Y) && s.isError(b.X) {
		return true
	}
	return s.isNil(b.X) && s.isError(b.Y)
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

// mergesErrorWithValue applies the membership rule from the package doc: at
// least one error-nil operand AND at least one operand that is not.
func (s *scanner) mergesErrorWithValue(cond ast.Expr) (errs, values int, ok bool) {
	var ops []ast.Expr
	orOperands(cond, &ops)
	if len(ops) < 2 {
		return 0, 0, false
	}
	for _, o := range ops {
		if s.isErrNotNil(o) {
			errs++
		} else {
			values++
		}
	}
	if errs == 0 || values == 0 {
		return 0, 0, false
	}
	return errs, values, true
}

func (s *scanner) reportMerge(pos token.Pos, errs, values int) {
	s.report(pos, "error is merged into a value test by || (%d error operand(s), %d value operand(s)): a failed read and a negative answer take the same branch", errs, values)
}

// mergeIf records the MERGE site, if any, carried by one if statement.
func (s *scanner) mergeIf(x *ast.IfStmt) {
	if errs, values, ok := s.mergesErrorWithValue(x.Cond); ok {
		s.reportMerge(x.Pos(), errs, values)
	}
}

// mergeSwitch records MERGE sites carried by a TAGLESS switch's case
// expressions. `switch { case err != nil || v: }` is the same branch as
// `if err != nil || v`, written the other way, and an IfStmt-only detector is
// blind to it.
//
// THIS IS NOT HYPOTHETICAL AND IT IS WHY THE ARM EXISTS. The predecessor's
// first version handled IfStmt alone. Its verdict was then probed by
// reintroducing a known in-class site into a file whose baseline recorded
// MERGE=0, and the ratchet stayed GREEN, because the reintroduced site was
// written as `case dbRun.FinishedAt == nil || err != nil` inside the very
// switch that agent-os-89ut's fix had just introduced. So the instrument was
// blind precisely to the shape its own remedy produces.
//
// A TAGGED switch is excluded on purpose: in `switch x { case a || b: }` the
// case expression is compared against x, so it is a value and not a branch
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
			if errs, values, ok := s.mergesErrorWithValue(e); ok {
				s.reportMerge(e.Pos(), errs, values)
			}
		}
	}
}

// -------------------------------------------------------------- traversal --

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

// file walks one file. A candidate is reached by exactly ONE code path -- the
// statement list that contains it, or the if/for/switch that owns it as an
// init -- so a site is reported once. The predecessor's prototype visited an
// if-init assignment twice and printed five of its thirteen SOFT rows in
// duplicate.
func (s *scanner) file(f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
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
