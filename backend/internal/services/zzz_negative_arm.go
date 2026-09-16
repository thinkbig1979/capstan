package services

import "os"

// NEGATIVE ARM for agent-os-946e criterion 3 -- DELETE WITH THIS BRANCH.
//
// A deliberately MERGED getter site: `if err != nil || <value>` collapses "I
// could not stat it" and "I stat'd it and it is not a directory" into one
// branch. This exists only to prove the getter-errors ratchet can turn the
// required "Build, vet, and unit tests" check RED in CI. A check proven only
// to pass has not been shown to discriminate, which is the defect 946e fixes.
//
// It is a NEW FILE with an UNREFERENCED function on purpose: reverting a real
// site (bareGitBranch) would fail the unit tests, which run BEFORE the ratchet
// step, so the job would stop early and the ratchet would never run -- proving
// nothing about the ratchet at all.
func negativeArmProbe(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}
