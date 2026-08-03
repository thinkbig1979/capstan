package handlers

import (
	"context"
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
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get dashboard stats"})
			return
		}

		containers, err := h.docker.GetAllContainersWithDetails(ctx, h.db)
		if err != nil {
			slog.Error("Failed to get containers for dashboard", "error", err)
			containers = nil
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
		if h.docker == nil {
			writeJSONError(c, http.StatusServiceUnavailable, "DOCKER_UNAVAILABLE", DockerUnavailableMessage)
			return
		}

		conn, err := upgradeConnection(c, h.db, jwtSecret, authDisabled)
		if err != nil {
			// gin.Context.Error's own return (a *gin.Error) is for chaining,
			// not a failure signal; recording the error is the point here.
			_ = c.Error(err)
			return
		}

		if err := h.cm.Add(conn.ID, conn); err != nil {
			writeCloseMessage(conn.Conn, 4401, "Connection limit exceeded")
			conn.Conn.Close()
			return
		}

		defer h.cm.Remove(conn.ID)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go safePingLoop(ctx, conn, DefaultPingInterval)

		containerIDs, err := h.docker.GetRunningContainerIDs(ctx)
		if err != nil {
			slog.Error("Failed to get running container IDs for dashboard metrics", "error", err)
			writeCloseMessage(conn.Conn, 1011, "Failed to get containers")
			return
		}

		if len(containerIDs) == 0 {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					frame := MetricsFrame{
						Timestamp:  time.Now().Format(time.RFC3339),
						Containers: nil,
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
			writeCloseMessage(conn.Conn, 1011, "Failed to stream metrics")
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
