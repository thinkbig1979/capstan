package services

import (
	"embed"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The package's own sources, embedded so a go test -overlay mutant is what
// this test reads (a test reading from disk would pass against the mutant).
//
//go:embed *.go
var serviceSources embed.FS

// contentSubcommands are git subcommands that can print file contents, or run
// a textconv or diff program to do it.
var contentSubcommands = map[string]bool{
	"show": true, "diff": true, "diff-tree": true, "diff-index": true, "diff-files": true,
	"format-patch": true, "blame": true, "annotate": true, "archive": true, "whatchanged": true,
	"range-diff": true, "cat-file": true, "grep": true, "stash": true,
}

// logSubcommands print a patch, with textconv, when given a patch flag.
var logSubcommands = map[string]bool{"log": true, "reflog": true}

func isPatchFlag(a string) bool {
	switch a {
	case "-p", "--patch", "-u", "--cc", "--patch-with-raw", "--patch-with-stat":
		return true
	}
	return strings.HasPrefix(a, "-L") || strings.HasPrefix(a, "-U") || strings.HasPrefix(a, "--unified")
}

// contentSites pins every argument list in package services that names a
// content subcommand: the declaration it sits in, and the exact string
// literals of the list, in order. An edit inside an allowed declaration (a
// cat-file that is no longer `blob`, a flag dropped from a patch argv) fails
// here as surely as a new site does.
var contentSites = map[string][]string{
	// The commit dialog's hardened patch (k81p.3).
	"changeDiffArgs": {`-c core.fsmonitor=false -c core.quotePath=false --no-optional-locks -c diff.submodule=short diff HEAD --no-ext-diff --no-textconv --no-color --submodule=short --src-prefix=a/ --dst-prefix=b/ --no-relative --`},
	// The history view's hardened patch (agent-os-2omy).
	"historyShowArgs": {`-c core.fsmonitor=false -c core.quotePath=false --no-optional-locks -c diff.submodule=short -c log.showSignature=false show --format= --no-show-signature --no-ext-diff --no-textconv --no-color --submodule=short --src-prefix=a/ --dst-prefix=b/ --no-relative`},
	// Names only, -z, no program.
	"historyNameArgs": {`-c core.fsmonitor=false -c core.quotePath=false --no-optional-locks diff-tree -z -r --root -m -M --name-status --no-commit-id --no-ext-diff --no-textconv`},
	// Names only.
	"pullCLI": {`diff --name-only`},
	// A blob's raw bytes (past compose files and link targets), parsed here
	// and never sent.
	"catBlob": {`-c core.fsmonitor=false cat-file blob`},
}

// TestGitContentArgvCensus keeps every git child that can emit file contents
// on a hardened, pinned argv (agent-os-2omy): a content subcommand anywhere
// but contentSites fails, an allowed site whose literals change fails, and a
// log or reflog given a patch flag fails.
//
// Accepted limits: it reads string literals in package services, so a
// subcommand built at runtime ("sh"+"ow", a variable) and a git child in
// another package are not seen. Every git child lives in this package today.
func TestGitContentArgvCensus(t *testing.T) {
	found := map[string]bool{}
	fset := token.NewFileSet()
	err := fs.WalkDir(serviceSources, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") {
			return err
		}
		src, err := serviceSources.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			return err
		}
		for _, u := range namedUnits(f) {
			ast.Inspect(u.node, func(n ast.Node) bool {
				var list []ast.Expr
				switch n := n.(type) {
				case *ast.CallExpr:
					list = n.Args
				case *ast.CompositeLit:
					list = n.Elts
				default:
					return true
				}
				lits := stringLits(list)
				content, log, patch := false, false, false
				for _, l := range lits {
					content = content || contentSubcommands[l]
					log = log || logSubcommands[l]
					patch = patch || isPatchFlag(l)
				}
				pos := fset.Position(n.Pos())
				if log && patch {
					t.Errorf("%s: %s runs git log with a patch flag (%q): a patch runs textconv; build it on historyShowArgs instead", pos, u.name, lits)
				}
				if !content {
					return true
				}
				got := strings.Join(lits, " ")
				want, ok := contentSites[u.name]
				switch {
				case !ok:
					t.Errorf("%s: %s names a content subcommand (%q): route it through changeDiffArgs or historyShowArgs, or pin it in contentSites with why it prints no contents", pos, u.name, lits)
				case !slices.Contains(want, got):
					t.Errorf("%s: %s's git argv changed:\n got  %s\n want one of %q", pos, u.name, got, want)
				default:
					found[u.name+"\x00"+got] = true
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Two-sided: every pinned argv must still be found, so a census that
	// matched nothing (or a stale pin) can't pass.
	for name, argvs := range contentSites {
		for _, a := range argvs {
			if !found[name+"\x00"+a] {
				t.Errorf("contentSites pins %s %q, but no such argv exists any more", name, a)
			}
		}
	}
}

// TestGitPatchArgsHardened: both patch argvs pin what repository config could
// otherwise turn into a program run or a submodule's contents.
func TestGitPatchArgsHardened(t *testing.T) {
	for name, args := range map[string][]string{"changeDiffArgs": changeDiffArgs, "historyShowArgs": historyShowArgs} {
		for _, want := range []string{"--no-ext-diff", "--no-textconv", "--no-color", "--submodule=short", "diff.submodule=short", "core.fsmonitor=false"} {
			if !slices.Contains(args, want) {
				t.Errorf("%s lacks %s", name, want)
			}
		}
	}
	for _, want := range []string{"--no-show-signature", "log.showSignature=false"} {
		if !slices.Contains(historyShowArgs, want) {
			t.Errorf("historyShowArgs lacks %s", want)
		}
	}
	if !slices.Contains(showEnv, "GIT_NO_LAZY_FETCH=1") || !slices.Contains(noLazyFetchEnv, "GIT_NO_LAZY_FETCH=1") {
		t.Errorf("the history view's children must run with GIT_NO_LAZY_FETCH=1")
	}
}

// namedUnit is a top-level function or one var/const spec, by name: each spec
// of a grouped `var (...)` block is its own unit, so a new var in a group led
// by an allowed name gets no pass from it.
type namedUnit struct {
	name string
	node ast.Node
}

func namedUnits(f *ast.File) []namedUnit {
	var out []namedUnit
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			out = append(out, namedUnit{d.Name.Name, d})
		case *ast.GenDecl:
			for _, s := range d.Specs {
				if v, ok := s.(*ast.ValueSpec); ok && len(v.Names) > 0 {
					out = append(out, namedUnit{v.Names[0].Name, v})
				}
			}
		}
	}
	return out
}

func stringLits(list []ast.Expr) []string {
	var out []string
	for _, e := range list {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if v, err := strconv.Unquote(lit.Value); err == nil {
			out = append(out, v)
		}
	}
	return out
}
