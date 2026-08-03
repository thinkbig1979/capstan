package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// OperationStreamer is the operations handler's view of DockerService: stream a
// compose subcommand. Narrow enough to fake in a test, which is what lets the
// connection-cap behaviour be covered without a live daemon.
type OperationStreamer interface {
	RunStreaming(ctx context.Context, stack models.Stack, subcommand string, extraArgs []string) <-chan services.StreamLine
}

type OperationsHandler struct {
	// docker is nil when the daemon was unreachable at startup.
	docker OperationStreamer
	db     *database.DB
	opLock *services.OperationLock
	cm     *ConnectionManager
}

func NewOperationsHandler(docker OperationStreamer, db *database.DB, opLock *services.OperationLock, cm *ConnectionManager) *OperationsHandler {
	return &OperationsHandler{
		docker: docker,
		db:     db,
		opLock: opLock,
		cm:     cm,
	}
}

func (h *OperationsHandler) RegisterRoutes(group *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	group.GET("/ws/operations/:id/:action", h.handleOperation(jwtSecret, authDisabled))
}

func (h *OperationsHandler) handleOperation(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		stackID := c.Param("id")
		action := c.Param("action")

		// main leaves dockerService nil when the daemon was unreachable at
		// startup. RunStreaming dereferences it inside a goroutine, so the
		// resulting nil-pointer panic is not caught by RecoveryMiddleware and
		// takes the whole process down. Refuse before the upgrade instead, the
		// way the checks below already do. (Same shape as agent-os-ck4; the
		// wider audit of nil-docker paths is agent-os-xay.)
		if h.docker == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Docker is unavailable"})
			return
		}

		stack, err := h.db.GetStack(stackID)
		if err != nil || stack == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Stack not found"})
			return
		}

		if _, err := h.opLock.Acquire(stackID); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		defer h.opLock.Release(stackID)

		var subcommand string
		var extraArgs []string
		switch action {
		case "pull":
			subcommand = "pull"
		case "start":
			subcommand = "up"
			extraArgs = []string{"-d"}
		case "stop":
			subcommand = "down"
		case "restart":
			subcommand = "restart"
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown action: " + action})
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			return
		}
		defer conn.Conn.Close()

		// After authentication, so the cap keys on a real user ID. Operations
		// streams were the other endpoint missing from the ConnectionManager
		// every other WebSocket handler already uses (agent-os-a0y).
		if err := h.cm.Add(conn.ID, conn); err != nil {
			slog.Warn("Operations connection refused: per-user limit reached",
				"user_id", conn.UserID, "stack_id", stackID, "action", action)
			writeCloseMessage(conn.Conn, CloseCodeRateLimit, "Too many open connections")
			return
		}
		defer h.cm.Remove(conn.ID)

		// The whole streaming body is wrapped in an IIFE so that every return
		// path below — not just the final fallthrough — releases the stack
		// lock (right after the closure call, below) before the outer
		// function's deferred conn.Conn.Close() unwinds. Defers run LIFO, so
		// without this the deferred Release (kept below as the safety net
		// for the early returns between Acquire and here, and for panics)
		// would be the LAST thing to run on return — after the socket is
		// already closed — letting a client that observed the close redial
		// the same stack and hit a spurious 409 ("operation already in
		// progress") because this goroutine had not finished unwinding yet
		// (agent-os-o26). Release is idempotent (operation_lock.go:59-64
		// no-ops once the slot is already free), so the deferred Release
		// still runs harmlessly.
		func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go safePingLoop(ctx, conn, DefaultPingInterval)

			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					default:
						_, _, err := conn.Conn.ReadMessage()
						if err != nil {
							cancel()
							return
						}
					}
				}
			}()

			// Best-effort notification; a write failure here surfaces on the
			// next read/ping and the connection is torn down there.
			_ = safeWriteJSON(conn, gin.H{
				"type":   "start",
				"action": action,
				"stack":  stack.ProjectName,
			})

			if action == "restart" {
				// Two-phase restart: stream the down phase first, then the up phase.
				// Only the up-phase terminal done frame is the definitive result
				// (finding #18 fix: down-phase done is consumed and not forwarded as terminal).
				stopCh := h.docker.RunStreaming(ctx, *stack, "down", nil)
				for line := range stopCh {
					if line.Type == "done" {
						if !line.Success {
							// The stop phase failed — emit the failure done frame and return.
							if err := safeWriteJSON(conn, line); err != nil {
								slog.Debug("Failed to write stop failure frame", "error", err)
							}
							return
						}
						// Stop succeeded — do not forward the intermediate done frame;
						// the client will see the phase announcement instead.
						continue
					}
					if err := safeWriteJSON(conn, line); err != nil {
						slog.Debug("Failed to write stop output", "error", err)
						return
					}
				}
				// Best-effort notification; a write failure here surfaces on the
				// next line written below, which is error-checked.
				_ = safeWriteJSON(conn, gin.H{
					"type":    "phase",
					"phase":   "starting",
					"message": "Stack stopped, starting...",
				})
				subcommand = "up"
				extraArgs = []string{"-d"}
			}

			// Stream the main (or up-phase) command. The terminal done frame now
			// carries outcome+reason from the verified end state (finding #5 + #18 fix).
			lineCh := h.docker.RunStreaming(ctx, *stack, subcommand, extraArgs)
			for line := range lineCh {
				if ctx.Err() != nil {
					return
				}
				if err := safeWriteJSON(conn, line); err != nil {
					slog.Debug("Failed to write operation output", "error", err)
					return
				}
			}

			slog.Info("Streaming operation completed", "stack_id", stackID, "action", action)
		}()

		h.opLock.Release(stackID)
	}
}
