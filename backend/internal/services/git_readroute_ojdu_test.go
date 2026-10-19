package services

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file pins agent-os-ojdu: a stack repository's own config made plain
// read routes run programs it names. Each test below configures one such
// program as a marker, runs every read route, and requires the marker never
// ran. Each also runs the same program from a raw git child on the same
// fixture, before and after the routes, so a marker that stayed silent because
// the fixture never reached it fails the test instead of passing it.

// ojduFix is a commit fixture whose repository config names three marker
// programs, each appending its name to marks when it runs:
//
//   - gpg.program with log.showSignature=true, over a signed HEAD commit that
//     touches x (GetLogForFile only verifies signatures on commits it lists);
//   - core.fsmonitor;
//   - a post-index-change hook under core.hooksPath.
//
// x is modified and dated a day ahead, so its index entry stays racy against
// any index timestamp: every status re-checks it and, unless told not to,
// rewrites the index, which is what runs post-index-change (the same trick as
// vwi7Repo's callers, measured there).
type ojduFix struct {
	*commitFix
	marks string
	head  string
}

func newOjduFix(t *testing.T) *ojduFix {
	t.Helper()
	f := newCommitFix(t, false)
	bin := t.TempDir()
	marks := filepath.Join(bin, "marks")
	script := func(name, body string) string {
		p := filepath.Join(bin, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		//nolint:gosec // a program git runs must be executable; test-owned file under t.TempDir()
		require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755))
		return p
	}
	mark := func(name string) string { return "echo " + name + " >> '" + marks + "'\n" }

	// A stand-in signer: git stores whatever it prints as the signature.
	signer := script("signer", "cat >/dev/null\n"+
		"echo '-----BEGIN PGP SIGNATURE-----'\necho 'AAAA'\necho '-----END PGP SIGNATURE-----'\n"+
		"echo '[GNUPG:] SIG_CREATED D 1 8 00 1 X' >&2\n")
	writeFile(t, filepath.Join(f.top, "x"), "signed\n")
	runGit(t, f.top, "-c", "gpg.program="+signer, "-c", "user.signingkey=x", "commit", "-q", "-S", "-am", "signed")

	runGit(t, f.top, "config", "log.showSignature", "true")
	runGit(t, f.top, "config", "gpg.program", script("gpg", mark("gpg")+"exit 1\n"))
	runGit(t, f.top, "config", "core.fsmonitor", script("fsmonitor", mark("fsmonitor")+"exit 1\n"))
	script("hooks/post-index-change", mark("post-index-change"))
	runGit(t, f.top, "config", "core.hooksPath", filepath.Join(bin, "hooks"))

	writeFile(t, filepath.Join(f.top, "x"), "modified\n")
	future := time.Now().Add(24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(f.top, "x"), future, future))

	return &ojduFix{commitFix: f, marks: marks, head: strings.TrimSpace(runGit(t, f.top, "rev-parse", "HEAD"))}
}

// ran reports how many times program ran since the last call, and resets.
func (f *ojduFix) ran(t *testing.T, program string) int {
	t.Helper()
	b, err := os.ReadFile(f.marks)
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	require.NoError(t, os.Remove(f.marks))
	n := 0
	for _, line := range strings.Split(string(b), "\n") {
		if line == program {
			n++
		}
	}
	return n
}

// ojduReadRoutes are the service calls behind the read routes in
// handlers/git.go: GET /git (GetStatus), /git/log (GetLog, GetLogForFile),
// /git/diff/:hash (GetDiff), /git/changes (GetChanges) and /git/changes/diff
// (GetChangeDiff).
func ojduReadRoutes(f *ojduFix) []struct {
	name string
	call func() error
} {
	return []struct {
		name string
		call func() error
	}{
		{"GetStatus", func() error { _, err := f.svc.GetStatus(f.dir); return err }},
		{"GetLog", func() error { _, err := f.svc.GetLog(f.dir, 10, 0); return err }},
		{"GetLogForFile", func() error { _, err := f.svc.GetLogForFile(f.dir, "x", 10); return err }},
		{"GetDiff", func() error { _, err := f.svc.GetDiff(f.dir, f.head); return err }},
		{"GetChanges", func() error { _, err := f.svc.GetChanges(f.dir); return err }},
		{"GetChangeDiff", func() error { _, err := f.svc.GetChangeDiff(f.dir, "x"); return err }},
	}
}

// ojduRequireNoRouteRuns runs every read route and requires program never ran,
// bracketed by a raw git child (control, without Capstan's argv prefix) that
// must run it, before and after.
func ojduRequireNoRouteRuns(t *testing.T, program string, control ...string) {
	f := newOjduFix(t)
	controlRuns := func(when string) {
		t.Helper()
		// The marker exits 1; whether git then fails is not the question.
		if out, err := gitTry(t, f.top, control...); err != nil {
			t.Logf("control (%s): raw git exited non-zero: %v\n%s", when, err, out)
		}
		require.Positive(t, f.ran(t, program), "control (%s): raw `git %s` did not run %s on this fixture, so a silent route proves nothing",
			when, strings.Join(control, " "), program)
	}
	controlRuns("before the routes")
	for _, r := range ojduReadRoutes(f) {
		require.NoError(t, r.call(), r.name)
		if n := f.ran(t, program); n != 0 {
			t.Errorf("%s ran the repository's %s %d time(s)", r.name, program, n)
		}
	}
	controlRuns("after the routes")
}

// TestOjduReadRoutesRunNoGPGProgram: log.showSignature=false in the prefix.
func TestOjduReadRoutesRunNoGPGProgram(t *testing.T) {
	ojduRequireNoRouteRuns(t, "gpg", "log", "-1", "--format=%s", "--", "x")
}

// TestOjduReadRoutesRunNoFSMonitor: core.fsmonitor=false in the prefix.
func TestOjduReadRoutesRunNoFSMonitor(t *testing.T) {
	ojduRequireNoRouteRuns(t, "fsmonitor", "status", "--porcelain")
}

// TestOjduReadRoutesRunNoPostIndexChangeHook: --no-optional-locks in the prefix.
func TestOjduReadRoutesRunNoPostIndexChangeHook(t *testing.T) {
	ojduRequireNoRouteRuns(t, "post-index-change", "status", "--porcelain")
}

// TestOjduGitCmdWithCredsArgvPrefix pins the argv every git child starts with,
// with and without a credential. The literals are spelled out rather than read
// from gitHardeningArgs, so emptying that slice fails here too.
func TestOjduGitCmdWithCredsArgvPrefix(t *testing.T) {
	svc := NewGitService(nil, nil)
	dir := t.TempDir()
	want := []string{
		"-c", "safe.directory=" + dir,
		"-c", "core.fsmonitor=false",
		"-c", "log.showSignature=false",
		"--no-optional-locks",
	}
	for _, tok := range []string{"", "a-token"} {
		cmd, _ := svc.gitCmdWithCreds(context.Background(), dir, "u", tok, "status", "--porcelain")
		args := cmd.Args[1:]
		require.GreaterOrEqual(t, len(args), len(want)+2, "token %q: argv %q", tok, args)
		require.Equal(t, want, args[:len(want)], "token %q: argv %q", tok, args)
		require.Equal(t, []string{"status", "--porcelain"}, args[len(args)-2:], "token %q: the caller's arguments must come last", tok)
	}
}

// TestOjduGitBinaryNamedOnlyInGitCmdWithCreds keeps every git child in package
// services on gitCmdWithCreds, and so on its prefix: the string literal "git"
// (or a path ending in /git) may appear in non-test code only inside that
// function. It reads the embedded sources, so a go test -overlay mutant is what
// it checks.
//
// What it cannot see: a binary name assembled at run time ("gi"+"t", a value
// read from config), `sh -c "git ..."`, and any other package. Outside package
// services, forbidigo already forces execx.Command, and no other package runs
// git today (agent-os-ojdu's sweep).
func TestOjduGitBinaryNamedOnlyInGitCmdWithCreds(t *testing.T) {
	files, err := fs.Glob(serviceSources, "*.go")
	require.NoError(t, err)
	var outside []string
	inHelper := 0
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := serviceSources.ReadFile(name)
		require.NoError(t, err)
		file, err := parser.ParseFile(fset, name, src, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			fn, _ := decl.(*ast.FuncDecl)
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil || (v != "git" && !strings.HasSuffix(v, "/git")) {
					return true
				}
				if fn != nil && fn.Recv != nil && fn.Name.Name == "gitCmdWithCreds" {
					inHelper++
				} else {
					outside = append(outside, fset.Position(lit.Pos()).String())
				}
				return true
			})
		}
	}
	require.Empty(t, outside, "a git binary named outside gitCmdWithCreds skips its hardened argv prefix")
	require.Equal(t, 1, inHelper, "gitCmdWithCreds no longer names the git binary as a literal; this census cannot see where git children come from")
}
