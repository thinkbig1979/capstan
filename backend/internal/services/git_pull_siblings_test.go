package services

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// These tests pin agent-os-88xz: a pull moves the whole work tree, but
// PullVerified locked only the stacks registered in the requesting folder
// (ListStacksByDirectory, an exact match), so a sibling stack in another
// folder of the same repository had its files rewritten without its lock, and
// nothing told the operator it needed a redeploy.
//
// Real git, a fake `docker` on PATH that records its argv and exits 1, and a
// wired OperationLock (unlike the 4d60 tests, which run with no lock).

type siblingsPull struct {
	files   map[string]string
	changes map[string]string
	stacks  []stack4d60
	pullDir string
	// held are stack ids whose lock is taken (as a backup) before the pull.
	held     []string
	redeploy bool
	// mount maps the work tree to the path the stacks are registered under
	// (a symlink to it); nil registers them under the work tree itself.
	mount func(t *testing.T, root, work string) string
	// noLock runs PullVerified with no OperationLock and over a closed
	// database: the post-pull stack list fails.
	noLock bool
}

type siblingsPulled struct {
	ar      truth.ActionResult
	work    string
	before  string // HEAD before the pull
	docker  string
	lock    *OperationLock
	tokens  map[string]string
	changed []siblingWire
}

// siblingWire is one details.siblingStacksChanged entry as the client reads
// it. Decoded through JSON so this file builds against the pre-fix code too.
type siblingWire struct {
	StackID string `json:"stack"`
	Name    string `json:"name"`
}

func runSiblings(t *testing.T, c siblingsPull) siblingsPulled {
	t.Helper()
	work, seed, root := pullFixture(t)
	commitUpstream4d60(t, seed, root, c.files)
	mustGit(t, work, "pull", "-q", "--ff-only")
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
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	svc := NewGitService(&config.Config{}, db)
	lock := NewOperationLock()
	if c.noLock {
		svc = NewGitService(&config.Config{}, closedTestDB(t))
	} else {
		svc.SetOperationLock(lock)
	}
	out := siblingsPulled{work: work, before: headOf(t, work), lock: lock, tokens: map[string]string{}}
	for _, id := range c.held {
		tok, err := lock.Acquire(id, OpKindBackup)
		if err != nil {
			t.Fatal(err)
		}
		out.tokens[id] = tok
	}

	out.ar, _ = svc.PullVerified(filepath.Join(base, filepath.FromSlash(c.pullDir)), c.redeploy,
		&DockerService{config: &config.Config{DataDir: t.TempDir()}})
	if b, err := os.ReadFile(rec); err == nil { //nolint:gosec // rec is a path this test built in its own temp dir
		out.docker = string(b)
	}
	if v, ok := out.ar.Details["siblingStacksChanged"]; ok {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &out.changed); err != nil {
			t.Fatalf("siblingStacksChanged %s: %v", b, err)
		}
	}
	return out
}

func (p siblingsPulled) dockerSaw(project string) bool {
	return pulled4d60{docker: p.docker}.dockerSaw(project)
}

func (p siblingsPulled) changedIDs() []string {
	ids := []string{}
	for _, s := range p.changed {
		ids = append(ids, s.StackID)
	}
	sort.Strings(ids)
	return ids
}

var siblingStacks = []stack4d60{
	{id: "A", dir: "stackA", compose: "compose.yaml"},
	{id: "B", dir: "stackB", compose: "compose.yaml", env: ".env"},
}

var siblingFiles = map[string]string{
	"stackA/compose.yaml": composeOld4d60,
	"stackB/compose.yaml": composeOld4d60,
	"stackB/.env":         "X=1\n",
}

// A pull from stackA rewrites stackB's files, so it must refuse while stackB
// is held, before anything is pulled.
func TestPullVerified_SiblingsLockedSiblingRefuses(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:    siblingFiles,
		changes:  map[string]string{"stackB/compose.yaml": composeNew4d60},
		stacks:   siblingStacks,
		pullDir:  "stackA",
		held:     []string{"B"},
		redeploy: true,
	})
	if got.ar.Outcome != truth.OutcomeFailed {
		t.Fatalf("pull ran while a sibling stack was locked: outcome=%s reason=%q", got.ar.Outcome, got.ar.Reason)
	}
	var appErr *models.AppError
	if !errors.As(got.ar.Err, &appErr) || appErr.Status != http.StatusConflict || appErr.Code != models.ErrOperationInProgress {
		t.Fatalf("refusal is not a classified 409 OPERATION_IN_PROGRESS: err=%v", got.ar.Err)
	}
	if !strings.Contains(appErr.Message, "stack B: backup in progress since") {
		t.Fatalf("refusal %q does not name the busy sibling", appErr.Message)
	}
	if h := headOf(t, got.work); h != got.before {
		t.Fatal("the pull advanced HEAD although a sibling stack was locked")
	}
	// The requested stack's lock was taken and then released by the refusal.
	tok, err := got.lock.Acquire("A", OpKindStart)
	if err != nil {
		t.Fatalf("the refused pull left stack A locked: %v", err)
	}
	got.lock.Release("A", tok)
	if _, err := got.lock.Acquire("B", OpKindStart); err == nil {
		t.Fatal("the refused pull released the backup's lock on stack B")
	}
}

// The other side: same repository, nothing held, so the pull lands and every
// stack of the repository is free afterwards.
func TestPullVerified_SiblingsFreePullsAndReleasesAll(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"README": "x\n"},
		stacks:  siblingStacks,
		pullDir: "stackA",
	})
	if got.ar.Outcome != truth.OutcomeSuccess {
		t.Fatalf("outcome=%s reason=%q err=%v", got.ar.Outcome, got.ar.Reason, got.ar.Err)
	}
	if headOf(t, got.work) == got.before {
		t.Fatal("the pull did not advance HEAD")
	}
	for _, id := range []string{"A", "B"} {
		tok, err := got.lock.Acquire(id, OpKindStart)
		if err != nil {
			t.Fatalf("PullVerified left stack %s locked: %v", id, err)
		}
		got.lock.Release(id, tok)
	}
	if ids := got.changedIDs(); len(ids) != 0 {
		t.Fatalf("siblingStacksChanged = %v for a README-only change", ids)
	}
}

// A sibling whose compose file changed is listed and NOT redeployed; the
// requested stack whose compose file changed still is.
func TestPullVerified_SiblingsChangedSiblingListedNotRedeployed(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files: siblingFiles,
		changes: map[string]string{
			"stackA/compose.yaml": composeNew4d60,
			"stackB/compose.yaml": composeNew4d60,
		},
		stacks:   siblingStacks,
		pullDir:  "stackA",
		redeploy: true,
	})
	if !got.dockerSaw("A") {
		t.Fatalf("the requested stack was not redeployed; docker saw %q", got.docker)
	}
	if got.dockerSaw("B") {
		t.Fatalf("the sibling stack was redeployed; docker saw %q", got.docker)
	}
	if ids := got.changedIDs(); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("siblingStacksChanged = %v, want [B]", ids)
	}
	if got.changed[0].Name != "B" {
		t.Fatalf("sibling name = %q, want the project name B", got.changed[0].Name)
	}
}

// A sibling's env file counts too, and the list is reported without redeploy.
func TestPullVerified_SiblingsEnvChangeListedWithoutRedeploy(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"stackB/.env": "X=2\n"},
		stacks:  siblingStacks,
		pullDir: "stackA",
	})
	if got.ar.Outcome != truth.OutcomeSuccess {
		t.Fatalf("outcome=%s reason=%q", got.ar.Outcome, got.ar.Reason)
	}
	if got.docker != "" {
		t.Fatalf("a pull without redeploy ran docker: %q", got.docker)
	}
	if ids := got.changedIDs(); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("siblingStacksChanged = %v, want [B]", ids)
	}
}

// Two stacks registered in the requested folder are both the redeploy set, as
// before; neither is a sibling.
func TestPullVerified_SiblingsTwoRequestedStacksUnchanged(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files: map[string]string{
			"stackA/compose.yaml": composeOld4d60,
			"stackA/other.yaml":   composeOld4d60,
			"stackB/compose.yaml": composeOld4d60,
		},
		changes: map[string]string{
			"stackA/compose.yaml": composeNew4d60,
			"stackA/other.yaml":   composeNew4d60,
		},
		stacks: []stack4d60{
			{id: "A1", dir: "stackA", compose: "compose.yaml"},
			{id: "A2", dir: "stackA", compose: "other.yaml"},
			{id: "B", dir: "stackB", compose: "compose.yaml"},
		},
		pullDir:  "stackA",
		redeploy: true,
	})
	if !got.dockerSaw("A1") || !got.dockerSaw("A2") {
		t.Fatalf("both requested stacks should be redeployed; docker saw %q", got.docker)
	}
	if got.dockerSaw("B") {
		t.Fatalf("an unchanged sibling was redeployed; docker saw %q", got.docker)
	}
	if ids := got.changedIDs(); len(ids) != 0 {
		t.Fatalf("siblingStacksChanged = %v, want none", ids)
	}
}

// A sibling that cannot be checked is not reported as unchanged: the outcome
// is partial and the sibling is named in siblingCheckFailures.
func TestPullVerified_SiblingsUncheckableSiblingIsPartial(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"README": "x\n"},
		stacks: []stack4d60{
			{id: "A", dir: "stackA", compose: "compose.yaml"},
			{id: "B", dir: "stackB", compose: "/abs/compose.yaml"},
		},
		pullDir: "stackA",
	})
	if got.ar.Outcome != truth.OutcomePartial {
		t.Fatalf("an uncheckable sibling did not make the outcome partial: outcome=%s reason=%q", got.ar.Outcome, got.ar.Reason)
	}
	fails, ok := got.ar.Details["siblingCheckFailures"].([]RedeployFailure)
	if !ok || len(fails) != 1 || fails[0].StackID != "B" {
		t.Fatalf("siblingCheckFailures = %#v, want stack B", got.ar.Details["siblingCheckFailures"])
	}
}

// With the lock wired the repository top is found before the pull. A bare
// repository still gets the pull's own 409, not the commit wording.
func TestPullVerified_SiblingsBareRepoKeepsPullRefusal(t *testing.T) {
	bare := t.TempDir()
	mustGit(t, bare, "init", "-q", "--bare")
	svc := NewGitService(&config.Config{}, newTestDB(t))
	svc.SetOperationLock(NewOperationLock())

	ar, _ := svc.PullVerified(bare, false, nil)

	var appErr *models.AppError
	if ar.Outcome != truth.OutcomeFailed || !errors.As(ar.Err, &appErr) {
		t.Fatalf("outcome=%s err=%v, want a classified failure", ar.Outcome, ar.Err)
	}
	if appErr.Status != http.StatusConflict || appErr.Code != models.ErrGitBareRepo {
		t.Fatalf("got %d %s, want 409 %s", appErr.Status, appErr.Code, models.ErrGitBareRepo)
	}
	if !strings.Contains(appErr.Message, "pull into") {
		t.Fatalf("message %q is not the pull's bare-repository refusal", appErr.Message)
	}
}

func symlinkMount(t *testing.T, root, work string) string {
	t.Helper()
	link := filepath.Join(root, "stacks-link")
	if err := os.Symlink(work, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// Stacks registered through a symlink: once the pull deleted stackB's folder,
// the link path no longer resolves and cannot be placed after the pull. The
// sibling is placed before the pull, so it is still reported as changed.
func TestPullVerified_SiblingsDeletedFolderViaSymlinkIsListed(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"stackB/compose.yaml": "", "stackB/.env": ""},
		stacks:  siblingStacks,
		pullDir: "stackA",
		mount:   symlinkMount,
	})
	if ids := got.changedIDs(); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("a sibling whose folder the pull deleted was not listed: outcome=%s reason=%q siblingStacksChanged=%v",
			got.ar.Outcome, got.ar.Reason, ids)
	}
}

// The other direction: the pull creates the sibling's folder, so it can be
// placed only after the pull. It is listed, not a check failure.
func TestPullVerified_SiblingsCreatedFolderViaSymlinkIsListed(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"stackC/compose.yaml": composeNew4d60},
		stacks: []stack4d60{
			{id: "A", dir: "stackA", compose: "compose.yaml"},
			{id: "C", dir: "stackC", compose: "compose.yaml"},
		},
		pullDir: "stackA",
		mount:   symlinkMount,
	})
	if got.ar.Outcome != truth.OutcomeSuccess {
		t.Fatalf("outcome=%s reason=%q details=%#v", got.ar.Outcome, got.ar.Reason, got.ar.Details)
	}
	if ids := got.changedIDs(); len(ids) != 1 || ids[0] != "C" {
		t.Fatalf("a sibling whose folder the pull created was not listed: siblingStacksChanged=%v", ids)
	}
}

// A sibling inside the repository (it is locked) whose folder cannot be
// placed before or after the pull, here a symlinked path to a folder that
// never exists, is a check failure, never "unchanged".
func TestPullVerified_SiblingsUnplaceableSiblingIsPartial(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"stackB/compose.yaml": composeNew4d60},
		stacks: []stack4d60{
			{id: "A", dir: "stackA", compose: "compose.yaml"},
			{id: "C", dir: "stackC", compose: "compose.yaml"},
		},
		pullDir: "stackA",
		mount:   symlinkMount,
	})
	if got.ar.Outcome != truth.OutcomePartial {
		t.Fatalf("an unplaceable sibling did not make the outcome partial: outcome=%s reason=%q changed=%v",
			got.ar.Outcome, got.ar.Reason, got.changedIDs())
	}
	fails, ok := got.ar.Details["siblingCheckFailures"].([]RedeployFailure)
	if !ok || len(fails) != 1 || fails[0].StackID != "C" {
		t.Fatalf("siblingCheckFailures = %#v, want stack C", got.ar.Details["siblingCheckFailures"])
	}
}

// When the stack list cannot be read after the pull (no lock wired, so the
// list is not read before it), the siblings are unknown: partial, with
// siblingListError, not success.
func TestPullVerified_SiblingsListFaultIsPartial(t *testing.T) {
	got := runSiblings(t, siblingsPull{
		files:   siblingFiles,
		changes: map[string]string{"stackB/compose.yaml": composeNew4d60},
		pullDir: "stackA",
		noLock:  true,
	})
	if got.ar.Outcome != truth.OutcomePartial {
		t.Fatalf("an unreadable stack list did not make the outcome partial: outcome=%s reason=%q", got.ar.Outcome, got.ar.Reason)
	}
	if e, _ := got.ar.Details["siblingListError"].(string); e == "" {
		t.Fatalf("details.siblingListError is empty: %#v", got.ar.Details)
	}
}

// With the lock wired the repository top is found before the pull, and its
// classified answers reach the client: 404 GIT_NOT_REPO for a folder outside
// any repository, 404 STACK_DIR_MISSING for a folder that is gone. Nothing is
// pulled either way.
func TestPullVerified_SiblingsTopFailureIsClassified(t *testing.T) {
	for _, tc := range []struct {
		name string
		dir  func(t *testing.T) string
		code string
	}{
		{"not a repository", func(t *testing.T) string { return t.TempDir() }, models.ErrGitNotRepo},
		{"missing folder", func(t *testing.T) string { return filepath.Join(t.TempDir(), "gone") }, models.ErrStackDirMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewGitService(&config.Config{}, newTestDB(t))
			svc.SetOperationLock(NewOperationLock())

			ar, pr := svc.PullVerified(tc.dir(t), false, nil)

			var appErr *models.AppError
			if ar.Outcome != truth.OutcomeFailed || !errors.As(ar.Err, &appErr) {
				t.Fatalf("outcome=%s err=%v, want a classified failure", ar.Outcome, ar.Err)
			}
			if appErr.Status != http.StatusNotFound || appErr.Code != tc.code {
				t.Fatalf("got %d %s, want 404 %s", appErr.Status, appErr.Code, tc.code)
			}
			if pr != nil {
				t.Fatalf("a pull result came back, so something was pulled: %+v", pr)
			}
		})
	}
}
