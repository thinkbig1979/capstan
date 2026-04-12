package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type TerminalHandler struct {
	terminal *services.TerminalService
	db       *database.DB
}

type ResizeMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func NewTerminalHandler(terminal *services.TerminalService, db *database.DB) *TerminalHandler {
	return &TerminalHandler{
		terminal: terminal,
		db:       db,
	}
}

func (h *TerminalHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/ws/terminal/:id/:container", h.WSTerminal)
}

func (h *TerminalHandler) WSTerminal(c *gin.Context) {
	stackID := c.Param("id")
	containerName := c.Param("container")

	jwtSecret := c.MustGet("jwt_secret").(string)
	authDisabled := c.MustGet("auth_disabled").(bool)

	conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
	if err != nil {
		models.HandleError(c, err)
		return
	}
	defer conn.Conn.Close()

	stack, err := h.db.GetStack(stackID)
	if err != nil || stack == nil {
		slog.Error("Failed to get stack", "stack_id", stackID, "error", err)
		writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Stack not found")
		return
	}

	session, err := h.terminal.CreateSession(stackID, containerName, stack.ComposeFile, stack.Directory)
	if err != nil {
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

func (h *TerminalHandler) readFromWebSocket(conn *Connection, session *services.TerminalSession, done chan struct{}, stop chan struct{}) {
	defer close(done)

	for {
		select {
		case <-stop:
			return
		default:
			conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			messageType, data, err := conn.Conn.ReadMessage()
			if err != nil {
				if err != io.EOF {
					slog.Debug("WebSocket read error", "error", err)
				}
				return
			}

			h.terminal.UpdateActivity(session.ID)

			if messageType == websocket.BinaryMessage {
				_, err := session.Pty.Write(data)
				if err != nil {
					slog.Error("Failed to write to PTY", "error", err)
					return
				}
			} else if messageType == websocket.TextMessage {
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

			conn.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
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
