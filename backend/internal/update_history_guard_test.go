package internal

import (
	"embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This directory holds no production code. It is the one package that can
// embed every internal/<pkg>/*.go at once (an embed pattern cannot use ".."),
// which is what a guard spanning packages needs: `go test -overlay` mutants only
// reach embedded content, so a guard that parsed these files from disk would
// stay green under the mutant meant to turn it red (agent-os-qags.14).
//
//go:embed */*.go
var internalSources embed.FS

// skippedEntryBuilder is the one function allowed to write a 'skipped' row.
const skippedEntryBuilder = "NewSkippedUpdateEntry"

// TestUpdateHistoryRowsAreWrittenSafely is agent-os-qags.19's guard for the
// classes fixed in agent-os-z91e.46 and agent-os-z91e.47.
//
//  1. A row with a final status and no completed_at is invisible to the
//     consumers that delete by it: deleteOldUpdateHistoryStmt in
//     database/retention.go (`completed_at IS NOT NULL`) and
//     DeleteUpdateHistoryOlderThan, so it never ages out. PRODUCERS checked: a
//     models.UpdateHistoryEntry composite literal (arm 1), and a map literal
//     passed straight to UpdateUpdateHistory (arm 1b). Each must carry a
//     completed_at unless its Status is the constant "pending". A completed_at
//     whose value is the literal nil counts as absent (it writes NULL). A Status
//     that is not a string literal (a const, a variable, no Status at all) is
//     treated as final.
//  2. A 'skipped' row built by hand skips the reason and completed_at that
//     NewSkippedUpdateEntry (services/scheduler.go) sets. The constant
//     "skipped" as an UpdateHistoryEntry Status, or as a "status" value in an
//     UpdateUpdateHistory map, is flagged anywhere but inside that function.
//     The bare string is not keyed on: backup runs and items also have a
//     'skipped' status, in a different table.
//
// Scope: every non-test file in internal/<pkg>/ (one level), read from the
// embed above. Allowlisted sites: none; the one exemption is the builder, by name.
//
// Cannot see: a map held in a variable and passed later (handlers/updates.go's
// histUpdates is one; it was checked by hand and carries completed_at), a
// Status assigned after the literal (`e.Status = "failed"`), an
// UpdateHistoryEntry built by a type alias or in a package nested deeper than
// internal/<pkg>/, and SQL written outside UpdateUpdateHistory/InsertUpdateHistory.
func TestUpdateHistoryRowsAreWrittenSafely(t *testing.T) {
	entries, err := internalSources.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, dir := range entries {
		files, err := internalSources.ReadDir(dir.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), "_test.go") {
				continue
			}
			path := dir.Name() + "/" + f.Name()
			b, err := internalSources.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sources[path] = string(b)
		}
	}

	scan := scanUpdateHistoryWrites(t, sources)
	for _, v := range scan.violations {
		t.Error(v)
	}
	// A guard that finds nothing to check passes on any tree, so each arm must
	// have examined the real writers.
	if scan.entryLiterals == 0 || scan.statusMaps == 0 {
		t.Fatalf("examined %d UpdateHistoryEntry literals and %d UpdateUpdateHistory status maps; the guard is blind (agent-os-qags.19)",
			scan.entryLiterals, scan.statusMaps)
	}
	t.Logf("examined %d UpdateHistoryEntry literals and %d UpdateUpdateHistory status maps in %d files",
		scan.entryLiterals, scan.statusMaps, len(sources))
}

// TestUpdateHistoryRowsAreWrittenSafely_CheckerSeesTheShapes plants each shape
// the guard exists for, and the shapes it must leave alone.
func TestUpdateHistoryRowsAreWrittenSafely_CheckerSeesTheShapes(t *testing.T) {
	const head = "package p\nimport \"x/models\"\nvar _ = 0\n"
	cases := []struct {
		name string
		body string
		want []string // substrings, one per expected violation
	}{
		{"arm 1: failed without completed_at", `func f() { _ = &models.UpdateHistoryEntry{Status: "failed"} }`, []string{"completed_at"}},
		{"arm 1: no Status at all", `func f() { _ = models.UpdateHistoryEntry{ID: "a"} }`, []string{"completed_at"}},
		{"arm 1: non-constant Status", `func f(s string) { _ = models.UpdateHistoryEntry{Status: s} }`, []string{"completed_at"}},
		{"arm 1: elided element of a slice", `func f() { _ = []models.UpdateHistoryEntry{{Status: "failed"}} }`, []string{"completed_at"}},
		{"arm 1: unkeyed literal", `func f() { _ = models.UpdateHistoryEntry{"a"} }`, []string{"unkeyed"}},
		{"arm 1: CompletedAt explicitly nil", `func f() { _ = &models.UpdateHistoryEntry{Status: "paused", CompletedAt: nil} }`, []string{"completed_at"}},
		{"arm 1b: completed_at explicitly nil", `func f(d D) { d.UpdateUpdateHistory("id", map[string]interface{}{"status": "failed", "completed_at": nil}) }`, []string{"completed_at"}},
		{"arm 1b: map with final status and no completed_at", `func f(d D) { d.UpdateUpdateHistory("id", map[string]interface{}{"status": "failed"}) }`, []string{"completed_at"}},
		{"arm 2: hand-built skipped with completed_at", `func f(c *string) { _ = models.UpdateHistoryEntry{Status: "skipped", CompletedAt: c} }`, []string{"NewSkippedUpdateEntry"}},
		{"arm 2: skipped in an UpdateUpdateHistory map", `func f(d D) { d.UpdateUpdateHistory("id", map[string]interface{}{"status": "skipped", "completed_at": "t"}) }`, []string{"NewSkippedUpdateEntry"}},
		{"ok: pending without completed_at", `func f() { _ = &models.UpdateHistoryEntry{Status: "pending"} }`, nil},
		{"ok: final status with completed_at", `func f(c *string) { _ = models.UpdateHistoryEntry{Status: "paused", CompletedAt: c} }`, nil},
		{"ok: the builder writes skipped", `func NewSkippedUpdateEntry(c *string) { _ = models.UpdateHistoryEntry{Status: "skipped", CompletedAt: c} }`, nil},
		{"ok: empty slice literal", `func f() { _ = []models.UpdateHistoryEntry{} }`, nil},
		{"ok: map without a status key", `func f(d D) { d.UpdateUpdateHistory("id", map[string]interface{}{"new_digest": "x"}) }`, nil},
		{"ok: pending map", `func f(d D) { d.UpdateUpdateHistory("id", map[string]interface{}{"status": "pending"}) }`, nil},
		{"ok: backup 'skipped' elsewhere", `func f(r R) { r.recordItem("skipped") }`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scan := scanUpdateHistoryWrites(t, map[string]string{"p/planted.go": head + tc.body})
			if len(scan.violations) != len(tc.want) {
				t.Fatalf("got %d violations %q, want %d", len(scan.violations), scan.violations, len(tc.want))
			}
			for i, w := range tc.want {
				if !strings.Contains(scan.violations[i], w) {
					t.Errorf("violation %d = %q, want it to mention %q", i, scan.violations[i], w)
				}
			}
		})
	}
}

type updateHistoryScan struct {
	violations    []string
	entryLiterals int // UpdateHistoryEntry struct literals examined
	statusMaps    int // UpdateUpdateHistory map literals with a "status" key examined
}

// scanUpdateHistoryWrites checks the two arms over sources (path -> content).
func scanUpdateHistoryWrites(t *testing.T, sources map[string]string) updateHistoryScan {
	t.Helper()
	var scan updateHistoryScan
	fset := token.NewFileSet()
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			fn := ""
			if fd, ok := decl.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					switch {
					case isHistoryEntryType(x.Type):
						scan.entryLiterals++
						scan.checkEntry(fset, x, fn)
					case isHistoryEntrySlice(x.Type):
						// Elided elements carry no type: []T{{Status: ...}}.
						for _, e := range x.Elts {
							if el, ok := e.(*ast.CompositeLit); ok && el.Type == nil {
								scan.entryLiterals++
								scan.checkEntry(fset, el, fn)
							}
						}
					}
				case *ast.CallExpr:
					sel, ok := x.Fun.(*ast.SelectorExpr)
					if ok && sel.Sel.Name == "UpdateUpdateHistory" && len(x.Args) >= 2 {
						if m, ok := x.Args[1].(*ast.CompositeLit); ok {
							scan.checkMap(fset, m, fn)
						}
					}
				}
				return true
			})
		}
	}
	return scan
}

// checkEntry applies both arms to one UpdateHistoryEntry literal.
func (s *updateHistoryScan) checkEntry(fset *token.FileSet, lit *ast.CompositeLit, fn string) {
	fields := map[string]ast.Expr{}
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			s.violations = append(s.violations, fmt.Sprintf("%s: unkeyed UpdateHistoryEntry literal cannot be checked; name its fields (agent-os-qags.19)", fset.Position(lit.Pos())))
			return
		}
		if id, ok := kv.Key.(*ast.Ident); ok {
			fields[id.Name] = kv.Value
		}
	}
	hasCompleted := isSet(fields["CompletedAt"])
	s.judge(fset, lit.Pos(), fields["Status"], hasCompleted, fn)
}

// checkMap applies both arms to a map literal passed to UpdateUpdateHistory.
func (s *updateHistoryScan) checkMap(fset *token.FileSet, lit *ast.CompositeLit, fn string) {
	var status ast.Expr
	hasStatus, hasCompleted := false, false
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch stringLiteral(kv.Key) {
		case "status":
			hasStatus, status = true, kv.Value
		case "completed_at":
			hasCompleted = isSet(kv.Value)
		}
	}
	if !hasStatus {
		return
	}
	s.statusMaps++
	s.judge(fset, lit.Pos(), status, hasCompleted, fn)
}

func (s *updateHistoryScan) judge(fset *token.FileSet, pos token.Pos, status ast.Expr, hasCompleted bool, fn string) {
	val := stringLiteral(status)
	if val == "skipped" && fn != skippedEntryBuilder {
		s.violations = append(s.violations, fmt.Sprintf("%s: a 'skipped' update_history row is built by hand; use NewSkippedUpdateEntry, which sets the reason and completed_at (agent-os-z91e.47, agent-os-qags.19)", fset.Position(pos)))
	}
	if val != "pending" && !hasCompleted {
		s.violations = append(s.violations, fmt.Sprintf("%s: an update_history write with a final or non-constant status has no completed_at, so retention (completed_at IS NOT NULL) never deletes it (agent-os-z91e.46, agent-os-qags.19)", fset.Position(pos)))
	}
}

// isSet reports whether a completed_at value is present and not the literal
// nil: `CompletedAt: nil` and `"completed_at": nil` write NULL, which retention
// skips exactly as if the key were missing. A nil-valued variable is not seen.
func isSet(e ast.Expr) bool {
	if e == nil {
		return false
	}
	id, ok := e.(*ast.Ident)
	return !ok || id.Name != "nil"
}

// stringLiteral returns the value of a string literal, or "" for anything else
// (nil, an identifier, a call, a constant reference).
func stringLiteral(e ast.Expr) string {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return v
}

// isHistoryEntryType matches models.UpdateHistoryEntry (and the bare name, for
// code inside the models package).
func isHistoryEntryType(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		return ok && pkg.Name == "models" && x.Sel.Name == "UpdateHistoryEntry"
	case *ast.Ident:
		return x.Name == "UpdateHistoryEntry"
	}
	return false
}

// isHistoryEntrySlice matches []models.UpdateHistoryEntry and the pointer form.
func isHistoryEntrySlice(e ast.Expr) bool {
	arr, ok := e.(*ast.ArrayType)
	if !ok {
		return false
	}
	elt := arr.Elt
	if star, ok := elt.(*ast.StarExpr); ok {
		elt = star.X
	}
	return isHistoryEntryType(elt)
}
