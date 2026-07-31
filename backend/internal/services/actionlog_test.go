package services

import (
	"testing"
)

// Without a shared ID there is no way to join a 500 in the HTTP log to the row
// it produced in action_log, or to follow one user action across several log
// lines (agent-os-7li).

func TestActionLogger_PersistsRequestID(t *testing.T) {
	db := newTestDB(t)
	logger := NewActionLogger(db)
	stackID := "stacks~demo:default"

	logger.LogWithRequest("11111111-2222-4333-8444-555555555555", "user-1", &stackID, ActionGitPull, map[string]string{"detail": "pulled"})

	actions, err := db.GetActionsByStack(stackID, 10)
	if err != nil {
		t.Fatalf("GetActionsByStack: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action row, got %d", len(actions))
	}
	if got := actions[0].RequestID; got != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("action row request ID = %q, want the one supplied at log time", got)
	}
}

// TestActionLogger_LogWithoutRequestID covers background jobs and schedulers,
// which serve no HTTP request. The column is nullable for exactly this reason,
// and the row must still be written.
func TestActionLogger_LogWithoutRequestID(t *testing.T) {
	db := newTestDB(t)
	logger := NewActionLogger(db)
	stackID := "stacks~demo:default"

	logger.Log("system", &stackID, ActionBackup, map[string]string{"kind": "scheduled"})

	actions, err := db.GetActionsByStack(stackID, 10)
	if err != nil {
		t.Fatalf("GetActionsByStack: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("expected the row to be written anyway, got %d rows", len(actions))
	}
	if actions[0].RequestID != "" {
		t.Errorf("expected an empty request ID for a background action, got %q", actions[0].RequestID)
	}
	if actions[0].UserID != "system" {
		t.Errorf("actor = %q, want system", actions[0].UserID)
	}
}
