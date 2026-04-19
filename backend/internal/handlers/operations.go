package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type OperationsHandler struct {
	docker *services.DockerService
	db     *database.DB
}

func NewOperationsHandler(docker *services.DockerService, db *database.DB) *OperationsHandler {
	return &OperationsHandler{
		docker: docker,
		db:     db,
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
			stopCh := h.docker.RunStreaming(ctx, *stack, "down", nil)
			for line := range stopCh {
				if line.Type == "done" {
					if !line.Success {
						if err := safeWriteJSON(conn, line); err != nil {
							slog.Debug("Failed to write stop output", "error", err)
						}
						return
					}
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
