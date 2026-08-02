package services

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

type EventBroadcaster func(event models.StackEvent)

// updateChecker is the narrow interface scheduler needs from DockerService.
type updateChecker interface {
	CheckForUpdates(ctx context.Context, db DashboardDB) ([]models.ContainerUpdateInfo, error)
	UpdateContainer(ctx context.Context, containerID string, db DashboardDB) (models.UpdateResult, truth.ActionResult)
}

type SchedulerService struct {
	docker       updateChecker
	db           *database.DB
	mu           sync.Mutex
	ticker       *time.Ticker
	done         chan struct{}
	logger       *slog.Logger
	scanning     bool
	broadcastFn  EventBroadcaster
	wg           sync.WaitGroup
	parentCtx    context.Context
	parentCancel context.CancelFunc
}

func NewSchedulerService(docker updateChecker, db *database.DB, logger *slog.Logger, broadcastFn EventBroadcaster) *SchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &SchedulerService{
		docker:       docker,
		db:           db,
		logger:       logger.With("component", "scheduler"),
		broadcastFn:  broadcastFn,
		parentCtx:    ctx,
		parentCancel: cancel,
	}
}

func (s *SchedulerService) Start(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.done != nil {
		close(s.done)
	}

	// Re-initialize the scan lifecycle context so a fresh Stop() works correctly.
	if s.parentCancel != nil {
		s.parentCancel()
	}
	s.parentCtx, s.parentCancel = context.WithCancel(context.Background())

	s.ticker = time.NewTicker(interval)
	s.done = make(chan struct{})

	// Capture locals so the goroutine does not race with Stop() zeroing struct fields.
	ticker := s.ticker
	done := s.done
	parentCtx := s.parentCtx

	go func() {
		s.logger.Info("Scheduler started", "interval", interval)
		for {
			select {
			case <-ticker.C:
				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					s.runCycle(parentCtx)
				}()
			case <-done:
				s.logger.Info("Scheduler stopped")
				return
			}
		}
	}()
}

func (s *SchedulerService) Stop() {
	s.mu.Lock()

	if s.ticker != nil {
		s.ticker.Stop()
		s.ticker = nil
	}
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
		s.done = nil
	}

	// Cancel any in-flight background scan contexts.
	if s.parentCancel != nil {
		s.parentCancel()
	}

	s.mu.Unlock()

	// Wait for in-flight background scan goroutines to finish.
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		s.logger.Warn("Timed out waiting for in-flight scan during shutdown")
	}
}

func (s *SchedulerService) Restart(interval time.Duration) {
	s.Stop()
	s.Start(interval)
}

func (s *SchedulerService) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticker != nil
}

// performScan executes the update scan body. It does not touch s.mu or s.scanning.
// On success it broadcasts update_scan_complete; on failure it broadcasts update_scan_failed.
// Finding #7: local/remote digests are persisted when available.
func (s *SchedulerService) performScan(ctx context.Context) ([]models.CachedUpdate, error) {
	results, err := s.docker.CheckForUpdates(ctx, s.db)
	if err != nil {
		if dbErr := s.db.SetSetting("update_scan_last_error", err.Error()); dbErr != nil {
			s.logger.Error("Failed to record scan error", "error", dbErr)
		}
		s.logger.Error("Scan failed", "error", err)
		if s.broadcastFn != nil {
			s.broadcastFn(models.StackEvent{Type: "update_scan_failed", Timestamp: time.Now()})
		}
		return nil, err
	}

	var cachedUpdates []models.CachedUpdate
	now := time.Now().Format(time.RFC3339)
	for _, r := range results {
		// Finding #7: persist both digests we already computed during detection.
		// selectUpdates resolved the remote index digest to decide local != remote,
		// so it travels through on the result — no re-fetch needed.
		cachedUpdates = append(cachedUpdates, models.CachedUpdate{
			ID:            uuid.New().String(),
			ContainerID:   r.ContainerID,
			ContainerName: r.ContainerName,
			Image:         r.ImageRef,
			ImageRef:      r.ImageRef,
			State:         r.State,
			StackID:       r.StackID,
			ProjectName:   r.ProjectName,
			ServiceName:   r.ServiceName,
			IsCompose:     r.IsCompose,
			LocalDigest:   r.LocalDigest,
			RemoteDigest:  r.RemoteDigest,
			ScannedAt:     now,
		})
	}

	if err := s.db.SetCachedUpdates(cachedUpdates); err != nil {
		s.logger.Error("Failed to cache updates", "error", err)
	}

	if err := s.db.SetSetting("update_scan_last_run", now); err != nil {
		s.logger.Error("Failed to record scan time", "error", err)
	}
	if err := s.db.SetSetting("update_scan_last_error", ""); err != nil {
		s.logger.Error("Failed to clear scan error", "error", err)
	}

	s.logger.Info("Scan completed", "updates_found", len(cachedUpdates))

	if s.broadcastFn != nil {
		s.broadcastFn(models.StackEvent{Type: "update_scan_complete", Timestamp: time.Now()})
	}

	return cachedUpdates, nil
}

func (s *SchedulerService) RunScan(ctx context.Context) ([]models.CachedUpdate, error) {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return nil, fmt.Errorf("scan already in progress")
	}
	s.scanning = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}()

	scanCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	return s.performScan(scanCtx)
}

func (s *SchedulerService) IsScanning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scanning
}

func (s *SchedulerService) StartBackgroundScan() error {
	s.mu.Lock()
	if s.scanning {
		s.mu.Unlock()
		return fmt.Errorf("scan already in progress")
	}
	s.scanning = true
	parentCtx := s.parentCtx // capture under lock to avoid data race
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			s.scanning = false
			s.mu.Unlock()
		}()

		scanCtx, cancel := context.WithTimeout(parentCtx, 10*time.Minute)
		defer cancel()

		if _, err := s.performScan(scanCtx); err != nil {
			s.logger.Error("Background scan failed", "error", err)
		}
	}()

	return nil
}

func (s *SchedulerService) runCycle(ctx context.Context) {
	updates, err := s.RunScan(ctx)
	if err != nil {
		s.logger.Error("Scheduler scan cycle failed", "error", err)
		return
	}

	if updates == nil {
		updates = []models.CachedUpdate{}
	}

	s.RunAutoUpdates(ctx, updates)
}

// RunAutoUpdates applies auto-update policies to the given update candidates.
//
// Finding #8 fix: uses typed truth.ActionResult so that:
//   - success (image advanced) → reset consecutive failure counter
//   - no_change (confirmed up-to-date) → do NOT reset counter; log and skip
//     to avoid infinite churn re-applying an image that will never advance
//   - failed → increment counter toward pause (unchanged behavior)
//
// Eviction (finding #4): on success or no_change, the cached_updates row is
// deleted so the frontend list converges without waiting for the next scan.
func (s *SchedulerService) RunAutoUpdates(ctx context.Context, updates []models.CachedUpdate) {
	autoEnabledStr, err := s.db.GetSetting("auto_update_enabled")
	if err != nil || autoEnabledStr != "true" {
		return
	}

	policies, err := s.db.GetEnabledAutoUpdatePolicies()
	if err != nil {
		s.logger.Error("Failed to get auto-update policies", "error", err)
		return
	}

	containerPolicies := make(map[string]*models.AutoUpdatePolicy)
	stackPolicies := make(map[string]*models.AutoUpdatePolicy)
	for i := range policies {
		p := &policies[i]
		switch p.TargetType {
		case "container":
			containerPolicies[p.TargetID] = p
		case "stack":
			stackPolicies[p.TargetID] = p
		}
	}

	succeeded := 0
	failed := 0
	skipped := 0

	for _, update := range updates {
		policy, hasPolicy := containerPolicies[update.ContainerID]
		if !hasPolicy {
			if update.StackID != "" {
				policy, hasPolicy = stackPolicies[update.StackID]
			}
		}

		if !hasPolicy {
			skipped++
			continue
		}

		historyID := uuid.New().String()
		now := time.Now().Format(time.RFC3339)

		historyEntry := &models.UpdateHistoryEntry{
			ID:            historyID,
			ContainerID:   update.ContainerID,
			ContainerName: update.ContainerName,
			Image:         update.ImageRef,
			OldDigest:     nil,
			Status:        "pending",
			Trigger:       "auto",
			StartedAt:     now,
		}
		if update.StackID != "" {
			historyEntry.StackID = &update.StackID
		}

		if err := s.db.InsertUpdateHistory(historyEntry); err != nil {
			s.logger.Error("Failed to insert update history", "error", err)
			continue
		}

		result, ar := s.docker.UpdateContainer(ctx, update.ContainerID, s.db)

		switch ar.Outcome {
		case truth.OutcomeSuccess:
			// Image actually advanced — record success, reset failure counter.
			succeeded++
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":       "success",
				"old_digest":   result.OldDigest,
				"new_digest":   result.NewDigest,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  result.DurationMs,
			}); err != nil {
				s.logger.Error("Failed to update success history", "error", err)
			}
			// Convergence: evict from cache.
			if evictErr := s.db.DeleteCachedUpdate(update.ContainerID); evictErr != nil {
				s.logger.Warn("Failed to evict cached update entry after auto-update",
					"containerID", update.ContainerID, "error", evictErr)
			}
			policy.ConsecutiveFailures = 0
			policy.UpdatedAt = time.Now().Format(time.RFC3339)
			if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
				s.logger.Error("Failed to reset policy failures", "error", err)
			}

		case truth.OutcomeNoChange:
			// Pull succeeded but image did not advance — it was already current.
			// Finding #8: do NOT reset consecutive failure counter; log and move on.
			// The item is evicted so it leaves the pending list without triggering
			// an infinite re-apply churn.
			s.logger.Info("Auto-update: image already up to date (no_change), skipping reset",
				"container", update.ContainerName,
				"reason", ar.Reason)
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":       "success",
				"old_digest":   result.OldDigest,
				"new_digest":   result.NewDigest,
				"completed_at": time.Now().Format(time.RFC3339),
				"duration_ms":  result.DurationMs,
			}); err != nil {
				s.logger.Error("Failed to update no-change history", "error", err)
			}
			// Convergence: evict from cache so this item leaves the list.
			if evictErr := s.db.DeleteCachedUpdate(update.ContainerID); evictErr != nil {
				s.logger.Warn("Failed to evict cached update entry after no_change",
					"containerID", update.ContainerID, "error", evictErr)
			}
			// Do NOT increment succeeded (no real update) and do NOT touch
			// consecutive failure counter.

		default: // OutcomeFailed
			failed++
			errMsg := ar.Reason
			if ar.Err != nil {
				errMsg = ar.Err.Error()
			}
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":        "failed",
				"error_message": errMsg,
				"completed_at":  time.Now().Format(time.RFC3339),
				"duration_ms":   result.DurationMs,
			}); err != nil {
				s.logger.Error("Failed to update failure history", "error", err)
			}

			policy.ConsecutiveFailures++
			if policy.ConsecutiveFailures >= 3 {
				policy.Paused = true
				policy.UpdatedAt = time.Now().Format(time.RFC3339)
				if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
					s.logger.Error("Failed to update paused policy", "error", err)
				}

				pausedHistory := &models.UpdateHistoryEntry{
					ID:            uuid.New().String(),
					ContainerID:   update.ContainerID,
					ContainerName: update.ContainerName,
					Image:         update.ImageRef,
					Status:        "paused",
					Trigger:       "auto",
					StartedAt:     time.Now().Format(time.RFC3339),
				}
				if update.StackID != "" {
					pausedHistory.StackID = &update.StackID
				}
				if err := s.db.InsertUpdateHistory(pausedHistory); err != nil {
					s.logger.Error("Failed to insert paused history", "error", err)
				}

				s.logger.Warn("Auto-update paused after 3 consecutive failures",
					"container", update.ContainerName,
					"target_type", policy.TargetType,
					"target_id", policy.TargetID)
			} else {
				policy.UpdatedAt = time.Now().Format(time.RFC3339)
				if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
					s.logger.Error("Failed to update policy", "error", err)
				}
			}
		}
	}

	s.logger.Info("Auto-update cycle completed",
		"succeeded", succeeded,
		"failed", failed,
		"skipped", skipped)

	if s.broadcastFn != nil {
		s.broadcastFn(models.StackEvent{Type: "update_policy_changed", Timestamp: time.Now()})
		if succeeded > 0 || failed > 0 {
			s.broadcastFn(models.StackEvent{Type: "resource_changed", Event: "container_update", Timestamp: time.Now()})
		}
	}
}
