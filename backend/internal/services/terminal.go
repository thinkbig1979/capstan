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

// sessionEnvVar is set (to the session's own ID) on the shell's environment
// when it is exec'd into the container, so CloseSession can find it again
// later purely from the container's own /proc — see reapContainerShell.
const sessionEnvVar = "CAPSTAN_SESSION"

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
		// -e tags the exec'd shell's environment with this session's own ID,
		// so CloseSession can find (and kill) it again later purely via the
		// container's own /proc — see reapContainerShell. session.ID is a
		// uuid.New() value, never attacker-controlled, so no shell-metachar
		// concerns even though it flows into a script string downstream.
		//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
		cmd := execCommand("docker", "exec", "-it", "-e", sessionEnvVar+"="+session.ID, "--", containerName, shell)
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

	// CloseSession has no incoming context (the handler calls it via a plain
	// `defer h.terminal.CloseSession(session.ID)`), so this is the natural
	// origination point for one; reapExpiredSessions instead threads through
	// the context StartReaper was given.
	s.terminateSession(context.Background(), sessionID, session)
}

// terminateSession kills a session's local `docker exec` CLI process and pty,
// reaps the shell (and any descendants) running INSIDE the container, and
// removes it from the map. Callers must hold s.mu.
func (s *TerminalService) terminateSession(ctx context.Context, sessionID string, session *TerminalSession) {
	if session.Cmd != nil && session.Cmd.Process != nil {
		// Best-effort; the session is being torn down regardless, and a
		// failed kill here (process already exited) isn't actionable. This
		// only ever kills the LOCAL `docker exec` CLI — it does not reach the
		// shell running inside the container, which is why
		// reapContainerShell below exists.
		_ = session.Cmd.Process.Kill()
	}
	if session.Pty != nil {
		session.Pty.Close()
	}
	s.reapContainerShell(ctx, session.ContainerName, session.ID)
	delete(s.sessions, sessionID)
}

// reapContainerShell SIGKILLs the shell CreateSession spawned inside
// containerName, plus any descendant that inherited its environment (e.g. a
// backgrounded `sleep 400 &`), by running the search-and-kill as a fresh
// `docker exec` rather than signalling a pid directly from this process: this
// service's own process is not guaranteed to share a PID namespace with the
// target container (e.g. Docker-out-of-Docker deployments, where this service
// and every managed container are sibling containers under one host daemon),
// so a plain syscall-level kill on a pid captured earlier would either fail
// or — worse — silently signal an unrelated process if the number had since
// been reused in this process's own namespace.
//
// Identification uses the CAPSTAN_SESSION=<sessionID> marker CreateSession set
// on the shell's environment (via `docker exec -e`), found by scanning
// /proc/*/environ from INSIDE the container — not `ps`, which a mainstream
// base image can lack (verified: debian:stable-slim has a shell but no `ps`
// binary at all, so a `ps`-based lookup silently applies no fix on it). /proc
// is always present wherever /bin/sh is (CreateSession already required a
// shell to exist to get this far), and every matching pid is killed, not just
// the first: a child process normally inherits its parent's environment, so
// this also catches a backgrounded child (e.g. `sleep 400 &`, verified) by
// construction — no process-group guess needed, and no ambiguity to give up
// on, since a per-session UUID cannot collide with an unrelated process.
// SIGKILL cannot be caught or ignored, so this also reaps a shell currently
// running a foreground full-screen program (vim, top, ssh, ...), unlike
// writing "exit\n" to the pty.
//
// `grep -a` reads /proc/<pid>/environ (NUL-separated key=value entries)
// directly — no `tr`/`grep -z` juggling needed to split it into lines first.
// The pid-1 check is defence in depth: `docker exec -e` scopes the marker to
// the exec'd process and its descendants, so pid 1 (the container's own init)
// should never match, but killing it would take down the whole container, so
// it is excluded unconditionally regardless.
//
// Best-effort: errors (container already gone, docker unreachable, no match
// found) are logged, not returned — CloseSession/reapExpiredSessions tear the
// session down from this service's side regardless.
func (s *TerminalService) reapContainerShell(ctx context.Context, containerName, sessionID string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	script := fmt.Sprintf(`for d in /proc/[0-9]*; do
  pid="${d#/proc/}"
  [ "$pid" = "1" ] && continue
  if grep -qa "%s=%s" "$d/environ" 2>/dev/null; then
    kill -KILL "$pid" 2>/dev/null
  fi
done
exit 0`, sessionEnvVar, sessionID)

	//nolint:gosec // explicit argv; script is built from constant text and a uuid.New() session ID, never attacker-controlled
	cmd := execCommandContext(ctx, "docker", "exec", "--", containerName, "sh", "-c", script)
	cmd.Env = dockerEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Debug("reapContainerShell: docker exec failed", "container", containerName, "session_id", sessionID, "error", err, "output", string(out))
	}
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
				s.reapExpiredSessions(ctx)
			}
		}
	}()
}

func (s *TerminalService) reapExpiredSessions(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for id, session := range s.sessions {
		if now.Sub(session.lastActivity) > SessionTimeout {
			slog.Info("Reaping inactive terminal session", "session_id", id, "stack_id", session.StackID)
			s.terminateSession(ctx, id, session)
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
