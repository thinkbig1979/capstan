package internal

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// stdPipeAllowlist names the functions allowed to call StdoutPipe or
// StderrPipe, keyed "<package>.<func>" or "<package>.(<recv>).<method>", each
// with the reason the Wait-first order does not apply there.
var stdPipeAllowlist = map[string]string{
	"handlers.(*LogsHandler).StreamLogs": "the scanner is never awaited before Wait: teardown is cancel, Kill, Wait, " +
		"and with StdoutPipe Wait returns at the child's exit and closes the read end, which ends the scanner " +
		"(probed with a grandchild holding stdout: 0s, agent-os-qags.29)",
}

// TestStdPipesOnlyInAllowlistedFunctions is agent-os-qags.16's guard for the
// class fixed twice, in agent-os-z91e.20 (streamComposeCmd, docker_update.go)
// and agent-os-z91e.24 (RunStreaming, docker_lifecycle.go): a child read
// through StdoutPipe/StderrPipe whose scanners are awaited before cmd.Wait(),
// so Wait, the only place WaitDelay acts, is never reached and a grandchild
// holding the pipe outlives the deadline. The safe shape is io.Pipe writers,
// cmd.Wait first, close the writers, then the readers. PRODUCER: a
// .StdoutPipe/.StderrPipe selector; CONSUMER: the scanner the caller waits on
// before Wait. The consumer's order is not decidable from syntax, so every
// producer outside the allowlist is flagged.
//
// Scope: every non-test file in internal/<pkg>/ (one level), read from the
// embed in update_history_guard_test.go so `go test -overlay` mutants reach
// it. Calls and method values (`f := cmd.StdoutPipe`) both count.
//
// Cannot see: backend/cmd/ and packages nested deeper than internal/<pkg>/
// (0 sites in either on ae96e29); the separate tools/ module; the same defect
// built without these methods (os.Pipe assigned to cmd.Stdout, read and
// awaited before Wait); StdinPipe (Wait closes it; not this class). Matching
// is by method name, not type, so a StdoutPipe method on some other type is
// flagged too (none exists).
func TestStdPipesOnlyInAllowlistedFunctions(t *testing.T) {
	scan := scanStdPipes(t, nonTestInternalSources(t))
	for _, v := range scan.violations {
		t.Error(v)
	}
	// A renamed or removed allowlisted function must not leave a stale entry
	// that would later admit a new site under the old name.
	for fn := range stdPipeAllowlist {
		if scan.allowedSites[fn] == 0 {
			t.Errorf("allowlist entry %s matched no StdoutPipe/StderrPipe call: remove it or correct the name", fn)
		}
	}
	if scan.files == 0 {
		t.Fatal("examined 0 files; the guard is blind (agent-os-qags.16)")
	}
	t.Logf("examined %d files; allowlisted sites: %v", scan.files, scan.allowedSites)
}

// TestStdPipesOnlyInAllowlistedFunctions_CheckerSeesTheShapes plants each
// shape the guard exists for, and the shapes it must leave alone.
func TestStdPipesOnlyInAllowlistedFunctions_CheckerSeesTheShapes(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		body string
		want []string // substrings, one per expected violation
	}{
		{"new function calling StdoutPipe", "services",
			"func run(cmd *exec.Cmd) { _, _ = cmd.StdoutPipe() }", []string{"p/planted.go:4", "services.run", "StdoutPipe"}},
		{"StderrPipe in a method", "services",
			"func (r *R) Run(cmd *exec.Cmd) { _, _ = cmd.StderrPipe() }", []string{"services.(*R).Run"}},
		{"method value", "services",
			"func run(cmd *exec.Cmd) { f := cmd.StdoutPipe; _ = f }", []string{"services.run"}},
		{"inside a func literal", "services",
			"func run(cmd *exec.Cmd) { go func() { _, _ = cmd.StdoutPipe() }() }", []string{"services.run"}},
		{"package-level initializer", "services",
			"var f = func(cmd *exec.Cmd) { _, _ = cmd.StdoutPipe() }", []string{"services.<package scope>"}},
		{"allowlisted name on another receiver", "handlers",
			"func (h *Other) StreamLogs(cmd *exec.Cmd) { _, _ = cmd.StdoutPipe() }", []string{"handlers.(*Other).StreamLogs"}},
		{"allowlisted name in another package", "services",
			"func (h *LogsHandler) StreamLogs(cmd *exec.Cmd) { _, _ = cmd.StdoutPipe() }", []string{"services.(*LogsHandler).StreamLogs"}},
		{"ok: the allowlisted function", "handlers",
			"func (h *LogsHandler) StreamLogs(cmd *exec.Cmd) { _, _ = cmd.StdoutPipe(); go func() { _, _ = cmd.StderrPipe() }() }", nil},
		{"ok: io.Pipe writers", "services",
			"func run(cmd *exec.Cmd) { r, w := io.Pipe(); cmd.Stdout = w; _ = r }", nil},
		{"ok: StdinPipe", "services",
			"func run(cmd *exec.Cmd) { _, _ = cmd.StdinPipe() }", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package " + tc.pkg + "\nimport \"os/exec\"\nvar _ exec.Cmd\n" + tc.body + "\n"
			scan := scanStdPipes(t, map[string]string{"p/planted.go": src})
			if len(tc.want) == 0 {
				if len(scan.violations) != 0 {
					t.Fatalf("got violations %q, want none", scan.violations)
				}
				return
			}
			if len(scan.violations) != 1 {
				t.Fatalf("got %d violations %q, want 1", len(scan.violations), scan.violations)
			}
			for _, w := range tc.want {
				if !strings.Contains(scan.violations[0], w) {
					t.Errorf("violation %q does not mention %q", scan.violations[0], w)
				}
			}
		})
	}
}

// nonTestInternalSources returns every non-test internal/<pkg>/*.go from the
// embed, keyed by path relative to internal/.
func nonTestInternalSources(t *testing.T) map[string]string {
	t.Helper()
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
	return sources
}

type stdPipeScan struct {
	violations   []string
	allowedSites map[string]int // allowlisted function -> sites found in it
	files        int
}

func scanStdPipes(t *testing.T, sources map[string]string) stdPipeScan {
	t.Helper()
	scan := stdPipeScan{allowedSites: map[string]int{}}
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scan.files++
		pkg := file.Name.Name
		for _, decl := range file.Decls {
			fn := pkg + ".<package scope>"
			if fd, ok := decl.(*ast.FuncDecl); ok {
				fn = pkg + "." + funcKey(fd)
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "StdoutPipe" && sel.Sel.Name != "StderrPipe") {
					return true
				}
				if _, ok := stdPipeAllowlist[fn]; ok {
					scan.allowedSites[fn]++
					return true
				}
				scan.violations = append(scan.violations, fmt.Sprintf(
					"%s: %s in %s: read output through io.Pipe writers and call cmd.Wait before awaiting the readers, "+
						"as streamComposeCmd (services/docker_update.go) does, or add the function to stdPipeAllowlist with a reason (agent-os-qags.16)",
					fset.Position(sel.Sel.Pos()), sel.Sel.Name, fn))
				return true
			})
		}
	}
	return scan
}

// funcKey renders a FuncDecl as "Name" or "(<recv>).Name", e.g.
// "(*LogsHandler).StreamLogs".
func funcKey(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	recv := fd.Recv.List[0].Type
	star := ""
	if s, ok := recv.(*ast.StarExpr); ok {
		star, recv = "*", s.X
	}
	switch r := recv.(type) {
	case *ast.IndexExpr:
		recv = r.X
	case *ast.IndexListExpr:
		recv = r.X
	}
	name := "?"
	if id, ok := recv.(*ast.Ident); ok {
		name = id.Name
	}
	return "(" + star + name + ")." + fd.Name.Name
}
