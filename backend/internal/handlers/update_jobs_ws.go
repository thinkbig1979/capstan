package handlers

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// UpdateJobsWSHandler handles WebSocket connections for streaming update job
// output. It is separate from ResourcesHandler so the wsGroup registration in
// main.go mirrors the existing OperationsHandler / BackupHandler pattern.
type UpdateJobsWSHandler struct {
	jobManager   *services.UpdateJobManager
	db           *database.DB
	jwtSecret    string
	authDisabled bool
	cm           *ConnectionManager
}

func NewUpdateJobsWSHandler(
	jobManager *services.UpdateJobManager,
	db *database.DB,
	jwtSecret string,
	authDisabled bool,
	cm *ConnectionManager,
) *UpdateJobsWSHandler {
	return &UpdateJobsWSHandler{
		jobManager:   jobManager,
		db:           db,
		jwtSecret:    jwtSecret,
		authDisabled: authDisabled,
		cm:           cm,
	}
}

// RegisterRoutes registers the WS route on the supplied group (the wsGroup).
func (h *UpdateJobsWSHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/ws/updates/jobs/:jobId", h.streamJob)
}

// wsJobFrame is the envelope for all frames sent to the client.
type wsJobFrame struct {
	Type   string            `json:"type"`
	Job    *services.Job     `json:"job,omitempty"`
	Line   *services.LogLine `json:"line,omitempty"`
	Status string            `json:"status,omitempty"`
	Error  string            `json:"error,omitempty"`
}

func (h *UpdateJobsWSHandler) streamJob(c *gin.Context) {
	jobID := c.Param("jobId")

	conn, err := upgradeConnection(c, h.db, h.jwtSecret, h.authDisabled)
	if err != nil {
		// upgradeConnection already handled the error response.
		return
	}
	defer conn.Conn.Close()

	if err := h.cm.Add(conn.ID, conn); err != nil {
		writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Connection limit exceeded")
		return
	}
	defer h.cm.Remove(conn.ID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Drain incoming messages (pong/close) so the connection stays alive.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_, _, readErr := conn.Conn.ReadMessage()
				if readErr != nil {
					cancel()
					return
				}
			}
		}
	}()

	go safePingLoop(ctx, conn, DefaultPingInterval)

	// If no job manager is configured, respond with an error and close.
	if h.jobManager == nil {
		_ = safeWriteJSON(conn, wsJobFrame{Type: "error", Error: "job not found"})
		return
	}

	snapshot, eventCh, unsubscribe := h.jobManager.Subscribe(jobID)
	if snapshot == nil {
		// Job unknown or already evicted.
		_ = safeWriteJSON(conn, wsJobFrame{Type: "error", Error: "job not found"})
		return
	}
	defer unsubscribe()

	// Send the full snapshot as the first frame.
	if err := safeWriteJSON(conn, wsJobFrame{Type: "snapshot", Job: snapshot}); err != nil {
		slog.Debug("Failed to send snapshot frame", "jobId", jobID, "error", err)
		return
	}

	// Stream live events until the job is done or the client disconnects.
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-eventCh:
			if !ok {
				// Channel was closed — job finished; there should have been a done event.
				return
			}
			var frame wsJobFrame
			switch ev.Kind {
			case services.EventKindLine:
				frame = wsJobFrame{Type: "line", Line: ev.Line}
			case services.EventKindStatus:
				frame = wsJobFrame{Type: "status", Status: string(ev.Status)}
			case services.EventKindDone:
				frame = wsJobFrame{Type: "done", Status: string(ev.Status), Error: ev.Error}
				if writeErr := safeWriteJSON(conn, frame); writeErr != nil {
					slog.Debug("Failed to write done frame", "jobId", jobID, "error", writeErr)
				}
				return
			default:
				continue
			}
			if writeErr := safeWriteJSON(conn, frame); writeErr != nil {
				slog.Debug("Failed to write event frame", "jobId", jobID, "error", writeErr)
				return
			}
		}
	}
}
