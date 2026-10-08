package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// exportedGitEntriesReaching returns the exported *GitService methods that
// reach target, directly or through other *GitService methods, in the given
// sources (file name -> content). A reference counts whether it is a call or a
// method value, so `f := s.pullCLI` is followed too.
func exportedGitEntriesReaching(t *testing.T, sources map[string]string, target string) []string {
	t.Helper()
	fset := token.NewFileSet()
	// method name -> the *GitService method names its body mentions.
	refs := map[string]map[string]bool{}
	for name, src := range sources {
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || len(fn.Recv.List) != 1 {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*ast.StarExpr); ok {
				recv = star.X
			}
			if id, ok := recv.(*ast.Ident); !ok || id.Name != "GitService" {
				continue
			}
			set := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					set[sel.Sel.Name] = true
				}
				return true
			})
			refs[fn.Name.Name] = set
		}
	}

	reaches := func(from string) bool {
		seen := map[string]bool{}
		var walk func(m string) bool
		walk = func(m string) bool {
			if seen[m] {
				return false
			}
			seen[m] = true
			for next := range refs[m] {
				if next == target {
					return true
				}
				if _, isMethod := refs[next]; isMethod && walk(next) {
					return true
				}
			}
			return false
		}
		return walk(from)
	}

	var out []string
	for m := range refs {
		if token.IsExported(m) && reaches(m) {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// TestGitServiceExportedPullEntry_OnlyPullVerifiedReachesPullCLI is
// agent-os-qags.12's guard. pullCLI rewrites files under the stack directories,
// and PullVerified is the one entry that takes the per-stack operation lock
// around it (agent-os-ai1z). GitService.Pull was a bare passthrough to pullCLI
// with no lock; it had no production caller, and the compiler would not have
// objected to one appearing. Removing it is the fix; this is what keeps it
// removed. The compiler alone would not catch a re-exported Pull: an unused
// exported method is legal Go, and the tests now call pullCLI directly, so
// none of them would go red either.
//
// Read through go:embed (packageSources), so a `go test -overlay` mutant of
// git.go is what this sees. Blind spot: a path to pullCLI through a different
// type or a func value stored in a field is not followed.
func TestGitServiceExportedPullEntry_OnlyPullVerifiedReachesPullCLI(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := packageSources.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sources[e.Name()] = string(b)
	}

	got := exportedGitEntriesReaching(t, sources, "pullCLI")
	if len(got) != 1 || got[0] != "PullVerified" {
		t.Fatalf("exported *GitService methods that reach pullCLI = %v, want exactly [PullVerified]; "+
			"pullCLI is unlocked, so every other entry is a pull that skips the stack operation lock "+
			"(agent-os-qags.12, safe-defaults rule 4)", got)
	}
}

// TestGitServiceExportedPullEntry_CheckerSeesTheShapes proves the walk above
// reports each way an unlocked entry can appear, and stays quiet on the
// shapes that must not trip it.
func TestGitServiceExportedPullEntry_CheckerSeesTheShapes(t *testing.T) {
	const head = "package services\n"
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"direct passthrough", head + "func (s *GitService) Pull(d string) { s.pullCLI(d) }\nfunc (s *GitService) pullCLI(d string) {}\n", []string{"Pull"}},
		{"value receiver", head + "func (s GitService) Pull(d string) { s.pullCLI(d) }\nfunc (s GitService) pullCLI(d string) {}\n", []string{"Pull"}},
		{"through an unexported wrapper", head + "func (s *GitService) Sync(d string) { s.pull(d) }\nfunc (s *GitService) pull(d string) { s.pullCLI(d) }\nfunc (s *GitService) pullCLI(d string) {}\n", []string{"Sync"}},
		{"method value", head + "func (s *GitService) Sync(d string) { f := s.pullCLI; f(d) }\nfunc (s *GitService) pullCLI(d string) {}\n", []string{"Sync"}},
		{"unexported only", head + "func (s *GitService) pull(d string) { s.pullCLI(d) }\nfunc (s *GitService) pullCLI(d string) {}\n", nil},
		{"other type is not followed", head + "func (o *Other) Pull(d string) { o.pullCLI(d) }\nfunc (s *GitService) pullCLI(d string) {}\n", nil},
		{"unrelated exported", head + "func (s *GitService) GetLog(d string) { s.log(d) }\nfunc (s *GitService) log(d string) {}\nfunc (s *GitService) pullCLI(d string) {}\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := exportedGitEntriesReaching(t, map[string]string{"x.go": tc.src}, "pullCLI")
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
