package handlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// hostSpaceReader is the slice of *services.HostSpaceService the host routes
// read, declared on the consumer side so they are testable without a daemon.
type hostSpaceReader interface {
	Summary(ctx context.Context) (*services.HostSummary, error)
	Candidates(ctx context.Context) (*services.HostCandidates, error)
}

// SetHostSpaceService installs the host summary service. Same typed-nil
// discipline as SetCleanupService: main.go calls it only with a non-nil
// *HostSpaceService, so a nil field always means "no Docker".
func (h *ResourcesHandler) SetHostSpaceService(svc hostSpaceReader) {
	h.hostSpace = svc
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
