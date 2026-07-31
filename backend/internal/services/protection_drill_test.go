package services

import "testing"

// Deliberate failure for the agent-os-x8n branch-protection drill.
// This branch must never be merged; it exists to observe protection BLOCKING.
func TestBranchProtectionDrillDeliberateFailure(t *testing.T) {
	t.Fatal("deliberate failure: agent-os-x8n branch protection drill")
}
