package services

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// These tests pin agent-os-4d60: a pull with redeploy never redeployed a stack
// that lives in a subfolder of its repository. `git diff --name-only` prints
// repository-top-relative names, a stack's ComposeFile/EnvFile are
// stack-folder-relative, and stackFilesChanged compared the two raw.
//
// Real git, a fake `docker` on PATH that records its argv and exits 1, so a
// redeploy shows up as the stack in details.failedRedeploys (and as a recorded
// docker call) rather than needing a daemon.

// stack4d60 places one stack: dir is its folder inside the repository ("" is
// the repository top).
type stack4d60 struct {
	id, dir, compose, env string
}

// pull4d60 is one pull: seed/work/root come from pullFixture, files are
// committed upstream and pulled once (so work holds them), then changes are
// committed upstream and pulled with redeploy by PullVerified in pullDir.
type pull4d60 struct {
	files   map[string]string // initial repository content
	changes map[string]string // content committed upstream after the first pull; "" deletes
	stacks  []stack4d60
	pullDir string // relative to the work tree; "" is the top
	// prep runs after the first pull and before the changing commit.
	prep func(t *testing.T, work string)
	// mount maps the work tree to the path the stacks are registered under
	// (a symlink to it); nil registers them under the work tree itself.
	mount func(t *testing.T, root, work string) string
	// pathHook runs before PullVerified, to put a fake git first on PATH.
	pathHook func(t *testing.T, bin string)
}

type pulled4d60 struct {
	ar     truth.ActionResult
	pr     *models.PullResult
	failed []string // stack ids in details.failedRedeploys, sorted
	reason map[string]string
	docker string // argv of every docker call, one per line
}

func commitUpstream4d60(t *testing.T, seed, root string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		abs := filepath.Join(seed, filepath.FromSlash(p))
		if content == "" {
			if err := os.Remove(abs); err != nil {
				t.Fatalf("remove %s: %v", p, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustGit(t, seed, "add", "-A")
	advanceUpstream(t, seed, root)
}

func run4d60(t *testing.T, c pull4d60) pulled4d60 {
	t.Helper()
	work, seed, root := pullFixture(t)
	commitUpstream4d60(t, seed, root, c.files)
	mustGit(t, work, "pull", "-q", "--ff-only")
	if c.prep != nil {
		c.prep(t, work)
	}
	commitUpstream4d60(t, seed, root, c.changes)

	base := work
	if c.mount != nil {
		base = c.mount(t, root, work)
	}
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, st := range c.stacks {
		dir := filepath.Join(base, filepath.FromSlash(st.dir))
		if err := db.UpsertDirectory(models.Directory{Path: dir, Name: st.id}); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertStack(models.Stack{ID: st.id, Directory: dir, ComposeFile: st.compose, EnvFile: st.env, ProjectName: st.id}); err != nil {
			t.Fatal(err)
		}
	}

	bin := t.TempDir()
	rec := filepath.Join(bin, "docker.rec")
	script := "#!/bin/sh\necho \"$*\" >> '" + rec + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil { //nolint:gosec // test fake, must be executable
		t.Fatal(err)
	}
	if c.pathHook != nil {
		c.pathHook(t, bin)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := NewGitService(&config.Config{}, db)
	// Not &DockerService{}: it panics in buildComposeArgs once a stack matches.
	ar, pr := svc.PullVerified(filepath.Join(base, filepath.FromSlash(c.pullDir)), true,
		&DockerService{config: &config.Config{DataDir: t.TempDir()}})

	out := pulled4d60{ar: ar, pr: pr}
	out.reason = map[string]string{}
	if fr, ok := ar.Details["failedRedeploys"].([]RedeployFailure); ok {
		for _, f := range fr {
			out.failed = append(out.failed, f.StackID)
			out.reason[f.StackID] = f.Reason
		}
		sort.Strings(out.failed)
	}
	if b, err := os.ReadFile(rec); err == nil { //nolint:gosec // rec is a path this test built in its own temp dir
		out.docker = string(b)
	}
	return out
}

// redeployed is true when a redeploy of the stack was attempted and failed
// (the fake docker always exits 1). A stack that failed because it could not be
// placed in the repository was never attempted, and is not counted.
func (p pulled4d60) redeployed(id string) bool {
	for _, f := range p.failed {
		if f == id {
			return !strings.Contains(p.reason[id], "repository")
		}
	}
	return false
}

func (p pulled4d60) dockerSaw(project string) bool {
	for _, line := range strings.Split(p.docker, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i] == "-p" && f[i+1] == project {
				return true
			}
		}
	}
	return false
}

const (
	composeOld4d60 = "services: {}\n"
	composeNew4d60 = "services: {a: {image: x}}\n"
)

func TestPullRedeploy_SubfolderStackComposeChange_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60},
		changes: map[string]string{"stackA/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
	})
	// The premise, observed here: git names the file from the repository top.
	if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "stackA/compose.yaml" {
		t.Fatalf("ChangedFiles = %q, want [stackA/compose.yaml]", got.pr.ChangedFiles)
	}
	if !got.redeployed("A") || !got.dockerSaw("A") {
		t.Fatalf("subfolder stack with a changed compose file was not redeployed: failed=%v docker=%q outcome=%s reason=%q",
			got.failed, got.docker, got.ar.Outcome, got.ar.Reason)
	}
}

func TestPullRedeploy_TopStackStillRedeploys_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"compose.yaml": composeOld4d60},
		changes: map[string]string{"compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "top", dir: "", compose: "compose.yaml"}},
	})
	if !got.redeployed("top") || !got.dockerSaw("top") {
		t.Fatalf("repo-top stack was not redeployed: failed=%v docker=%q", got.failed, got.docker)
	}
}

func TestPullRedeploy_SubfolderStackEnvChange_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60, "stackA/.env": "X=1\n"},
		changes: map[string]string{"stackA/.env": "X=2\n"},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml", env: ".env"}},
		pullDir: "stackA",
	})
	if !got.redeployed("A") {
		t.Fatalf("subfolder stack with a changed env file was not redeployed: failed=%v outcome=%s", got.failed, got.ar.Outcome)
	}
}

// The change is in a sibling folder, outside the stack: the same instrument
// must say "no redeploy". Without it the cases above are satisfied by a
// matcher that redeploys everything.
func TestPullRedeploy_SiblingFolderChangeDoesNotRedeploy_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60, "stackB/compose.yaml": composeOld4d60},
		changes: map[string]string{"stackB/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
	})
	if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "stackB/compose.yaml" {
		t.Fatalf("ChangedFiles = %q, want [stackB/compose.yaml]", got.pr.ChangedFiles)
	}
	if got.redeployed("A") || got.docker != "" {
		t.Fatalf("a change outside the stack folder redeployed it: failed=%v docker=%q", got.failed, got.docker)
	}
	if got.ar.Outcome != truth.OutcomeSuccess {
		t.Fatalf("outcome = %s (%q), want success with nothing redeployed", got.ar.Outcome, got.ar.Reason)
	}
}

// A file of the same name in a different folder is not this stack's file.
func TestPullRedeploy_SameNameInOtherFolderDoesNotRedeploy_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60, "other/stackA/compose.yaml": composeOld4d60},
		changes: map[string]string{"other/stackA/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
	})
	if got.redeployed("A") || got.docker != "" {
		t.Fatalf("other/stackA/compose.yaml redeployed stackA: failed=%v docker=%q", got.failed, got.docker)
	}
}

// diff.relative=true in the repository config makes a bare `git diff` print
// names relative to the folder git runs in, which is the stack's frame, not
// the repository's. The pull must not depend on it either way: the change is
// in the stack folder and ChangedFiles must still be repository-top names.
func TestPullRedeploy_DiffRelativeConfig_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60},
		changes: map[string]string{"stackA/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
		prep: func(t *testing.T, work string) {
			mustGit(t, work, "config", "diff.relative", "true")
		},
	})
	if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "stackA/compose.yaml" {
		t.Fatalf("with diff.relative=true ChangedFiles = %q, want [stackA/compose.yaml]", got.pr.ChangedFiles)
	}
	if !got.redeployed("A") {
		t.Fatalf("stack not redeployed under diff.relative=true: failed=%v", got.failed)
	}
}

// git quotes a name with non-ASCII bytes ("st\303\244ck/...") unless told not
// to; a quoted name matches no stack file.
func TestPullRedeploy_NonASCIIFolder_4d60(t *testing.T) {
	folder := "stäck ünï"
	got := run4d60(t, pull4d60{
		files:   map[string]string{folder + "/compose.yaml": composeOld4d60},
		changes: map[string]string{folder + "/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "U", dir: folder, compose: "compose.yaml"}},
		pullDir: folder,
	})
	if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != folder+"/compose.yaml" {
		t.Fatalf("ChangedFiles = %q, want the unquoted name %q", got.pr.ChangedFiles, folder+"/compose.yaml")
	}
	if !got.redeployed("U") {
		t.Fatalf("stack in a non-ASCII folder was not redeployed: failed=%v", got.failed)
	}
}

// A name with a leading space must reach the matcher intact: the first record
// is the one a TrimSpace over the whole output would damage.
func TestPullRedeploy_LeadingSpaceFolder_4d60(t *testing.T) {
	folder := " lead"
	got := run4d60(t, pull4d60{
		files:   map[string]string{folder + "/compose.yaml": composeOld4d60},
		changes: map[string]string{folder + "/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "L", dir: folder, compose: "compose.yaml"}},
		pullDir: folder,
	})
	if !got.redeployed("L") {
		t.Fatalf("stack in a folder with a leading space was not redeployed: changed=%q failed=%v", got.pr.ChangedFiles, got.failed)
	}
}

// The stacks are registered under a symlink to the work tree, git reports the
// real path as the top. A lexical Rel of the two would not find the stack.
func TestPullRedeploy_SymlinkedStacksRoot_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60},
		changes: map[string]string{"stackA/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
		mount: func(t *testing.T, root, work string) string {
			link := filepath.Join(root, "stacks-link")
			if err := os.Symlink(work, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
	})
	if !got.redeployed("A") {
		t.Fatalf("stack under a symlinked stacks root was not redeployed: failed=%v outcome=%s reason=%q",
			got.failed, got.ar.Outcome, got.ar.Reason)
	}
}

// Default rename detection lists only the new name, so a compose file renamed
// away is never named and the stack silently keeps the old content. With
// --no-renames the old name is listed (deleted) and the redeploy is attempted
// and fails loudly.
func TestPullRedeploy_ComposeRenamedAway_4d60(t *testing.T) {
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60},
		changes: map[string]string{"stackA/compose.yaml": "", "stackA/compose.yml": composeOld4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
	})
	if !got.redeployed("A") {
		t.Fatalf("a compose file renamed away did not trigger a redeploy attempt: changed=%q failed=%v", got.pr.ChangedFiles, got.failed)
	}
}

// Fail closed: when the repository top cannot be found the changed files
// cannot be placed, and "success, redeployed nothing" would be a lie. Every
// listed stack is reported as a failed redeploy.
func TestPullRedeploy_TopLevelFaultFailsClosed_4d60(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git on PATH")
	}
	got := run4d60(t, pull4d60{
		files:   map[string]string{"stackA/compose.yaml": composeOld4d60},
		changes: map[string]string{"stackA/compose.yaml": composeNew4d60},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
		pathHook: func(t *testing.T, bin string) {
			// Only the --show-toplevel child fails; every other git call is the real one.
			script := "#!/bin/sh\ncase \"$*\" in *--show-toplevel*) echo 'fatal: injected' >&2; exit 128;; esac\nexec '" + gitBin + "' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil { //nolint:gosec // test fake, must be executable
				t.Fatal(err)
			}
		},
	})
	if got.ar.Outcome != truth.OutcomePartial || len(got.failed) != 1 || got.failed[0] != "A" {
		t.Fatalf("a failed repository-top lookup did not fail closed: outcome=%s reason=%q failed=%v",
			got.ar.Outcome, got.ar.Reason, got.failed)
	}
	// Without the guard repoRel("", dir) fails too and also lands in
	// failedRedeploys, so the outcome alone cannot tell the two apart: the
	// guard's own message can.
	if r := got.reason["A"]; !strings.Contains(r, "could not find the repository top") {
		t.Fatalf("failure reason %q does not come from the repository-top guard", r)
	}
}

// A stack file that cannot be placed under the stack folder fails the stack
// (fail closed) instead of being skipped: skipping reads as "nothing changed".
// Unreachable through today's producers (scanner and stack_crud store bare
// names), so this is a unit test on stackFilesChanged.
func TestStackFilesChanged_UnplaceableFileFailsClosed_4d60(t *testing.T) {
	top := t.TempDir()
	dir := filepath.Join(top, "stackA")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	changed := []string{"stackA/compose.yaml"}
	for name, st := range map[string]models.Stack{
		"absolute compose": {Directory: dir, ComposeFile: "/etc/compose.yaml"},
		"escaping env":     {Directory: dir, ComposeFile: "compose.yaml", EnvFile: "../../.env"},
	} {
		if _, err := stackFilesChanged(top, st, changed); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
	// Same instrument, placeable names: no error, and the match is found.
	ok, err := stackFilesChanged(top, models.Stack{Directory: dir, ComposeFile: "compose.yaml", EnvFile: ".env"}, changed)
	if err != nil || !ok {
		t.Errorf("placeable names: changed=%v err=%v, want true,nil", ok, err)
	}
}
