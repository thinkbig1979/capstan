package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

func (h *StacksHandler) Start(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	if _, err := h.opLock.Acquire(id); err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id)

	startTime := time.Now()
	ar, output := h.docker.StartVerified(*stack)
	duration := time.Since(startTime)

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "start", output)

	verifiedStatus := lifecycleStatus(ar)
	if statusErr := h.db.UpdateStackStatus(id, verifiedStatus); statusErr != nil {
		slog.Warn("Failed to persist verified stack status", "stackID", id, "status", verifiedStatus, "error", statusErr)
	}

	renderResult(c, truth.ActionResult{
		Outcome: ar.Outcome,
		Reason:  ar.Reason,
		Details: mergeDetails(ar.Details, map[string]any{
			"status":   verifiedStatus,
			"output":   output,
			"duration": duration.Milliseconds(),
		}),
		Err: ar.Err,
	})
}

func (h *StacksHandler) Stop(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	if _, err := h.opLock.Acquire(id); err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id)

	startTime := time.Now()
	ar, output := h.docker.StopVerified(*stack)
	duration := time.Since(startTime)

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "stop", output)

	verifiedStatus := lifecycleStatus(ar)
	if statusErr := h.db.UpdateStackStatus(id, verifiedStatus); statusErr != nil {
		slog.Warn("Failed to persist verified stack status", "stackID", id, "status", verifiedStatus, "error", statusErr)
	}

	renderResult(c, truth.ActionResult{
		Outcome: ar.Outcome,
		Reason:  ar.Reason,
		Details: mergeDetails(ar.Details, map[string]any{
			"status":   verifiedStatus,
			"output":   output,
			"duration": duration.Milliseconds(),
		}),
		Err: ar.Err,
	})
}

func (h *StacksHandler) Restart(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	if _, err := h.opLock.Acquire(id); err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id)

	startTime := time.Now()
	ar, output := h.docker.RestartVerified(*stack)
	duration := time.Since(startTime)

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "restart", output)

	verifiedStatus := lifecycleStatus(ar)
	if statusErr := h.db.UpdateStackStatus(id, verifiedStatus); statusErr != nil {
		slog.Warn("Failed to persist verified stack status", "stackID", id, "status", verifiedStatus, "error", statusErr)
	}

	renderResult(c, truth.ActionResult{
		Outcome: ar.Outcome,
		Reason:  ar.Reason,
		Details: mergeDetails(ar.Details, map[string]any{
			"status":   verifiedStatus,
			"output":   output,
			"duration": duration.Milliseconds(),
		}),
		Err: ar.Err,
	})
}

func (h *StacksHandler) Pull(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.db.GetStack(id)
	if err != nil || stack == nil {
		c.JSON(http.StatusNotFound, models.NewAppError(
			http.StatusNotFound,
			models.ErrStackNotFound,
			"Stack not found",
		))
		return
	}

	if _, err := h.opLock.Acquire(id); err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id)

	startTime := time.Now()
	pullAR, pullOutput := h.docker.PullVerified(*stack)
	duration := time.Since(startTime)

	userID, _ := c.Get("userID")
	h.logAction(userID.(string), id, "pull", pullOutput)

	restartAfterPull := c.Query("restart") == "true"

	if pullAR.Outcome == truth.OutcomeFailed {
		renderResult(c, truth.ActionResult{
			Outcome: pullAR.Outcome,
			Reason:  pullAR.Reason,
			Details: mergeDetails(pullAR.Details, map[string]any{
				"output":    pullOutput,
				"restarted": false,
				"duration":  duration.Milliseconds(),
			}),
			Err: pullAR.Err,
		})
		return
	}

	if restartAfterPull {
		restartAR, restartOutput := h.docker.RestartVerified(*stack)
		verifiedStatus := lifecycleStatus(restartAR)
		if statusErr := h.db.UpdateStackStatus(id, verifiedStatus); statusErr != nil {
			slog.Warn("Failed to persist verified stack status", "stackID", id, "status", verifiedStatus, "error", statusErr)
		}

		renderResult(c, truth.ActionResult{
			Outcome: restartAR.Outcome,
			Reason:  restartAR.Reason,
			Details: mergeDetails(restartAR.Details, map[string]any{
				"status":    verifiedStatus,
				"output":    pullOutput + restartOutput,
				"restarted": true,
				"duration":  duration.Milliseconds(),
			}),
			Err: restartAR.Err,
		})
		return
	}

	renderResult(c, truth.ActionResult{
		Outcome: pullAR.Outcome,
		Reason:  pullAR.Reason,
		Details: mergeDetails(pullAR.Details, map[string]any{
			"output":    pullOutput,
			"restarted": false,
			"duration":  duration.Milliseconds(),
		}),
	})
}

// lifecycleStatus maps an ActionResult outcome to a stack status string for
// persistence in the database. It prefers the "status" key in details if
// populated by verifyLifecycle, otherwise falls back to outcome-derived defaults.
func lifecycleStatus(ar truth.ActionResult) string {
	if ar.Details != nil {
		if s, ok := ar.Details["status"].(string); ok && s != "" {
			return s
		}
	}
	switch ar.Outcome {
	case truth.OutcomeSuccess, truth.OutcomeNoChange:
		return "running"
	case truth.OutcomePartial:
		return "partial"
	default:
		return "error"
	}
}

// mergeDetails returns a new map containing all keys from base plus all keys
// from extra, with extra winning on conflicts. base may be nil.
func mergeDetails(base map[string]any, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}
