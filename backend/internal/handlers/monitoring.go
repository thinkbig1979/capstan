package handlers

import (
	"context"
	"errors"
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
			// A registration refusal already wrote its own close frame and
			// closed the socket inside serveWS — reporting it here too would
			// write into an already-hijacked ResponseWriter. Only an
			// upgrade/auth failure is reported (agent-os-o1jp.1).
			if !errors.Is(err, errWSRefused) {
				handleError(c, err)
			}
			return
		}
		// gorilla's Upgrade hijacks the connection, so net/http never closes it.
		// Every return below (stream-error, write-error — ctx-done never
		// actually fires today, since nothing external ever cancels ctx) left
		// the socket and its goroutines open until this defer (agent-os-14gr),
		// same shape as every other serveWS call site (operations.go,
		// logs.go, dashboard.go, handleEventsWebSocket below, update_jobs_ws.go).
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

		// An empty container list used to fall straight into StreamStats,
		// whose own empty-list branch hands back an already-closed channel
		// (services/monitor.go); the loop below then took its `!ok` exit and
		// the socket died within a millisecond of opening. The frontend
		// reconnects on close, so that read as a redial storm — one open and
		// exit per second, forever (agent-os-74rl). Hold the socket open and
		// report "no containers" on a ticker instead.
		//
		// This mirrors handleDashboardMetricsWebSocket's STRUCTURE (guard
		// before StreamStats, ticker, one frame per tick) and deliberately
		// differs from it on the empty value — see the Containers note below.
		// Do not copy that handler's `Containers: nil` here; it is a live
		// defect over there, tracked as agent-os-5scv, not a model to follow.
		//
		// Guarding here rather than in StreamStats keeps the change local to
		// the one caller that lacked it: dashboard.go guards before its own
		// call, so making StreamStats emit instead of close would leave that
		// guard unreachable. StreamStats keeps its empty-list branch as the
		// defensive contract of an exported method.
		//
		// Containers is a non-nil empty slice, NOT nil, and the difference is
		// load-bearing: encoding/json renders a nil slice as `null` and an
		// empty one as `[]`, while the frontend hook on the other end of this
		// socket calls .forEach on the field with no null guard and types it
		// non-nullable (frontend/src/hooks/useMetricsBase.ts:60, declared at
		// :25; reached from MetricsPanel.tsx:264 and StackDetail.tsx:63).
		// Sending null throws a TypeError on every tick, and it is not a
		// contained one: the throw happens inside the setContainers updater,
		// which React runs during the render phase, so the parse-time
		// try/catch at useWebSocket.ts:187-193 never sees it (useMetricsBase
		// itself has no try/catch at all). The TabErrorBoundary elements
		// StackDetail renders at :166 and :178 do not catch it either — the
		// hook is called in that component's own body at :63, ABOVE those
		// boundaries, so a throw during its render escapes past them to the
		// app-level ErrorBoundary at App.tsx:171 (the production auth path;
		// App.tsx:136 is its AUTH_DISABLED twin, inside `if (authDisabled)`
		// at :133) and the whole app is replaced by the error fallback. That
		// would trade this bead's loud, server-visible redial storm for a
		// silent client-side crash.
		//
		// The Go half is observed in the wire bytes by the regression test,
		// not inferred: the nil form emits
		// `{"timestamp":...,"containers":null}`. The React unwinding is not
		// something this package can execute; it was established by a browser
		// probe recorded on agent-os-74rl and agent-os-5scv.
		if len(containerIDs) == 0 {
			// After this fix an empty stack no longer announces itself as one
			// WS session per second in the request log, so this line is the
			// replacement signal for agent-os-fg55 — the still-open question
			// of why the list is empty for a stack whose containers exist.
			// Once at branch entry, never per tick: a per-tick line would be
			// the same storm in a different colour.
			slog.Info("metrics stream has no containers; holding socket open",
				"stackId", stackID, "projectName", stack.ProjectName)

			// 2s is a deliberate ceiling, not a tuning knob. This handler has
			// no reader goroutine and ctx.Done() never fires, so a failed
			// write is the ONLY client-disconnect detector — the ticker
			// interval IS the detection latency. safePingLoop is not a
			// backstop: on error it returns its own goroutine, not this
			// handler. An interval past the 30s ping would leave no detector
			// at all and leak the ConnectionManager slot for good. The
			// `return` on write failure below is what makes that true, and is
			// enforced by
			// TestMonitoringMetricsWS_EmptyListClientDisconnectClosesConnection
			// — turning it into a `continue` parks the goroutine forever.
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					frame := MetricsFrame{
						Timestamp:  time.Now().Format(time.RFC3339),
						Containers: []models.ContainerMetrics{},
					}
					// safePingLoop is already running on this connection, so
					// this second writer must go through the WriteMutex-guarded
					// helper rather than a raw write (agent-os-1jzj). The
					// enclosing `defer release()` covers this loop's exits too
					// (agent-os-14gr).
					if err := safeWriteJSON(conn, frame); err != nil {
						slog.Debug("Failed to write empty metrics frame", "stackId", stackID, "error", err)
						return
					}
				}
			}
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
			// A registration refusal already wrote its own close frame and
			// closed the socket inside serveWS — reporting it here too would
			// write into an already-hijacked ResponseWriter. Only an
			// upgrade/auth failure is reported (agent-os-o1jp.1).
			if !errors.Is(err, errWSRefused) {
				handleError(c, err)
			}
			return
		}
		// gorilla's Upgrade hijacks the connection, so net/http never closes
		// it. The only reachable exit below is the write-error case (client
		// vanishes mid-stream) — ctx.Done() never fires (nothing external
		// ever cancels ctx) and the eventChan-closed case never fires either
		// (only this function's own deferred Unsubscribe closes eventChan,
		// and that runs after this loop has already exited) — but every
		// exit left the socket and safePingLoop's goroutine open until this
		// defer (agent-os-iz9w), same shape as handleMetricsWebSocket above
		// (agent-os-14gr) and every other serveWS call site (operations.go,
		// logs.go, dashboard.go, update_jobs_ws.go).
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
