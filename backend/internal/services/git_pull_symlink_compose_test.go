package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin agent-os-juq9: a stack whose compose (or env) file is a
// committed symlink was not redeployed by a pull that changed the link's
// TARGET. git lists the target, stackFilesChanged matched only the stack's own
// file names, so the pull reported success with the old content still running.
//
// They reuse run4d60's real-git, fake-docker harness. Symlinks cannot be
// written through its files/changes maps, so prep builds them in the seed
// repository (root/seed, a sibling of the work tree) and pulls them in; a
// symlink replaced in the seed without a commit rides into the `changes`
// commit, whose `git add -A` picks it up.

// seedLinks makes each link -> target in the seed and, when commit is set,
// commits it upstream and pulls it into work.
func seedLinks(t *testing.T, work string, links map[string]string, commit bool) {
	t.Helper()
	root := filepath.Dir(work)
	seed := filepath.Join(root, "seed")
	for link, target := range links {
		abs := filepath.Join(seed, filepath.FromSlash(link))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Symlink(target, abs); err != nil {
			t.Fatal(err)
		}
	}
	if commit {
		commitUpstream4d60(t, seed, root, nil)
		mustGit(t, work, "pull", "-q", "--ff-only")
	}
}

func prepLinks(links map[string]string) func(t *testing.T, work string) {
	return func(t *testing.T, work string) { seedLinks(t, work, links, true) }
}

func TestPull_SymlinkedComposeTargetChangeRedeploys_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		files := map[string]string{"shared/compose.yaml": composeOld4d60, "shared/other.yaml": "a: 1\n"}
		links := map[string]string{"stackA/compose.yaml": "../shared/compose.yaml"}
		stacks := []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}}

		changed := run4d60(t, pull4d60{
			lock:  lock,
			files: files, changes: map[string]string{"shared/compose.yaml": composeNew4d60},
			stacks: stacks, pullDir: "stackA", prep: prepLinks(links),
		})
		// The premise, observed: git names the target and not the link.
		if len(changed.pr.ChangedFiles) != 1 || changed.pr.ChangedFiles[0] != "shared/compose.yaml" {
			t.Fatalf("ChangedFiles = %q, want [shared/compose.yaml]", changed.pr.ChangedFiles)
		}
		if !changed.redeployed("A") || !changed.dockerSaw("A") {
			t.Fatalf("stack whose compose file links to a changed target was not redeployed: failed=%v docker=%q outcome=%s",
				changed.failed, changed.docker, changed.ar.Outcome)
		}

		// Control, same instrument: a change to a file the link does not point at
		// leaves the stack alone, so the match is the target and not "anything".
		other := run4d60(t, pull4d60{
			lock:  lock,
			files: files, changes: map[string]string{"shared/other.yaml": "a: 2\n"},
			stacks: stacks, pullDir: "stackA", prep: prepLinks(links),
		})
		if len(other.pr.ChangedFiles) != 1 || other.redeployed("A") || other.dockerSaw("A") {
			t.Fatalf("an unrelated change redeployed the stack: changed=%q failed=%v docker=%q",
				other.pr.ChangedFiles, other.failed, other.docker)
		}
	})
}

func TestPull_SymlinkedEnvTargetChangeRedeploys_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"shared/stack.env": "X=1\n", "stackA/compose.yaml": composeOld4d60},
			changes: map[string]string{"shared/stack.env": "X=2\n"},
			stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml", env: ".env"}},
			pullDir: "stackA",
			prep:    prepLinks(map[string]string{"stackA/.env": "../shared/stack.env"}),
		})
		if !got.redeployed("A") || !got.dockerSaw("A") {
			t.Fatalf("stack whose env file links to a changed target was not redeployed: failed=%v docker=%q",
				got.failed, got.docker)
		}
	})
}

// A link to a link: the final target changing, and the middle link being
// retargeted, both change what the stack reads.
func TestPull_SymlinkChainChangeRedeploys_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		files := map[string]string{"shared/real.yaml": composeOld4d60, "shared/real2.yaml": composeNew4d60}
		links := map[string]string{
			"stackA/compose.yaml": "../shared/mid.yaml",
			"shared/mid.yaml":     "real.yaml",
		}
		stacks := []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}}

		target := run4d60(t, pull4d60{
			lock:  lock,
			files: files, changes: map[string]string{"shared/real.yaml": composeNew4d60},
			stacks: stacks, pullDir: "stackA", prep: prepLinks(links),
		})
		if !target.redeployed("A") || !target.dockerSaw("A") {
			t.Fatalf("a change to the end of a link chain did not redeploy: failed=%v changed=%q", target.failed, target.pr.ChangedFiles)
		}

		retarget := run4d60(t, pull4d60{
			lock:  lock,
			files: files, changes: map[string]string{},
			stacks: stacks, pullDir: "stackA", prep: func(t *testing.T, work string) {
				seedLinks(t, work, links, true)
				seedLinks(t, work, map[string]string{"shared/mid.yaml": "real2.yaml"}, false)
			},
		})
		if len(retarget.pr.ChangedFiles) != 1 || retarget.pr.ChangedFiles[0] != "shared/mid.yaml" {
			t.Fatalf("ChangedFiles = %q, want [shared/mid.yaml]", retarget.pr.ChangedFiles)
		}
		if !retarget.redeployed("A") || !retarget.dockerSaw("A") {
			t.Fatalf("retargeting a middle link did not redeploy: failed=%v", retarget.failed)
		}
	})
}

// A link to a file outside the repository: the pull cannot change that file,
// so only the link itself (inside the repository) can show in the diff. An
// unrelated change must not redeploy the stack and must not fail it either.
func TestPull_SymlinkOutsideRepoDoesNotRedeploy_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		outside := filepath.Join(t.TempDir(), "elsewhere.yaml")
		if err := os.WriteFile(outside, []byte(composeOld4d60), 0o600); err != nil {
			t.Fatal(err)
		}
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"shared/other.yaml": "a: 1\n"},
			changes: map[string]string{"shared/other.yaml": "a: 2\n"},
			stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
			pullDir: "stackA",
			prep:    prepLinks(map[string]string{"stackA/compose.yaml": outside}),
		})
		if len(got.failed) != 0 || got.dockerSaw("A") || got.ar.Outcome != "success" {
			t.Fatalf("outside-repo link: failed=%v docker=%q outcome=%s reason=%q", got.failed, got.docker, got.ar.Outcome, got.ar.Reason)
		}
	})
}

// The pull deleting the link's target leaves the link dangling. The stack's
// compose file is gone as far as docker is concerned, so the redeploy is
// attempted and fails loudly rather than being skipped as unchanged.
func TestPull_SymlinkTargetDeletedRedeploys_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"shared/compose.yaml": composeOld4d60, "stackA/other.txt": "x\n"},
			changes: map[string]string{"shared/compose.yaml": ""},
			stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
			pullDir: "stackA",
			prep:    prepLinks(map[string]string{"stackA/compose.yaml": "../shared/compose.yaml"}),
		})
		if !got.redeployed("A") || !got.dockerSaw("A") {
			t.Fatalf("a deleted link target was not acted on: failed=%v reason=%v outcome=%s", got.failed, got.reason, got.ar.Outcome)
		}
	})
}

// A link loop must not fail the pull or hide the stack: the loop's stack is
// reported (details.failedRedeploys, outcome partial, no docker call for it)
// and the repository's other stack is still checked and redeployed.
func TestPull_SymlinkLoopReportedNotSkippedRedeploy_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"c.yaml": composeOld4d60, "shared/other.yaml": "a: 1\n"},
			changes: map[string]string{"c.yaml": composeNew4d60},
			stacks: []stack4d60{
				{id: "A", dir: "", compose: "compose.yaml"},
				{id: "C", dir: "", compose: "c.yaml"},
			},
			prep: prepLinks(map[string]string{
				"compose.yaml":      "shared/loop1.yaml",
				"shared/loop1.yaml": "loop2.yaml",
				"shared/loop2.yaml": "loop1.yaml",
			}),
		})
		if got.ar.Outcome != "partial" || !strings.Contains(got.reason["A"], "symbolic links") || got.dockerSaw("A") {
			t.Fatalf("loop stack: outcome=%s reasons=%v docker=%q", got.ar.Outcome, got.reason, got.docker)
		}
		// C's redeploy was attempted (the fake docker always fails it).
		if !got.dockerSaw("C") || strings.Contains(got.reason["C"], "symbolic") {
			t.Fatalf("the other stack was not redeployed alongside the loop one: reasons=%v docker=%q", got.reason, got.docker)
		}
	})
}

// eachLock runs f without the operation lock and with it. Production always
// has the lock (main.go SetOperationLock), and the lock decides which code
// places the stacks: with it every stack is placed and walked before the pull,
// without it after (agent-os-juq9 review S2).
func eachLock(t *testing.T, f func(t *testing.T, lock bool)) {
	t.Helper()
	for _, lock := range []bool{false, true} {
		name := "unlocked"
		if lock {
			name = "locked"
		}
		t.Run(name, func(t *testing.T) { f(t, lock) })
	}
}

// removeInSeed deletes a path in the seed without committing it, so the
// `changes` commit carries the deletion (see seedLinks).
func removeInSeed(t *testing.T, work, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(filepath.Dir(work), "seed", filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// deletedDirLink is the shape where the pull deletes a FOLDER link on the
// compose file's path: shared -> real, stackA/compose.yaml ->
// ../shared/compose.yaml, and the pull deletes `shared`. git lists only
// "shared"; the compose file now dangles (agent-os-juq9 review S1).
func deletedDirLink(pullDir string, stacks []stack4d60, lock bool) pull4d60 {
	return pull4d60{
		lock:    lock,
		files:   map[string]string{"real/compose.yaml": composeOld4d60, "plain/compose.yaml": composeOld4d60},
		changes: map[string]string{},
		stacks:  stacks,
		pullDir: pullDir,
		prep: func(t *testing.T, work string) {
			seedLinks(t, work, map[string]string{"shared": "real", "stackA/compose.yaml": "../shared/compose.yaml"}, true)
			removeInSeed(t, work, "shared")
		},
	}
}

// The compose file is gone after the pull, so the redeploy is attempted and
// fails (the fake docker always does): the result is a failedRedeploy and a
// partial outcome, never a silent success.
func TestPull_SymlinkedDirLinkDeletedRedeploys_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, deletedDirLink("stackA", []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}}, lock))
		if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "shared" {
			t.Fatalf("ChangedFiles = %q, want [shared]", got.pr.ChangedFiles)
		}
		if !got.redeployed("A") || !got.dockerSaw("A") || got.ar.Outcome != "partial" {
			t.Fatalf("deleting the folder link on the compose path did not redeploy: failed=%v reason=%v docker=%q outcome=%s",
				got.failed, got.reason, got.docker, got.ar.Outcome)
		}
	})
}

// A folder link on the compose path becomes a real folder without the file:
// git lists "X" and "X/other.txt", never X/compose.yaml. Only the walk made
// before the pull knows the stack read through X, so this needs the lock, as
// production has.
func TestPull_SymlinkedDirLinkBecomesDirRedeploys_Juq9(t *testing.T) {
	got := run4d60(t, pull4d60{
		lock:    true,
		files:   map[string]string{"Y/compose.yaml": composeOld4d60},
		changes: map[string]string{},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
		prep: func(t *testing.T, work string) {
			seedLinks(t, work, map[string]string{"X": "Y", "stackA/compose.yaml": "../X/compose.yaml"}, true)
			seed := filepath.Join(filepath.Dir(work), "seed")
			removeInSeed(t, work, "X")
			if err := os.MkdirAll(filepath.Join(seed, "X"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(seed, "X", "other.txt"), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	})
	if !got.redeployed("A") || !got.dockerSaw("A") || got.ar.Outcome != "partial" {
		t.Fatalf("a folder link replaced by a folder without the file did not redeploy: changed=%q failed=%v reason=%v docker=%q outcome=%s",
			got.pr.ChangedFiles, got.failed, got.reason, got.docker, got.ar.Outcome)
	}
}

// A link target given as an absolute path inside the repository.
func TestPull_SymlinkAbsoluteInRepoTargetRedeploys_Juq9(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"shared/compose.yaml": composeOld4d60},
			changes: map[string]string{"shared/compose.yaml": composeNew4d60},
			stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
			pullDir: "stackA",
			prep: func(t *testing.T, work string) {
				seedLinks(t, work, map[string]string{"stackA/compose.yaml": filepath.Join(work, "shared", "compose.yaml")}, true)
			},
		})
		if !got.redeployed("A") || !got.dockerSaw("A") {
			t.Fatalf("a change behind an absolute in-repo link did not redeploy: changed=%q failed=%v", got.pr.ChangedFiles, got.failed)
		}
	})
}

// linkLoopThenFolder is the shape where the walk before the pull fails and
// the one after it is clean and matches nothing: X <-> X2 is a loop on the
// compose path, then the pull turns X into a real folder holding only
// other.txt. git lists "X" and "X/other.txt"; the post walk records
// X/compose.yaml, which is missing and not listed. Only the pre-pull error
// says this stack's files were unreadable, and dropping it would read the
// stack as unchanged.
func linkLoopThenFolder(t *testing.T, work string) {
	t.Helper()
	seedLinks(t, work, map[string]string{"X": "X2", "X2": "X", "stackA/compose.yaml": "../X/compose.yaml"}, true)
	removeInSeed(t, work, "X")
	seed := filepath.Join(filepath.Dir(work), "seed")
	if err := os.MkdirAll(filepath.Join(seed, "X"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "X", "other.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The requested stack is reported, never read as unchanged: a failedRedeploy
// with the loop error, outcome partial, no docker call for it. Locked only: the
// pre-pull walk exists only with the lock, as in production.
func TestPull_SymlinkPreWalkErrorCleanPostWalkRedeploy_Juq9(t *testing.T) {
	got := run4d60(t, pull4d60{
		lock:    true,
		files:   map[string]string{"keep.txt": "k\n"},
		changes: map[string]string{},
		stacks:  []stack4d60{{id: "A", dir: "stackA", compose: "compose.yaml"}},
		pullDir: "stackA",
		prep:    linkLoopThenFolder,
	})
	if got.ar.Outcome != "partial" || !strings.Contains(got.reason["A"], "symbolic links") || got.dockerSaw("A") {
		t.Fatalf("a pre-pull walk error was lost: changed=%q failed=%v reason=%v docker=%q outcome=%s",
			got.pr.ChangedFiles, got.failed, got.reason, got.docker, got.ar.Outcome)
	}
}
