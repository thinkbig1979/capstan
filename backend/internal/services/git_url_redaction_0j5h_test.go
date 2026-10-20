package services

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// TestPull0j5h_ShortTokenDoesNotFlipTheURLClass is agent-os-0j5h on the pull
// side: the ssh-or-not check read `ls-remote --get-url` AFTER redactToken
// had replaced the token with "***". A token that occurs in the URL changed
// its shape: "http" turns http://h:1/ into "***://h:1/", "s" turns https://
// into "http***://", both scp-like and so refused as ssh; ":o" turns
// git@h:o/r.git into "git@h***/r.git", no longer ssh, so the folder's https
// auth was bypassed and core.sshCommand ran.
func TestPull0j5h_ShortTokenDoesNotFlipTheURLClass(t *testing.T) {
	for name, tc := range map[string]struct{ token, url string }{
		`token "http" in an http url`: {"http", "http://127.0.0.1:1/o/r.git"},
		`token "s" in an https url`:   {"s", "https://127.0.0.1:1/o/r.git"},
	} {
		t.Run(name+" still reaches the network", func(t *testing.T) {
			f := pullFix(t)
			require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "0j5h-user", tc.token))
			runGit(t, f.top, "remote", "set-url", "origin", tc.url)
			_, err := f.svc.pullCLI(f.dir)
			var ae *models.AppError
			require.ErrorAs(t, err, &ae)
			assert.Equal(t, models.ErrGitRemoteUnreachable, ae.Code, "an https remote was classified as ssh")
		})
	}
	t.Run(`token ":o" in an scp-like url is still refused as ssh`, func(t *testing.T) {
		f := pullFix(t)
		require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "0j5h-user", ":o"))
		runGit(t, f.top, "remote", "set-url", "origin", "git@example.invalid:o/r.git")
		record := rde7Ssh(t, f)
		before := f.head(t)

		_, err := f.svc.pullCLI(f.dir)

		requireSSHCode(t, err, models.ErrGitSSHUnsupported)
		assert.Empty(t, rde7Ran(t, record), "an ssh child ran for an https-auth folder")
		assert.Equal(t, before, f.head(t))
	})
}

// TestPush0j5h_ShortTokenDoesNotFlipTheURLClass is the push side, the same
// read in pushPreconditions: a refused https URL, and an scp-like ssh URL that
// read as a local path and was allowed.
func TestPush0j5h_ShortTokenDoesNotFlipTheURLClass(t *testing.T) {
	for name, tc := range map[string]struct{ token, url, code string }{
		`token "s" in an https url is allowed`:   {"s", "https://example.invalid/o/r.git", ""},
		`token "http" in an http url is allowed`: {"http", "http://example.invalid/o/r.git", ""},
		`token ":o" in an scp-like url is ssh`:   {":o", "git@example.invalid:o/r.git", models.ErrGitSSHUnsupported},
	} {
		t.Run(name, func(t *testing.T) {
			f := newCommitFix(t, false)
			require.NoError(t, f.db.UpdateDirectoryCredentials(f.dir, "https", "", "0j5h-user", tc.token))
			runGit(t, f.top, "remote", "set-url", "origin", tc.url)
			cred := f.svc.gitCredentials(f.dir)
			require.Equal(t, tc.token, cred.token, "the fixture's token did not reach the credentials")

			_, err := f.svc.pushPreconditions(f.top, "main", cred)

			if tc.code == "" {
				assert.NoError(t, err)
				return
			}
			requireSSHCode(t, err, tc.code)
		})
	}
}

// TestGitCommandUnredactedCallers0j5h keeps the unredacted read on its two
// URL classifications. Its output can hold the token, so every caller is a
// place a credential could reach a message or a log; a new caller fails here
// and has to be added deliberately. Read through go:embed (packageSources),
// so a `go test -overlay` mutant reaches it. A method value
// (`f := s.gitCommandUnredacted`) counts as a caller; a call through a
// function value obtained some other way is not seen.
func TestGitCommandUnredactedCallers0j5h(t *testing.T) {
	entries, err := packageSources.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	var callers []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := packageSources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		// Every declaration, not only function bodies: a package-level
		// `var x = func(...) { ... }` caller is named "<var x>".
		for _, d := range file.Decls {
			ast.Inspect(d, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "gitCommandUnredacted" {
					callers = append(callers, declName(d))
				}
				return true
			})
		}
	}
	sort.Strings(callers)
	assert.Equal(t, []string{"gitCommandWithCreds", "pushPreconditions", "sshPullPrecheck"}, callers,
		"gitCommandUnredacted returns the token unmasked: use gitCommandWithCreds unless the value only classifies a URL and never reaches a message or a log")
}

// declName names a top-level declaration for the caller list: the function,
// or "<var a,b>" for the names a var/const declaration binds.
func declName(d ast.Decl) string {
	if fn, ok := d.(*ast.FuncDecl); ok {
		return fn.Name.Name
	}
	var names []string
	if g, ok := d.(*ast.GenDecl); ok {
		for _, spec := range g.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, id := range vs.Names {
					names = append(names, id.Name)
				}
			}
		}
	}
	return "<var " + strings.Join(names, ",") + ">"
}
