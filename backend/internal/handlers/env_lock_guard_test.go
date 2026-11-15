package handlers

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"
)

// Embedded, not read from disk, so a `go test -overlay` mutant of either file
// is what this guard sees.
//
//go:embed terminal.go
var terminalSource string

//go:embed logs.go
var logsSource string

// calledFuncs returns the position of the first call to each bare function
// name in body (closures included).
func calledFuncs(body ast.Node) map[string]token.Pos {
	calls := map[string]token.Pos{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok {
			if _, seen := calls[id.Name]; !seen {
				calls[id.Name] = call.Pos()
			}
		}
		return true
	})
	return calls
}

// TestEnvLockGuard_ContainerOutputHandlersReadTheUnlock is agent-os-rbmi's
// guard. A container's shell and its logs show env values verbatim, so every
// handler in terminal.go and logs.go that serves them must read the env
// unlock:
//
//   - a func that opens a terminal (calls serveWS in terminal.go) reads
//     envUnlocked BEFORE serveWS (refused before the upgrade) and keeps a
//     newUnlockWatch (re-checked while open);
//   - a func that streams log lines (calls serveWS in logs.go) keeps a
//     newUnlockWatch and masks with maskLogLine;
//   - a func that serves parsed log lines (calls parseLogLines or
//     parseLogLine) reads the unlock (envUnlocked or newUnlockWatch) and masks
//     with maskLogLine.
//
// The set of funcs each rule finds is pinned, so a rename or a new handler
// turns this red instead of leaving it vacuously green.
func TestEnvLockGuard_ContainerOutputHandlersReadTheUnlock(t *testing.T) {
	type rule struct {
		file, src string
	}
	found := map[string][]string{}
	for _, r := range []rule{{"terminal.go", terminalSource}, {"logs.go", logsSource}} {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, r.file, r.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			name := r.file + ":" + fn.Name.Name
			calls := calledFuncs(fn.Body)
			_, unlocked := calls["envUnlocked"]
			_, watched := calls["newUnlockWatch"]
			_, masked := calls["maskLogLine"]
			serve, serves := calls["serveWS"]

			if serves && r.file == "terminal.go" {
				found["terminal"] = append(found["terminal"], name)
				gate, ok := calls["envUnlocked"]
				if !ok || gate > serve {
					t.Errorf("%s opens a terminal without reading envUnlocked before serveWS", name)
				}
				if !watched {
					t.Errorf("%s opens a terminal without a newUnlockWatch to re-check it", name)
				}
			}
			if serves && r.file == "logs.go" {
				found["stream"] = append(found["stream"], name)
				if !watched || !masked {
					t.Errorf("%s streams log lines without newUnlockWatch and maskLogLine", name)
				}
			}
			_, many := calls["parseLogLines"]
			_, one := calls["parseLogLine"]
			if (many || one) && fn.Name.Name != "parseLogLines" {
				found["lines"] = append(found["lines"], name)
				if (!unlocked && !watched) || !masked {
					t.Errorf("%s serves log lines without reading the unlock and masking with maskLogLine", name)
				}
			}
		}
	}

	want := map[string][]string{
		"terminal": {"terminal.go:handleTerminalWS"},
		"stream":   {"logs.go:StreamLogs"},
		"lines":    {"logs.go:GetLogs", "logs.go:StreamLogs"},
	}
	for k, w := range want {
		got := append([]string(nil), found[k]...)
		sort.Strings(got)
		if len(got) != len(w) {
			t.Fatalf("rule %q found %v, want %v: a handler was added, renamed or moved; extend this guard", k, got, w)
		}
		for i := range w {
			if got[i] != w[i] {
				t.Fatalf("rule %q found %v, want %v", k, got, w)
			}
		}
	}
}
