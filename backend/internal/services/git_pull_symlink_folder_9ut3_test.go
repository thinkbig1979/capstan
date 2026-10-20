package services

import (
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
	got := run4d60(t, pull4d60{
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
}

func TestPullSiblings_RetargetedFolderLink_9ut3(t *testing.T) {
	stacks := []stack4d60{
		{id: "P", dir: "plain", compose: "compose.yaml"},
		{id: "B", dir: "stackB", compose: "compose.yaml"},
	}
	// Both shapes the bead names: moved to another folder, and moved out of
	// the repository (the target need not exist).
	for name, target := range map[string]string{"another folder": "folderY", "out of the repository": "/nonexistent-9ut3/stack"} {
		got := run4d60(t, pull4d60{
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
		files: folderFiles9ut3, changes: map[string]string{"plain/compose.yaml": composeNew4d60},
		stacks: stacks, pullDir: "plain", prep: prepLinks(map[string]string{"stackB": "folderX"}),
	})
	if ids := siblingIDs9ut3(t, ctrl); len(ids) != 0 {
		t.Fatalf("control: siblingStacksChanged = %v, want none", ids)
	}
}

// The loop arm for siblings: a sibling whose compose link loops is a
// siblingCheckFailure (outcome partial), never silently "unchanged".
func TestPullSiblings_SymlinkLoopIsReported_9ut3(t *testing.T) {
	got := run4d60(t, pull4d60{
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
}
