//go:build integration

package integrationtest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// insertDirectoryAndStack upserts both the directory and stack records so FK
// constraints are satisfied.
func insertDirectoryAndStack(t *testing.T, db *database.DB, stack models.Stack) {
	t.Helper()
	dir := models.Directory{
		Path:      stack.Directory,
		Name:      filepath.Base(stack.Directory),
		IsGitRepo: false,
		ScannedAt: time.Now(),
	}
	if err := db.UpsertDirectory(dir); err != nil {
		t.Fatalf("UpsertDirectory: %v", err)
	}
	if err := db.UpsertStack(stack); err != nil {
		t.Fatalf("UpsertStack: %v", err)
	}
}

// newGitRepo initialises a new bare "remote" and a local clone in t.TempDir().
// Returns (localDir, remoteDir).
func newGitRepo(t *testing.T) (localDir, remoteDir string) {
	t.Helper()

	base := t.TempDir()
	remoteDir = filepath.Join(base, "remote.git")
	localDir = filepath.Join(base, "local")

	mustGit(t, base, "init", "--bare", "--initial-branch=main", remoteDir)
	mustGit(t, base, "clone", remoteDir, localDir)

	// Configure identity so commits succeed in a bare environment.
	mustGit(t, localDir, "config", "user.email", "test@test.invalid")
	mustGit(t, localDir, "config", "user.name", "Test")

	// Create an initial commit so HEAD exists.
	initialFile := filepath.Join(localDir, "compose.yaml")
	writeFile(t, initialFile, "services:\n  web:\n    image: alpine:3.21\n    command: sleep 600\n")
	mustGit(t, localDir, "add", ".")
	mustGit(t, localDir, "commit", "-m", "initial")
	mustGit(t, localDir, "push", "origin", "main")

	return localDir, remoteDir
}

// advanceRemote creates a new commit directly in a second local clone and
// pushes it to the remote so localDir is behind by one commit.
func advanceRemote(t *testing.T, remoteDir string, localDir string) {
	t.Helper()

	base := t.TempDir()
	clone2 := filepath.Join(base, "clone2")
	mustGit(t, base, "clone", remoteDir, clone2)
	mustGit(t, clone2, "config", "user.email", "test@test.invalid")
	mustGit(t, clone2, "config", "user.name", "Test")

	writeFile(t, filepath.Join(clone2, "compose.yaml"),
		"services:\n  web:\n    image: alpine:3.21\n    command: sleep 600\n# changed\n")
	mustGit(t, clone2, "add", ".")
	mustGit(t, clone2, "commit", "-m", "advance remote")
	mustGit(t, clone2, "push", "origin", "main")

	// Fetch into local so git pull can see the new commits.
	mustGit(t, localDir, "fetch", "origin")
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", args[0], dir, err, string(out))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// ── Test #9: git pull+redeploy outcome contract ─────────────────────────────

func TestGit_PullVerified_NoChange(t *testing.T) {
	RequireDocker(t)

	localDir, _ := newGitRepo(t)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	cfg := &config.Config{StacksDir: t.TempDir()}
	gitSvc := services.NewGitService(cfg, db)

	// No remote advance — HEAD should not change.
	ar, pullResult := gitSvc.PullVerified(localDir, false, nil)

	if ar.Outcome != truth.OutcomeNoChange {
		t.Errorf("expected no_change, got %s (reason: %s)", ar.Outcome, ar.Reason)
	}
	if pullResult == nil {
		t.Fatal("expected non-nil pullResult")
	}
	if pullResult.PreviousCommit != pullResult.CurrentCommit {
		t.Errorf("commits should be equal when no_change: prev=%s cur=%s",
			pullResult.PreviousCommit, pullResult.CurrentCommit)
	}
}

func TestGit_PullVerified_Success_NoRedeploy(t *testing.T) {
	RequireDocker(t)

	localDir, remoteDir := newGitRepo(t)
	advanceRemote(t, remoteDir, localDir)

	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	cfg := &config.Config{StacksDir: t.TempDir()}
	gitSvc := services.NewGitService(cfg, db)

	// HEAD advanced; no stacks registered, so redeploy step is a no-op.
	ar, pullResult := gitSvc.PullVerified(localDir, false, nil)

	if ar.Outcome != truth.OutcomeSuccess {
		t.Errorf("expected success, got %s (reason: %s)", ar.Outcome, ar.Reason)
	}
	if pullResult == nil {
		t.Fatal("expected non-nil pullResult")
	}
	if pullResult.PreviousCommit == pullResult.CurrentCommit {
		t.Errorf("expected commits to differ after advance; prev=%s cur=%s",
			pullResult.PreviousCommit, pullResult.CurrentCommit)
	}
}

func TestGit_PullVerified_PartialOnFailedRedeploy(t *testing.T) {
	// Critical test for finding #9: a failing redeploy must produce "partial",
	// never "success". This test is load-bearing: removing the partial branch in
	// PullVerified (services/git.go) must make it FAIL.
	//
	// Setup:
	//  - localDir is a git repo. The initial commit has a healthy compose.yaml.
	//  - We commit a crash-looping compose.yaml into localDir AND push it, so
	//    the working tree is clean when pullCLI runs (no dirty-tree rejection).
	//  - The remote is then advanced one more commit so git pull fast-forwards.
	//  - The stack row in the DB points at localDir so ListStacksByDirectory finds it.
	//  - After the pull, RestartVerified runs the crash-looping service and the
	//    dwell window expires without the container staying up → partial.
	RequireDocker(t)

	localDir, remoteDir := newGitRepo(t)
	// newGitRepo commits a healthy compose.yaml as the initial commit.

	// Step 1: commit the crash-looping compose into localDir and push it so the
	// working tree is clean. This is what the remote will fast-forward INTO when
	// the next commit is pulled.
	crashYAML := "services:\n  app:\n    image: alpine:3.21\n    command: [\"sh\",\"-c\",\"exit 1\"]\n    restart: \"no\"\n"
	writeFile(t, filepath.Join(localDir, "compose.yaml"), crashYAML)
	mustGit(t, localDir, "add", ".")
	mustGit(t, localDir, "commit", "-m", "switch to crash-loop service")
	mustGit(t, localDir, "push", "origin", "main")
	// Working tree is now clean and HEAD is at the crash commit.

	// Step 2: push one more commit from a second clone so localDir is behind.
	// This is the commit that git pull will fast-forward to.
	clone2Base := t.TempDir()
	clone2 := filepath.Join(clone2Base, "c2")
	mustGit(t, clone2Base, "clone", remoteDir, clone2)
	mustGit(t, clone2, "config", "user.email", "test@test.invalid")
	mustGit(t, clone2, "config", "user.name", "Test")
	// Keep the crash-looping service but bump a comment so compose.yaml appears
	// in changedFiles (the diff between the two commits).
	crashYAMLv2 := crashYAML + "# v2\n"
	writeFile(t, filepath.Join(clone2, "compose.yaml"), crashYAMLv2)
	mustGit(t, clone2, "add", ".")
	mustGit(t, clone2, "commit", "-m", "bump crash yaml")
	mustGit(t, clone2, "push", "origin", "main")
	// Fetch so localDir knows about the new commit without pulling yet.
	mustGit(t, localDir, "fetch", "origin")

	// Step 3: register the stack pointing at localDir. PullVerified calls
	// ListStacksByDirectory(localDir), so the Directory must match exactly.
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	// Use a sanitised project name (Docker Compose only allows [a-z0-9-]).
	projectName := "it-gitpullpartial"
	stackID := "it~gitpullpartial:default"
	stack := models.Stack{
		ID:          stackID,
		Directory:   localDir,
		ComposeFile: "compose.yaml",
		ProjectName: projectName,
		Status:      "stopped",
	}
	insertDirectoryAndStack(t, db, stack)

	cfg := &config.Config{StacksDir: localDir}
	dockerSvc, err := services.NewDockerService(cfg)
	if err != nil {
		t.Skipf("docker service unavailable: %v", err)
	}

	// Clean up the compose project after the test.
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose",
			"-p", projectName,
			"-f", filepath.Join(localDir, "compose.yaml"),
			"down", "-v", "--remove-orphans")
		cmd.Dir = localDir
		_ = cmd.Run()
	})

	gitSvc := services.NewGitService(cfg, db)
	ar, pullResult := gitSvc.PullVerified(localDir, true, dockerSvc)

	// Sanity: the pull must have advanced HEAD. If not, the test setup is wrong.
	if pullResult == nil || pullResult.PreviousCommit == pullResult.CurrentCommit {
		t.Fatalf("test setup error: HEAD did not advance (prev=%v cur=%v) outcome=%s reason=%s",
			func() string {
				if pullResult != nil { return pullResult.PreviousCommit }
				return "<nil>"
			}(),
			func() string {
				if pullResult != nil { return pullResult.CurrentCommit }
				return "<nil>"
			}(),
			ar.Outcome, ar.Reason)
	}

	// THE LOAD-BEARING ASSERTION: must be partial, not success.
	// Removing the `if len(failures) > 0` branch in services/git.go must make
	// this fail because the function would return success instead of partial.
	if ar.Outcome != truth.OutcomePartial {
		t.Errorf("FAIL (finding #9): expected outcome=partial but got %s\nreason: %s\ndetails: %v",
			ar.Outcome, ar.Reason, ar.Details)
	}

	// THE CONTENT ASSERTION: failedRedeploys must list the stack.
	if ar.Details == nil {
		t.Fatal("expected non-nil details on partial outcome")
	}
	failedRaw, ok := ar.Details["failedRedeploys"]
	if !ok {
		t.Fatalf("expected details.failedRedeploys to be present; details=%v", ar.Details)
	}
	failures, ok := failedRaw.([]services.RedeployFailure)
	if !ok {
		t.Fatalf("expected failedRedeploys to be []RedeployFailure, got %T: %v", failedRaw, failedRaw)
	}
	if len(failures) == 0 {
		t.Fatal("expected at least one entry in failedRedeploys")
	}
	found := false
	for _, f := range failures {
		if f.StackID == stackID {
			found = true
			t.Logf("failedRedeploys entry: stack=%s reason=%s", f.StackID, f.Reason)
		}
	}
	if !found {
		t.Errorf("stackID %q not found in failedRedeploys: %v", stackID, failures)
	}
}

func TestGit_PullVerified_SuccessWithRedeploy(t *testing.T) {
	// Pull with redeploy where the stack actually stays running → success.
	// The stack Directory must equal localDir so ListStacksByDirectory finds it.
	RequireDocker(t)

	localDir, remoteDir := newGitRepo(t)
	// newGitRepo creates localDir with a healthy initial compose.yaml already committed.

	goodYAML := "services:\n  web:\n    image: alpine:3.21\n    command: [\"sleep\",\"600\"]\n    restart: \"no\"\n"

	// Step 1: commit the healthy compose into localDir so the tree is clean before pull.
	writeFile(t, filepath.Join(localDir, "compose.yaml"), goodYAML)
	mustGit(t, localDir, "add", ".")
	mustGit(t, localDir, "commit", "-m", "healthy service v1")
	mustGit(t, localDir, "push", "origin", "main")

	// Step 2: push one more commit from a clone so localDir is behind by one.
	clone2Base := t.TempDir()
	clone2 := filepath.Join(clone2Base, "c2")
	mustGit(t, clone2Base, "clone", remoteDir, clone2)
	mustGit(t, clone2, "config", "user.email", "test@test.invalid")
	mustGit(t, clone2, "config", "user.name", "Test")
	goodYAMLv2 := goodYAML + "# v2\n"
	writeFile(t, filepath.Join(clone2, "compose.yaml"), goodYAMLv2)
	mustGit(t, clone2, "add", ".")
	mustGit(t, clone2, "commit", "-m", "bump healthy yaml")
	mustGit(t, clone2, "push", "origin", "main")
	mustGit(t, localDir, "fetch", "origin")

	// Step 3: register the stack pointing at localDir.
	db, err := database.NewWithMigrations(":memory:")
	if err != nil {
		t.Fatalf("db: %v", err)
	}

	projectName := "it-gitpullsuccess"
	stackID := "it~gitpullsuccess:default"
	stack := models.Stack{
		ID:          stackID,
		Directory:   localDir,
		ComposeFile: "compose.yaml",
		ProjectName: projectName,
		Status:      "stopped",
	}
	insertDirectoryAndStack(t, db, stack)

	cfg := &config.Config{StacksDir: localDir}
	dockerSvc, err := services.NewDockerService(cfg)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}

	// Clean up after the test.
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose",
			"-p", projectName,
			"-f", filepath.Join(localDir, "compose.yaml"),
			"down", "-v", "--remove-orphans")
		cmd.Dir = localDir
		_ = cmd.Run()
	})

	// Start the stack from localDir so RestartVerified has a running container
	// to stop-then-start.
	startCmd := exec.Command("docker", "compose",
		"-p", projectName,
		"-f", filepath.Join(localDir, "compose.yaml"),
		"up", "-d")
	startCmd.Dir = localDir
	if out, err := startCmd.CombinedOutput(); err != nil {
		t.Fatalf("initial stack start failed: %v\n%s", err, string(out))
	}

	gitSvc := services.NewGitService(cfg, db)
	ar, pullResult := gitSvc.PullVerified(localDir, true, dockerSvc)

	// Sanity check that HEAD actually advanced.
	if pullResult == nil || pullResult.PreviousCommit == pullResult.CurrentCommit {
		t.Logf("warning: HEAD did not advance; this may indicate a fetch/setup issue")
	}

	if ar.Outcome != truth.OutcomeSuccess {
		t.Errorf("expected success after healthy redeploy, got %s (reason: %s; details: %v)",
			ar.Outcome, ar.Reason, ar.Details)
	}
}

// ── JSON shape test for failedRedeploys ─────────────────────────────────────

func TestGit_FailedRedeployShape(t *testing.T) {
	// Unit-level: verify that a partial ActionResult with failedRedeploys details
	// serialises to the documented JSON shape.
	failures := []services.RedeployFailure{
		{StackID: "stacks~myapp:default", Reason: "stack did not start: no containers found"},
	}
	ar := truth.Partial("pulled new commits but some stacks failed to redeploy",
		truth.KV("previousCommit", "abc1234"),
		truth.KV("currentCommit", "def5678"),
		truth.KV("failedRedeploys", failures),
	)

	data, err := json.Marshal(ar)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out["outcome"] != "partial" {
		t.Errorf("expected outcome=partial, got %v", out["outcome"])
	}

	details, ok := out["details"].(map[string]any)
	if !ok {
		t.Fatalf("expected details object, got %T: %v", out["details"], out["details"])
	}

	failedRaw := details["failedRedeploys"]
	if failedRaw == nil {
		t.Fatal("expected details.failedRedeploys to be present")
	}

	failedSlice, ok := failedRaw.([]any)
	if !ok {
		t.Fatalf("expected failedRedeploys to be a slice, got %T", failedRaw)
	}
	if len(failedSlice) != 1 {
		t.Fatalf("expected 1 failed entry, got %d", len(failedSlice))
	}

	entry, ok := failedSlice[0].(map[string]any)
	if !ok {
		t.Fatalf("expected entry to be a map, got %T", failedSlice[0])
	}
	if entry["stack"] != "stacks~myapp:default" {
		t.Errorf("expected stack key, got %v", entry["stack"])
	}
	if !strings.Contains(entry["reason"].(string), "no containers") {
		t.Errorf("expected reason to contain 'no containers', got %q", entry["reason"])
	}
}
