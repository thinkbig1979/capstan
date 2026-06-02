package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	dockertypes "github.com/docker/docker/api/types"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

type ResourcesHandler struct {
	docker     *services.DockerService
	db         *database.DB
	scheduler  *services.SchedulerService
	jobManager *services.UpdateJobManager
}

func NewResourcesHandler(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService) *ResourcesHandler {
	return &ResourcesHandler{docker: docker, db: db, scheduler: scheduler}
}

func NewResourcesHandlerWithJobManager(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService, jobManager *services.UpdateJobManager) *ResourcesHandler {
	return &ResourcesHandler{docker: docker, db: db, scheduler: scheduler, jobManager: jobManager}
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
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list images"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": images})
}

func (h *ResourcesHandler) deleteImage(c *gin.Context) {
	id := c.Param("id")
	force := c.Query("force") == "true"

	resp, err := h.docker.DeleteImage(c.Request.Context(), id, force)
	if err != nil {
		slog.Error("Failed to delete image", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete image"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": resp})
}

// pruneUntilRegex bounds the `until` filter to a simple Go duration (e.g. "24h",
// "30m", "168h") so an arbitrary, potentially malicious value never reaches the
// Docker daemon. The frontend only ever sends hour-based presets.
var pruneUntilRegex = regexp.MustCompile(`^[0-9]{1,6}(h|m|s)$`)

// parsePruneOptions reads the optional all/until flags shared by every prune
// endpoint. An invalid `until` is dropped (treated as absent) rather than erroring,
// keeping prune forgiving — the worst case is a slightly broader prune.
func parsePruneOptions(c *gin.Context) services.PruneOptions {
	opts := services.PruneOptions{All: c.Query("all") == "true"}
	if until := c.Query("until"); pruneUntilRegex.MatchString(until) {
		opts.Until = until
	}
	return opts
}

func (h *ResourcesHandler) pruneImages(c *gin.Context) {
	report, err := h.docker.PruneImages(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune images", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to prune images"))
		return
	}
	deleted := make([]string, 0, len(report.ImagesDeleted))
	for _, d := range report.ImagesDeleted {
		deleted = append(deleted, d.Deleted)
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{
		"deleted":        deleted,
		"spaceReclaimed": report.SpaceReclaimed,
	})
}

func (h *ResourcesHandler) listContainers(c *gin.Context) {
	containers, err := h.docker.GetAllContainersWithDetails(c.Request.Context(), nil)
	if err != nil {
		slog.Error("Failed to list containers", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list containers"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": containers})
}

func (h *ResourcesHandler) startContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to start container", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to start container"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_start", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container started"})
}

func (h *ResourcesHandler) stopContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StopContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to stop container", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to stop container"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_stop", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container stopped"})
}

func (h *ResourcesHandler) restartContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.RestartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to restart container", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to restart container"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_restart", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container restarted"})
}

func (h *ResourcesHandler) deleteContainer(c *gin.Context) {
	id := c.Param("id")
	force := c.Query("force") == "true"

	if err := h.docker.DeleteContainer(c.Request.Context(), id, force); err != nil {
		slog.Error("Failed to delete container", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to delete container"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_delete", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ResourcesHandler) pruneContainers(c *gin.Context) {
	report, err := h.docker.PruneContainers(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune containers", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to prune containers"))
		return
	}
	deleted := report.ContainersDeleted
	if deleted == nil {
		deleted = []string{}
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_prune", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{
		"deleted":        deleted,
		"spaceReclaimed": report.SpaceReclaimed,
	})
}

func (h *ResourcesHandler) inspectContainer(c *gin.Context) {
	id := c.Param("id")
	inspect, err := h.docker.InspectContainer(c.Request.Context(), id)
	if err != nil {
		slog.Error("Failed to inspect container", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to inspect container"))
		return
	}

	formatted, err := json.MarshalIndent(inspect, "", "  ")
	if err != nil {
		slog.Error("Failed to format inspect output", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to format inspect output"))
		return
	}

	c.Data(http.StatusOK, "application/json", formatted)
}

func (h *ResourcesHandler) listVolumes(c *gin.Context) {
	volumes, err := h.docker.ListVolumes(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list volumes", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list volumes"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"volumes": volumes})
}

func (h *ResourcesHandler) deleteVolume(c *gin.Context) {
	name := c.Param("name")
	force := c.Query("force") == "true"

	if err := h.docker.DeleteVolume(c.Request.Context(), name, force); err != nil {
		slog.Error("Failed to delete volume", "name", name, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to delete volume"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "volume_delete", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": name})
}

func (h *ResourcesHandler) pruneVolumes(c *gin.Context) {
	report, err := h.docker.PruneVolumes(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune volumes", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to prune volumes"))
		return
	}
	deleted := report.VolumesDeleted
	if deleted == nil {
		deleted = []string{}
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "volume_prune", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{
		"deleted":        deleted,
		"spaceReclaimed": report.SpaceReclaimed,
	})
}

func (h *ResourcesHandler) listNetworks(c *gin.Context) {
	networks, err := h.docker.ListNetworks(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list networks", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list networks"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"networks": networks})
}

type createNetworkRequest struct {
	Name       string `json:"name" binding:"required"`
	Driver     string `json:"driver"`
	Internal   bool   `json:"internal"`
	Attachable bool   `json:"attachable"`
}

var networkNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

func (h *ResourcesHandler) createNetwork(c *gin.Context) {
	var req createNetworkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, "INVALID_INPUT", "Invalid request body"))
		return
	}
	if !networkNameRegex.MatchString(req.Name) {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, "INVALID_INPUT", "Network name must start with a letter or digit and contain only letters, digits, '_', '.', or '-' (max 63 chars)"))
		return
	}
	driver := req.Driver
	if driver == "" {
		driver = "bridge"
	}

	opts := dockertypes.NetworkCreate{
		Driver:     driver,
		Internal:   req.Internal,
		Attachable: req.Attachable,
	}
	id, err := h.docker.CreateNetwork(c.Request.Context(), req.Name, opts)
	if err != nil {
		slog.Error("Failed to create network", "name", req.Name, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to create network"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_create", Timestamp: time.Now()})
	c.JSON(http.StatusCreated, gin.H{"id": id, "name": req.Name})
}

func (h *ResourcesHandler) deleteNetwork(c *gin.Context) {
	id := c.Param("id")

	if err := h.docker.DeleteNetwork(c.Request.Context(), id); err != nil {
		slog.Error("Failed to delete network", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to delete network"))
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_delete", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ResourcesHandler) pruneNetworks(c *gin.Context) {
	report, err := h.docker.PruneNetworks(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune networks", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to prune networks"))
		return
	}
	deleted := report.NetworksDeleted
	if deleted == nil {
		deleted = []string{}
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_prune", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{
		"deleted": deleted,
	})
}

func (h *ResourcesHandler) listBuildCache(c *gin.Context) {
	entries, err := h.docker.ListBuildCache(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list build cache", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list build cache"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

func (h *ResourcesHandler) pruneBuildCache(c *gin.Context) {
	report, err := h.docker.PruneBuildCache(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune build cache", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to prune build cache"))
		return
	}
	deleted := report.CachesDeleted
	if deleted == nil {
		deleted = []string{}
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "build_cache_prune", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{
		"deleted":        deleted,
		"spaceReclaimed": report.SpaceReclaimed,
	})
}

func (h *ResourcesHandler) checkUpdates(c *gin.Context) {
	refresh := c.Query("refresh")

	if refresh == "true" {
		if h.scheduler != nil {
			err := h.scheduler.StartBackgroundScan()
			if err != nil && err.Error() != "scan already in progress" {
				slog.Error("Failed to start background scan", "error", err)
				models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to start update scan"))
				return
			}

			cachedUpdates, err := h.db.GetCachedUpdates()
			if err != nil {
				slog.Error("Failed to get cached updates for refresh response", "error", err)
				cachedUpdates = nil
			}
			var updates []models.ContainerUpdateInfo
			for _, cu := range cachedUpdates {
				updates = append(updates, models.ContainerUpdateInfo{
					ContainerID:   cu.ContainerID,
					ContainerName: cu.ContainerName,
					Image:         cu.Image,
					ImageRef:      cu.ImageRef,
					State:         cu.State,
					StackID:       cu.StackID,
					ProjectName:   cu.ProjectName,
					ServiceName:   cu.ServiceName,
					IsCompose:     cu.IsCompose,
				})
			}
			if updates == nil {
				updates = []models.ContainerUpdateInfo{}
			}
			lastScanAt, _ := h.db.GetSetting("update_scan_last_run")
			c.JSON(http.StatusAccepted, gin.H{
				"updates":   updates,
				"fromCache": len(cachedUpdates) > 0,
				"scannedAt": lastScanAt,
				"scanning":  true,
			})
			return
		}

		updates, err := h.docker.CheckForUpdates(c.Request.Context(), h.db)
		if err != nil {
			slog.Error("Failed to check for updates", "error", err)
			models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check for updates"))
			return
		}
		if updates == nil {
			updates = []models.ContainerUpdateInfo{}
		}
		c.JSON(http.StatusOK, gin.H{"updates": updates, "fromCache": false})
		return
	}

	isScanning := false
	if h.scheduler != nil {
		isScanning = h.scheduler.IsScanning()
	}

	cachedUpdates, err := h.db.GetCachedUpdates()
	if err != nil {
		slog.Error("Failed to get cached updates", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get cached updates"))
		return
	}

	if len(cachedUpdates) == 0 {
		lastScanAt, _ := h.db.GetSetting("update_scan_last_run")
		c.JSON(http.StatusOK, gin.H{
			"updates":   []models.ContainerUpdateInfo{},
			"fromCache": false,
			"scannedAt": lastScanAt,
			"scanning":  isScanning,
		})
		return
	}

	var updates []models.ContainerUpdateInfo
	for _, cu := range cachedUpdates {
		updates = append(updates, models.ContainerUpdateInfo{
			ContainerID:   cu.ContainerID,
			ContainerName: cu.ContainerName,
			Image:         cu.Image,
			ImageRef:      cu.ImageRef,
			State:         cu.State,
			StackID:       cu.StackID,
			ProjectName:   cu.ProjectName,
			ServiceName:   cu.ServiceName,
			IsCompose:     cu.IsCompose,
		})
	}

	lastScanAt, _ := h.db.GetSetting("update_scan_last_run")
	c.JSON(http.StatusOK, gin.H{
		"updates":   updates,
		"fromCache": true,
		"scannedAt": lastScanAt,
		"scanning":  isScanning,
	})
}

func (h *ResourcesHandler) updateContainer(c *gin.Context) {
	id := c.Param("id")

	// If no job manager is wired, fall back to the synchronous path.
	if h.jobManager == nil {
		h.updateContainerSync(c, id)
		return
	}

	inspect, err := h.docker.InspectContainer(c.Request.Context(), id)
	if err != nil {
		slog.Error("Failed to inspect container before update", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to inspect container"))
		return
	}

	oldDigest := inspect.Image
	imageRef := ""
	if inspect.Config != nil {
		imageRef = inspect.Config.Image
	}
	containerName := ""
	if inspect.Name != "" {
		containerName = inspect.Name[1:]
	}
	stackID := ""
	stackName := ""
	projectName := ""
	if inspect.Config != nil && inspect.Config.Labels != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
	}
	if projectName != "" {
		stack, err := h.db.GetStackByProjectName(projectName)
		if err == nil && stack != nil {
			stackID = stack.ID
			stackName = stack.ProjectName
		}
	}

	historyID := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	historyEntry := &models.UpdateHistoryEntry{
		ID:            historyID,
		ContainerID:   id,
		ContainerName: containerName,
		Image:         imageRef,
		OldDigest:     &oldDigest,
		Status:        "pending",
		Trigger:       "manual",
		StartedAt:     now,
	}
	if stackID != "" {
		historyEntry.StackID = &stackID
		historyEntry.StackName = &stackName
	}

	if err := h.db.InsertUpdateHistory(historyEntry); err != nil {
		slog.Error("Failed to insert update history", "error", err)
	}

	spec := services.JobSpec{
		TargetType: "container",
		TargetID:   id,
		Name:       containerName,
		StackID:    stackID,
	}

	// Capture locals for the closure.
	docker := h.docker
	db := h.db
	containerIDCopy := id
	historyIDCopy := historyID
	stackIDCopy := stackID

	run := func(ctx context.Context, emit func(services.LogLine), setStatus func(services.Status)) error {
		result, runErr := docker.UpdateContainerStreaming(ctx, containerIDCopy, db, emit, setStatus)

		if runErr != nil {
			updates := map[string]interface{}{
				"status":        "failed",
				"error_message": runErr.Error(),
				"completed_at":  time.Now().Format(time.RFC3339),
				"duration_ms":   result.DurationMs,
			}
			if result.NewDigest != "" {
				updates["new_digest"] = result.NewDigest
			}
			db.UpdateUpdateHistory(historyIDCopy, updates)
			BroadcastEvent(models.StackEvent{Type: "update_completed", ContainerID: containerIDCopy, StackID: stackIDCopy, Timestamp: time.Now()})
			return runErr
		}

		db.UpdateUpdateHistory(historyIDCopy, map[string]interface{}{
			"status":       "success",
			"new_digest":   result.NewDigest,
			"completed_at": time.Now().Format(time.RFC3339),
			"duration_ms":  result.DurationMs,
		})
		BroadcastEvent(models.StackEvent{Type: "update_completed", ContainerID: containerIDCopy, StackID: stackIDCopy, Timestamp: time.Now()})
		return nil
	}

	job := h.enqueueJobWithBroadcasts(spec, run)
	c.JSON(http.StatusAccepted, gin.H{
		"jobId": job.ID,
		"wsUrl": "/ws/updates/jobs/" + job.ID,
	})
}

// updateContainerSync is the legacy synchronous update path, used when no job
// manager is configured (e.g., in tests that use NewResourcesHandler directly).
func (h *ResourcesHandler) updateContainerSync(c *gin.Context, id string) {
	inspect, err := h.docker.InspectContainer(c.Request.Context(), id)
	if err != nil {
		slog.Error("Failed to inspect container before update", "id", id, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to inspect container"))
		return
	}

	oldDigest := inspect.Image
	imageRef := ""
	if inspect.Config != nil {
		imageRef = inspect.Config.Image
	}
	containerName := ""
	if inspect.Name != "" {
		containerName = inspect.Name[1:]
	}
	stackID := ""
	stackName := ""
	projectName := ""
	if inspect.Config != nil && inspect.Config.Labels != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
	}
	if projectName != "" {
		stack, err := h.db.GetStackByProjectName(projectName)
		if err == nil && stack != nil {
			stackID = stack.ID
			stackName = stack.ProjectName
		}
	}

	historyID := uuid.New().String()
	now := time.Now().Format(time.RFC3339)
	historyEntry := &models.UpdateHistoryEntry{
		ID:            historyID,
		ContainerID:   id,
		ContainerName: containerName,
		Image:         imageRef,
		OldDigest:     &oldDigest,
		Status:        "pending",
		Trigger:       "manual",
		StartedAt:     now,
	}
	if stackID != "" {
		historyEntry.StackID = &stackID
		historyEntry.StackName = &stackName
	}

	if err := h.db.InsertUpdateHistory(historyEntry); err != nil {
		slog.Error("Failed to insert update history", "error", err)
	}

	result, err := h.docker.UpdateContainer(c.Request.Context(), id, h.db)
	if err != nil {
		slog.Error("Failed to update container", "id", id, "error", err)

		updates := map[string]interface{}{
			"status":        "failed",
			"error_message": err.Error(),
			"completed_at":  time.Now().Format(time.RFC3339),
			"duration_ms":   result.DurationMs,
		}
		if result.NewDigest != "" {
			updates["new_digest"] = result.NewDigest
		}
		h.db.UpdateUpdateHistory(historyID, updates)

		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to update container"))
		return
	}

	h.db.UpdateUpdateHistory(historyID, map[string]interface{}{
		"status":       "success",
		"new_digest":   result.NewDigest,
		"completed_at": time.Now().Format(time.RFC3339),
		"duration_ms":  result.DurationMs,
	})

	BroadcastEvent(models.StackEvent{Type: "update_completed", ContainerID: id, Timestamp: time.Now()})

	c.JSON(http.StatusOK, gin.H{
		"message":    "Container updated",
		"historyId":  historyID,
		"oldDigest":  result.OldDigest,
		"newDigest":  result.NewDigest,
		"durationMs": result.DurationMs,
	})
}

// enqueueJobWithBroadcasts enqueues a job and wraps its setStatus to emit
// update_job_progress broadcasts, and a final update_job_complete on terminal.
func (h *ResourcesHandler) enqueueJobWithBroadcasts(
	spec services.JobSpec,
	run func(ctx context.Context, emit func(services.LogLine), setStatus func(services.Status)) error,
) *services.Job {
	wrapped := func(ctx context.Context, emit func(services.LogLine), setStatus func(services.Status)) error {
		wrappedSetStatus := func(s services.Status) {
			setStatus(s)
			BroadcastEvent(models.StackEvent{
				Type:      "update_job_progress",
				StackID:   spec.StackID,
				Event:     string(s),
				Status:    string(s),
				Timestamp: time.Now(),
			})
		}
		runErr := run(ctx, emit, wrappedSetStatus)
		finalStatus := services.StatusSuccess
		if runErr != nil {
			finalStatus = services.StatusError
		}
		BroadcastEvent(models.StackEvent{
			Type:      "update_job_complete",
			StackID:   spec.StackID,
			Event:     string(finalStatus),
			Status:    string(finalStatus),
			Timestamp: time.Now(),
		})
		return runErr
	}

	return h.jobManager.Enqueue(spec, wrapped)
}

// updateStack enqueues an outdated-only streaming stack update.
func (h *ResourcesHandler) updateStack(c *gin.Context) {
	if h.jobManager == nil {
		models.HandleError(c, models.NewAppError(http.StatusServiceUnavailable, "INTERNAL_ERROR", "Job manager not available"))
		return
	}

	stackID := c.Param("id")
	stack, err := h.db.GetStack(stackID)
	if err != nil || stack == nil {
		models.HandleError(c, models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Stack not found"))
		return
	}

	// Find outdated services for this stack from the cache.
	cachedUpdates, err := h.db.GetCachedUpdates()
	if err != nil {
		slog.Error("Failed to get cached updates for stack", "stackId", stackID, "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get cached updates"))
		return
	}

	var outdated []models.CachedUpdate
	for _, cu := range cachedUpdates {
		if cu.StackID == stack.ID && cu.IsCompose && cu.ServiceName != "" {
			outdated = append(outdated, cu)
		}
	}

	if len(outdated) == 0 {
		c.JSON(http.StatusOK, gin.H{"jobId": "", "noUpdates": true})
		return
	}

	spec := services.JobSpec{
		TargetType: "stack",
		TargetID:   stack.ID,
		Name:       stack.ProjectName,
		StackID:    stack.ID,
	}

	docker := h.docker
	db := h.db
	stackCopy := *stack
	outdatedCopy := outdated

	run := func(ctx context.Context, emit func(services.LogLine), setStatus func(services.Status)) error {
		for _, svc := range outdatedCopy {
			emit(services.LogLine{Ts: time.Now().UTC(), Text: "==> Updating service: " + svc.ServiceName, Stream: services.StreamStatus})

			// History row.
			historyID := uuid.New().String()
			nowStr := time.Now().Format(time.RFC3339)
			stackIDStr := stackCopy.ID
			stackNameStr := stackCopy.ProjectName
			oldDigest := svc.LocalDigest
			entry := &models.UpdateHistoryEntry{
				ID:            historyID,
				ContainerName: svc.ServiceName,
				Image:         svc.Image,
				OldDigest:     &oldDigest,
				Status:        "pending",
				Trigger:       "manual",
				StartedAt:     nowStr,
				StackID:       &stackIDStr,
				StackName:     &stackNameStr,
			}
			_ = db.InsertUpdateHistory(entry)

			start := time.Now()
			oldD, newD, durMs, svcErr := docker.UpdateComposeServiceStreaming(ctx, stackCopy, svc.ServiceName, emit, setStatus)
			_ = oldD

			if svcErr != nil {
				db.UpdateUpdateHistory(historyID, map[string]interface{}{
					"status":        "failed",
					"error_message": svcErr.Error(),
					"completed_at":  time.Now().Format(time.RFC3339),
					"duration_ms":   time.Since(start).Milliseconds(),
				})
				return fmt.Errorf("service %s: %w", svc.ServiceName, svcErr)
			}

			db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":       "success",
				"new_digest":   newD,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  durMs,
			})
			emit(services.LogLine{Ts: time.Now().UTC(), Text: "Service " + svc.ServiceName + " updated successfully", Stream: services.StreamStatus})
		}
		return nil
	}

	job := h.enqueueJobWithBroadcasts(spec, run)
	c.JSON(http.StatusAccepted, gin.H{
		"jobId": job.ID,
		"wsUrl": "/ws/updates/jobs/" + job.ID,
	})
}

// listUpdateJobs returns all known jobs (active + recently finished).
func (h *ResourcesHandler) listUpdateJobs(c *gin.Context) {
	if h.jobManager == nil {
		c.JSON(http.StatusOK, gin.H{"jobs": []*services.Job{}})
		return
	}
	jobs := h.jobManager.List()
	if jobs == nil {
		jobs = []*services.Job{}
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

// getUpdateJob returns a single job by ID.
func (h *ResourcesHandler) getUpdateJob(c *gin.Context) {
	if h.jobManager == nil {
		models.HandleError(c, models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Job not found"))
		return
	}
	jobID := c.Param("jobId")
	job := h.jobManager.Get(jobID)
	if job == nil {
		models.HandleError(c, models.NewAppError(http.StatusNotFound, models.ErrNotFound, "Job not found"))
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *ResourcesHandler) getUpdateHistory(c *gin.Context) {
	filters := models.UpdateHistoryFilters{
		Page:        1,
		Limit:       25,
		Status:      c.Query("status"),
		Trigger:     c.Query("trigger"),
		ContainerID: c.Query("containerId"),
		StackID:     c.Query("stackId"),
	}

	if p := c.Query("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			filters.Page = v
		}
	}
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			filters.Limit = v
		}
	}
	if from := c.Query("from"); from != "" {
		if t, err := time.Parse(time.RFC3339, from); err == nil {
			filters.From = &t
		}
	}
	if to := c.Query("to"); to != "" {
		if t, err := time.Parse(time.RFC3339, to); err == nil {
			filters.To = &t
		}
	}

	entries, total, err := h.db.GetUpdateHistory(filters)
	if err != nil {
		slog.Error("Failed to get update history", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get update history"))
		return
	}

	if entries == nil {
		entries = []models.UpdateHistoryEntry{}
	}

	totalPages := total / filters.Limit
	if total%filters.Limit > 0 {
		totalPages++
	}

	c.JSON(http.StatusOK, gin.H{
		"entries":    entries,
		"total":      total,
		"page":       filters.Page,
		"limit":      filters.Limit,
		"totalPages": totalPages,
	})
}

func (h *ResourcesHandler) clearUpdateHistory(c *gin.Context) {
	olderThan := c.Query("olderThan")
	if olderThan == "" {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "olderThan parameter is required"))
		return
	}

	t, err := time.Parse(time.RFC3339, olderThan)
	if err != nil {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid olderThan date format"))
		return
	}

	deleted, err := h.db.DeleteUpdateHistoryOlderThan(t)
	if err != nil {
		slog.Error("Failed to clear update history", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to clear update history"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func (h *ResourcesHandler) listAutoUpdatePolicies(c *gin.Context) {
	policies, err := h.db.GetAutoUpdatePolicies()
	if err != nil {
		slog.Error("Failed to get auto-update policies", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get auto-update policies"))
		return
	}

	autoEnabledStr, _ := h.db.GetSetting("auto_update_enabled")

	if policies == nil {
		policies = []models.AutoUpdatePolicy{}
	}

	c.JSON(http.StatusOK, gin.H{
		"globalEnabled": autoEnabledStr == "true",
		"policies":      policies,
	})
}

func (h *ResourcesHandler) upsertAutoUpdatePolicy(c *gin.Context) {
	targetType := c.Param("targetType")
	targetId := c.Param("targetId")

	if targetType != "container" && targetType != "stack" {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "targetType must be 'container' or 'stack'"))
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid request body"))
		return
	}

	now := time.Now().Format(time.RFC3339)

	existing, err := h.db.GetAutoUpdatePolicy(targetType, targetId)

	policy := &models.AutoUpdatePolicy{
		ID:         uuid.New().String(),
		TargetType: targetType,
		TargetID:   targetId,
		Enabled:    req.Enabled,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err == nil && existing != nil {
		policy.ID = existing.ID
		policy.ConsecutiveFailures = existing.ConsecutiveFailures
		policy.Paused = existing.Paused
		policy.CreatedAt = existing.CreatedAt
		if req.Enabled && existing.Paused {
			policy.Paused = false
			policy.ConsecutiveFailures = 0
		}
	}

	if !req.Enabled {
		policy.Paused = false
		policy.ConsecutiveFailures = 0
	}

	if err := h.db.UpsertAutoUpdatePolicy(policy); err != nil {
		slog.Error("Failed to upsert auto-update policy", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save auto-update policy"))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "update_policy_changed", Timestamp: time.Now()})
	c.JSON(http.StatusOK, policy)
}

func (h *ResourcesHandler) deleteAutoUpdatePolicy(c *gin.Context) {
	targetType := c.Param("targetType")
	targetId := c.Param("targetId")

	if targetType != "container" && targetType != "stack" {
		models.HandleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "targetType must be 'container' or 'stack'"))
		return
	}

	if err := h.db.DeleteAutoUpdatePolicy(targetType, targetId); err != nil {
		slog.Error("Failed to delete auto-update policy", "error", err)
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete auto-update policy"))
		return
	}

	c.Status(http.StatusNoContent)
}
