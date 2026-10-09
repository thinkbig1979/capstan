package main

import "testing"

// TestAnalyzersRegistered: an analyzer left out of the vettool binary reports
// nothing, which looks exactly like a clean tree.
func TestAnalyzersRegistered(t *testing.T) {
	got := map[string]bool{}
	for _, a := range analyzers {
		got[a.Name] = true
	}
	for _, name := range []string{"geterrors", "timebind", "wirevalue"} {
		if !got[name] {
			t.Errorf("analyzer %q is not registered in cmd/geterrors", name)
		}
	}
}
