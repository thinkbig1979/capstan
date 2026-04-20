package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type MonitoringHandler struct {
	monitor *services.MonitorService
	docker  *services.DockerService
	db      *database.DB
	cm      *ConnectionManager
}

func NewMonitoringHandler(monitor *services.MonitorService, docker *services.DockerService, db *database.DB, cm *ConnectionManager) *MonitoringHandler {
	return &MonitoringHandler{
		monitor: monitor,
		docker:  docker,
		db:      db,
		cm:      cm,
	}
}

type MetricsFrame struct {
	Timestamp  string                    `json:"timestamp"`
	Containers []models.ContainerMetrics `json:"containers"`
}

func (h *MonitoringHandler) RegisterRoutes(r *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	r.GET("/stacks/:id/containers", h.getStackContainers(jwtSecret, authDisabled))
	r.GET("/ws/metrics/:id", h.handleMetricsWebSocket(jwtSecret, authDisabled))
	r.GET("/ws/events", h.handleEventsWebSocket(jwtSecret, authDisabled))
}

func (h *MonitoringHandler) getStackContainers(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, err := authenticateWS(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			c.Error(err)
			return
		}

		stackID := c.Param("id")
		stack, err := h.db.GetStack(stackID)
		if err != nil {
			c.Error(&models.AppError{
				Code:    models.ErrNotFound,
				Message: "Stack not found",
				Status:  http.StatusNotFound,
			})
			return
		}

		containers, err := h.docker.GetContainerList(stack.ProjectName)
		if err != nil {
			slog.Error("Failed to get container list", "userId", userID, "stackId", stackID, "error", err)
			c.Error(&models.AppError{
				Code:    "INTERNAL_ERROR",
				Message: "Failed to retrieve container list",
				Status:  http.StatusInternalServerError,
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"containers": containers,
		})
	}
}

func (h *MonitoringHandler) handleMetricsWebSocket(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		stackID := c.Param("id")

		stack, err := h.db.GetStack(stackID)
		if err != nil {
			c.Error(&models.AppError{
				Code:    models.ErrNotFound,
				Message: "Stack not found",
				Status:  http.StatusNotFound,
			})
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			c.Error(err)
			return
		}

		if err := h.cm.Add(conn.ID, conn); err != nil {
			writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Connection limit exceeded")
			conn.Conn.Close()
			return
		}

		defer h.cm.Remove(conn.ID)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)

		containerIDs, err := h.monitor.GetContainersForStack(ctx, stack.ProjectName)
		if err != nil {
			slog.Error("Failed to get containers for stack", "stackId", stackID, "error", err)
			writeCloseMessage(conn.Conn, websocket.CloseInternalServerErr, "Failed to get containers")
			return
		}

		statsChan, err := h.monitor.StreamStats(ctx, containerIDs)
		if err != nil {
			slog.Error("Failed to start stats stream", "stackId", stackID, "error", err)
			writeCloseMessage(conn.Conn, websocket.CloseInternalServerErr, "Failed to stream metrics")
			return
		}

		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-statsChan:
				if !ok {
					return
				}

				frame := MetricsFrame{
					Timestamp:  time.Now().Format(time.RFC3339),
					Containers: batch,
				}

				if err := safeWriteJSON(conn, frame); err != nil {
					slog.Debug("Failed to write metrics frame", "error", err)
					return
				}
			}
		}
	}
}

var eventSubscribers struct {
	sync.RWMutex
	channels map[chan models.StackEvent]bool
}

func init() {
	eventSubscribers.channels = make(map[chan models.StackEvent]bool)
}

func BroadcastEvent(event models.StackEvent) {
	eventSubscribers.RLock()
	defer eventSubscribers.RUnlock()
	for ch := range eventSubscribers.channels {
		select {
		case ch <- event:
		default:
			slog.Debug("Event subscriber channel full, dropping event", "type", event.Type)
		}
	}
}

func (h *MonitoringHandler) handleEventsWebSocket(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			c.Error(err)
			return
		}

		if err := h.cm.Add(conn.ID, conn); err != nil {
			writeCloseMessage(conn.Conn, websocket.CloseNormalClosure, "Connection limit exceeded")
			conn.Conn.Close()
			return
		}

		defer h.cm.Remove(conn.ID)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)

		eventChan := make(chan models.StackEvent, 50)

		eventSubscribers.Lock()
		eventSubscribers.channels[eventChan] = true
		eventSubscribers.Unlock()

		defer func() {
			eventSubscribers.Lock()
			delete(eventSubscribers.channels, eventChan)
			eventSubscribers.Unlock()
			close(eventChan)
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-eventChan:
				if !ok {
					return
				}

				if err := safeWriteJSON(conn, event); err != nil {
					slog.Debug("Failed to write event", "error", err)
					return
				}
			}
		}
	}
}

func StartEventBroadcaster(ctx context.Context, monitor *services.MonitorService) {
	eventChan, err := monitor.ListenEvents(ctx)
	if err != nil {
		slog.Error("Failed to start event listener", "error", err)
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-eventChan:
				if !ok {
					return
				}

				slog.Debug("Broadcasting event", "type", event.Type, "stackId", event.StackID, "event", event.Event, "subscribers", len(eventSubscribers.channels))

				eventSubscribers.RLock()
				for ch := range eventSubscribers.channels {
					select {
					case ch <- event:
					default:
						slog.Debug("Event subscriber channel full, dropping event", "stackId", event.StackID)
					}
				}
				eventSubscribers.RUnlock()
			}
		}
	}()
}
