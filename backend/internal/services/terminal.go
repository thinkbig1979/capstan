package services

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/docker-manager/backend/internal/config"
	"github.com/google/uuid"
	"github.com/kr/pty"
)

var validContainerNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

const SessionTimeout = 30 * time.Minute
const ReaperInterval = 60 * time.Second

type TerminalSession struct {
	ID            string
	StackID       string
	ContainerName string
	Cmd           *exec.Cmd
	Pty           *os.File
	lastActivity  time.Time
}

type TerminalService struct {
	sessions map[string]*TerminalSession
	config   *config.Config
	mu       sync.Mutex
}

func NewTerminalService(cfg *config.Config) *TerminalService {
	return &TerminalService{
		sessions: make(map[string]*TerminalSession),
		config:   cfg,
	}
}

func (s *TerminalService) CreateSession(stackID, containerName string) (*TerminalSession, error) {
	if !validContainerNameRegex.MatchString(containerName) {
		return nil, &invalidContainerNameError{containerName}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

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
		cmd := exec.Command("docker", "exec", "-it", "--", containerName, shell)

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
		session.Cmd.Process.Kill()
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
				session.Cmd.Process.Kill()
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
