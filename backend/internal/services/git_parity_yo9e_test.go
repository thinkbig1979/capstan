package services

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// CHARACTERIZATION OF THE GIT-STATUS CONTRACT (agent-os-yo9e).
//
// services/git.go used to carry two complete git-status implementations:
// getStatusGoGit (the go-git library) and getStatusCLI (shelling out), with
// GetStatus running the first and falling back to the second. This corpus was
// built to answer whether they produced the SAME *models.GitStatusResult,
// because that was the precondition for deleting either one.
//
// MEASURED on accc6d7: they did not. Fourteen of fifteen states differed in at
// least one field, and the divergence was BIDIRECTIONAL — neither path was a
// superset of the other, so "one is redundant" was false in both directions:
//
//   - TrackingBranch was served ONLY by go-git; getStatusCLI never set it.
//   - RemoteURL was served ONLY by the CLI when no refs/remotes/<remote>/<branch>
//     existed, because getDivergence returned early and dropped the URL with it.
//   - Ahead/Behind: go-git hardcoded origin/<same-branch-name>; the CLI asks git
//     for @{upstream}, and is right whenever those differ.
//   - DirtyCount: go-git counted FILES, porcelain counts ENTRIES.
//   - A linked worktree (.git is a FILE) could not be opened by go-git at all.
//
// The resolution was to port the one field only go-git served into getStatusCLI
// — as a real @{upstream} lookup rather than go-git's assumption — and then
// delete go-git. So this file no longer diffs two implementations; it pins the
// surviving one, over the same corpus, state by state. The rows that used to
// differ only in TrackingBranch are now simply correct, and the four remaining
// behaviour changes are asserted here as the accepted answers they now are:
//
//   - case 03: DirtyCount is an ENTRY count (1 for an untracked directory),
//     where go-git returned a FILE count (3).
//   - case 05 and 11: RemoteURL is now populated where go-git dropped it.
//   - case 14: a linked worktree now WORKS, where go-git errored and only the
//     silent fallback kept the endpoint answering.
//   - case 15: Ahead is now correct for a differently-named upstream.
//
// Every fixture asserts, via git itself, that it reached the state it claims
// before anything is compared — a corpus of identical clean repos would pass
// every assertion below and mean nothing.

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

// yo9eWant is the expected status for one fixture state. RemoteURL is asserted
// as present/absent rather than by value: the fixtures live under t.TempDir().
type yo9eWant struct {
	branch         string
	dirty          bool
	dirtyCount     int
	ahead          int
	behind         int
	trackingBranch string
	remoteURLSet   bool
	// wantErr, when non-empty, asserts GetStatus fails with exactly this
	// message instead of returning a result.
	wantErr string
}

type yo9eCase struct {
	name string
	// build returns the directory to run GetStatus against.
	build func(t *testing.T) string
	// assert proves the fixture reached the intended state. It must fail loudly.
	assert func(t *testing.T, dir string)
	want   yo9eWant
}

func yo9eCheck(t *testing.T, got *models.GitStatusResult, err error, want yo9eWant) {
	t.Helper()
	if want.wantErr != "" {
		if err == nil {
			t.Fatalf("GetStatus succeeded, want error %q (got %+v)", want.wantErr, got)
		}
		if err.Error() != want.wantErr {
			t.Errorf("error = %q, want %q", err.Error(), want.wantErr)
		}
		return
	}
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if got.Branch != want.branch {
		t.Errorf("Branch = %q, want %q", got.Branch, want.branch)
	}
	if got.Dirty != want.dirty {
		t.Errorf("Dirty = %v, want %v", got.Dirty, want.dirty)
	}
	if got.DirtyCount != want.dirtyCount {
		t.Errorf("DirtyCount = %d, want %d", got.DirtyCount, want.dirtyCount)
	}
	if got.Ahead != want.ahead {
		t.Errorf("Ahead = %d, want %d", got.Ahead, want.ahead)
	}
	if got.Behind != want.behind {
		t.Errorf("Behind = %d, want %d", got.Behind, want.behind)
	}
	if got.TrackingBranch != want.trackingBranch {
		t.Errorf("TrackingBranch = %q, want %q", got.TrackingBranch, want.trackingBranch)
	}
	if (got.RemoteURL != "") != want.remoteURLSet {
		t.Errorf("RemoteURL = %q, want set=%v", got.RemoteURL, want.remoteURLSet)
	}
	if got.Commit == nil || len(got.Commit.Hash) != 40 {
		t.Fatalf("Commit = %+v, want a 40-char hash", got.Commit)
	}
	if got.Commit.Short != got.Commit.Hash[:7] {
		t.Errorf("Commit.Short = %q, want %q", got.Commit.Short, got.Commit.Hash[:7])
	}
}

func TestYo9eGitStatusContract(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	svc := NewGitService(&config.Config{}, nil)

	cases := []yo9eCase{
		{
			name:  "01 clean clone (loose+packed mix)",
			want:  yo9eWant{branch: "main", trackingBranch: "origin/main", remoteURLSet: true},
			build: func(t *testing.T) string { l, _, _ := yo9eSeeded(t); return l },
			assert: func(t *testing.T, dir string) {
				if out := yo9eRun(t, dir, "status", "--porcelain"); out != "" {
					t.Fatalf("fixture not clean: %q", out)
				}
			},
		},
		{
			name: "02 dirty: one modified tracked file",
			want: yo9eWant{branch: "main", dirty: true, dirtyCount: 1, trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "03 dirty: untracked DIRECTORY of 3 files",
			want: yo9eWant{
				branch: "main", dirty: true,
				// ONE, not three. porcelain reports the untracked DIRECTORY as a
				// single entry; go-git listed the three files under it. This is
				// the accepted behaviour change from deleting go-git.
				dirtyCount:     1,
				trackingBranch: "origin/main", remoteURLSet: true,
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
			name: "04 staged only (index differs, worktree clean vs index)",
			want: yo9eWant{branch: "main", dirty: true, dirtyCount: 1, trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "05 detached HEAD",
			want: yo9eWant{
				// A detached HEAD has no upstream, and the origin/<branch>
				// fallback is deliberately skipped here: a clone carries
				// refs/remotes/origin/HEAD as a resolvable symbolic ref, so
				// without that guard this would read "origin/HEAD".
				branch: "HEAD", trackingBranch: "",
				// Populated where go-git dropped it — getDivergence returned
				// early on the unresolvable ref and took the URL with it.
				remoteURLSet: true,
			},
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
			name: "06 unborn HEAD (git init, no commit)",
			want: yo9eWant{wantErr: "Repository has no commits yet"},
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
			name: "07 ahead only (2 local commits, not pushed)",
			want: yo9eWant{branch: "main", ahead: 2, trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "08 behind only (remote moved 3, fetched)",
			want: yo9eWant{branch: "main", behind: 3, trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "09 ahead 1 and behind 2, WITH A MERGE in the local history",
			want: yo9eWant{branch: "main", ahead: 3, behind: 2, trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "10 no remote configured at all",
			want: yo9eWant{branch: "main"},
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
			name: "11 remote CONFIGURED but never fetched (no refs/remotes)",
			want: yo9eWant{
				// A remote is configured but never fetched, so there is neither
				// an upstream nor a refs/remotes ref: no tracking branch, but
				// the URL is still readable. go-git returned "" for both.
				branch: "main", trackingBranch: "", remoteURLSet: true,
			},
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
			name: "12 packfile-only repo (git gc --aggressive)",
			want: yo9eWant{branch: "main", trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "13 repo with a submodule",
			want: yo9eWant{branch: "main", ahead: 1, trackingBranch: "origin/main", remoteURLSet: true},
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
			name: "14 linked worktree checkout (.git is a FILE)",
			want: yo9eWant{
				// go-git could not open this at all ("lstat .../.git/config: not
				// a directory"); only the silent CLI fallback kept the endpoint
				// answering. It is now read directly. The branch is new, so it
				// has no upstream and no origin/<branch> ref.
				branch: "wtbranch", trackingBranch: "", remoteURLSet: true,
			},
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
			name: "15 upstream is a DIFFERENTLY-NAMED remote branch",
			want: yo9eWant{
				// The whole point of resolving @{upstream} instead of assuming
				// origin/<same-name>: go-git looked for refs/remotes/origin/deploy,
				// did not find it, and reported level with no tracking branch.
				branch: "deploy", ahead: 1, trackingBranch: "origin/main", remoteURLSet: true,
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
		{
			// The fallback arm of the TrackingBranch port, and the state
			// redact_url_test.go's fixture is built in: a remote-tracking ref
			// exists but no branch.<name>.merge, so @{upstream} exits 128 while
			// refs/remotes/origin/main resolves. go-git answered origin/main
			// here; dropping that would have been an unannounced regression, so
			// getStatusCLI falls back to the conventional name.
			name: "16 tracking ref exists, NO upstream config (fallback arm)",
			want: yo9eWant{
				branch: "main", trackingBranch: "origin/main", remoteURLSet: true,
				// Deliberately 0/0 even though HEAD could be compared to the
				// ref: git reports no divergence for a branch with no upstream,
				// and counting against a ref the operator never configured
				// would be the same guess this change removed elsewhere.
				ahead: 0, behind: 0,
			},
			build: func(t *testing.T) string {
				_, r, _ := yo9eSeeded(t)
				d := t.TempDir()
				yo9eRun(t, d, "init", "--initial-branch=main")
				yo9eWrite(t, filepath.Join(d, "compose.yaml"), "# fallback\n")
				yo9eRun(t, d, "add", ".")
				yo9eRun(t, d, "commit", "-m", "fallback")
				yo9eRun(t, d, "remote", "add", "origin", r)
				yo9eRun(t, d, "update-ref", "refs/remotes/origin/main", "HEAD")
				return d
			},
			assert: func(t *testing.T, dir string) {
				if _, code := yo9eTry(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); code == 0 {
					t.Fatal("fixture HAS an upstream configured — the fallback arm is not reached")
				}
				if _, code := yo9eTry(t, dir, "rev-parse", "--verify", "refs/remotes/origin/main"); code != 0 {
					t.Fatal("fixture has no refs/remotes/origin/main — the fallback has nothing to find")
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

			got, err := svc.GetStatus(dir)
			yo9eCheck(t, got, err, tc.want)

			switch {
			case err != nil:
				rows = append(rows, fmt.Sprintf("| %-52s | error: %v |", tc.name, err))
			default:
				rows = append(rows, fmt.Sprintf(
					"| %-52s | branch=%-9s dirty=%-5v n=%d ahead=%d behind=%d tracking=%-11q remote=%v |",
					tc.name, got.Branch, got.Dirty, got.DirtyCount, got.Ahead, got.Behind,
					got.TrackingBranch, got.RemoteURL != ""))
			}
		})
	}
	t.Logf("GIT STATUS CONTRACT (agent-os-yo9e)\n%s", strings.Join(rows, "\n"))
}
