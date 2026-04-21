package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ResourcesHandler struct {
	docker    *services.DockerService
	db        *database.DB
	scheduler *services.SchedulerService
}

func NewResourcesHandler(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService) *ResourcesHandler {
	return &ResourcesHandler{docker: docker, db: db, scheduler: scheduler}
}

func (h *ResourcesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/resources/images", h.listImages)
	r.DELETE("/resources/images/:id", h.deleteImage)
	r.POST("/resources/images/prune", h.pruneImages)

	r.GET("/resources/containers", h.listContainers)
	r.POST("/resources/containers/:id/start", h.startContainer)
	r.POST("/resources/containers/:id/stop", h.stopContainer)
	r.POST("/resources/containers/:id/restart", h.restartContainer)
	r.DELETE("/resources/containers/:id", h.deleteContainer)
	r.POST("/resources/containers/prune", h.pruneContainers)

	r.GET("/resources/updates", h.checkUpdates)
	r.POST("/resources/containers/:id/update", h.updateContainer)

	r.GET("/resources/updates/history", h.getUpdateHistory)
	r.DELETE("/resources/updates/history", h.clearUpdateHistory)

	r.GET("/resources/auto-update/policies", h.listAutoUpdatePolicies)
	r.PUT("/resources/auto-update/policies/:targetType/:targetId", h.upsertAutoUpdatePolicy)
	r.DELETE("/resources/auto-update/policies/:targetType/:targetId", h.deleteAutoUpdatePolicy)

	r.GET("/resources/volumes", h.listVolumes)
	r.DELETE("/resources/volumes/:name", h.deleteVolume)
	r.POST("/resources/volumes/prune", h.pruneVolumes)

	r.GET("/resources/networks", h.listNetworks)
	r.DELETE("/resources/networks/:id", h.deleteNetwork)
	r.POST("/resources/networks/prune", h.pruneNetworks)

	r.GET("/resources/build-cache", h.listBuildCache)
	r.POST("/resources/build-cache/prune", h.pruneBuildCache)
}

func (h *ResourcesHandler) listImages(c *gin.Context) {
	images, err := h.docker.ListImages(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list images", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list images"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete image: " + err.Error()})
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": resp})
}

func (h *ResourcesHandler) pruneImages(c *gin.Context) {
	report, err := h.docker.PruneImages(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune images", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune images: " + err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list containers"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": containers})
}

func (h *ResourcesHandler) startContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to start container", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start container: " + err.Error()})
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_start", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container started"})
}

func (h *ResourcesHandler) stopContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StopContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to stop container", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to stop container: " + err.Error()})
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_stop", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container stopped"})
}

func (h *ResourcesHandler) restartContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.RestartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to restart container", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to restart container: " + err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete container: " + err.Error()})
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_delete", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ResourcesHandler) pruneContainers(c *gin.Context) {
	report, err := h.docker.PruneContainers(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune containers", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune containers: " + err.Error()})
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

func (h *ResourcesHandler) listVolumes(c *gin.Context) {
	volumes, err := h.docker.ListVolumes(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list volumes", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list volumes"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"volumes": volumes})
}

func (h *ResourcesHandler) deleteVolume(c *gin.Context) {
	name := c.Param("name")
	force := c.Query("force") == "true"

	if err := h.docker.DeleteVolume(c.Request.Context(), name, force); err != nil {
		slog.Error("Failed to delete volume", "name", name, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete volume: " + err.Error()})
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "volume_delete", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": name})
}

func (h *ResourcesHandler) pruneVolumes(c *gin.Context) {
	report, err := h.docker.PruneVolumes(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune volumes", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune volumes: " + err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list networks"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"networks": networks})
}

func (h *ResourcesHandler) deleteNetwork(c *gin.Context) {
	id := c.Param("id")

	if err := h.docker.DeleteNetwork(c.Request.Context(), id); err != nil {
		slog.Error("Failed to delete network", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete network: " + err.Error()})
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_delete", Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ResourcesHandler) pruneNetworks(c *gin.Context) {
	report, err := h.docker.PruneNetworks(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune networks", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune networks: " + err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list build cache"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

func (h *ResourcesHandler) pruneBuildCache(c *gin.Context) {
	report, err := h.docker.PruneBuildCache(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune build cache", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune build cache: " + err.Error()})
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
			cachedUpdates, err := h.scheduler.RunScan(c.Request.Context())
			if err != nil {
				if err.Error() == "scan already in progress" {
					c.JSON(http.StatusConflict, gin.H{"error": "Scan already in progress", "code": "SCAN_IN_PROGRESS"})
					return
				}
				slog.Error("Failed to scan for updates", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for updates: " + err.Error()})
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
			if updates == nil {
				updates = []models.ContainerUpdateInfo{}
			}

			lastScanAt, _ := h.db.GetSetting("update_scan_last_run")
			BroadcastEvent(models.StackEvent{Type: "update_scan_complete", Timestamp: time.Now()})
			c.JSON(http.StatusOK, gin.H{
				"updates":   updates,
				"fromCache": false,
				"scannedAt": lastScanAt,
			})
			return
		}

		updates, err := h.docker.CheckForUpdates(c.Request.Context(), h.db)
		if err != nil {
			slog.Error("Failed to check for updates", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for updates: " + err.Error()})
			return
		}
		if updates == nil {
			updates = []models.ContainerUpdateInfo{}
		}
		c.JSON(http.StatusOK, gin.H{"updates": updates, "fromCache": false})
		return
	}

	cachedUpdates, err := h.db.GetCachedUpdates()
	if err != nil {
		slog.Error("Failed to get cached updates", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get cached updates"})
		return
	}

	if len(cachedUpdates) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"updates":   []models.ContainerUpdateInfo{},
			"fromCache": false,
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
	})
}

func (h *ResourcesHandler) updateContainer(c *gin.Context) {
	id := c.Param("id")

	inspect, err := h.docker.InspectContainer(c.Request.Context(), id)
	if err != nil {
		slog.Error("Failed to inspect container before update", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to inspect container"})
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

		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update container: " + err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get update history"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "olderThan parameter is required", "code": "VALIDATION_ERROR"})
		return
	}

	t, err := time.Parse(time.RFC3339, olderThan)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid olderThan date format", "code": "VALIDATION_ERROR"})
		return
	}

	deleted, err := h.db.DeleteUpdateHistoryOlderThan(t)
	if err != nil {
		slog.Error("Failed to clear update history", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to clear update history"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func (h *ResourcesHandler) listAutoUpdatePolicies(c *gin.Context) {
	policies, err := h.db.GetAutoUpdatePolicies()
	if err != nil {
		slog.Error("Failed to get auto-update policies", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get auto-update policies"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "targetType must be 'container' or 'stack'", "code": "VALIDATION_ERROR"})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "code": "VALIDATION_ERROR"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save auto-update policy"})
		return
	}

	BroadcastEvent(models.StackEvent{Type: "update_policy_changed", Timestamp: time.Now()})
	c.JSON(http.StatusOK, policy)
}

func (h *ResourcesHandler) deleteAutoUpdatePolicy(c *gin.Context) {
	targetType := c.Param("targetType")
	targetId := c.Param("targetId")

	if targetType != "container" && targetType != "stack" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "targetType must be 'container' or 'stack'", "code": "VALIDATION_ERROR"})
		return
	}

	if err := h.db.DeleteAutoUpdatePolicy(targetType, targetId); err != nil {
		slog.Error("Failed to delete auto-update policy", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete auto-update policy"})
		return
	}

	c.Status(http.StatusNoContent)
}
