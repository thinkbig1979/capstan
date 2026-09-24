package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// GitStatusResult.Commit is always filled: services/git.go getStatusCLI returns
// either a populated commit or an error, on the bare path too. The pointer let
// tygo emit `commit?: GitCommit` and let a zero value marshal `"commit":null`.
// Same shape and fix as DiffResult.Commit (agent-os-apmw); this one is
// type-only, because handlers/git.go builds the status body as a gin.H and
// never marshals the struct (agent-os-xy9j).
//
// The zero value is the case that discriminates.
func TestGitStatusResultCommitIsAlwaysAnObject(t *testing.T) {
	// Decoded rather than a literal so this compiles against either field type
	// and goes red on the pointer declaration instead of failing to build.
	var populated GitStatusResult
	if err := json.Unmarshal([]byte(`{"branch":"main","commit":{"hash":"abc"}}`), &populated); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	cases := []struct {
		name  string
		value GitStatusResult
	}{
		{"zero value", GitStatusResult{}},
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
