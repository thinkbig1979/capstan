package services

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
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

// containerPIDDiscoveryAttempts / containerPIDDiscoveryInterval bound the poll
// that looks up the shell's pid inside the container after spawning it (see
// findContainerPID). The remote shell can take a moment to appear in the
// container's process table after the local `docker exec` CLI has started, so
// a single immediate sample would be timing-sensitive.
const containerPIDDiscoveryAttempts = 20
const containerPIDDiscoveryInterval = 25 * time.Millisecond

type TerminalSession struct {
	ID            string
	StackID       string
	ContainerName string
	Cmd           *exec.Cmd
	Pty           *os.File
	// ContainerPID is the shell's process ID as seen from INSIDE the
	// container's own PID namespace (i.e. what `docker exec <container> ps`
	// reports), not the host-namespace PID `docker top`/`docker inspect`
	// report for the same process — those two numbers differ (verified: a
	// process shown as PID 1 by an in-container `ps` was PID 51264 in
	// `docker top`). CloseSession uses this to kill the shell from within the
	// container's own namespace, which works regardless of whether this
	// service's own process shares the host's PID namespace.
	//
	// Zero means discovery failed or was ambiguous (e.g. the container has no
	// `ps` binary) — CloseSession degrades to its pre-existing local-CLI-only
	// cleanup in that case, never blocking session creation on this being
	// found.
	ContainerPID int
	lastActivity time.Time
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
	var shellUsed string

	for _, shell := range shells {
		//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
		cmd := execCommand("docker", "exec", "-it", "--", containerName, shell)
		cmd.Env = dockerEnv()

		ptyFile, err = pty.Start(cmd)
		if err == nil {
			session.Cmd = cmd
			session.Pty = ptyFile
			shellUsed = shell
			break
		}
		slog.Debug("Failed to create PTY session", "shell", shell, "error", err)
	}

	if session.Pty == nil {
		return nil, err
	}

	// Best-effort: find the shell's pid inside the container so CloseSession
	// can reap it later (see ContainerPID doc comment). Excludes pids already
	// tracked for this container so a sibling session's shell is never
	// mistaken for this one; the whole map is already held under s.mu, so
	// this is race-free with any concurrent CreateSession/CloseSession.
	session.ContainerPID = s.findContainerPID(containerName, shellUsed)

	s.sessions[session.ID] = session
	return session, nil
}

// findContainerPID polls `docker exec <containerName> ps` for a process
// running shellPath that isn't already tracked by an existing session for the
// same container, and returns its pid as seen from inside the container's own
// PID namespace. It returns 0 (never found, or ambiguous) rather than guess —
// picking the wrong pid to kill later is worse than not killing anything, and
// CloseSession's existing local-CLI cleanup still runs either way.
//
// Callers must hold s.mu (this reads s.sessions to build the exclusion set).
func (s *TerminalService) findContainerPID(containerName, shellPath string) int {
	known := make(map[int]struct{})
	for _, existing := range s.sessions {
		if existing.ContainerName == containerName && existing.ContainerPID != 0 {
			known[existing.ContainerPID] = struct{}{}
		}
	}

	wantComm := path.Base(shellPath) // e.g. "/bin/sh" -> "sh"

	for attempt := 0; attempt < containerPIDDiscoveryAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(containerPIDDiscoveryInterval)
		}

		//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
		cmd := execCommand("docker", "exec", "--", containerName, "ps", "-o", "pid,args")
		cmd.Env = dockerEnv()
		out, err := cmd.Output()
		if err != nil {
			// No `ps` in the container (or it's already gone) — give up
			// silently. This is a best-effort enhancement, not a
			// requirement for the session to work.
			slog.Debug("findContainerPID: ps failed", "container", containerName, "error", err)
			return 0
		}

		candidate := 0
		ambiguous := false
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil || pid <= 1 {
				// Not a pid line (e.g. the header), or PID 1 — never a
				// candidate: killing a container's init process would kill
				// the whole container.
				continue
			}
			if _, tracked := known[pid]; tracked {
				continue
			}
			argv0 := strings.TrimPrefix(fields[1], "-") // login shells: "-sh"
			if path.Base(argv0) != wantComm {
				continue
			}
			if candidate != 0 {
				ambiguous = true
				break
			}
			candidate = pid
		}

		if ambiguous {
			slog.Debug("findContainerPID: multiple untracked candidates, giving up rather than guessing", "container", containerName)
			return 0
		}
		if candidate != 0 {
			return candidate
		}
	}

	slog.Debug("findContainerPID: shell did not appear in the container's process table in time", "container", containerName)
	return 0
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
// reaps the shell running INSIDE the container (best-effort, only when
// findContainerPID succeeded at creation time — see ContainerPID), and
// removes it from the map. Callers must hold s.mu.
func (s *TerminalService) terminateSession(ctx context.Context, sessionID string, session *TerminalSession) {
	if session.Cmd != nil && session.Cmd.Process != nil {
		// Best-effort; the session is being torn down regardless, and a
		// failed kill here (process already exited) isn't actionable. This
		// only ever kills the LOCAL `docker exec` CLI — it does not reach the
		// shell running inside the container, which is why killInContainer
		// below exists.
		_ = session.Cmd.Process.Kill()
	}
	if session.Pty != nil {
		session.Pty.Close()
	}
	if session.ContainerPID != 0 {
		s.killInContainer(ctx, session.ContainerName, session.ContainerPID)
	}
	delete(s.sessions, sessionID)
}

// killInContainer SIGKILLs pid from INSIDE containerName's own PID namespace,
// by running the kill as a fresh `docker exec` rather than signalling pid
// directly from this process: this service's own process is not guaranteed to
// share a PID namespace with the target container (e.g. Docker-out-of-Docker
// deployments, where this service and every managed container are sibling
// containers under one host daemon), so a plain syscall-level kill on pid
// would either fail or — worse — silently signal an unrelated process if the
// number happened to be reused in this process's own namespace.
//
// Sends the signal to pid's whole process group first (so a backgrounded
// child survives the shell it was spawned from, e.g. `sleep 400 &`, is also
// caught) and then to pid alone as a fallback if it is not a group leader.
// SIGKILL cannot be caught or ignored, so this also reaps a shell currently
// running a foreground full-screen program (vim, top, ssh, ...), unlike
// writing "exit\n" to the pty.
//
// Best-effort: errors (container already gone, docker unreachable) are
// logged, not returned — CloseSession/reapExpiredSessions tear the session
// down from this service's side regardless.
func (s *TerminalService) killInContainer(ctx context.Context, containerName string, pid int) {
	if pid <= 1 {
		// Defensive: findContainerPID already excludes pid <= 1, but never
		// let a caller reach the container's init process from here either.
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	killScript := fmt.Sprintf("kill -KILL -%d 2>/dev/null; kill -KILL %d 2>/dev/null; exit 0", pid, pid)
	//nolint:gosec // explicit argv built from an int and a validated container name, not a shell string
	cmd := execCommandContext(ctx, "docker", "exec", "--", containerName, "sh", "-c", killScript)
	cmd.Env = dockerEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Debug("killInContainer: docker exec kill failed", "container", containerName, "pid", pid, "error", err, "output", string(out))
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
