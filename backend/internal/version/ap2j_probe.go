package version

import "os"

// ap2jProbe exists only to prove that a new unchecked error is BLOCKED from
// merging, not merely marked red (agent-os-ap2j). Single-value form on purpose:
// the getter-errors ratchet only classifies multi-value assignments, so this
// site is visible to errcheck check-blank ALONE, which makes the golangci job (os.Chdir, not os.Remove: os.Remove is on errcheck's built-in exclude list and reads as a false clean)
// the only red check. This branch is never merged.
func ap2jProbe() {
	_ = os.Chdir("/nonexistent/agent-os-ap2j")
}

var _ = ap2jProbe
