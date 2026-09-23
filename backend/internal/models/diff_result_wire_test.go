package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// DiffResult.Commit is always filled on the one production path
// (services/git.go getDiffCLI returns either a populated commit or an error),
// so the generated TypeScript must declare it required. A pointer field let
// tygo emit `commit?: GitCommit` and let a zero DiffResult marshal
// `"commit":null`, neither of which the handler ever sends (agent-os-apmw).
//
// The zero value is the case that discriminates: whatever a future
// constructor forgets to set, the key must still carry an object.
func TestDiffResultCommitIsAlwaysAnObject(t *testing.T) {
	// The populated arm is decoded rather than written as a literal so this
	// file compiles against either field type: that is what lets it go red on
	// the pointer declaration instead of failing to build.
	var populated DiffResult
	if err := json.Unmarshal([]byte(`{"commit":{"hash":"abc"},"diff":"d","files":["a"]}`), &populated); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	cases := []struct {
		name  string
		value DiffResult
	}{
		{"zero value", DiffResult{}},
		{"populated", populated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, present := decoded["commit"]
			if !present {
				t.Fatalf("commit absent from %s", raw)
			}
			if !strings.HasPrefix(string(got), "{") {
				t.Fatalf("commit = %s, want a JSON object", got)
			}
		})
	}
}
