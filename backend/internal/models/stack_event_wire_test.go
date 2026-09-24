package models

import (
	"encoding/json"
	"testing"
)

// parseStackEvent (frontend/src/hooks/useStackEvents.ts) reads status as
// required on stack_status and update_job_* frames, and targetType as required
// on update_job_* frames. Every writer of those frame types sets both
// (services/monitor.go stackEventFor, handlers/updates.go
// enqueueJobWithBroadcasts), so the wire must declare them rather than tag
// them omitempty (agent-os-9kp2).
//
// The frames below leave the field at its zero value on purpose: that is the
// case omitempty drops and the only one that can discriminate the tag.
func TestStackEventRequiredKeysAreAlwaysPresent(t *testing.T) {
	cases := []struct {
		name  string
		event StackEvent
		keys  []string
	}{
		{"stack_status", StackEvent{Type: "stack_status", StackID: "s1"}, []string{"status"}},
		{"update_job_progress", StackEvent{Type: "update_job_progress", JobID: "j1"}, []string{"status", "targetType"}},
		{"update_job_complete", StackEvent{Type: "update_job_complete", JobID: "j1"}, []string{"status", "targetType"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.event)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for _, k := range tc.keys {
				if _, ok := fields[k]; !ok {
					t.Errorf("%s frame %s: key %q missing", tc.name, raw, k)
				}
			}
		})
	}
}
