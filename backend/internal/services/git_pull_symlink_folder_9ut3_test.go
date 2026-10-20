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
		// the repository (the target need not exist).
		for name, target := range map[string]string{"another folder": "folderY", "out of the repository": "/nonexistent-9ut3/stack"} {
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
