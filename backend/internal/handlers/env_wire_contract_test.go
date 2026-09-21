package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEnvEntry_SensitiveIsAlwaysOnTheWire pins the half of the EnvEntry wire
// contract that TypeScript declares as REQUIRED: frontend/src/types/index.ts
// declares `sensitive: boolean`, so a response that omits the key makes the
// declared type a lie and every consumer reads the absence as false by
// accident rather than by contract (agent-os-6wrb).
//
// Both arms are asserted on the same instrument: the false entry MUST carry
// "sensitive":false and the true entry MUST carry "sensitive":true. An arm
// that could only come out one way would not discriminate a working tag from
// a broken one.
func TestEnvEntry_SensitiveIsAlwaysOnTheWire(t *testing.T) {
	tests := []struct {
		name  string
		entry EnvEntry
		want  string
	}{
		{
			name:  "non-sensitive entry still carries the key",
			entry: EnvEntry{Key: "PORT", Value: "8080", Line: 1, Sensitive: false},
			want:  `"sensitive":false`,
		},
		{
			name:  "sensitive entry carries the key",
			entry: EnvEntry{Key: "API_KEY", Value: "x", Line: 2, Sensitive: true},
			want:  `"sensitive":true`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.entry)
			if err != nil {
				t.Fatalf("json.Marshal(%+v): %v", tt.entry, err)
			}
			if !strings.Contains(string(b), tt.want) {
				t.Errorf("EnvEntry JSON missing %s\n got: %s", tt.want, b)
			}
		})
	}
}

// TestEnvResponse_SensitiveSurvivesTheRealParsePath runs the same assertion
// through the path a client actually sees — parseEnvFile into EnvResponse —
// so the guarantee is about the response, not only about a hand-built struct.
func TestEnvResponse_SensitiveSurvivesTheRealParsePath(t *testing.T) {
	h := &EnvHandler{}
	entries := h.parseEnvFile("PORT=8080\nAPI_KEY=secret\n")

	b, err := json.Marshal(EnvResponse{HasEnvFile: true, Filename: ".env", Entries: entries})
	if err != nil {
		t.Fatalf("json.Marshal(EnvResponse): %v", err)
	}
	got := string(b)

	if !strings.Contains(got, `"key":"PORT"`) {
		t.Fatalf("fixture did not parse as expected, got: %s", got)
	}
	if strings.Count(got, `"sensitive":`) != len(entries) {
		t.Errorf("every entry must declare sensitive: wanted %d occurrences, got %d\n json: %s",
			len(entries), strings.Count(got, `"sensitive":`), got)
	}
}

// TestEnvEntry_CommentStaysOmitted guards the SCOPE of the fix above rather
// than the fix itself: `comment` is declared OPTIONAL in TypeScript
// (`comment?: boolean`), so Go's `omitempty` there is already correct and
// must not be removed alongside Sensitive's. This assertion holds both
// before and after agent-os-6wrb — it is a scope pin, not the gate.
func TestEnvEntry_CommentStaysOmitted(t *testing.T) {
	b, err := json.Marshal(EnvEntry{Key: "PORT", Value: "8080", Line: 1, Comment: false})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(b), `"comment"`) {
		t.Errorf("comment is optional in TypeScript and must stay omitempty, got: %s", b)
	}
}
