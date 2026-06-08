package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

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
			h.actionLog.LogFromContext(c, nil, services.ActionScan, gin.H{"trigger": "manual"})

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
	// Audit who initiated the update; this covers both the async job path below
	// and the synchronous fallback (updateContainerSync) it delegates to.
	h.actionLog.LogFromContext(c, nil, services.ActionUpdateContainer, gin.H{"container_id": id})

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
	jobManager := h.jobManager
	containerIDCopy := id
	historyIDCopy := historyID
	stackIDCopy := stackID

	run := func(ctx context.Context, jobID string, emit func(services.LogLine), setStatus func(services.Status)) error {
		result, ar := docker.UpdateContainerStreaming(ctx, containerIDCopy, db, emit, setStatus)

		// Persist outcome on the job (findable via GET /updates/jobs/:id).
		jobManager.SetOutcome(jobID, string(ar.Outcome), ar.Reason)

		// History update based on typed outcome.
		switch ar.Outcome {
		case truth.OutcomeSuccess:
			if histErr := db.UpdateUpdateHistory(historyIDCopy, map[string]interface{}{
				"status":       "success",
				"new_digest":   result.NewDigest,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  result.DurationMs,
			}); histErr != nil {
				slog.Warn("Failed to update update history", "historyID", historyIDCopy, "error", histErr)
			}
		case truth.OutcomeNoChange:
			if histErr := db.UpdateUpdateHistory(historyIDCopy, map[string]interface{}{
				"status":       "success",
				"new_digest":   result.NewDigest,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  result.DurationMs,
			}); histErr != nil {
				slog.Warn("Failed to update update history", "historyID", historyIDCopy, "error", histErr)
			}
		default: // failed
			errMsg := ar.Reason
			if ar.Err != nil {
				errMsg = ar.Err.Error()
			}
			if histErr := db.UpdateUpdateHistory(historyIDCopy, map[string]interface{}{
				"status":        "failed",
				"error_message": errMsg,
				"completed_at":  time.Now().Format(time.RFC3339),
				"duration_ms":   result.DurationMs,
			}); histErr != nil {
				slog.Warn("Failed to update update history", "historyID", historyIDCopy, "error", histErr)
			}
		}

		// Convergence (finding #4): evict from cached_updates on success or no_change.
		if ar.Outcome == truth.OutcomeSuccess || ar.Outcome == truth.OutcomeNoChange {
			if evictErr := db.DeleteCachedUpdate(containerIDCopy); evictErr != nil {
				slog.Warn("Failed to evict cached update entry", "containerID", containerIDCopy, "error", evictErr)
			}
			BroadcastEvent(models.StackEvent{
				Type:        "updates_changed",
				ContainerID: containerIDCopy,
				StackID:     stackIDCopy,
				Timestamp:   time.Now(),
			})
		}

		BroadcastEvent(models.StackEvent{
			Type:        "update_completed",
			ContainerID: containerIDCopy,
			StackID:     stackIDCopy,
			Timestamp:   time.Now(),
		})

		if ar.Outcome == truth.OutcomeFailed {
			if ar.Err != nil {
				return ar.Err
			}
			return fmt.Errorf("%s", ar.Reason)
		}
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

	result, ar := h.docker.UpdateContainer(c.Request.Context(), id, h.db)

	switch ar.Outcome {
	case truth.OutcomeSuccess:
		if histErr := h.db.UpdateUpdateHistory(historyID, map[string]interface{}{
			"status":       "success",
			"new_digest":   result.NewDigest,
			"completed_at": time.Now().Format(time.RFC3339),
			"duration_ms":  result.DurationMs,
		}); histErr != nil {
			slog.Warn("Failed to update update history", "historyID", historyID, "error", histErr)
		}
	case truth.OutcomeNoChange:
		if histErr := h.db.UpdateUpdateHistory(historyID, map[string]interface{}{
			"status":       "success",
			"new_digest":   result.NewDigest,
			"completed_at": time.Now().Format(time.RFC3339),
			"duration_ms":  result.DurationMs,
		}); histErr != nil {
			slog.Warn("Failed to update update history", "historyID", historyID, "error", histErr)
		}
	default:
		errMsg := ar.Reason
		if ar.Err != nil {
			errMsg = ar.Err.Error()
		}
		slog.Error("Failed to update container", "id", id, "error", errMsg)
		if histErr := h.db.UpdateUpdateHistory(historyID, map[string]interface{}{
			"status":        "failed",
			"error_message": errMsg,
			"completed_at":  time.Now().Format(time.RFC3339),
			"duration_ms":   result.DurationMs,
		}); histErr != nil {
			slog.Warn("Failed to update update history", "historyID", historyID, "error", histErr)
		}
		models.HandleError(c, models.NewAppError(http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to update container"))
		return
	}

	// Convergence (finding #4): evict on success or no_change.
	if ar.Outcome == truth.OutcomeSuccess || ar.Outcome == truth.OutcomeNoChange {
		if evictErr := h.db.DeleteCachedUpdate(id); evictErr != nil {
			slog.Warn("Failed to evict cached update entry", "containerID", id, "error", evictErr)
		}
		BroadcastEvent(models.StackEvent{Type: "updates_changed", ContainerID: id, Timestamp: time.Now()})
	}

	BroadcastEvent(models.StackEvent{Type: "update_completed", ContainerID: id, Timestamp: time.Now()})

	c.JSON(http.StatusOK, gin.H{
		"message":    "Container updated",
		"historyId":  historyID,
		"oldDigest":  result.OldDigest,
		"newDigest":  result.NewDigest,
		"durationMs": result.DurationMs,
		"outcome":    string(ar.Outcome),
		"reason":     ar.Reason,
	})
}

// enqueueJobWithBroadcasts enqueues a job and wraps its setStatus to emit
// update_job_progress broadcasts, and a final update_job_complete on terminal.
// The jobID is injected by the manager (race-free) via the run signature.
func (h *ResourcesHandler) enqueueJobWithBroadcasts(
	spec services.JobSpec,
	run func(ctx context.Context, jobID string, emit func(services.LogLine), setStatus func(services.Status)) error,
) *services.Job {
	wrapped := func(ctx context.Context, jobID string, emit func(services.LogLine), setStatus func(services.Status)) error {
		wrappedSetStatus := func(s services.Status) {
			setStatus(s)
			BroadcastEvent(models.StackEvent{
				Type:       "update_job_progress",
				JobID:      jobID,
				TargetType: spec.TargetType,
				TargetID:   spec.TargetID,
				StackID:    spec.StackID,
				Name:       spec.Name,
				Event:      string(s),
				Status:     string(s),
				Timestamp:  time.Now(),
			})
		}
		runErr := run(ctx, jobID, emit, wrappedSetStatus)

		// Read the outcome/reason set by the run closure via SetOutcome.
		var outcome, reason string
		if j := h.jobManager.Get(jobID); j != nil {
			outcome = j.Outcome
			reason = j.Reason
		}

		finalStatus := services.StatusSuccess
		errMsg := ""
		if runErr != nil {
			finalStatus = services.StatusError
			errMsg = runErr.Error()
		}
		BroadcastEvent(models.StackEvent{
			Type:       "update_job_complete",
			JobID:      jobID,
			TargetType: spec.TargetType,
			TargetID:   spec.TargetID,
			StackID:    spec.StackID,
			Name:       spec.Name,
			Event:      string(finalStatus),
			Status:     string(finalStatus),
			JobError:   errMsg,
			Outcome:    outcome,
			Reason:     reason,
			Timestamp:  time.Now(),
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

	stackIDForLog := stack.ID
	h.actionLog.LogFromContext(c, &stackIDForLog, services.ActionUpdateStack, gin.H{
		"stack":    stack.ProjectName,
		"services": len(outdated),
	})

	spec := services.JobSpec{
		TargetType: "stack",
		TargetID:   stack.ID,
		Name:       stack.ProjectName,
		StackID:    stack.ID,
	}

	docker := h.docker
	db := h.db
	jobManager := h.jobManager
	stackCopy := *stack
	outdatedCopy := outdated

	run := func(ctx context.Context, jobID string, emit func(services.LogLine), setStatus func(services.Status)) error {
		total := len(outdatedCopy)
		serviceNames := make([]string, total)
		for i, s := range outdatedCopy {
			serviceNames[i] = s.ServiceName
		}
		emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStatus,
			Text: fmt.Sprintf("==> Updating %d outdated service(s) sequentially: %s", total, strings.Join(serviceNames, ", "))})

		// Track overall outcome for the job.
		overallOutcome := string(truth.OutcomeSuccess)
		overallReason := fmt.Sprintf("All %d service(s) up to date or updated", total)
		anyAdvanced := false

		for i, svc := range outdatedCopy {
			emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStatus,
				Text: fmt.Sprintf("==> [%d/%d] Updating service: %s", i+1, total, svc.ServiceName)})

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
			if histErr := db.InsertUpdateHistory(entry); histErr != nil {
				slog.Warn("Failed to insert update history", "historyID", historyID, "error", histErr)
			}

			_, newD, durMs, ar := docker.UpdateComposeServiceStreaming(ctx, stackCopy, svc.ServiceName, emit, setStatus)

			if ar.Outcome == truth.OutcomeFailed {
				histUpdates := map[string]interface{}{
					"status":        "failed",
					"error_message": ar.Reason,
					"completed_at":  time.Now().Format(time.RFC3339),
					"duration_ms":   durMs,
				}
				if ar.Err != nil {
					histUpdates["error_message"] = ar.Err.Error()
				}
				if histErr := db.UpdateUpdateHistory(historyID, histUpdates); histErr != nil {
					slog.Warn("Failed to update update history", "historyID", historyID, "error", histErr)
				}

				// Fail-fast: surface exactly which services were left un-updated.
				skipped := serviceNames[i+1:]
				emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStderr,
					Text: fmt.Sprintf("✗ Service %q failed to update: %v", svc.ServiceName, ar.Reason)})
				if len(skipped) > 0 {
					emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStderr,
						Text: fmt.Sprintf("Stopped after the failure. %d service(s) were NOT updated: %s",
							len(skipped), strings.Join(skipped, ", "))})
				}

				overallOutcome = string(truth.OutcomeFailed)
				overallReason = fmt.Sprintf("service %q failed", svc.ServiceName)
				jobManager.SetOutcome(jobID, overallOutcome, overallReason)

				if len(skipped) > 0 {
					return fmt.Errorf("service %q failed; stopped with %d of %d service(s) not updated (%s): %s",
						svc.ServiceName, len(skipped), total, strings.Join(skipped, ", "), ar.Reason)
				}
				return fmt.Errorf("service %q failed (last of %d): %s", svc.ServiceName, total, ar.Reason)
			}

			// success or no_change for this service.
			if histErr := db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":       "success",
				"new_digest":   newD,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  durMs,
			}); histErr != nil {
				slog.Warn("Failed to update update history", "historyID", historyID, "error", histErr)
			}

			if ar.Outcome == truth.OutcomeSuccess {
				anyAdvanced = true
				emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStatus,
					Text: fmt.Sprintf("✓ [%d/%d] Service %s updated: %s", i+1, total, svc.ServiceName, ar.Reason)})
			} else {
				emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStatus,
					Text: fmt.Sprintf("✓ [%d/%d] Service %s: %s", i+1, total, svc.ServiceName, ar.Reason)})
			}

			// Convergence (finding #4): evict each confirmed service from cache.
			if evictErr := db.DeleteCachedUpdate(svc.ContainerID); evictErr != nil {
				slog.Warn("Failed to evict cached update entry", "containerID", svc.ContainerID, "error", evictErr)
			}
		}

		emit(services.LogLine{Ts: time.Now().UTC(), Stream: services.StreamStatus,
			Text: fmt.Sprintf("All %d service(s) processed", total)})

		// Determine overall outcome.
		if anyAdvanced {
			overallOutcome = string(truth.OutcomeSuccess)
			overallReason = fmt.Sprintf("%d service(s) updated", total)
		} else {
			overallOutcome = string(truth.OutcomeNoChange)
			overallReason = "All services already up to date"
		}
		jobManager.SetOutcome(jobID, overallOutcome, overallReason)

		// Broadcast convergence event so the FE re-fetches the update list.
		BroadcastEvent(models.StackEvent{
			Type:      "updates_changed",
			StackID:   stackCopy.ID,
			Timestamp: time.Now(),
		})

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
