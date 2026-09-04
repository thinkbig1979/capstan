package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type MonitoringHandler struct {
	monitor  *services.MonitorService
	docker   *services.DockerService
	db       *database.DB
	cm       *ConnectionManager
	eventBus *EventBus
}

func NewMonitoringHandler(monitor *services.MonitorService, docker *services.DockerService, db *database.DB, cm *ConnectionManager, eventBus *EventBus) *MonitoringHandler {
	return &MonitoringHandler{
		monitor:  monitor,
		docker:   docker,
		db:       db,
		cm:       cm,
		eventBus: eventBus,
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
		userID := c.GetString("userID")

		stackID := c.Param("id")
		stack, err := h.db.GetStack(stackID)
		if err != nil {
			handleError(c, models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Stack not found"))
			return
		}

		containers, err := h.docker.GetContainerList(stack.ProjectName)
		if err != nil {
			slog.Error("Failed to get container list", "userId", userID, "stackId", stackID, "error", err)
			respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve container list")
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
			handleError(c, models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Stack not found"))
			return
		}

		conn, release, err := serveWS(c, h.db, jwtSecret, authDisabled, h.cm, wsRegistration{
			refuseCode:   websocket.CloseNormalClosure,
			refuseReason: "Connection limit exceeded",
		})
		if err != nil {
			handleError(c, err)
			return
		}
		// gorilla's Upgrade hijacks the connection, so net/http never closes it.
		// Every return below (stream-error, write-error — ctx-done never
		// actually fires today, since nothing external ever cancels ctx) left
		// the socket and its goroutines open until this defer (agent-os-14gr),
		// same shape as operations.go:91 / logs.go:130.
		defer release()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)

		containerIDs, err := h.monitor.GetContainersForStack(ctx, stack.ProjectName)
		if err != nil {
			slog.Error("Failed to get containers for stack", "stackId", stackID, "error", err)
			// safePingLoop is already running on this connection (started
			// above); the raw writeCloseMessage races its WriteMutex-guarded
			// ping over the same *websocket.Conn (agent-os-1jzj).
			safeWriteCloseMessage(conn, websocket.CloseInternalServerErr, "Failed to get containers")
			return
		}

		statsChan, err := h.monitor.StreamStats(ctx, containerIDs)
		if err != nil {
			slog.Error("Failed to start stats stream", "stackId", stackID, "error", err)
			// Same race as above: safePingLoop is already running.
			safeWriteCloseMessage(conn, websocket.CloseInternalServerErr, "Failed to stream metrics")
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

type EventBus struct {
	mu       sync.RWMutex
	channels map[chan models.StackEvent]bool
}

func NewEventBus() *EventBus {
	return &EventBus{
		channels: make(map[chan models.StackEvent]bool),
	}
}

func (eb *EventBus) Subscribe(ch chan models.StackEvent) {
	eb.mu.Lock()
	eb.channels[ch] = true
	eb.mu.Unlock()
}

func (eb *EventBus) Unsubscribe(ch chan models.StackEvent) {
	eb.mu.Lock()
	delete(eb.channels, ch)
	eb.mu.Unlock()
	close(ch)
}

func (eb *EventBus) Broadcast(event models.StackEvent) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	for ch := range eb.channels {
		select {
		case ch <- event:
		default:
			slog.Debug("Event subscriber channel full, dropping event", "type", event.Type)
		}
	}
}

func (eb *EventBus) SubscriberCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.channels)
}

var defaultEventBus = NewEventBus()

func BroadcastEvent(event models.StackEvent) {
	defaultEventBus.Broadcast(event)
}

func DefaultEventBus() *EventBus {
	return defaultEventBus
}

func (h *MonitoringHandler) handleEventsWebSocket(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, release, err := serveWS(c, h.db, jwtSecret, authDisabled, h.cm, wsRegistration{
			refuseCode:   websocket.CloseNormalClosure,
			refuseReason: "Connection limit exceeded",
		})
		if err != nil {
			handleError(c, err)
			return
		}
		// gorilla's Upgrade hijacks the connection, so net/http never closes
		// it. The only reachable exit below is the write-error case (client
		// vanishes mid-stream) — ctx.Done() never fires (nothing external
		// ever cancels ctx) and the eventChan-closed case never fires either
		// (only this function's own deferred Unsubscribe closes eventChan,
		// and that runs after this loop has already exited) — but every
		// exit left the socket and safePingLoop's goroutine open until this
		// defer (agent-os-iz9w), same shape as handleMetricsWebSocket
		// (agent-os-14gr) / operations.go:91 / logs.go:130.
		defer release()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)

		eventChan := make(chan models.StackEvent, 50)

		h.eventBus.Subscribe(eventChan)

		defer func() {
			h.eventBus.Unsubscribe(eventChan)
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

func StartEventBroadcaster(ctx context.Context, monitor *services.MonitorService, eventBus *EventBus) {
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

				slog.Debug("Broadcasting event", "type", event.Type, "stackId", event.StackID, "event", event.Event, "subscribers", eventBus.SubscriberCount())

				eventBus.Broadcast(event)
			}
		}
	}()
}
