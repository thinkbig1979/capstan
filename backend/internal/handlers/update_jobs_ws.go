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
// The "done" frame carries outcome and reason in addition to status/error so
// the frontend can derive the correct toast/badge without a separate GET.
type wsJobFrame struct {
	Type    string            `json:"type"`
	Job     *services.Job     `json:"job,omitempty"`
	Line    *services.LogLine `json:"line,omitempty"`
	Status  string            `json:"status,omitempty"`
	Error   string            `json:"error,omitempty"`
	Outcome string            `json:"outcome,omitempty"`
	Reason  string            `json:"reason,omitempty"`
}

func (h *UpdateJobsWSHandler) streamJob(c *gin.Context) {
	jobID := c.Param("jobId")

	conn, release, err := serveWS(c, h.db, h.jwtSecret, h.authDisabled, h.cm, wsRegistration{
		refuseCode:   websocket.CloseNormalClosure,
		refuseReason: "Connection limit exceeded",
	})
	if err != nil {
		// serveWS already handled the error response either way: an
		// upgrade/auth failure has its own close frame written by
		// upgradeConnection, and a registration refusal (errWSRefused) has
		// its close frame written by serveWS itself. Nothing to do here.
		return
	}
	defer release()

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

	// If the job already finished before this client connected, Subscribe does not
	// register a live subscriber (eventCh never delivers), so emit the terminal
	// frame from the snapshot and close out instead of blocking forever.
	if snapshot.Status == services.StatusSuccess || snapshot.Status == services.StatusError {
		_ = safeWriteJSON(conn, wsJobFrame{
			Type:    "done",
			Status:  string(snapshot.Status),
			Error:   snapshot.Error,
			Outcome: snapshot.Outcome,
			Reason:  snapshot.Reason,
		})
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
				frame = wsJobFrame{
					Type:    "done",
					Status:  string(ev.Status),
					Error:   ev.Error,
					Outcome: ev.Outcome,
					Reason:  ev.Reason,
				}
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
