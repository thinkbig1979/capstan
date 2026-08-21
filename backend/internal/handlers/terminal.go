package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/middleware"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// ContainerLister resolves the containers belonging to a compose project. It is
// the terminal handler's view of DockerService, narrow enough to fake in tests
// without a daemon.
type ContainerLister interface {
	GetContainerList(projectName string) ([]models.Container, error)
}

type TerminalHandler struct {
	terminal *services.TerminalService
	// docker is nil when the daemon was unreachable at startup. Membership
	// cannot be verified without it, so the handler denies rather than falling
	// back to the unchecked behaviour — a security check that fails open is not
	// a security check.
	docker  ContainerLister
	db      *database.DB
	cm      *ConnectionManager
	actions *services.ActionLogger
}

type ResizeMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func NewTerminalHandler(terminal *services.TerminalService, docker ContainerLister, db *database.DB, cm *ConnectionManager, actions *services.ActionLogger) *TerminalHandler {
	return &TerminalHandler{
		terminal: terminal,
		docker:   docker,
		db:       db,
		cm:       cm,
		actions:  actions,
	}
}

func (h *TerminalHandler) RegisterRoutes(group *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	group.GET("/ws/terminal/:id/:container", h.handleTerminalWS(jwtSecret, authDisabled))
}

func (h *TerminalHandler) handleTerminalWS(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		stackID := c.Param("id")
		containerName := c.Param("container")

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			handleError(c, err)
			return
		}
		defer conn.Conn.Close()

		// After authentication, so the cap keys on a real user ID rather than an
		// unauthenticated placeholder. Terminals get their own manager with a
		// lower cap than the shared one — see main.go (agent-os-a0y).
		if err := h.cm.Add(conn.ID, conn); err != nil {
			slog.Warn("Terminal connection refused: per-user limit reached",
				"user_id", conn.UserID, "stack_id", stackID, "container", containerName)
			writeCloseMessage(conn.Conn, CloseCodeRateLimit, "Too many open terminal sessions")
			return
		}
		defer h.cm.Remove(conn.ID)

		stack, err := h.db.GetStack(stackID)
		if err != nil || stack == nil {
			slog.Error("Failed to get stack", "stack_id", stackID, "error", err)
			writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Stack not found")
			return
		}

		// Enforce the scoping the route signature already implies. Without this
		// the :id parameter is decorative and any valid stack ID admits a shell
		// in any container on the host (agent-os-7u5). This must run before
		// CreateSession: rejecting after the fork is no fix at all.
		if err := h.assertContainerInStack(c, conn, stackID, stack.ProjectName, containerName); err != nil {
			return
		}

		session, err := h.terminal.CreateSession(stack.ProjectName, containerName)
		if err != nil {
			var tooMany services.TooManySessionsError
			if errors.As(err, &tooMany) {
				slog.Warn("Terminal session refused: host-wide session ceiling reached",
					"user_id", conn.UserID, "stack_id", stackID, "container", containerName, "limit", tooMany.TooManySessions())
				writeCloseMessage(conn.Conn, CloseCodeRateLimit, "Too many open terminal sessions")
				return
			}
			slog.Error("Failed to create terminal session", "stack_id", stackID, "container", containerName, "error", err)
			writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Failed to create terminal session")
			return
		}
		defer h.terminal.CloseSession(session.ID)

		done := make(chan struct{})
		readDone := make(chan struct{})
		writeDone := make(chan struct{})

		go h.readFromWebSocket(conn, session, readDone, done)
		go h.writeToWebSocket(conn, session, writeDone, done)
		go h.waitForProcessExit(session, conn, done)

		select {
		case <-readDone:
		case <-writeDone:
		case <-done:
		}
	}
}

// assertContainerInStack verifies that containerName is one of the containers
// carrying the compose project label for projectName, rejecting with the
// auth-style close code otherwise.
//
// Membership is decided on the com.docker.compose.project label — the same
// filter DockerService.GetContainerList already applies — rather than on the
// container name's prefix. Naming conventions vary (project-service-1, but also
// arbitrary `container_name:` entries), so prefix matching would both over-match
// and under-match.
//
// Returns a non-nil error when the caller must stop; the close message has
// already been written by then.
func (h *TerminalHandler) assertContainerInStack(c *gin.Context, conn *Connection, stackID, projectName, containerName string) error {
	deny := func(reason string, logArgs ...any) error {
		slog.Warn("Terminal denied: container does not belong to the requested stack",
			append([]any{
				"user_id", conn.UserID,
				"stack_id", stackID,
				"project", projectName,
				"container", containerName,
				"reason", reason,
			}, logArgs...)...)

		// An authenticated user probing for containers outside their stack is
		// exactly what an audit trail is for.
		if h.actions != nil {
			h.actions.LogWithRequest(middleware.RequestIDFrom(c), conn.UserID, &stackID, "terminal_denied", map[string]any{
				"container": containerName,
				"project":   projectName,
				"reason":    reason,
			})
		}

		writeCloseMessage(conn.Conn, CloseCodeAuthFailure, "Container does not belong to this stack")
		return errors.New(reason)
	}

	if h.docker == nil {
		return deny("docker_unavailable")
	}

	containers, err := h.docker.GetContainerList(projectName)
	if err != nil {
		return deny("container_lookup_failed", "error", err)
	}

	for _, ctr := range containers {
		// The frontend sends the docker container ID in the :container path
		// segment (TerminalToolbar keys its select on container.id); docker
		// exec accepts either form, so membership must too. Exact match only —
		// no ID-prefix shortening, which could be probed.
		if ctr.Name == containerName || ctr.ID == containerName {
			return nil
		}
	}

	return deny("container_not_in_stack")
}

func (h *TerminalHandler) readFromWebSocket(conn *Connection, session *services.TerminalSession, done chan struct{}, stop chan struct{}) {
	defer close(done)

	for {
		select {
		case <-stop:
			return
		default:
			// A failed deadline set surfaces immediately as a read error
			// below, which is already handled.
			_ = conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			messageType, data, err := conn.Conn.ReadMessage()
			if err != nil {
				if err != io.EOF {
					slog.Debug("WebSocket read error", "error", err)
				}
				return
			}

			h.terminal.UpdateActivity(session.ID)

			switch messageType {
			case websocket.BinaryMessage:
				_, err := session.Pty.Write(data)
				if err != nil {
					slog.Error("Failed to write to PTY", "error", err)
					return
				}
			case websocket.TextMessage:
				var resize ResizeMessage
				if err := json.Unmarshal(data, &resize); err == nil && resize.Type == "resize" {
					if err := h.terminal.ResizeSession(session.ID, resize.Cols, resize.Rows); err != nil {
						slog.Error("Failed to resize PTY", "error", err)
					}
				}
			}
		}
	}
}

func (h *TerminalHandler) writeToWebSocket(conn *Connection, session *services.TerminalSession, done chan struct{}, stop chan struct{}) {
	defer close(done)

	buffer := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return
		default:
			n, err := session.Pty.Read(buffer)
			if err != nil {
				if err != io.EOF {
					slog.Debug("PTY read error", "error", err)
				}
				return
			}

			h.terminal.UpdateActivity(session.ID)

			// A failed deadline set surfaces immediately as a write error
			// below, which is already handled.
			_ = conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.Conn.WriteMessage(websocket.BinaryMessage, buffer[:n]); err != nil {
				slog.Debug("WebSocket write error", "error", err)
				return
			}
		}
	}
}

func (h *TerminalHandler) waitForProcessExit(session *services.TerminalSession, conn *Connection, stop chan struct{}) {
	if session.Cmd != nil && session.Cmd.Process != nil {
		_, err := session.Cmd.Process.Wait()
		if err != nil {
			slog.Error("Process wait error", "error", err)
		}
		slog.Info("Terminal process exited", "session_id", session.ID)
		close(stop)
	}
}
