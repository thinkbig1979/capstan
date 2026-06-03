package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type OperationsHandler struct {
	docker *services.DockerService
	db     *database.DB
	opLock *services.OperationLock
}

func NewOperationsHandler(docker *services.DockerService, db *database.DB, opLock *services.OperationLock) *OperationsHandler {
	return &OperationsHandler{
		docker: docker,
		db:     db,
		opLock: opLock,
	}
}

func (h *OperationsHandler) RegisterRoutes(group *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	group.GET("/ws/operations/:id/:action", h.handleOperation(jwtSecret, authDisabled))
}

func (h *OperationsHandler) handleOperation(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		stackID := c.Param("id")
		action := c.Param("action")

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

		safeWriteJSON(conn, gin.H{
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
			safeWriteJSON(conn, gin.H{
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
	}
}
