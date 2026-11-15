package handlers

import (
	_ "embed"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"strings"
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

// Blind spot (review F5): the shapes below are matched at the call, by name,
// so a function value is not seen: `read := h.docker.Logs; read(...)`, or a
// method expression passed elsewhere and called there.
//
// containerTextSource reports what a call serves when it reaches a container's
// own text: "logs" for its log output, "shell" for a terminal, or "". The
// shapes are a selector call named Logs (services Logs), a terminal service's
// CreateSession (h.terminal.CreateSession, not the database's session row),
// and any call passing the string literal "logs", "exec" or "attach" (a
// compose or docker subcommand).
func containerTextSource(call *ast.CallExpr) string {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if sel.Sel.Name == "Logs" {
			return "logs"
		}
		if x, ok := sel.X.(*ast.SelectorExpr); ok && x.Sel.Name == "terminal" && sel.Sel.Name == "CreateSession" {
			return "shell"
		}
	}
	for _, a := range call.Args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		switch lit.Value {
		case `"logs"`:
			return "logs"
		case `"exec"`, `"attach"`:
			return "shell"
		}
	}
	return ""
}

// containerTextHelpers are functions that reach a container's text only to
// hand it to their callers, each with its reason. Every function that calls
// one is held to the rule in its place.
var containerTextHelpers = map[string]string{
	"logs.go:buildLogsCmd": "builds the `compose logs -f` command StreamLogs runs",
}

// calledNames is every called function or method name in body (closures
// included), by its last identifier.
func calledNames(body ast.Node) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			names[f.Name] = true
		case *ast.SelectorExpr:
			names[f.Sel.Name] = true
		}
		return true
	})
	return names
}

// TestEnvLockGuard_PackageWideContainerText_2oxy is the package-wide form of
// the guard above (agent-os-2oxy item 1): ANY handlers file, a new one
// included, whose function serves a container's log output or a shell must
// read the unlock (envUnlocked or newUnlockWatch), and log output must also
// be masked with maskLogLine. Sources come from go:embed (handlerSources4hxa),
// which sees an -overlay file added to the package.
func TestEnvLockGuard_PackageWideContainerText_2oxy(t *testing.T) {
	type fnInfo struct {
		key    string
		body   *ast.BlockStmt
		source string
	}
	var fns []fnInfo
	entries, err := handlerSources4hxa.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := handlerSources4hxa.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, e.Name(), src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			info := fnInfo{key: e.Name() + ":" + fn.Name.Name, body: fn.Body}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && info.source != "logs" {
					if s := containerTextSource(call); s != "" {
						info.source = s
					}
				}
				return true
			})
			fns = append(fns, info)
		}
	}

	helperNames := map[string]string{}
	for key := range containerTextHelpers {
		_, name, _ := strings.Cut(key, ":")
		helperNames[name] = key
	}
	var served []string
	usedHelper := map[string]bool{}
	check := func(key string, names map[string]bool, source string) {
		served = append(served, key)
		if !names["envUnlocked"] && !names["newUnlockWatch"] {
			t.Errorf("%s serves a container's %s without reading the env unlock (envUnlocked or newUnlockWatch): a locked session would see its env values", key, source)
		}
		if source == "logs" && !names["maskLogLine"] {
			t.Errorf("%s serves a container's log output without masking it with maskLogLine", key)
		}
	}
	for _, fn := range fns {
		names := calledNames(fn.body)
		if fn.source != "" {
			if _, ok := containerTextHelpers[fn.key]; ok {
				usedHelper[fn.key] = true
			} else {
				check(fn.key, names, fn.source)
			}
		}
		for name, key := range helperNames {
			if names[name] && fn.key != key {
				check(fn.key, names, "logs")
			}
		}
	}
	for key := range containerTextHelpers {
		if !usedHelper[key] {
			t.Errorf("containerTextHelpers names %s but it reaches no container text; remove the stale entry", key)
		}
	}
	// Not vacuous: every handler that serves container text today is found.
	sort.Strings(served)
	for _, want := range []string{"logs.go:GetLogs", "logs.go:StreamLogs", "terminal.go:handleTerminalWS"} {
		if !slices.Contains(served, want) {
			t.Errorf("the guard no longer finds %s (found %v): its shapes went blind", want, served)
		}
	}
}

// lockedListAllowCompose names the functions that may read
// ComposeSecretValues while masking container text, each with its reason.
var lockedListAllowCompose = map[string]string{
	"logs.go:StreamLogs": "its unlocked arm reads the file list; the locked arm reads LockedSecretValues",
}

// TestEnvLockGuard_LockedViewsMaskWithLockedSecretValues_2oxy (agent-os-2oxy
// item 3): a function that masks container text for a locked session
// (maskLogLine, or inspectSecrets in resources.go) must take its list from
// LockedSecretValues, which adds the containers' own values to the file
// list. ComposeSecretValues there would show a value set only in the running
// container.
func TestEnvLockGuard_LockedViewsMaskWithLockedSecretValues_2oxy(t *testing.T) {
	entries, err := handlerSources4hxa.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var found []string
	used := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := handlerSources4hxa.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, e.Name(), src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == "maskLogLine" {
				continue
			}
			key := e.Name() + ":" + fn.Name.Name
			names := calledNames(fn.Body)
			if !names["maskLogLine"] && fn.Name.Name != "inspectSecrets" {
				continue
			}
			found = append(found, key)
			if !names["LockedSecretValues"] {
				t.Errorf("%s masks container text without LockedSecretValues", key)
			}
			if names["ComposeSecretValues"] {
				if _, ok := lockedListAllowCompose[key]; ok {
					used[key] = true
				} else {
					t.Errorf("%s masks container text but reads ComposeSecretValues, which lacks the containers' own values: use LockedSecretValues", key)
				}
			}
		}
	}
	for key := range lockedListAllowCompose {
		if !used[key] {
			t.Errorf("lockedListAllowCompose names %s but it no longer reads ComposeSecretValues; remove the stale entry", key)
		}
	}
	sort.Strings(found)
	want := []string{"logs.go:GetLogs", "logs.go:StreamLogs", "resources.go:inspectSecrets"}
	if !slices.Equal(found, want) {
		t.Fatalf("found %v, want %v: a masking function was added, renamed or moved; extend this guard", found, want)
	}
}
