package handlers

import (
	"encoding/json"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// TestUpdateJobsWS_FramesCarryRequiredKeys pins the Go frames to the
// JobStreamFrame union in frontend/src/hooks/useUpdateJobStream.ts: every key
// a variant declares required must be on the wire even when its value is the
// zero value, because parseJobStreamFrame rejects a frame missing one
// (agent-os-onmw).
func TestUpdateJobsWS_FramesCarryRequiredKeys(t *testing.T) {
	cases := []struct {
		name     string
		frame    any
		required []string
		objects  []string
	}{
		{"snapshot", snapshotFrame(services.Job{}), []string{"type", "job"}, []string{"job"}},
		{"line", lineFrame(services.LogLine{}), []string{"type", "line"}, []string{"line"}},
		{"status", statusFrame(""), []string{"type", "status"}, nil},
		{"done", doneFrame("", "", "", ""), []string{"type", "status"}, nil},
		{"error", errorFrame(""), []string{"type", "error"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.frame)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for _, key := range tc.required {
				if _, ok := got[key]; !ok {
					t.Errorf("%s frame %s: missing required key %q", tc.name, raw, key)
				}
			}
			for _, key := range tc.objects {
				if v, ok := got[key]; ok && (len(v) == 0 || v[0] != '{') {
					t.Errorf("%s frame %s: key %q is %s, want a JSON object", tc.name, raw, key, v)
				}
			}
			var typ string
			if err := json.Unmarshal(got["type"], &typ); err != nil || typ != tc.name {
				t.Errorf("type = %q (err %v), want %q", typ, err, tc.name)
			}
		})
	}
}
