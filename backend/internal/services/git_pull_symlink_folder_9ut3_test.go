package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin agent-os-9ut3: a pull whose only change to a stack is
// retargeting its symlinked FOLDER reported the stack unchanged. git lists the
// link path (stackB) and nothing under it; the requested folder's redeploy
// matching and siblingPlacement.changed both looked for compose/env files
// under the placed (resolved) folder, never for the link itself.
//
// The link is retargeted in the seed without a commit, so it rides into the
// `changes` commit (see seedLinks).

func siblingIDs9ut3(t *testing.T, p pulled4d60) []string {
	t.Helper()
	sibs, ok := p.ar.Details["siblingStacksChanged"].([]SiblingStack)
	if !ok {
		t.Fatalf("details.siblingStacksChanged = %#v, want []SiblingStack", p.ar.Details["siblingStacksChanged"])
	}
	ids := []string{}
	for _, s := range sibs {
		ids = append(ids, s.StackID)
	}
	return ids
}

var folderFiles9ut3 = map[string]string{
	"folderX/compose.yaml": composeOld4d60,
	"folderY/compose.yaml": composeNew4d60,
	"plain/compose.yaml":   composeOld4d60,
}

func retargetStackB9ut3(target string) func(t *testing.T, work string) {
	return func(t *testing.T, work string) {
		seedLinks(t, work, map[string]string{"stackB": "folderX"}, true)
		seedLinks(t, work, map[string]string{"stackB": target}, false)
	}
}

func TestPullRedeploy_RetargetedFolderLink_9ut3(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:  lock,
			files: folderFiles9ut3, changes: map[string]string{},
			stacks:  []stack4d60{{id: "B", dir: "stackB", compose: "compose.yaml"}},
			pullDir: "stackB", prep: retargetStackB9ut3("folderY"),
		})
		// The premise, observed: the only changed path is the link itself.
		if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "stackB" {
			t.Fatalf("ChangedFiles = %q, want [stackB]", got.pr.ChangedFiles)
		}
		if !got.redeployed("B") || !got.dockerSaw("B") {
			t.Fatalf("retargeting the requested stack's folder link did not redeploy it: failed=%v docker=%q outcome=%s",
				got.failed, got.docker, got.ar.Outcome)
		}
	})
}

func TestPullSiblings_RetargetedFolderLink_9ut3(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		stacks := []stack4d60{
			{id: "P", dir: "plain", compose: "compose.yaml"},
			{id: "B", dir: "stackB", compose: "compose.yaml"},
		}
		// Both shapes the bead names: moved to another folder, and moved out of
		// the repository, to a target that does not exist and to one that
		// does (agent-os-7xpm: git tracks the link, so the folder stays a
		// member of the repository wherever it points).
		for name, target := range map[string]string{
			"another folder":                     "folderY",
			"out of the repository":              "/nonexistent-9ut3/stack",
			"out of the repository, live target": t.TempDir(),
		} {
			got := run4d60(t, pull4d60{
				lock:  lock,
				files: folderFiles9ut3, changes: map[string]string{},
				stacks: stacks, pullDir: "plain", prep: retargetStackB9ut3(target),
			})
			if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "stackB" {
				t.Fatalf("%s: ChangedFiles = %q, want [stackB]", name, got.pr.ChangedFiles)
			}
			if ids := siblingIDs9ut3(t, got); len(ids) != 1 || ids[0] != "B" {
				t.Fatalf("%s: siblingStacksChanged = %v, want [B]; outcome=%s details=%v", name, ids, got.ar.Outcome, got.ar.Details)
			}
		}

		// Control, same instrument: a change that touches no link leaves the
		// sibling out, so the match is the link path and not "anything changed".
		ctrl := run4d60(t, pull4d60{
			lock:  lock,
			files: folderFiles9ut3, changes: map[string]string{"plain/compose.yaml": composeNew4d60},
			stacks: stacks, pullDir: "plain", prep: prepLinks(map[string]string{"stackB": "folderX"}),
		})
		if ids := siblingIDs9ut3(t, ctrl); len(ids) != 0 {
			t.Fatalf("control: siblingStacksChanged = %v, want none", ids)
		}
	})
}

// The loop arm for siblings: a sibling whose compose link loops is a
// siblingCheckFailure (outcome partial), never silently "unchanged".
func TestPullSiblings_SymlinkLoopIsReported_9ut3(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"plain/compose.yaml": composeOld4d60},
			changes: map[string]string{"plain/compose.yaml": composeNew4d60},
			stacks: []stack4d60{
				{id: "P", dir: "plain", compose: "compose.yaml"},
				{id: "L", dir: "loopy", compose: "compose.yaml"},
			},
			pullDir: "plain",
			prep: prepLinks(map[string]string{
				"loopy/compose.yaml": "a.yaml", "loopy/a.yaml": "b.yaml", "loopy/b.yaml": "a.yaml",
			}),
		})
		fails, _ := got.ar.Details["siblingCheckFailures"].([]RedeployFailure)
		if got.ar.Outcome != "partial" || len(fails) != 1 || fails[0].StackID != "L" {
			t.Fatalf("outcome=%s siblingCheckFailures=%v", got.ar.Outcome, got.ar.Details["siblingCheckFailures"])
		}
	})
}

// A sibling's compose file reads through a folder link the pull deletes: git
// lists only the link, and the compose file dangles afterwards.
func TestPullSiblings_DeletedDirLinkOnComposePath_9ut3(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, deletedDirLink("plain", []stack4d60{
			{id: "P", dir: "plain", compose: "compose.yaml"},
			{id: "A", dir: "stackA", compose: "compose.yaml"},
		}, lock))
		if ids := siblingIDs9ut3(t, got); len(ids) != 1 || ids[0] != "A" {
			t.Fatalf("siblingStacksChanged = %v, want [A]; changed=%q fails=%v", ids, got.pr.ChangedFiles, got.ar.Details["siblingCheckFailures"])
		}
	})
}

// A sibling's compose link dangles before the pull (X is missing); the pull
// adds X as a link to a folder that holds the file. Only a walk made after
// the pull sees the stack read through X.
func TestPullSiblings_DanglingLinkFixedByPull_9ut3(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"Y/compose.yaml": composeOld4d60, "plain/compose.yaml": composeOld4d60},
			changes: map[string]string{},
			stacks: []stack4d60{
				{id: "P", dir: "plain", compose: "compose.yaml"},
				{id: "S", dir: "stackS", compose: "compose.yaml"},
			},
			pullDir: "plain",
			prep: func(t *testing.T, work string) {
				seedLinks(t, work, map[string]string{"stackS/compose.yaml": "../X/compose.yaml"}, true)
				seedLinks(t, work, map[string]string{"X": "Y"}, false)
			},
		})
		if ids := siblingIDs9ut3(t, got); len(ids) != 1 || ids[0] != "S" {
			t.Fatalf("siblingStacksChanged = %v, want [S]; changed=%q", ids, got.pr.ChangedFiles)
		}
	})
}

// A sibling's folder link on the compose path becomes a real folder without
// the file: git lists "X" and "X/other.txt", never X/compose.yaml. Only the
// walk made before the pull knows the sibling read through X, so this runs
// with the lock, as production does.
func TestPullSiblings_LinkBecomesDirOnComposePath_9ut3(t *testing.T) {
	got := run4d60(t, pull4d60{
		lock:    true,
		files:   map[string]string{"Y/compose.yaml": composeOld4d60, "plain/compose.yaml": composeOld4d60},
		changes: map[string]string{},
		stacks: []stack4d60{
			{id: "P", dir: "plain", compose: "compose.yaml"},
			{id: "S", dir: "stackS", compose: "compose.yaml"},
		},
		pullDir: "plain",
		prep: func(t *testing.T, work string) {
			seedLinks(t, work, map[string]string{"X": "Y", "stackS/compose.yaml": "../X/compose.yaml"}, true)
			removeInSeed(t, work, "X")
			seed := filepath.Join(filepath.Dir(work), "seed")
			if err := os.MkdirAll(filepath.Join(seed, "X"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(seed, "X", "other.txt"), []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	})
	if ids := siblingIDs9ut3(t, got); len(ids) != 1 || ids[0] != "S" {
		t.Fatalf("siblingStacksChanged = %v, want [S]; changed=%q", ids, got.pr.ChangedFiles)
	}
}

// The pull fixes a sibling's link loop. Before the pull its walk failed, after
// it the walk crosses the fixed link, which git lists: the sibling changed, and
// is not reported with the old loop error.
func TestPullSiblings_LoopFixedByPull_9ut3(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:    lock,
			files:   map[string]string{"plain/compose.yaml": composeOld4d60, "loopy/real.yaml": composeOld4d60},
			changes: map[string]string{},
			stacks: []stack4d60{
				{id: "P", dir: "plain", compose: "compose.yaml"},
				{id: "L", dir: "loopy", compose: "compose.yaml"},
			},
			pullDir: "plain",
			prep: func(t *testing.T, work string) {
				seedLinks(t, work, map[string]string{"loopy/compose.yaml": "a.yaml", "loopy/a.yaml": "b.yaml", "loopy/b.yaml": "a.yaml"}, true)
				seedLinks(t, work, map[string]string{"loopy/b.yaml": "real.yaml"}, false)
			},
		})
		if ids := siblingIDs9ut3(t, got); len(ids) != 1 || ids[0] != "L" || got.ar.Details["siblingCheckFailures"] != nil {
			t.Fatalf("siblingStacksChanged = %v, want [L]; changed=%q fails=%v", ids, got.pr.ChangedFiles, got.ar.Details["siblingCheckFailures"])
		}
	})
}

// The sibling form of the pre-walk-error shape (see linkLoopThenFolder): the
// sibling is a siblingCheckFailure, never absent from both lists.
func TestPullSiblings_PreWalkErrorCleanPostWalk_9ut3(t *testing.T) {
	got := run4d60(t, pull4d60{
		lock:    true,
		files:   map[string]string{"plain/compose.yaml": composeOld4d60},
		changes: map[string]string{"plain/compose.yaml": composeNew4d60},
		stacks: []stack4d60{
			{id: "P", dir: "plain", compose: "compose.yaml"},
			{id: "A", dir: "stackA", compose: "compose.yaml"},
		},
		pullDir: "plain",
		prep:    linkLoopThenFolder,
	})
	fails, _ := got.ar.Details["siblingCheckFailures"].([]RedeployFailure)
	if ids := siblingIDs9ut3(t, got); len(ids) != 0 || len(fails) != 1 || fails[0].StackID != "A" || !strings.Contains(fails[0].Reason, "symbolic links") {
		t.Fatalf("a pre-pull walk error was lost: siblings=%v fails=%v changed=%q", ids, got.ar.Details["siblingCheckFailures"], got.pr.ChangedFiles)
	}
}

// agent-os-vysd, agent-os-7xpm: a sibling's folder is a committed link that
// resolves OUTSIDE the repository before the pull, and the pull retargets it
// to a folder inside. git lists only the link. The stack is a member of the
// repository by the link's location, so it is locked and placed before the
// pull (deps only, no folder), and the retarget lists it. Both an existing
// and a dangling outside target, locked (production) and unlocked.
func TestPullSiblings_OutsideFolderLinkRetargetedIn_vysd(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		stacks := []stack4d60{
			{id: "P", dir: "plain", compose: "compose.yaml"},
			{id: "B", dir: "stackB", compose: "compose.yaml"},
		}
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"compose.yaml", "sub/compose.yaml"} {
			if err := os.WriteFile(filepath.Join(outside, f), []byte(composeOld4d60), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		files := map[string]string{"folderY/sub/compose.yaml": composeNew4d60}
		for k, v := range folderFiles9ut3 {
			files[k] = v
		}
		// The stack folder is the link itself (stackB), or lies under it
		// (stackB/sub: the link is an ancestor, not the parent; review
		// rv-dm-gitpull F1). On the locked path siblings=[B] also proves B
		// was locked: only lockRepoStacks' list is placed before the pull.
		for _, dir := range []string{"stackB", "stackB/sub"} {
			stacks := []stack4d60{stacks[0], {id: "B", dir: dir, compose: "compose.yaml"}}
			for name, from := range map[string]string{"live outside target": outside, "dangling outside target": "/nonexistent-vysd/stack"} {
				name := dir + ", " + name
				got := run4d60(t, pull4d60{
					lock:  lock,
					files: files, changes: map[string]string{},
					stacks: stacks, pullDir: "plain",
					prep: func(t *testing.T, work string) {
						seedLinks(t, work, map[string]string{"stackB": from}, true)
						seedLinks(t, work, map[string]string{"stackB": "folderY"}, false)
					},
				})
				if len(got.pr.ChangedFiles) != 1 || got.pr.ChangedFiles[0] != "stackB" {
					t.Fatalf("%s: ChangedFiles = %q, want [stackB]", name, got.pr.ChangedFiles)
				}
				if ids := siblingIDs9ut3(t, got); len(ids) != 1 || ids[0] != "B" || got.ar.Details["siblingCheckFailures"] != nil {
					t.Fatalf("%s: siblingStacksChanged = %v, want [B]; outcome=%s details=%v", name, ids, got.ar.Outcome, got.ar.Details)
				}
			}
		}

		// Control, same instrument: the outside link is left alone and another
		// file changes. The stack is placed with no folder, which is neither a
		// change nor a failure.
		ctrl := run4d60(t, pull4d60{
			lock:  lock,
			files: folderFiles9ut3, changes: map[string]string{"plain/compose.yaml": composeNew4d60},
			stacks: stacks, pullDir: "plain", prep: prepLinks(map[string]string{"stackB": outside}),
		})
		if ids := siblingIDs9ut3(t, ctrl); len(ids) != 0 || ctrl.ar.Details["siblingCheckFailures"] != nil || ctrl.ar.Details["siblingListError"] != nil {
			t.Fatalf("control: siblingStacksChanged = %v, want none and no sibling failure; details=%v", ids, ctrl.ar.Details)
		}
	})
}

// Pin (agent-os-vysd D3): the pull retargets the REQUESTED stack's folder link
// out of the repository. The stack is never reported unchanged or redeployed
// from outside. OBSERVED on cce3cd0: pullCLI's post-pull `rev-parse HEAD` runs
// in the folder, which is no longer in a repository, so the whole pull answers
// failed (with a reason that does not say the pull landed; a follow-up), and
// nothing is redeployed, locked or not.
func TestPullRedeploy_FolderLinkRetargetedOut_vysd(t *testing.T) {
	eachLock(t, func(t *testing.T, lock bool) {
		got := run4d60(t, pull4d60{
			lock:  lock,
			files: folderFiles9ut3, changes: map[string]string{},
			stacks:  []stack4d60{{id: "B", dir: "stackB", compose: "compose.yaml"}},
			pullDir: "stackB", prep: retargetStackB9ut3(t.TempDir()),
		})
		if got.ar.Outcome != "failed" || got.redeployed("B") || got.dockerSaw("B") {
			t.Fatalf("outcome=%s reason=%q err=%v docker=%q, want failed and B not redeployed", got.ar.Outcome, got.ar.Reason, got.ar.Err, got.docker)
		}
		if got.ar.Err == nil || !strings.Contains(got.ar.Err.Error(), "not a git repository") {
			t.Fatalf("err = %v, want the post-pull HEAD read to fail outside the repository", got.ar.Err)
		}
	})
}

// stackInRepo: a stack folder is a member when it resolves inside top or when
// ANY of its own path's ancestors inside top is an entry of top, however deep
// the link sits (review rv-dm-gitpull F1).
func TestStackInRepoAncestorLink_7xpm(t *testing.T) {
	top := t.TempDir()
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(out, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(top, "L")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent-7xpm", filepath.Join(top, "D")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		top:                               true,  // top itself
		filepath.Join(top, "L"):           true,  // the link
		filepath.Join(top, "L", "a"):      true,  // under the link
		filepath.Join(top, "L", "a", "b"): true,  // two levels under
		filepath.Join(top, "D", "x", "y"): true,  // under a dangling link
		filepath.Join(top, "new", "f"):    true,  // not yet existing
		filepath.Join(out, "a"):           false, // unrelated outside folder
		filepath.Join(filepath.Dir(top)):  false, // top's parent
	}
	for dir, want := range cases {
		got, err := stackInRepo(top, dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if got != want {
			t.Errorf("stackInRepo(top, %s) = %v, want %v", dir, got, want)
		}
	}
}
