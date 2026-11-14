package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

func (h *StacksHandler) Start(c *gin.Context) {
	id := c.Param("id")

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	if refuseSharedProjectName(c, h.db, stack) {
		return
	}

	lockToken, err := h.opLock.Acquire(id, services.OpKindStart)
	if err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id, lockToken)

	startTime := time.Now()
	ar, output := h.dockerSvc().StartVerified(*stack)
	duration := time.Since(startTime)

	h.logAction(c, id, "start", output)

	verifiedStatus := services.StoreLifecycleStatus(h.db, id, ar)

	renderDockerResult(c, ar.Err, truth.ActionResult{
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

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	if refuseSharedProjectName(c, h.db, stack) {
		return
	}

	lockToken, err := h.opLock.Acquire(id, services.OpKindStop)
	if err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id, lockToken)

	startTime := time.Now()
	ar, output := h.dockerSvc().StopVerified(*stack)
	duration := time.Since(startTime)

	h.logAction(c, id, "stop", output)

	verifiedStatus := services.StoreLifecycleStatus(h.db, id, ar)

	renderDockerResult(c, ar.Err, truth.ActionResult{
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

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	if refuseSharedProjectName(c, h.db, stack) {
		return
	}

	lockToken, err := h.opLock.Acquire(id, services.OpKindRestart)
	if err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id, lockToken)

	startTime := time.Now()
	ar, output := h.dockerSvc().RestartVerified(*stack)
	duration := time.Since(startTime)

	h.logAction(c, id, "restart", output)

	verifiedStatus := services.StoreLifecycleStatus(h.db, id, ar)

	renderDockerResult(c, ar.Err, truth.ActionResult{
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

	// nil arm dropped, dead per GetStack's return shape (GetStack() in
	// internal/database/stacks.go always returns either &stack or a non-nil
	// err, never (nil, nil)).
	stack, err := h.db.GetStack(id)
	if err != nil {
		handleDBError(c, err, "Failed to load stack")
		return
	}

	if refuseSharedProjectName(c, h.db, stack) {
		return
	}

	lockToken, err := h.opLock.Acquire(id, services.OpKindPull)
	if err != nil {
		c.JSON(http.StatusConflict, models.NewAppError(
			http.StatusConflict,
			"OPERATION_IN_PROGRESS",
			err.Error(),
		))
		return
	}
	defer h.opLock.Release(id, lockToken)

	startTime := time.Now()
	pullAR, pullOutput := h.dockerSvc().PullVerified(*stack)
	duration := time.Since(startTime)

	h.logAction(c, id, "pull", pullOutput)

	restartAfterPull := c.Query("restart") == "true"

	if pullAR.Outcome == truth.OutcomeFailed {
		renderDockerResult(c, pullAR.Err, truth.ActionResult{
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
		restartAR, restartOutput := h.dockerSvc().RestartVerified(*stack)
		verifiedStatus := services.StoreLifecycleStatus(h.db, id, restartAR)

		renderDockerResult(c, restartAR.Err, truth.ActionResult{
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
