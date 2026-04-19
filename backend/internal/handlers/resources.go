package handlers

import (
	"log/slog"
	"net/http"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/docker-manager/backend/internal/services"
	"github.com/gin-gonic/gin"
)

type ResourcesHandler struct {
	docker *services.DockerService
	db     *database.DB
}

func NewResourcesHandler(docker *services.DockerService, db *database.DB) *ResourcesHandler {
	return &ResourcesHandler{docker: docker, db: db}
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
	c.JSON(http.StatusOK, gin.H{"message": "Container started"})
}

func (h *ResourcesHandler) stopContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.StopContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to stop container", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to stop container: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Container stopped"})
}

func (h *ResourcesHandler) restartContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.RestartContainer(c.Request.Context(), id); err != nil {
		slog.Error("Failed to restart container", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to restart container: " + err.Error()})
		return
	}
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
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ResourcesHandler) pruneContainers(c *gin.Context) {
	report, err := h.docker.PruneContainers(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune containers", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune containers: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"deleted":        report.ContainersDeleted,
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
	c.JSON(http.StatusOK, gin.H{"deleted": name})
}

func (h *ResourcesHandler) pruneVolumes(c *gin.Context) {
	report, err := h.docker.PruneVolumes(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune volumes", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune volumes: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"deleted":        report.VolumesDeleted,
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
	c.JSON(http.StatusOK, gin.H{"deleted": id})
}

func (h *ResourcesHandler) pruneNetworks(c *gin.Context) {
	report, err := h.docker.PruneNetworks(c.Request.Context())
	if err != nil {
		slog.Error("Failed to prune networks", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to prune networks: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"deleted": report.NetworksDeleted,
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
	c.JSON(http.StatusOK, gin.H{
		"deleted":        report.CachesDeleted,
		"spaceReclaimed": report.SpaceReclaimed,
	})
}

func (h *ResourcesHandler) checkUpdates(c *gin.Context) {
	updates, err := h.docker.CheckForUpdates(c.Request.Context(), h.db)
	if err != nil {
		slog.Error("Failed to check for updates", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for updates: " + err.Error()})
		return
	}
	if updates == nil {
		updates = []models.ContainerUpdateInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"updates": updates})
}

func (h *ResourcesHandler) updateContainer(c *gin.Context) {
	id := c.Param("id")
	if err := h.docker.UpdateContainer(c.Request.Context(), id, h.db); err != nil {
		slog.Error("Failed to update container", "id", id, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update container: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Container updated"})
}
