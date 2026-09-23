package handlers

import (
	"context"
	"log/slog"

	"github.com/gin-gonic/gin"
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

// One struct per frame type, mirroring the JobStreamFrame union in
// frontend/src/hooks/useUpdateJobStream.ts. A payload the union declares
// required carries no omitempty and is a value, not a pointer, so the wire can
// never omit it or send null; parseJobStreamFrame drops a frame that does
// (agent-os-onmw). The "done" frame carries outcome and reason in addition to
// status/error so the frontend can derive the correct toast/badge without a
// separate GET.

type jobSnapshotFrame struct {
	Type string       `json:"type"`
	Job  services.Job `json:"job"`
}

type jobLineFrame struct {
	Type string           `json:"type"`
	Line services.LogLine `json:"line"`
}

type jobStatusFrame struct {
	Type   string          `json:"type"`
	Status services.Status `json:"status"`
}

type jobDoneFrame struct {
	Type    string          `json:"type"`
	Status  services.Status `json:"status"`
	Error   string          `json:"error,omitempty"`
	Outcome string          `json:"outcome,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

type jobErrorFrame struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

func snapshotFrame(job services.Job) jobSnapshotFrame {
	return jobSnapshotFrame{Type: "snapshot", Job: job}
}

func lineFrame(line services.LogLine) jobLineFrame {
	return jobLineFrame{Type: "line", Line: line}
}

func statusFrame(status services.Status) jobStatusFrame {
	return jobStatusFrame{Type: "status", Status: status}
}

func doneFrame(status services.Status, errMsg, outcome, reason string) jobDoneFrame {
	return jobDoneFrame{Type: "done", Status: status, Error: errMsg, Outcome: outcome, Reason: reason}
}

func errorFrame(msg string) jobErrorFrame {
	return jobErrorFrame{Type: "error", Error: msg}
}

func (h *UpdateJobsWSHandler) streamJob(c *gin.Context) {
	jobID := c.Param("jobId")

	conn, release, err := serveWS(c, h.db, h.jwtSecret, h.authDisabled, h.cm, wsRegistration{
		refuseCode:   CloseCodeRateLimit,
		refuseReason: "Connection limit exceeded",
	})
	if err != nil {
		// serveWS already handled the error response either way: an
		// upgrade/auth failure has its own close frame written by
		// upgradeConnection, and a registration refusal (errWSRefused) has
		// its close frame written by serveWS itself. Nothing to do here.
		return
	}
	// release() closes the connection and deregisters it, in that order.
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
		_ = safeWriteJSON(conn, errorFrame("job not found")) //nolint:errcheck // Best-effort frame: a write failure surfaces on the next read/ping and the connection is torn down there.
		return
	}

	snapshot, eventCh, unsubscribe := h.jobManager.Subscribe(jobID)
	if snapshot == nil {
		// Job unknown or already evicted.
		_ = safeWriteJSON(conn, errorFrame("job not found")) //nolint:errcheck // Best-effort frame: a write failure surfaces on the next read/ping and the connection is torn down there.
		return
	}
	defer unsubscribe()

	// Send the full snapshot as the first frame.
	if err := safeWriteJSON(conn, snapshotFrame(*snapshot)); err != nil {
		slog.Debug("Failed to send snapshot frame", "jobId", jobID, "error", err)
		return
	}

	// If the job already finished before this client connected, Subscribe does not
	// register a live subscriber (eventCh never delivers), so emit the terminal
	// frame from the snapshot and close out instead of blocking forever.
	if snapshot.Status == services.StatusSuccess || snapshot.Status == services.StatusError {
		_ = safeWriteJSON(conn, doneFrame(snapshot.Status, snapshot.Error, snapshot.Outcome, snapshot.Reason)) //nolint:errcheck // Best-effort frame: a write failure surfaces on the next read/ping and the connection is torn down there.
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
			var frame any
			switch ev.Kind {
			case services.EventKindLine:
				// runJob always sends &line; skip rather than dereference nil.
				if ev.Line == nil {
					continue
				}
				frame = lineFrame(*ev.Line)
			case services.EventKindStatus:
				frame = statusFrame(ev.Status)
			case services.EventKindDone:
				frame = doneFrame(ev.Status, ev.Error, ev.Outcome, ev.Reason)
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
