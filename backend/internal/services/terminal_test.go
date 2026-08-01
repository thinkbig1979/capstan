package services

import (
	"errors"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
)

// TestCreateSessionEnforcesHostWideCeiling covers the ceiling that sits behind
// the handler's per-user WebSocket cap. The per-user cap scales with the number
// of users; the host does not, and every session is a real `docker exec`
// process with a PTY held for up to SessionTimeout (agent-os-a0y).
//
// The limit is set to zero so the refusal happens before any process is
// spawned — a test that first opened MaxConcurrentSessions real shells would
// need a live daemon and would leave processes behind.
func TestCreateSessionEnforcesHostWideCeiling(t *testing.T) {
	svc := NewTerminalService(&config.Config{})
	svc.maxSessions = 0

	_, err := svc.CreateSession("proj-a", "proj-a-web-1")
	if err == nil {
		t.Fatal("expected the host-wide ceiling to refuse the session")
	}

	var tooMany TooManySessionsError
	if !errors.As(err, &tooMany) {
		t.Fatalf("error %v does not report itself as a session-limit error, so the handler cannot map it to close code 4429", err)
	}
	if tooMany.TooManySessions() != 0 {
		t.Errorf("reported limit = %d, want 0", tooMany.TooManySessions())
	}
	if svc.SessionCount() != 0 {
		t.Error("a refused session was still recorded")
	}
}

// TestCreateSessionRejectsInvalidContainerNameBeforeCeiling keeps the existing
// name validation ahead of the new check, so a malformed name still reports as
// malformed rather than as a limit problem.
func TestCreateSessionRejectsInvalidContainerName(t *testing.T) {
	svc := NewTerminalService(&config.Config{})

	_, err := svc.CreateSession("proj-a", "bad;name")
	if err == nil {
		t.Fatal("expected an invalid container name to be rejected")
	}

	var tooMany TooManySessionsError
	if errors.As(err, &tooMany) {
		t.Errorf("invalid name reported as a session-limit error: %v", err)
	}
	if svc.SessionCount() != 0 {
		t.Error("a refused session was still recorded")
	}
}

// TestDefaultServiceUsesTheExportedCeiling guards against the constructor
// drifting from the documented constant.
func TestDefaultServiceUsesTheExportedCeiling(t *testing.T) {
	svc := NewTerminalService(&config.Config{})
	if svc.maxSessions != MaxConcurrentSessions {
		t.Errorf("maxSessions = %d, want MaxConcurrentSessions (%d)", svc.maxSessions, MaxConcurrentSessions)
	}
}
