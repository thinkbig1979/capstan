package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// PARITY CHARACTERIZATION (agent-os-yo9e).
//
// services/git.go carries two complete git-status implementations: getStatusGoGit
// (the go-git library) and getStatusCLI (shelling out). GetStatus runs the first
// and falls back to the second. The question this file answers is whether the two
// produce the SAME *models.GitStatusResult, because that is the precondition for
// deleting either one.
//
// MEASURED on accc6d7: they do not. Fourteen of fifteen fixture states differ in
// at least one field, and the divergence is BIDIRECTIONAL — neither path is a
// superset of the other:
//
//   - TrackingBranch is populated only by go-git (getStatusCLI never assigns the
//     field). Deleting go-git empties it for every stack, which is the second
//     symptom of agent-os-r1a; TestGetStatus_PackedRepoPopulatesTrackingBranch
//     exists to catch exactly that reinstatement.
//   - RemoteURL is populated only by the CLI when no refs/remotes/<remote>/<branch>
//     exists, because getDivergence returns early and drops the URL with it.
//   - Ahead/Behind: go-git hardcodes origin/<same-branch-name> as the comparison
//     ref; the CLI asks git for @{upstream}. A branch tracking a differently-named
//     upstream gets 0/0 from go-git and the true counts from the CLI.
//   - DirtyCount: go-git counts FILES, `git status --porcelain` counts ENTRIES, and
//     an untracked directory is one entry covering many files.
//   - A linked worktree (.git is a file) cannot be opened by go-git at all.
//
// This test pins that divergence set rather than asserting parity. It fails if a
// field moves in EITHER direction, so it is equally a guard on "someone deleted a
// path assuming equivalence" and on "someone closed one of these gaps without
// recording it".
//
// Every fixture asserts it actually reached the state it claims before the
// comparison runs — a corpus of identical clean repos would show perfect parity
// and mean nothing.

func yo9eRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	//nolint:gosec // test helper, explicit argv
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.invalid",
		"GIT_AUTHOR_DATE=2026-01-02T03:04:05+01:00",
		"GIT_COMMITTER_DATE=2026-01-02T03:04:05+01:00",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func yo9eTry(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	//nolint:gosec // test helper, explicit argv
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		code = -1
	}
	return strings.TrimSpace(string(out)), code
}

// yo9eWrite writes a file and returns nothing; failure is fatal.
func yo9eWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// yo9eSeeded builds a bare remote plus a clone of it with two commits on main.
func yo9eSeeded(t *testing.T) (local, remote, base string) {
	t.Helper()
	base = t.TempDir()
	remote = filepath.Join(base, "remote.git")
	seed := filepath.Join(base, "seed")
	local = filepath.Join(base, "local")

	yo9eRun(t, base, "init", "--bare", "--initial-branch=main", remote)
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	yo9eRun(t, seed, "init", "--initial-branch=main")
	for _, msg := range []string{"first", "second"} {
		yo9eWrite(t, filepath.Join(seed, "compose.yaml"), "# "+msg+"\n")
		yo9eRun(t, seed, "add", ".")
		yo9eRun(t, seed, "commit", "-m", msg)
	}
	yo9eRun(t, seed, "remote", "add", "origin", remote)
	yo9eRun(t, seed, "push", "-u", "origin", "main")
	yo9eRun(t, base, "clone", "--branch", "main", remote, local)
	return local, remote, base
}

// pushMoreToRemote adds n commits to the remote via the seed clone.
func yo9ePushMore(t *testing.T, base, remote string, n int) {
	t.Helper()
	seed := filepath.Join(base, "seed")
	for i := 0; i < n; i++ {
		yo9eWrite(t, filepath.Join(seed, "compose.yaml"), fmt.Sprintf("# upstream %d\n", i))
		yo9eRun(t, seed, "add", ".")
		yo9eRun(t, seed, "commit", "-m", fmt.Sprintf("upstream %d", i))
	}
	yo9eRun(t, seed, "push", "origin", "main")
	_ = remote
}

type yo9eCase struct {
	name string
	// build returns the directory to run GetStatus against.
	build func(t *testing.T) string
	// assert proves the fixture reached the intended state. It must fail loudly.
	assert func(t *testing.T, dir string)
	// wantDiff is the set of *field names* expected to differ, sorted. An empty
	// slice asserts full field-by-field parity for that state — case 10 is the
	// arm that proves this instrument can still report IDENTICAL.
	wantDiff []string
	// extra runs value-level assertions where the direction of the divergence,
	// not just its existence, is the finding.
	extra func(t *testing.T, goRes, cliRes *models.GitStatusResult)
}

// yo9eDiff returns the sorted names of the fields that differ, plus a
// human-readable detail line per difference. An implementation that errors where
// the other succeeds is reported under the pseudo-field GOGIT_ERROR / CLI_ERROR;
// both erroring is BOTH_ERROR.
func yo9eDiff(a, b *models.GitStatusResult, aErr, bErr error) (fields, detail []string) {
	switch {
	case aErr != nil && bErr != nil:
		return []string{"BOTH_ERROR"}, []string{fmt.Sprintf("BOTH ERROR: go-git=%q cli=%q", aErr, bErr)}
	case aErr != nil:
		return []string{"GOGIT_ERROR"}, []string{fmt.Sprintf("go-git ERROR %q; cli OK (branch=%q ahead=%d behind=%d)", aErr, b.Branch, b.Ahead, b.Behind)}
	case bErr != nil:
		return []string{"CLI_ERROR"}, []string{fmt.Sprintf("cli ERROR %q; go-git OK (branch=%q ahead=%d behind=%d)", bErr, a.Branch, a.Ahead, a.Behind)}
	}
	cmp := func(field string, x, y interface{}) {
		if fmt.Sprint(x) != fmt.Sprint(y) {
			fields = append(fields, field)
			detail = append(detail, fmt.Sprintf("%s: go-git=%q cli=%q", field, fmt.Sprint(x), fmt.Sprint(y)))
		}
	}
	cmp("Branch", a.Branch, b.Branch)
	cmp("Dirty", a.Dirty, b.Dirty)
	cmp("DirtyCount", a.DirtyCount, b.DirtyCount)
	cmp("Ahead", a.Ahead, b.Ahead)
	cmp("Behind", a.Behind, b.Behind)
	cmp("RemoteURL", a.RemoteURL, b.RemoteURL)
	cmp("TrackingBranch", a.TrackingBranch, b.TrackingBranch)
	switch {
	case a.Commit == nil && b.Commit == nil:
	case a.Commit == nil || b.Commit == nil:
		fields = append(fields, "Commit")
		detail = append(detail, fmt.Sprintf("Commit: go-git nil=%v cli nil=%v", a.Commit == nil, b.Commit == nil))
	default:
		cmp("Commit.Hash", a.Commit.Hash, b.Commit.Hash)
		cmp("Commit.Short", a.Commit.Short, b.Commit.Short)
		cmp("Commit.Author", a.Commit.Author, b.Commit.Author)
		cmp("Commit.Email", a.Commit.Email, b.Commit.Email)
		cmp("Commit.Message", a.Commit.Message, b.Commit.Message)
		cmp("Commit.Date", a.Commit.Date, b.Commit.Date)
	}
	sort.Strings(fields)
	return fields, detail
}

func TestYo9eGoGitVsCLIParity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	svc := NewGitService(&config.Config{}, nil)

	cases := []yo9eCase{
		{
			name:     "01 clean clone (loose+packed mix)",
			wantDiff: []string{"TrackingBranch"},
			build:    func(t *testing.T) string { l, _, _ := yo9eSeeded(t); return l },
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "status", "--porcelain"); out != "" {
					t.Fatalf("fixture not clean: %q", out)
				}
			},
		},
		{
			name:     "02 dirty: one modified tracked file",
			wantDiff: []string{"TrackingBranch"},
			build: func(t *testing.T) string {
				l, _, _ := yo9eSeeded(t)
				yo9eWrite(t, filepath.Join(l, "compose.yaml"), "# modified\n")
				return l
			},
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "status", "--porcelain"); !strings.Contains(out, "M compose.yaml") {
					t.Fatalf("fixture not modified-dirty: %q", out)
				}
			},
		},
		{
			name:     "03 dirty: untracked DIRECTORY of 3 files",
			wantDiff: []string{"DirtyCount", "TrackingBranch"},
			extra: func(t *testing.T, goRes, cliRes *models.GitStatusResult) {
				// The direction matters: go-git counts the three FILES, porcelain
				// reports the one untracked DIRECTORY entry.
				if goRes.DirtyCount != 3 || cliRes.DirtyCount != 1 {
					t.Errorf("DirtyCount go-git=%d cli=%d, want 3 and 1", goRes.DirtyCount, cliRes.DirtyCount)
				}
			},
			build: func(t *testing.T) string {
				l, _, _ := yo9eSeeded(t)
				for _, n := range []string{"a", "b", "c"} {
					yo9eWrite(t, filepath.Join(l, "newdir", n+".yaml"), "x\n")
				}
				return l
			},
			assert: func(t *testing.T, dir string) {
				out := yo9eRun(t, dir, "status", "--porcelain")
				if out != "?? newdir/" {
					t.Fatalf("fixture is not a single-line untracked directory: %q", out)
				}
			},
		},
		{
			name:     "04 staged only (index differs, worktree clean vs index)",
			wantDiff: []string{"TrackingBranch"},
			build: func(t *testing.T) string {
				l, _, _ := yo9eSeeded(t)
				yo9eWrite(t, filepath.Join(l, "staged.yaml"), "s\n")
				yo9eRun(t, l, "add", "staged.yaml")
				return l
			},
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "status", "--porcelain"); !strings.HasPrefix(out, "A ") {
					t.Fatalf("fixture has no staged addition: %q", out)
				}
			},
		},
		{
			name:     "05 detached HEAD",
			wantDiff: []string{"RemoteURL"},
			build: func(t *testing.T) string {
				l, _, _ := yo9eSeeded(t)
				yo9eRun(t, l, "checkout", "--detach", "HEAD")
				return l
			},
			assert: func(t *testing.T, dir string) {
				if out, code := yo9eTry(t, dir, "symbolic-ref", "-q", "HEAD"); code == 0 {
					t.Fatalf("fixture HEAD is still symbolic: %q", out)
				}
			},
		},
		{
			name:     "06 unborn HEAD (git init, no commit)",
			wantDiff: []string{"BOTH_ERROR"},
			build: func(t *testing.T) string {
				d := t.TempDir()
				yo9eRun(t, d, "init", "--initial-branch=main")
				return d
			},
			assert: func(t *testing.T, dir string) {
				if out, code := yo9eTry(t, dir, "rev-parse", "--verify", "HEAD"); code == 0 {
					t.Fatalf("fixture HEAD resolves, so it is not unborn: %q", out)
				}
			},
		},
		{
			name:     "07 ahead only (2 local commits, not pushed)",
			wantDiff: []string{"TrackingBranch"},
			build: func(t *testing.T) string {
				l, _, _ := yo9eSeeded(t)
				for i := 0; i < 2; i++ {
					yo9eWrite(t, filepath.Join(l, "compose.yaml"), fmt.Sprintf("# local %d\n", i))
					yo9eRun(t, l, "add", ".")
					yo9eRun(t, l, "commit", "-m", fmt.Sprintf("local %d", i))
				}
				return l
			},
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); out != "0\t2" {
					t.Fatalf("fixture is not ahead-2/behind-0: %q", out)
				}
			},
		},
		{
			name:     "08 behind only (remote moved 3, fetched)",
			wantDiff: []string{"TrackingBranch"},
			build: func(t *testing.T) string {
				l, r, base := yo9eSeeded(t)
				yo9ePushMore(t, base, r, 3)
				yo9eRun(t, l, "fetch", "origin")
				return l
			},
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); out != "3\t0" {
					t.Fatalf("fixture is not ahead-0/behind-3: %q", out)
				}
			},
		},
		{
			name:     "09 ahead 1 and behind 2, WITH A MERGE in the local history",
			wantDiff: []string{"TrackingBranch"}, // ahead/behind AGREE across a merge commit: countCommits' all-parents walk matches git
			build: func(t *testing.T) string {
				l, r, base := yo9eSeeded(t)
				// a side branch merged into main: exercises countCommits' parent walk
				yo9eRun(t, l, "checkout", "-b", "side")
				yo9eWrite(t, filepath.Join(l, "side.yaml"), "s\n")
				yo9eRun(t, l, "add", ".")
				yo9eRun(t, l, "commit", "-m", "side work")
				yo9eRun(t, l, "checkout", "main")
				yo9eWrite(t, filepath.Join(l, "main.yaml"), "m\n")
				yo9eRun(t, l, "add", ".")
				yo9eRun(t, l, "commit", "-m", "main work")
				yo9eRun(t, l, "merge", "--no-ff", "-m", "merge side", "side")
				yo9ePushMore(t, base, r, 2)
				yo9eRun(t, l, "fetch", "origin")
				return l
			},
			assert: func(t *testing.T, dir string) {
				out := yo9eRun(t, dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD")
				if out != "2\t3" {
					t.Fatalf("fixture is not behind-2/ahead-3: %q", out)
				}
				if m := yo9eRun(t, dir, "rev-list", "--merges", "--count", "@{upstream}..HEAD"); m != "1" {
					t.Fatalf("fixture has no merge commit ahead of upstream: %q", m)
				}
			},
		},
		{
			name:     "10 no remote configured at all",
			wantDiff: nil, // the IDENTITY arm: no remote at all, so every remote-derived field is empty on both sides
			build: func(t *testing.T) string {
				d := t.TempDir()
				yo9eRun(t, d, "init", "--initial-branch=main")
				yo9eWrite(t, filepath.Join(d, "compose.yaml"), "# solo\n")
				yo9eRun(t, d, "add", ".")
				yo9eRun(t, d, "commit", "-m", "solo")
				return d
			},
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "remote"); out != "" {
					t.Fatalf("fixture has a remote: %q", out)
				}
			},
		},
		{
			name:     "11 remote CONFIGURED but never fetched (no refs/remotes)",
			wantDiff: []string{"RemoteURL"},
			build: func(t *testing.T) string {
				l, r, _ := yo9eSeeded(t)
				d := t.TempDir()
				yo9eRun(t, d, "init", "--initial-branch=main")
				yo9eWrite(t, filepath.Join(d, "compose.yaml"), "# unfetched\n")
				yo9eRun(t, d, "add", ".")
				yo9eRun(t, d, "commit", "-m", "unfetched")
				yo9eRun(t, d, "remote", "add", "origin", r)
				_ = l
				return d
			},
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "remote", "get-url", "origin"); out == "" {
					t.Fatal("fixture has no origin URL")
				}
				out, code := yo9eTry(t, dir, "rev-parse", "--verify", "refs/remotes/origin/main")
				if code == 0 {
					t.Fatalf("fixture HAS a remote-tracking ref: %q", out)
				}
			},
		},
		{
			name:     "12 packfile-only repo (git gc --aggressive)",
			wantDiff: []string{"TrackingBranch"},
			build: func(t *testing.T) string {
				l, _, _ := yo9eSeeded(t)
				yo9eRun(t, l, "gc", "--aggressive", "--prune=now")
				return l
			},
			assert: func(t *testing.T, dir string) {
				packs, err := filepath.Glob(filepath.Join(dir, ".git", "objects", "pack", "*.pack"))
				if err != nil || len(packs) == 0 {
					t.Fatalf("fixture is not packed (%d packs, err=%v)", len(packs), err)
				}
			},
		},
		{
			name:     "13 repo with a submodule",
			wantDiff: []string{"TrackingBranch"},
			build: func(t *testing.T) string {
				l, r, _ := yo9eSeeded(t)
				yo9eRun(t, l, "-c", "protocol.file.allow=always", "submodule", "add", r, "sub")
				yo9eRun(t, l, "commit", "-m", "add submodule")
				return l
			},
			assert: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".gitmodules")); err != nil {
					t.Fatalf("fixture has no .gitmodules: %v", err)
				}
				if out := yo9eRun(t, dir, "submodule", "status"); out == "" {
					t.Fatal("fixture reports no submodule")
				}
			},
		},
		{
			name:     "14 linked worktree checkout (.git is a FILE)",
			wantDiff: []string{"GOGIT_ERROR"},
			build: func(t *testing.T) string {
				l, _, base := yo9eSeeded(t)
				wt := filepath.Join(base, "wt")
				yo9eRun(t, l, "worktree", "add", "-b", "wtbranch", wt)
				return wt
			},
			assert: func(t *testing.T, dir string) {
				fi, err := os.Stat(filepath.Join(dir, ".git"))
				if err != nil {
					t.Fatalf("no .git in worktree: %v", err)
				}
				if fi.IsDir() {
					t.Fatal("fixture .git is a DIRECTORY, not a file — worktree case not reached")
				}
			},
		},
		{
			name:     "15 upstream is a DIFFERENTLY-NAMED remote branch",
			wantDiff: []string{"Ahead", "RemoteURL"},
			extra: func(t *testing.T, goRes, cliRes *models.GitStatusResult) {
				// The CLI is the correct one here. go-git looks for
				// refs/remotes/origin/deploy, which does not exist, and reports
				// level; git resolves @{upstream} to origin/main and reports the
				// real one-commit divergence.
				if goRes.Ahead != 0 || cliRes.Ahead != 1 {
					t.Errorf("Ahead go-git=%d cli=%d, want 0 and 1", goRes.Ahead, cliRes.Ahead)
				}
				if goRes.RemoteURL != "" || cliRes.RemoteURL == "" {
					t.Errorf("RemoteURL go-git=%q cli=%q, want empty and non-empty", goRes.RemoteURL, cliRes.RemoteURL)
				}
			},
			build: func(t *testing.T) string {
				l, r, base := yo9eSeeded(t)
				// local branch 'deploy' tracking origin/main, one commit ahead
				yo9eRun(t, l, "checkout", "-b", "deploy", "--track", "origin/main")
				yo9eWrite(t, filepath.Join(l, "compose.yaml"), "# deploy\n")
				yo9eRun(t, l, "add", ".")
				yo9eRun(t, l, "commit", "-m", "deploy work")
				_ = base
				_ = r
				return l
			},
			assert: func(t *testing.T, dir string) {
				if b := yo9eRun(t, dir, "rev-parse", "--abbrev-ref", "HEAD"); b != "deploy" {
					t.Fatalf("fixture branch is %q, not deploy", b)
				}
				if u := yo9eRun(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); u != "origin/main" {
					t.Fatalf("fixture upstream is %q, not origin/main", u)
				}
				if out, code := yo9eTry(t, dir, "rev-parse", "--verify", "refs/remotes/origin/deploy"); code == 0 {
					t.Fatalf("origin/deploy unexpectedly exists: %q", out)
				}
			},
		},
	}

	var rows []string
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.build(t)
			tc.assert(t, dir) // fixture reached the state, or this fatals

			goRes, goErr := svc.getStatusGoGit(dir)
			cliRes, cliErr := svc.getStatusCLI(dir)

			fields, detail := yo9eDiff(goRes, cliRes, goErr, cliErr)
			want := append([]string(nil), tc.wantDiff...)
			sort.Strings(want)

			if len(fields) == 0 {
				rows = append(rows, fmt.Sprintf("| %-52s | IDENTICAL |", tc.name))
			} else {
				rows = append(rows, fmt.Sprintf("| %-52s | DIFFERS: %s |", tc.name, strings.Join(detail, "; ")))
			}

			if strings.Join(fields, ",") != strings.Join(want, ",") {
				t.Errorf("divergence set changed for %q\n  got:  [%s]\n  want: [%s]\n  detail:\n    %s",
					tc.name, strings.Join(fields, ","), strings.Join(want, ","), strings.Join(detail, "\n    "))
			}
			if tc.extra != nil && goErr == nil && cliErr == nil {
				tc.extra(t, goRes, cliRes)
			}
			t.Logf("PARITY %s: [%s]\n    %s", tc.name, strings.Join(fields, ","), strings.Join(detail, "\n    "))
		})
	}
	t.Logf("PARITY TABLE (agent-os-yo9e)\n%s", strings.Join(rows, "\n"))
}
