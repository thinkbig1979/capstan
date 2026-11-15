package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// secretListFiles are the files that read a stack's secret list from disk.
// Each read must go through readRegularFileCapped, which refuses a FIFO, a
// device or an oversized file: a bare read of a FIFO hangs a locked log
// stream (agent-os-vvmx).
var secretListFiles = []string{
	"compose_secret_values.go",
	"compose_secret_files.go",
	"runtime_secret_values.go",
}

// secretListPending are secret-list files that still read bare, each with the
// bead converting them. Not scanned until then; an entry whose file no longer
// holds a bare read fails, so it moves into secretListFiles.
var secretListPending = map[string]string{
	"redact_compose.go": "StackSecretValues: os.ReadFile and dotenv.GetEnvFromFile (agent-os-vvmx, agent-os-uf0a)",
}

// bareFileReads are the calls that read a file without the cap, by package
// and function. Blind spot (review F5): matched at the call by name, so a
// function value is not seen (`var readFile = os.ReadFile`, then
// `readFile(p)`), nor a read through any helper outside this list.
var bareFileReads = map[string]bool{
	"os.ReadFile": true, "os.Open": true, "os.OpenFile": true,
	"ioutil.ReadFile": true, "io.ReadAll": true,
	"dotenv.GetEnvFromFile": true, "dotenv.Read": true, "godotenv.Read": true,
}

// bareReadsIn returns the position of every bare file read in src outside
// readRegularFileCapped, the one function allowed to make them.
func bareReadsIn(t *testing.T, name string) []string {
	t.Helper()
	src, err := packageSources.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name == "readRegularFileCapped" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if ok && bareFileReads[pkg.Name+"."+sel.Sel.Name] {
				hits = append(hits, fset.Position(call.Pos()).String()+" "+pkg.Name+"."+sel.Sel.Name)
			}
			return true
		})
	}
	return hits
}

// TestSecretListReads_StayOnTheCappedReader_2oxy (agent-os-2oxy item 4):
// the files that build a stack's secret list read through
// readRegularFileCapped and nothing else. Sources come from go:embed
// (packageSources), so an -overlay mutant is what is parsed.
func TestSecretListReads_StayOnTheCappedReader_2oxy(t *testing.T) {
	for _, name := range secretListFiles {
		for _, hit := range bareReadsIn(t, name) {
			t.Errorf("%s: a bare file read in a secret-list file; read through readRegularFileCapped", hit)
		}
	}
	for name, reason := range secretListPending {
		if len(bareReadsIn(t, name)) == 0 {
			t.Errorf("secretListPending names %s (%s) but it holds no bare read: move it into secretListFiles", name, reason)
		}
	}
}
