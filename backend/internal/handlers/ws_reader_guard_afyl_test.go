package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// wsReaderCalls are the calls that read from a WebSocket. A handler that makes
// none notices a departed client only when a write fails, so on a quiet stream
// its per-user slot stays counted until restart (agent-os-afyl).
var wsReaderCalls = map[string]bool{
	"ReadMessage": true,
	"ReadJSON":    true,
	"readJSON":    true,
	"NextReader":  true,
	"watchPeer":   true,
}

// TestEveryServeWSCallerStartsAReader: every function in this package that
// calls serveWS also starts a reader (in its own body or a closure inside it).
// A function means the innermost FuncLit or FuncDecl holding the serveWS call.
//
// Blind spots, where it passes SILENTLY although no reader runs: the check is
// by name and by syntax, not by type or reachability. (1) A same-named method
// on an unrelated type (any `.ReadMessage(` or `.watchPeer(` counts). (2) Dead
// code, e.g. `if false { watchPeer(conn, cancel) }`. (3) A call to a package
// func whose name is in wsReaderCalls but whose body reads nothing; and a
// package func counts as a reader if ANY call in it is reader-named, read or
// not. It fails LOUDLY (a false flag) on a reader two named calls deep or one
// passed in as a func value.
func TestEveryServeWSCallerStartsAReader(t *testing.T) {
	// handlerSources is the package's go:embed of *.go, not a disk read, so
	// a `go test -overlay` mutant of a handler file is what this parses.
	entries, err := handlerSources.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := handlerSources.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}

	// A package function or method whose own body reads counts as a reader
	// too, so a handler may start one by name (terminal.go's
	// readFromWebSocket). One level: that is the depth every caller uses.
	readers := map[string]bool{}
	for k := range wsReaderCalls {
		readers[k] = true
	}
	for _, f := range files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Body != nil && callsAReader(fd.Body, wsReaderCalls) {
				readers[fd.Name.Name] = true
			}
		}
	}

	callers := map[string]bool{} // key -> has a reader
	for name, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "serveWS" {
				continue
			}
			for _, fn := range serveWSCallingFuncs(fd) {
				key := name + ":" + fd.Name.Name
				callers[key] = callers[key] || callsAReader(fn, readers)
			}
		}
	}

	// Positive control on the instrument: the eight serveWS callers known on
	// agent-os-afyl's base must all be found, or a parse change made it blind.
	if len(callers) < 8 {
		t.Fatalf("found %d serveWS callers, want at least 8: %v", len(callers), callers)
	}

	// No exceptions. A handler that writes on a timer still needs a reader:
	// a failed write notices an abrupt or clean close, but a half-open peer
	// makes no write fail until TCP gives up, so its slot stays counted that
	// long (agent-os-q48p removed the last two exceptions).
	var missing []string
	for key, hasReader := range callers {
		if !hasReader {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		t.Errorf("%s calls serveWS but starts no reader (ReadMessage, readJSON, NextReader or watchPeer): "+
			"a client that leaves without the handler writing keeps its per-user slot until restart, "+
			"and a half-open one until TCP gives up", key)
	}
}

// serveWSCallingFuncs returns the innermost function bodies (the FuncDecl
// itself or a FuncLit inside it) that call serveWS.
func serveWSCallingFuncs(fd *ast.FuncDecl) []ast.Node {
	var found []ast.Node
	var stack []ast.Node
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return false
		}
		stack = append(stack, n)
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "serveWS" {
				for i := len(stack) - 1; i >= 0; i-- {
					switch stack[i].(type) {
					case *ast.FuncLit, *ast.FuncDecl:
						found = append(found, stack[i])
						i = -1
					}
				}
			}
		}
		return true
	})
	return found
}

func callsAReader(fn ast.Node, readers map[string]bool) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			found = found || readers[f.Name]
		case *ast.SelectorExpr:
			found = found || readers[f.Sel.Name]
		}
		return !found
	})
	return found
}
