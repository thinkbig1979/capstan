package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kr/pty"
	"github.com/thinkbig1979/capstan/backend/internal/config"
)

var validContainerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

const SessionTimeout = 30 * time.Minute
const ReaperInterval = 60 * time.Second

// MaxConcurrentSessions is a host-wide ceiling on live PTY sessions, on top of
// the per-user WebSocket cap in the handler.
//
// The per-user cap is what a well-behaved client hits; this is what protects the
// host. They coincide on a single-user deployment — the common case here — but
// the per-user cap scales with the number of users and the machine does not.
// Every session is a real `docker exec` process with a PTY and file
// descriptors, held for up to SessionTimeout (agent-os-a0y).
const MaxConcurrentSessions = 20

type TerminalSession struct {
	ID            string
	StackID       string
	ContainerName string
	Cmd           *exec.Cmd
	Pty           *os.File
	lastActivity  time.Time
}

type TerminalService struct {
	sessions    map[string]*TerminalSession
	config      *config.Config
	maxSessions int
	mu          sync.Mutex
}

func NewTerminalService(cfg *config.Config) *TerminalService {
	return &TerminalService{
		sessions:    make(map[string]*TerminalSession),
		config:      cfg,
		maxSessions: MaxConcurrentSessions,
	}
}

func (s *TerminalService) CreateSession(stackID, containerName string) (*TerminalSession, error) {
	if !validContainerNameRegex.MatchString(containerName) {
		return nil, &invalidContainerNameError{containerName}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Checked under the same lock that guards the map, and before any process is
	// spawned — a check outside the lock would let concurrent connects race past
	// the ceiling.
	if len(s.sessions) >= s.maxSessions {
		return nil, &tooManySessionsError{limit: s.maxSessions}
	}

	session := &TerminalSession{
		ID:            uuid.New().String(),
		StackID:       stackID,
		ContainerName: containerName,
		lastActivity:  time.Now(),
	}

	shells := []string{"/bin/sh", "/bin/bash"}
	var err error
	var ptyFile *os.File

	for _, shell := range shells {
		//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
		cmd := execCommand("docker", "exec", "-it", "--", containerName, shell)
		cmd.Env = dockerEnv()

		ptyFile, err = pty.Start(cmd)
		if err == nil {
			session.Cmd = cmd
			session.Pty = ptyFile
			break
		}
		slog.Debug("Failed to create PTY session", "shell", shell, "error", err)
	}

	if session.Pty == nil {
		return nil, err
	}

	s.sessions[session.ID] = session
	return session, nil
}

// SessionCount reports the number of live PTY sessions. Every session is a
// spawned `docker exec` process, so a test can assert that a rejected request
// spawned nothing by asserting this stayed at zero.
func (s *TerminalService) SessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *TerminalService) GetSession(sessionID string) *TerminalSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sessionID]
}

func (s *TerminalService) ResizeSession(sessionID string, cols, rows uint16) error {
	s.mu.Lock()
	session := s.sessions[sessionID]
	s.mu.Unlock()

	if session == nil {
		return &sessionNotFoundError{sessionID}
	}

	return pty.Setsize(session.Pty, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}

func (s *TerminalService) CloseSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.sessions[sessionID]
	if session == nil {
		return
	}

	if session.Cmd != nil && session.Cmd.Process != nil {
		// Best-effort; the session is being torn down regardless, and a
		// failed kill here (process already exited) isn't actionable.
		_ = session.Cmd.Process.Kill()
	}
	if session.Pty != nil {
		session.Pty.Close()
	}
	delete(s.sessions, sessionID)
}

func (s *TerminalService) UpdateActivity(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if session, exists := s.sessions[sessionID]; exists {
		session.lastActivity = time.Now()
	}
}

func (s *TerminalService) StartReaper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(ReaperInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.reapExpiredSessions()
			}
		}
	}()
}

func (s *TerminalService) reapExpiredSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, session := range s.sessions {
		if now.Sub(session.lastActivity) > SessionTimeout {
			slog.Info("Reaping inactive terminal session", "session_id", id, "stack_id", session.StackID)
			if session.Cmd != nil && session.Cmd.Process != nil {
				// Best-effort; the session is being torn down regardless,
				// and a failed kill here (process already exited) isn't
				// actionable.
				_ = session.Cmd.Process.Kill()
			}
			if session.Pty != nil {
				session.Pty.Close()
			}
			delete(s.sessions, id)
		}
	}
}

type sessionNotFoundError struct {
	sessionID string
}

func (e *sessionNotFoundError) Error() string {
	return "session not found: " + e.sessionID
}

type invalidContainerNameError struct {
	name string
}

func (e *invalidContainerNameError) Error() string {
	return "invalid container name: " + e.name
}

// TooManySessionsError reports that the host-wide session ceiling is reached.
// Exposed as an interface check so the handler can map it to a rate-limit close
// code rather than a generic failure.
type TooManySessionsError interface {
	error
	TooManySessions() int
}

type tooManySessionsError struct {
	limit int
}

func (e *tooManySessionsError) Error() string {
	return fmt.Sprintf("terminal session limit reached (%d concurrent sessions)", e.limit)
}

func (e *tooManySessionsError) TooManySessions() int { return e.limit }
