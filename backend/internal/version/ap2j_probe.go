package version

import "os"

// ap2jProbe exists only to prove that a new blank-discarded error is BLOCKED
// from merging, not merely marked red (agent-os-ap2j). This branch is never merged.
func ap2jProbe() string {
	f, _ := os.Open("/nonexistent/agent-os-ap2j")
	if f != nil {
		_ = f.Close()
	}
	return "probe"
}
