package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// hostSpaceReader is the slice of *services.HostSpaceService the host routes
// read, declared on the consumer side so they are testable without a daemon.
type hostSpaceReader interface {
	Summary(ctx context.Context) (*services.HostSummary, error)
	Candidates(ctx context.Context) (*services.HostCandidates, error)
	// The stacks a volume or network prune would take (agent-os-8d8a).
	VolumePruneImpact(ctx context.Context) (*services.PruneImpact, error)
	NetworkPruneImpact(ctx context.Context) (*services.PruneImpact, error)
}

// hostSpaceFreer is the free-space removal. Every method on it removes
// objects of any stack, so it is in exclusiveGatedInterfaces: a handler calling
// FreeSpace without acquireExclusiveLock first fails
// TestContainerLockGuard_EveryContainerPruneTakesTheExclusiveTurnFirst.
type hostSpaceFreer interface {
	FreeSpace(ctx context.Context, req services.HostFreeSpaceRequest) (*services.HostFreeSpaceResult, error)
}

// hostSpaceService is everything the handler uses. Invalidate is kept off
// hostSpaceFreer so the guard does not demand the exclusive turn for it.
type hostSpaceService interface {
	hostSpaceReader
	hostSpaceFreer
	Invalidate()
}

// SetHostSpaceService installs the host summary service. Same typed-nil
// discipline as SetCleanupService: main.go calls it only with a non-nil
// *HostSpaceService, so a nil field always means "no Docker".
func (h *ResourcesHandler) SetHostSpaceService(svc hostSpaceService) {
	h.hostSpace = svc
}

// invalidateHostSpace drops the host summary snapshot, so the next summary
// shows what a removal route just removed. Every route that removes Docker
// objects defers it (TestHostSpaceGuard_EveryRemovalRouteInvalidates).
func (h *ResourcesHandler) invalidateHostSpace() {
	if h.hostSpace != nil {
		h.hostSpace.Invalidate()
	}
}

// maxFreeSpaceIDs caps one category of a free-space request. The dialog splits
// larger sets into a second pass.
const maxFreeSpaceIDs = 1000

// freeSpaceIDRegex admits Docker object IDs and names (sha256:<hex>, container
// and network IDs, volume names) and nothing that could be a path or a flag.
var freeSpaceIDRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func validateFreeSpaceRequest(req services.HostFreeSpaceRequest) string {
	categories := []struct {
		name string
		ids  []string
	}{
		{"images", req.Images}, {"containers", req.Containers},
		{"volumes", req.Volumes}, {"networks", req.Networks},
	}
	total := 0
	for _, cat := range categories {
		if len(cat.ids) > maxFreeSpaceIDs {
			return fmt.Sprintf("%s: at most %d IDs per request", cat.name, maxFreeSpaceIDs)
		}
		for _, id := range cat.ids {
			if !freeSpaceIDRegex.MatchString(id) {
				return fmt.Sprintf("%s: %q is not a Docker ID or name", cat.name, id)
			}
		}
		total += len(cat.ids)
	}
	if total == 0 && !req.BuildCache {
		return "nothing selected to remove"
	}
	return ""
}

// freeSpace removes the listed items that are still removable
// (POST /resources/host/free-space). The candidates are re-read under the
// exclusive stack turn, so nothing a stack operation creates or starts using
// in the meantime can be removed, and nothing is forced.
func (h *ResourcesHandler) freeSpace(c *gin.Context) {
	if h.hostSpace == nil {
		respondDockerErr(c, services.ErrDockerUnavailable, http.StatusServiceUnavailable,
			"DOCKER_UNAVAILABLE", DockerUnavailableMessage)
		return
	}
	var req services.HostFreeSpaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, "Invalid request body"))
		return
	}
	if msg := validateFreeSpaceRequest(req); msg != "" {
		handleError(c, models.NewAppError(http.StatusBadRequest, models.ErrValidation, msg))
		return
	}

	// Removing an image, volume or network can break a stack operation that
	// created it and has not used it yet (agent-os-qags.30/.33), so this takes
	// every stack's turn before Docker is touched.
	release, ok := acquireExclusiveLock(c, h.opLock, services.OpKindHostFreeSpace)
	if !ok {
		return
	}
	defer release()
	defer h.invalidateHostSpace()

	// The run belongs to the operation, not the request (safe-defaults rule
	// 15): closing the tab must not stop it halfway.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), services.HostFreeSpaceTimeout)
	defer cancel()
	res, err := h.hostSpace.FreeSpace(runCtx, req)
	if err != nil {
		slog.Error("Free-space run could not read what is removable", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read what can be removed; nothing was removed")
		return
	}

	result := classifyFreeSpace(res)
	logActionFromContext(h.actionLog, c, nil, services.ActionPrune, gin.H{
		"resource":        "host_free_space",
		"outcome":         result.Outcome,
		"images":          len(res.Images.Removed),
		"containers":      len(res.Containers.Removed),
		"volumes":         len(res.Volumes.Removed),
		"networks":        len(res.Networks.Removed),
		"build_cache":     res.BuildCache.Removed,
		"space_reclaimed": res.SpaceReclaimed,
	})
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "host_free_space", Timestamp: time.Now()})
	renderResult(c, result)
}

// classifyFreeSpace maps a run onto an ActionResult: success when everything
// requested was removed, partial when some was and the rest failed or was
// skipped, no_change when nothing was removed and nothing failed, failed when
// nothing was removed because Docker refused every removal.
func classifyFreeSpace(res *services.HostFreeSpaceResult) truth.ActionResult {
	removed, failed, skipped := 0, 0, 0
	for _, cat := range []services.HostFreeSpaceCategoryResult{res.Images, res.Containers, res.Volumes, res.Networks} {
		removed += len(cat.Removed)
		failed += len(cat.Failed)
		skipped += len(cat.Skipped)
	}
	if res.BuildCache.Removed > 0 {
		removed++
	}
	if res.BuildCache.Failed != "" {
		failed++
	}
	if res.BuildCache.Skipped != "" {
		skipped++
	}

	result := truth.ActionResult{Details: map[string]any{
		"images":     res.Images,
		"containers": res.Containers,
		"volumes":    res.Volumes,
		"networks":   res.Networks,
		"buildCache": res.BuildCache,
	}}
	// Omitted when the after-read failed: an unknown is not a measured 0.
	if res.SpaceReclaimed != nil {
		result.Details["spaceReclaimed"] = *res.SpaceReclaimed
	}
	switch {
	case removed > 0 && failed == 0 && skipped == 0:
		result.Outcome, result.Reason = truth.OutcomeSuccess, fmt.Sprintf("removed %d item(s)", removed)
	case removed > 0:
		result.Outcome, result.Reason = truth.OutcomePartial, fmt.Sprintf("removed %d item(s); %d failed, %d skipped", removed, failed, skipped)
	case failed > 0:
		result.Outcome, result.Reason = truth.OutcomeFailed, fmt.Sprintf("nothing removed; %d failed, %d skipped", failed, skipped)
	default:
		result.Outcome, result.Reason = truth.OutcomeNoChange, fmt.Sprintf("nothing removed; %d skipped", skipped)
	}
	return result
}

// getHostSummary serves the per-category figures from the service's 30 s
// snapshot. A failed read is an error response, never a zeroed body: "0 B
// reclaimable" is a claim an operator would act on (the agent-os-p9e1 rule).
func (h *ResourcesHandler) getHostSummary(c *gin.Context) {
	if h.hostSpace == nil {
		respondDockerErr(c, services.ErrDockerUnavailable, http.StatusServiceUnavailable,
			"DOCKER_UNAVAILABLE", DockerUnavailableMessage)
		return
	}
	summary, err := h.hostSpace.Summary(c.Request.Context())
	if err != nil {
		slog.Error("Failed to read the Docker host summary", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to read Docker disk usage")
		return
	}
	c.JSON(http.StatusOK, summary)
}

// getHostCandidates serves the removable items from a fresh read.
func (h *ResourcesHandler) getHostCandidates(c *gin.Context) {
	if h.hostSpace == nil {
		respondDockerErr(c, services.ErrDockerUnavailable, http.StatusServiceUnavailable,
			"DOCKER_UNAVAILABLE", DockerUnavailableMessage)
		return
	}
	candidates, err := h.hostSpace.Candidates(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list Docker cleanup candidates", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list what can be removed")
		return
	}
	c.JSON(http.StatusOK, candidates)
}
