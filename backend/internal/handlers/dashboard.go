package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type DashboardHandler struct {
	monitor *services.MonitorService
	docker  *services.DockerService
	db      *database.DB
	cm      *ConnectionManager
}

func NewDashboardHandler(monitor *services.MonitorService, docker *services.DockerService, db *database.DB, cm *ConnectionManager) *DashboardHandler {
	return &DashboardHandler{
		monitor: monitor,
		docker:  docker,
		db:      db,
		cm:      cm,
	}
}

func (h *DashboardHandler) RegisterRoutes(r *gin.RouterGroup, jwtSecret string, authDisabled bool) {
	r.GET("/dashboard/stats", h.getDashboardStats())
	r.GET("/ws/dashboard/metrics", h.handleDashboardMetricsWebSocket(jwtSecret, authDisabled))
}

func (h *DashboardHandler) getDashboardStats() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		stacks, err := h.db.ListStacks()
		if err != nil {
			slog.Error("Failed to list stacks for dashboard", "error", err)
			// agent-os-ua4y: was a direct c.JSON write with a bespoke {"error":
			// ...} body, bypassing handleError (so logServerFault never ran)
			// and diverging from the AppError {code,message} shape every other
			// endpoint uses. frontend/src/lib/error-handler.ts:110 reads
			// response.data.error falling back to response.data.message, so
			// the AppError shape's Message satisfies the same consumer.
			handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get dashboard stats", err))
			return
		}

		containers, err := h.docker.GetAllContainersWithDetails(ctx, h.db)
		if err != nil {
			slog.Error("Failed to get containers for dashboard", "error", err)
			// Empty slice, NOT nil: this still serialises under "containers" in
			// a 200 OK below, and encoding/json renders a nil slice as `null`
			// while DashboardStats.containers is declared a non-nullable array
			// on the frontend. Same defect class as the metrics WS empty-host
			// frame fixed in handleDashboardMetricsWebSocket (agent-os-5scv);
			// this path survives only because its two current consumers happen
			// to use `??`/`?.`, which is not a property the type guarantees.
			containers = []models.DashboardContainerInfo{}
		}

		// Derive live stack status from the container snapshot we already fetched,
		// matched by compose project, instead of a per-stack `docker compose ps`.
		// This is the same liveness the Stacks view shows, at zero extra cost.
		runningStacks, stoppedStacks := countLiveStackStatuses(stacks, containers)

		runningContainers := 0
		for _, ctr := range containers {
			if ctr.State == "running" {
				runningContainers++
			}
		}

		var diskUsage *services.DiskUsageBreakdown
		diskUsage, err = h.docker.GetDiskUsage(ctx)
		if err != nil {
			slog.Error("Failed to get disk usage", "error", err)
			diskUsage = &services.DiskUsageBreakdown{}
		}

		c.JSON(http.StatusOK, gin.H{
			"totalStacks":       len(stacks),
			"runningStacks":     runningStacks,
			"stoppedStacks":     stoppedStacks,
			"totalContainers":   len(containers),
			"runningContainers": runningContainers,
			"imageDiskUsage":    diskUsage.Images,
			"diskUsage":         diskUsage,
			"containers":        containers,
		})
	}
}

// countLiveStackStatuses reports how many stacks have at least one running
// container vs none, derived from a single already-fetched container snapshot
// rather than per-stack `docker compose ps`. Stacks are matched to containers by
// compose project name: because Docker namespaces compose projects by name,
// multiple stacks that share a project are each counted against that project's
// live state (mirroring the Stacks view). running + stopped always equals the
// total stack count, so the dashboard summary is internally consistent.
func countLiveStackStatuses(stacks []models.Stack, containers []models.DashboardContainerInfo) (running, stopped int) {
	runningProjects := make(map[string]struct{}, len(containers))
	for _, c := range containers {
		if c.ProjectName != "" && c.State == "running" {
			runningProjects[c.ProjectName] = struct{}{}
		}
	}

	for _, s := range stacks {
		if _, ok := runningProjects[s.ProjectName]; ok && s.ProjectName != "" {
			running++
		} else {
			stopped++
		}
	}
	return running, stopped
}

func (h *DashboardHandler) handleDashboardMetricsWebSocket(jwtSecret string, authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Refuse before the upgrade so the caller gets a 503 naming the cause
		// rather than a socket that opens and closes (agent-os-xay).
		//
		// respondDockerErr, not writeJSONError (agent-os-ua4y): this runs
		// before serveWS's upgrade, so the writer is plain HTTP, not yet
		// hijacked — writeJSONError's raw c.JSON bypassed handleError and so
		// never reached logServerFault. services.ErrDockerUnavailable is
		// itself the sentinel error respondDockerErr checks for, passed as the
		// cause since there is no distinct triggering error here (the
		// condition is h.docker == nil, not a call that returned one).
		if h.docker == nil {
			respondDockerErr(c, services.ErrDockerUnavailable, http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", DockerUnavailableMessage)
			return
		}

		conn, release, err := serveWS(c, h.db, jwtSecret, authDisabled, h.cm, wsRegistration{
			refuseCode:   CloseCodeRateLimit,
			refuseReason: "Connection limit exceeded",
		})
		if err != nil {
			// A registration refusal already wrote its own close frame and
			// closed the socket inside serveWS — reporting it here too would
			// write into an already-hijacked ResponseWriter. Only an
			// upgrade/auth failure is reported (agent-os-o1jp.1).
			if !errors.Is(err, errWSRefused) {
				// handleError, not c.Error: this was the ONLY production
				// c.Error call site in the tree, and NOTHING reads gin's
				// c.Errors — no gin.Logger (main.go builds a bare gin.New()),
				// no ErrorLogger, nothing in middleware/logging.go. OBSERVED
				// 2026-09-04: `command grep -rn "\.Errors" --include=*.go
				// internal cmd` returns only watcher.go:103 (fsnotify) and
				// linter.go:33 (yaml), neither of them gin's. So the upgrade
				// failure was recorded into a sink with no reader and left no
				// trace anywhere — a 100% silent failure (agent-os-zaor).
				//
				// handleError routes it through logServerFault, which is what
				// the three sibling WS handlers already do under this exact
				// guard (monitoring.go x2, terminal.go). The divergence was
				// the defect; a fourth way of reporting this is how it arose.
				//
				// KNOWN LIMIT, not an oversight: logServerFault is silent
				// below 500 by design (respond.go, pinned by
				// TestHandleError_4xxStaysSilent). upgradeConnection returns a
				// raw *websocket.HandshakeError for an UPGRADE failure — not
				// an AppError, so it takes handleError's 500 fallback and does
				// log — but a 401 *models.AppError for an AUTH failure
				// (ws.go:543, ws.go:549), which still logs nothing. That gap
				// is class-wide across all four serveWS call sites and its fix
				// belongs in respond.go, not here.
				handleError(c, err)
			}
			return
		}
		// gorilla's Upgrade hijacks the connection, so net/http never closes it.
		// Every return below (stream-error, write-error — ctx-done never
		// actually fires today, since nothing external ever cancels ctx) left
		// the socket and its goroutines open until this defer (agent-os-14gr),
		// same shape as every other serveWS call site (operations.go,
		// logs.go, monitoring.go, terminal.go, update_jobs_ws.go).
		defer release()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)

		containerIDs, err := h.docker.GetRunningContainerIDs(ctx)
		if err != nil {
			slog.Error("Failed to get running container IDs for dashboard metrics", "error", err)
			// safePingLoop is already running on this connection (started
			// above); the raw writeCloseMessage races its WriteMutex-guarded
			// ping over the same *websocket.Conn (agent-os-1jzj).
			safeWriteCloseMessage(conn, 1011, "Failed to get containers")
			return
		}

		if len(containerIDs) == 0 {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
		emptyHostLoop:
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					// agent-os-ear5: re-check the host on every tick instead of
					// trusting the list fetched once at connect — same class as
					// monitoring.go's identical shape (agent-os-74rl). Before this
					// fix a container started after connect never appeared for
					// the life of the socket. A re-check failure is treated as
					// "still empty" (best-effort — a transient Docker hiccup on
					// a HEALTHY, already-open socket must not tear it down; the
					// initial GetRunningContainerIDs error path above still owns
					// the "refuse before streaming" behaviour for a genuine
					// setup failure).
					ids, err := h.docker.GetRunningContainerIDs(ctx)
					if err == nil && len(ids) > 0 {
						// The host stopped being empty: leave this branch with
						// the now-populated list and fall through to the real
						// streaming setup below — same shared code path a
						// non-empty connect would have taken.
						containerIDs = ids
						break emptyHostLoop
					}
					if err != nil {
						slog.Debug("re-check for empty dashboard metrics stream failed; still holding socket open", "error", err)
					}
					frame := MetricsFrame{
						Timestamp: time.Now().Format(time.RFC3339),
						// A nil slice marshals to JSON null, but
						// MetricsMessage.containers on the frontend is typed as a
						// non-nullable array (useMetricsBase.ts) and dereferenced
						// unguarded — an empty slice keeps the wire payload matching
						// the declared type (agent-os-5scv).
						Containers: []models.ContainerMetrics{},
					}
					if err := safeWriteJSON(conn, frame); err != nil {
						return
					}
				}
			}
		}

		statsChan, err := h.monitor.StreamStats(ctx, containerIDs)
		if err != nil {
			slog.Error("Failed to start dashboard stats stream", "error", err)
			// Same race as above: safePingLoop is already running.
			safeWriteCloseMessage(conn, 1011, "Failed to stream metrics")
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
					slog.Debug("Failed to write dashboard metrics frame", "error", err)
					return
				}
			}
		}
	}
}
