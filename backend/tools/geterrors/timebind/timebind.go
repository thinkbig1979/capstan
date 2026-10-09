// Package timebind is a go/analysis Analyzer that keeps time values off the
// SQL driver's bind path: every instant written to or compared against a
// column goes through storedInstant (backend/internal/database/stored_instant.go).
//
// WHY. Bound as a plain time.Time, the modernc SQLite driver stores t.String()
// in the value's own zone ("2026-11-01 01:10:00 -0500 EST"): variable width
// and zone-local, so a text comparison or ORDER BY on that column goes wrong
// across a DST change or a TZ change (agent-os-6exk). A raw time.Time argument
// is legal Go, and forbidigo matches names, not types, so nothing else sees it
// (agent-os-omkb).
//
// WHAT IS REPORTED. A value whose type is a time value (below) when it is
//   - an argument to Exec, Query or QueryRow, or their Context variants, on
//     *sql.DB, *sql.Tx, *sql.Conn or *sql.Stmt, or
//   - appended to a []any / []interface{} in package database, which is how
//     this repo builds the argument list for a dynamic WHERE
//     (GetUpdateHistory, GetBackupRunsFiltered) before spreading it with
//     args...; a spread slice's elements are not visible at the call itself.
//     Outside package database a []any is mostly logging, so this arm is
//     scoped; the bind arm runs on every package.
//
// A time value is time.Time, sql.NullTime, sql.Null[T] over a time value, a
// named type whose underlying type is one of those, or a pointer to any of
// them. The sql wrappers are in because their Value() hands the driver a
// time.Time, the same bind.
//
// NOT REPORTED: a time value reaching the driver through a local helper that
// takes `any`, inside a []any{...} literal, or wrapped in sql.Named; and a
// value whose static type is already an interface. None of those shapes
// carried a time value in package database when this was written: a wider
// throwaway analyzer covering interface parameters, []any literals and
// interface assignments found no bind beyond the ones reported here
// (agent-os-omkb close reason). _test.go files are skipped: a fixture
// may need to store a value the way an older Capstan or an outside writer
// did. With the skip removed the analyzer reported one test-file site at the
// commit that added it, null_text_columns_test.go:27 (a raw action_log
// insert), so the skip hides that one site and no other.
//
// Suppress a site with //timebind:ignore <reason> on the site line or the line
// above. The reason is mandatory.
package timebind

import (
	"errors"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const Doc = `report time values bound raw into SQL instead of through storedInstant

A time.Time (or sql.NullTime, a named type over either, or a pointer to one)
passed to Exec/Query/QueryRow(+Context) on *sql.DB, *sql.Tx, *sql.Conn or
*sql.Stmt, or appended to a []any in package database, is stored by the driver
as t.String() in the local zone, which does not sort as the instant does.
Bind storedInstant(t) instead. _test.go files are skipped. Suppress a site with
//timebind:ignore <reason> on the site line or the line above.`

var Analyzer = &analysis.Analyzer{
	Name: "timebind",
	Doc:  Doc,
	Run:  run,
}

const directive = "//timebind:ignore"

var bindMethods = map[string]bool{
	"Exec": true, "ExecContext": true,
	"Query": true, "QueryContext": true,
	"QueryRow": true, "QueryRowContext": true,
}

var sqlReceivers = map[string]bool{"DB": true, "Tx": true, "Conn": true, "Stmt": true}

func run(pass *analysis.Pass) (any, error) {
	// The append arm is scoped to package database (agent-os-omkb R6): there
	// a []any exists to carry SQL arguments, elsewhere it is mostly logging.
	inDatabasePkg := pass.Pkg.Name() == "database"
	for _, f := range pass.Files {
		// PositionFor(..., false) so a //line directive cannot rename a file
		// into a skip; the same trap geterrors documents in posOf.
		if strings.HasSuffix(pass.Fset.PositionFor(f.Pos(), false).Filename, "_test.go") {
			continue
		}
		suppressed := collectDirectives(pass, f)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var args []ast.Expr
			var where string
			switch {
			case isSQLBind(pass, call):
				args, where = call.Args, "bound as an SQL argument"
			case inDatabasePkg && isAnyAppend(pass, call):
				args, where = call.Args[1:], "appended to an SQL argument list"
			default:
				return true
			}
			for _, a := range args {
				t := pass.TypesInfo.TypeOf(a)
				if t == nil || !isTimeValue(t) {
					continue
				}
				line := pass.Fset.PositionFor(a.Pos(), false).Line
				if suppressed[line] {
					continue
				}
				pass.Reportf(a.Pos(), "%s %s: bind storedInstant(t) instead (the driver stores a time value as t.String() in the local zone)",
					types.TypeString(t, types.RelativeTo(pass.Pkg)), where)
			}
			return true
		})
	}
	return nil, nil
}

// isSQLBind reports whether call is one of bindMethods on a database/sql
// DB, Tx, Conn or Stmt (pointer or value receiver expression).
func isSQLBind(pass *analysis.Pass, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !bindMethods[sel.Sel.Name] {
		return false
	}
	fn, ok := pass.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok {
		return false
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return false
	}
	named, ok := deref(recv.Type()).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "database/sql" && sqlReceivers[obj.Name()]
}

// isAnyAppend reports whether call is the builtin append on a slice whose
// element type is an empty interface.
func isAnyAppend(pass *analysis.Pass, call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	if !ok || len(call.Args) < 2 || call.Ellipsis.IsValid() {
		return false
	}
	if _, ok := pass.TypesInfo.Uses[id].(*types.Builtin); !ok || id.Name != "append" {
		return false
	}
	sl, ok := pass.TypesInfo.TypeOf(call.Args[0]).Underlying().(*types.Slice)
	if !ok {
		return false
	}
	iface, ok := sl.Elem().Underlying().(*types.Interface)
	return ok && iface.Empty()
}

func deref(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

// isTimeValue: time.Time, sql.NullTime, a named type whose underlying type is
// either's, or a pointer to one of those. It matches on the underlying struct's
// declaring package, so a named type over time.Time is caught without the
// analysed package having to import "time" itself.
func isTimeValue(t types.Type) bool {
	st, ok := deref(t).Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 {
		return false
	}
	f := st.Field(0)
	if f.Pkg() == nil {
		return false
	}
	switch f.Pkg().Path() {
	case "time":
		return f.Name() == "wall" // time.Time{wall, ext, loc}
	case "database/sql":
		// sql.NullTime{Time, Valid} and sql.Null[T]{V, Valid} over a time value
		return (f.Name() == "Time" || f.Name() == "V") && isTimeValue(f.Type())
	}
	return false
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
				pass.Reportf(c.Pos(), "timebind:ignore needs a reason: write %s <why this site is not a defect>", directive)
			case strings.HasPrefix(text, directive+" "), strings.HasPrefix(text, directive+"\t"):
				if strings.TrimSpace(text[len(directive):]) == "" {
					pass.Reportf(c.Pos(), "timebind:ignore needs a reason: write %s <why this site is not a defect>", directive)
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
			case strings.HasPrefix(text, "// timebind:ignore"):
				// With a space it is an ordinary comment that suppresses nothing
				// while reading like a suppression.
				pass.Reportf(c.Pos(), "timebind:ignore must have no space after //: write %s <reason>", directive)
			}
		}
	}
	return lines
}
