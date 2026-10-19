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

// contentSites is every declaration allowed to name one, and why.
var contentSites = map[string]string{
	"changeDiffArgs":  "the commit dialog's hardened patch (k81p.3)",
	"historyShowArgs": "the history view's hardened patch (agent-os-2omy)",
	"historyNameArgs": "names only, -z, no program",
	"pullCLI":         "diff --name-only: names only",
	"historyEnvRule":  "cat-file blob of a past compose file, parsed here, never sent",
	"followTreeLink":  "cat-file blob of a past symlink, parsed here, never sent",
}

// TestGitContentArgvCensus keeps every git child that can emit file contents
// on a hardened argv (agent-os-2omy). A new site fails here until it is
// routed through changeDiffArgs or historyShowArgs (or shown to print names
// only) and listed above.
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
		for _, decl := range f.Decls {
			name := declName(decl)
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil || !contentSubcommands[v] {
					return true
				}
				if _, ok := contentSites[name]; !ok {
					t.Errorf("%s: %s names git %q: route it through changeDiffArgs or historyShowArgs, or list it in contentSites with why it prints no contents",
						fset.Position(lit.Pos()), name, v)
				}
				found[name] = true
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Two-sided: every listed site must still be found, so a census that
	// matched nothing (or a stale list) can't pass.
	for name := range contentSites {
		if !found[name] {
			t.Errorf("contentSites lists %s, but it names no content subcommand any more", name)
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
}

func declName(d ast.Decl) string {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return d.Name.Name
	case *ast.GenDecl:
		for _, s := range d.Specs {
			if v, ok := s.(*ast.ValueSpec); ok && len(v.Names) > 0 {
				return v.Names[0].Name
			}
		}
	}
	return ""
}
