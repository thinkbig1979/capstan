package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockedRefusalSinks are the calls that put request text where a locked
// session's reply passes it through the secret masking (agent-os-ly6i): a
// lint (its messages are redacted with the secrets) and a compose or env
// write (a later locked GET, diff, inspect or compose output masks the file).
var lockedRefusalSinks = map[string]bool{
	"LintCompose":            true,
	"writeComposeFileAtomic": true,
	"writeEnvFileAtomic":     true,
	// agent-os-h71i: what a locked session must not start through backups:
	// a restore or DR restore, a prune, and a repository init.
	"LaunchRestore":    true,
	"LaunchDRRestore":  true,
	"LaunchPrune":      true,
	"EnsureRepository": true,
}

// lockedRefusalFileSinks are sinks only in the named file: backup.go's
// SetSettings is where the repository, its password, the rclone remote and
// the retention are written (agent-os-h71i). SetSettings elsewhere writes
// settings a locked session may change.
var lockedRefusalFileSinks = map[string]string{
	"SetSettings": "backup.go",
}

// lockedRefusalExempt names the handlers that reach a sink without
// refuseLocked, each with its reason. An entry that no longer matches a
// function reaching a sink fails the guard.
var lockedRefusalExempt = map[string]string{
	// Writes back the file's own bytes from before the save, never request text.
	"compose.go:restoreEnv": "restore of the pre-save bytes",
	// Gated by the older `if !envUnlocked(c)` 403 FORBIDDEN before the write;
	// moving them onto refuseLocked is a follow-up (orch-dm-60 ruling, (i)).
	"env.go:Put":                  "older envUnlocked gate, follow-up (i)",
	"settings.go:UpdateGlobalEnv": "older envUnlocked gate, follow-up (i)",
}

// lockedRefusalConditional names the handlers whose gate may be
// `if <cond> && refuseLocked(...) { return }`: they refuse only when the
// request carries text, each with its reason.
var lockedRefusalConditional = map[string]string{
	// An empty env create carries no text (orch-dm-60 ruling A).
	"env.go:Create": "refused only when the body carries content",
	// Refused when the body changes where or with what credential backups
	// go, or the retention; a schedule edit stays allowed (agent-os-h71i).
	"backup.go:updateSettings": "refused only when a destination, credential or retention field is present",
}

// TestLockedRefusalGuard_ly6i keeps every handler that lints or writes
// request compose or env text on refuseLocked: the function must open with
// `if refuseLocked(...) { ...; return }` at its top level, before the first
// sink call. Sources come from go:embed (handlerSources4hxa), so an -overlay
// mutant of a handler file is what is parsed.
func TestLockedRefusalGuard_ly6i(t *testing.T) {
	entries, err := handlerSources4hxa.ReadDir(".")
	require.NoError(t, err)

	var inScope []string
	used := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := handlerSources4hxa.ReadFile(name)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err, name)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := name + ":" + fn.Name.Name
			sink := firstLockedRefusalSink(fn.Body, name)
			if sink == token.NoPos {
				continue
			}
			inScope = append(inScope, key)
			if _, ok := lockedRefusalExempt[key]; ok {
				used[key] = true
				continue
			}
			_, conditional := lockedRefusalConditional[key]
			gate, viaCond := topLevelRefuseLocked(fn.Body, conditional)
			// A conditional entry is used only while its gate really is the
			// `<cond> && refuseLocked` form (review F8).
			used[key] = used[key] || (conditional && viaCond)
			if !assert.NotEqual(t, token.NoPos, gate, "%s lints or writes request text, or starts a restore, prune or repository change, with no top-level `if refuseLocked(c, ...) { return }`: a locked session would read its own text back through the secret masking (agent-os-ly6i) or reach secrets through backups (agent-os-h71i)", fset.Position(sink)) {
				continue
			}
			assert.Less(t, gate, sink, "%s: refuseLocked must come before the first lint or write at %s", key, fset.Position(sink))
		}
	}
	for key := range lockedRefusalExempt {
		assert.True(t, used[key], "lockedRefusalExempt names %s but it reaches no sink; remove the stale entry", key)
	}
	for key := range lockedRefusalConditional {
		assert.True(t, used[key], "lockedRefusalConditional names %s but it reaches no sink through a `<cond> && refuseLocked` gate; remove the stale entry", key)
	}
	sort.Strings(inScope)
	// Not vacuous: every handler this bead gated is found.
	for _, want := range []string{
		"compose.go:Lint", "compose.go:Put", "compose.go:PutComposeAndEnv",
		"stack_crud.go:Create", "stacks.go:Lint", "env.go:Create",
		"backup.go:runRestore", "backup.go:runDRRestore", "backup.go:runPrune",
		"backup.go:repoInit", "backup.go:updateSettings",
	} {
		assert.Contains(t, inScope, want)
	}
}

// firstLockedRefusalSink is the position of the first sink call in body, or
// NoPos. file is the source file's name, for the file-scoped sinks.
func firstLockedRefusalSink(body *ast.BlockStmt, file string) token.Pos {
	first := token.NoPos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch f := call.Fun.(type) {
		case *ast.Ident:
			name = f.Name
		case *ast.SelectorExpr:
			name = f.Sel.Name
		}
		inFile := lockedRefusalFileSinks[name] == file
		if (lockedRefusalSinks[name] || inFile) && (first == token.NoPos || call.Pos() < first) {
			first = call.Pos()
		}
		return true
	})
	return first
}

// topLevelRefuseLocked is the position of the first top-level statement of
// body shaped `if refuseLocked(...) { ...; return }`, or NoPos, and whether it
// matched the `if <cond> && refuseLocked(...)` form, which counts only with
// conditional. A call whose result is dropped, or one nested in a branch, does
// not count.
func topLevelRefuseLocked(body *ast.BlockStmt, conditional bool) (token.Pos, bool) {
	for _, st := range body.List {
		ifs, ok := st.(*ast.IfStmt)
		if !ok || ifs.Init != nil || ifs.Else != nil || len(ifs.Body.List) == 0 {
			continue
		}
		cond, viaCond := ifs.Cond, false
		if bin, ok := cond.(*ast.BinaryExpr); ok && conditional && bin.Op == token.LAND {
			cond, viaCond = bin.Y, true
		}
		call, ok := cond.(*ast.CallExpr)
		if !ok {
			continue
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "refuseLocked" {
			continue
		}
		if _, ok := ifs.Body.List[len(ifs.Body.List)-1].(*ast.ReturnStmt); ok {
			return ifs.Pos(), viaCond
		}
	}
	return token.NoPos, false
}
