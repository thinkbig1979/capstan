package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// updateScanner is the narrow seam checkUpdates needs from a scheduler: start
// a background scan and report whether one is in flight. *services.SchedulerService
// satisfies it structurally, so production wiring is unchanged. Tests substitute
// a fake to drive scanStartIsBenign's unreached branch through the router: a real
// SchedulerService's StartBackgroundScan can only ever return ErrScanInProgress,
// ErrSchedulerStopping, or nil (see scheduler.go's StartBackgroundScan), so there
// is no way to reach checkUpdates' 500 branch through the concrete type alone
// (agent-os-10hb).
type updateScanner interface {
	StartBackgroundScan() error
	IsScanning() bool
}

type ResourcesHandler struct {
	docker     *services.DockerService
	db         *database.DB
	scheduler  updateScanner
	jobManager *services.UpdateJobManager
	actionLog  *services.ActionLogger
}

// NewResourcesHandler delegates to NewResourcesHandlerWithJobManager (nil
// jobManager) so the nil-check below has exactly one copy to keep correct.
//
// Both constructors keep *services.SchedulerService as their parameter type
// (production callers, e.g. cmd/server/main.go, are unaffected) and nil-check
// the concrete pointer before assigning it to the interface field. That order
// matters: main.go can pass a typed-nil *SchedulerService when Docker is
// unavailable, and boxing a typed nil pointer into an interface value directly
// produces a NON-nil interface (the classic typed-nil trap) — h.scheduler != nil
// would then be true and StartBackgroundScan/IsScanning would run on a nil
// receiver, which panics (both dereference s.mu with no nil-receiver guard).
// Checking scheduler != nil on the still-concrete parameter avoids that.
func NewResourcesHandler(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService) *ResourcesHandler {
	return NewResourcesHandlerWithJobManager(docker, db, scheduler, nil)
}

func NewResourcesHandlerWithJobManager(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService, jobManager *services.UpdateJobManager) *ResourcesHandler {
	h := &ResourcesHandler{docker: docker, db: db, jobManager: jobManager, actionLog: services.NewActionLogger(db)}
	if scheduler != nil {
		h.scheduler = scheduler
	}
	return h
}

func (h *ResourcesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/resources/images", h.listImages)
	r.DELETE("/resources/images/:id", h.deleteImage)
	r.POST("/resources/images/prune", h.pruneImages)

	r.GET("/resources/containers", h.listContainers)
	r.GET("/resources/containers/:id/inspect", h.inspectContainer)
	r.POST("/resources/containers/:id/start", h.startContainer)
	r.POST("/resources/containers/:id/stop", h.stopContainer)
	r.POST("/resources/containers/:id/restart", h.restartContainer)
	r.DELETE("/resources/containers/:id", h.deleteContainer)
	r.POST("/resources/containers/prune", h.pruneContainers)

	r.GET("/resources/updates", h.checkUpdates)
	r.POST("/resources/containers/:id/update", h.updateContainer)
	r.POST("/resources/stacks/:id/update", h.updateStack)

	r.GET("/resources/updates/jobs", h.listUpdateJobs)
	r.GET("/resources/updates/jobs/:jobId", h.getUpdateJob)

	r.GET("/resources/updates/history", h.getUpdateHistory)
	r.DELETE("/resources/updates/history", h.clearUpdateHistory)

	r.GET("/resources/auto-update/policies", h.listAutoUpdatePolicies)
	r.PUT("/resources/auto-update/policies/:targetType/:targetId", h.upsertAutoUpdatePolicy)
	r.DELETE("/resources/auto-update/policies/:targetType/:targetId", h.deleteAutoUpdatePolicy)

	r.GET("/resources/volumes", h.listVolumes)
	r.DELETE("/resources/volumes/:name", h.deleteVolume)
	r.POST("/resources/volumes/prune", h.pruneVolumes)

	r.GET("/resources/networks", h.listNetworks)
	r.POST("/resources/networks", h.createNetwork)
	r.DELETE("/resources/networks/:id", h.deleteNetwork)
	r.POST("/resources/networks/prune", h.pruneNetworks)

	r.GET("/resources/build-cache", h.listBuildCache)
	r.POST("/resources/build-cache/prune", h.pruneBuildCache)
}

func (h *ResourcesHandler) listImages(c *gin.Context) {
	images, err := h.docker.ListImages(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list images", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list images")
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": images})
}

func (h *ResourcesHandler) listContainers(c *gin.Context) {
	containers, err := h.docker.GetAllContainersWithDetails(c.Request.Context(), nil)
	if err != nil {
		slog.Error("Failed to list containers", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list containers")
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": containers})
}

func (h *ResourcesHandler) startContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to start container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to start container")
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_start", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container started"})
}

func (h *ResourcesHandler) stopContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StopContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to stop container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to stop container")
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_stop", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container stopped"})
}

func (h *ResourcesHandler) restartContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.RestartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to restart container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to restart container")
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_restart", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container restarted"})
}

func (h *ResourcesHandler) inspectContainer(c *gin.Context) {
	id := c.Param("id")
	inspect, err := h.docker.InspectContainer(c.Request.Context(), id)
	if err != nil {
		slog.Error("Failed to inspect container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to inspect container")
		return
	}

	formatted, err := json.MarshalIndent(inspect, "", "  ")
	if err != nil {
		slog.Error("Failed to format inspect output", "id", id, "error", err)
		handleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to format inspect output"))
		return
	}

	c.Data(http.StatusOK, "application/json", formatted)
}

func (h *ResourcesHandler) listVolumes(c *gin.Context) {
	volumes, err := h.docker.ListVolumes(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list volumes", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list volumes")
		return
	}
	c.JSON(http.StatusOK, gin.H{"volumes": volumes})
}

func (h *ResourcesHandler) listNetworks(c *gin.Context) {
	networks, err := h.docker.ListNetworks(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list networks", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list networks")
		return
	}
	c.JSON(http.StatusOK, gin.H{"networks": networks})
}

// BuildCacheEntry is our own wire representation of a Docker build-cache
// record.
//
// The endpoint used to serialize build.CacheRecord from the Docker SDK
// straight to the wire, which made it the only PascalCase payload in the API
// and inherited an upstream tag typo: CacheRecord declares
// `json:" Parents,omitempty"` with a LEADING SPACE, so the field went out as
// " Parents" and the frontend's Parents was permanently undefined
// (agent-os-iuby). Declaring our own type closes both problems for good — a
// tag change upstream can no longer alter our contract.
//
// The deprecated CacheRecord.Parent (singular, deprecated in API v1.42) is
// deliberately not carried over; nothing consumed it.
type BuildCacheEntry struct {
	ID          string     `json:"id"`
	Parents     []string   `json:"parents,omitempty"`
	Type        string     `json:"type"`
	Description string     `json:"description"`
	InUse       bool       `json:"inUse"`
	Shared      bool       `json:"shared"`
	Size        int64      `json:"size"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
	UsageCount  int        `json:"usageCount"`
}

// toBuildCacheEntries maps the Docker SDK records onto our own response type.
func toBuildCacheEntries(records []*build.CacheRecord) []BuildCacheEntry {
	entries := make([]BuildCacheEntry, 0, len(records))
	for _, r := range records {
		if r == nil {
			continue
		}
		entries = append(entries, BuildCacheEntry{
			ID:          r.ID,
			Parents:     r.Parents,
			Type:        r.Type,
			Description: r.Description,
			InUse:       r.InUse,
			Shared:      r.Shared,
			Size:        r.Size,
			CreatedAt:   r.CreatedAt,
			LastUsedAt:  r.LastUsedAt,
			UsageCount:  r.UsageCount,
		})
	}
	return entries
}

func (h *ResourcesHandler) listBuildCache(c *gin.Context) {
	records, err := h.docker.ListBuildCache(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list build cache", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list build cache")
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": toBuildCacheEntries(records)})
}
