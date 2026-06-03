package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

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

// classifyImageDeleteResponse inspects the Docker delete-response slice and
// returns an honest ActionResult. Docker reports a Deleted entry when the image
// layer is physically removed and an Untagged entry when only a tag is removed
// but the underlying image still exists (referenced by another tag). Reporting
// success when only tags were removed is the false-positive fixed by finding #12.
func classifyImageDeleteResponse(resp []image.DeleteResponse) truth.ActionResult {
	var deleted, untagged []string
	for _, r := range resp {
		if r.Deleted != "" {
			deleted = append(deleted, r.Deleted)
		}
		if r.Untagged != "" {
			untagged = append(untagged, r.Untagged)
		}
	}

	if len(resp) == 0 {
		return truth.NoChange("image delete returned no entries",
			truth.KV("deleted", []string{}),
			truth.KV("untagged", []string{}),
		)
	}

	if len(deleted) > 0 {
		return truth.Success("image removed",
			truth.KV("deleted", deleted),
			truth.KV("untagged", untagged),
		)
	}

	// Only untagged entries — the image still exists under another tag.
	return truth.NoChange("image still referenced by other tags; untagged only",
		truth.KV("deleted", []string{}),
		truth.KV("untagged", untagged),
	)
}

func (h *ResourcesHandler) deleteImage(c *gin.Context) {
	id := c.Param("id")
	force := c.Query("force") == "true"

	resp, err := h.docker.DeleteImage(c.Request.Context(), id, force)
	if err != nil {
		slog.Error("Failed to delete image", "id", id, "error", err)
		truth.Render(c, truth.Failed("failed to delete image", err,
			truth.KV("id", id),
		))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Timestamp: time.Now()})
	truth.Render(c, classifyImageDeleteResponse(resp))
}

// classifyImagePruneReport counts both Deleted and Untagged entries from the
// prune report. The previous code only counted Deleted entries, producing
// "0 images deleted, 1.2 GB reclaimed" when only tags were removed (finding #13).
func classifyImagePruneReport(deleted []image.DeleteResponse, spaceReclaimed uint64) truth.ActionResult {
	var deletedLayers, untaggedTags []string
	for _, d := range deleted {
		if d.Deleted != "" {
			deletedLayers = append(deletedLayers, d.Deleted)
		}
		if d.Untagged != "" {
			untaggedTags = append(untaggedTags, d.Untagged)
		}
	}

	totalEntries := len(deletedLayers) + len(untaggedTags)
	if totalEntries == 0 && spaceReclaimed == 0 {
		return truth.NoChange("nothing to prune",
			truth.KV("imagesDeleted", 0),
			truth.KV("tagsRemoved", 0),
			truth.KV("spaceReclaimed", uint64(0)),
		)
	}

	return truth.Success(
		fmt.Sprintf("pruned %d image(s), %d tag(s), reclaimed %d bytes",
			len(deletedLayers), len(untaggedTags), spaceReclaimed),
		truth.KV("imagesDeleted", len(deletedLayers)),
		truth.KV("tagsRemoved", len(untaggedTags)),
		truth.KV("spaceReclaimed", spaceReclaimed),
	)
}

func (h *ResourcesHandler) pruneImages(c *gin.Context) {
	report, err := h.docker.PruneImages(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune images", "error", err)
		truth.Render(c, truth.Failed("failed to prune images", err))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Timestamp: time.Now()})
	truth.Render(c, classifyImagePruneReport(report.ImagesDeleted, report.SpaceReclaimed))
}

func (h *ResourcesHandler) pruneContainers(c *gin.Context) {
	report, err := h.docker.PruneContainers(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune containers", "error", err)
		truth.Render(c, truth.Failed("failed to prune containers", err))
		return
	}

	deleted := report.ContainersDeleted
	if deleted == nil {
		deleted = []string{}
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_prune", Timestamp: time.Now()})

	if len(deleted) == 0 && report.SpaceReclaimed == 0 {
		truth.Render(c, truth.NoChange("nothing to prune",
			truth.KV("deleted", deleted),
			truth.KV("spaceReclaimed", report.SpaceReclaimed),
		))
		return
	}

	truth.Render(c, truth.Success(
		fmt.Sprintf("pruned %d container(s), reclaimed %d bytes", len(deleted), report.SpaceReclaimed),
		truth.KV("deleted", deleted),
		truth.KV("spaceReclaimed", report.SpaceReclaimed),
	))
}

func (h *ResourcesHandler) deleteContainer(c *gin.Context) {
	id := c.Param("id")
	force := c.Query("force") == "true"

	if err := h.docker.DeleteContainer(c.Request.Context(), id, force); err != nil {
		slog.Error("Failed to delete container", "id", id, "error", err)
		truth.Render(c, truth.Failed("failed to delete container", err,
			truth.KV("id", id),
		))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_delete", ContainerID: id, Timestamp: time.Now()})
	truth.Render(c, truth.Success("container deleted",
		truth.KV("id", id),
	))
}

func (h *ResourcesHandler) deleteVolume(c *gin.Context) {
	name := c.Param("name")
	force := c.Query("force") == "true"

	if err := h.docker.DeleteVolume(c.Request.Context(), name, force); err != nil {
		slog.Error("Failed to delete volume", "name", name, "error", err)
		truth.Render(c, truth.Failed("failed to delete volume", err,
			truth.KV("name", name),
		))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "volume_delete", Timestamp: time.Now()})
	truth.Render(c, truth.Success("volume deleted",
		truth.KV("name", name),
	))
}

func (h *ResourcesHandler) pruneVolumes(c *gin.Context) {
	report, err := h.docker.PruneVolumes(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune volumes", "error", err)
		truth.Render(c, truth.Failed("failed to prune volumes", err))
		return
	}

	deleted := report.VolumesDeleted
	if deleted == nil {
		deleted = []string{}
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "volume_prune", Timestamp: time.Now()})

	if len(deleted) == 0 && report.SpaceReclaimed == 0 {
		truth.Render(c, truth.NoChange("nothing to prune",
			truth.KV("deleted", deleted),
			truth.KV("spaceReclaimed", report.SpaceReclaimed),
		))
		return
	}

	truth.Render(c, truth.Success(
		fmt.Sprintf("pruned %d volume(s), reclaimed %d bytes", len(deleted), report.SpaceReclaimed),
		truth.KV("deleted", deleted),
		truth.KV("spaceReclaimed", report.SpaceReclaimed),
	))
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

	opts := network.CreateOptions{
		Driver:     driver,
		Internal:   req.Internal,
		Attachable: req.Attachable,
	}
	id, err := h.docker.CreateNetwork(c.Request.Context(), req.Name, opts)
	if err != nil {
		slog.Error("Failed to create network", "name", req.Name, "error", err)
		truth.Render(c, truth.Failed("failed to create network", err,
			truth.KV("name", req.Name),
		))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_create", Timestamp: time.Now()})
	c.JSON(http.StatusCreated, truth.ActionResult{
		Outcome: truth.OutcomeSuccess,
		Reason:  "network created",
		Details: map[string]any{
			"id":   id,
			"name": req.Name,
		},
	})
}

func (h *ResourcesHandler) deleteNetwork(c *gin.Context) {
	id := c.Param("id")

	if err := h.docker.DeleteNetwork(c.Request.Context(), id); err != nil {
		slog.Error("Failed to delete network", "id", id, "error", err)
		truth.Render(c, truth.Failed("failed to delete network", err,
			truth.KV("id", id),
		))
		return
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_delete", Timestamp: time.Now()})
	truth.Render(c, truth.Success("network deleted",
		truth.KV("id", id),
	))
}

func (h *ResourcesHandler) pruneNetworks(c *gin.Context) {
	report, err := h.docker.PruneNetworks(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune networks", "error", err)
		truth.Render(c, truth.Failed("failed to prune networks", err))
		return
	}

	deleted := report.NetworksDeleted
	if deleted == nil {
		deleted = []string{}
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "network_prune", Timestamp: time.Now()})

	if len(deleted) == 0 {
		truth.Render(c, truth.NoChange("nothing to prune",
			truth.KV("deleted", deleted),
		))
		return
	}

	truth.Render(c, truth.Success(
		fmt.Sprintf("pruned %d network(s)", len(deleted)),
		truth.KV("deleted", deleted),
	))
}

func (h *ResourcesHandler) pruneBuildCache(c *gin.Context) {
	report, err := h.docker.PruneBuildCache(c.Request.Context(), parsePruneOptions(c))
	if err != nil {
		slog.Error("Failed to prune build cache", "error", err)
		truth.Render(c, truth.Failed("failed to prune build cache", err))
		return
	}

	deleted := report.CachesDeleted
	if deleted == nil {
		deleted = []string{}
	}

	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "build_cache_prune", Timestamp: time.Now()})

	if len(deleted) == 0 && report.SpaceReclaimed == 0 {
		truth.Render(c, truth.NoChange("nothing to prune",
			truth.KV("deleted", deleted),
			truth.KV("spaceReclaimed", report.SpaceReclaimed),
		))
		return
	}

	truth.Render(c, truth.Success(
		fmt.Sprintf("pruned %d cache entry/entries, reclaimed %d bytes", len(deleted), report.SpaceReclaimed),
		truth.KV("deleted", deleted),
		truth.KV("spaceReclaimed", report.SpaceReclaimed),
	))
}
