package services

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/docker-manager/backend/internal/database"
	"github.com/docker-manager/backend/internal/models"
	"github.com/google/uuid"
)

type EventBroadcaster func(event models.StackEvent)

type SchedulerService struct {
	docker      *DockerService
	db          *database.DB
	mu          sync.Mutex
	ticker      *time.Ticker
	done        chan struct{}
	logger      *slog.Logger
	scanning    bool
	broadcastFn EventBroadcaster
}

func NewSchedulerService(docker *DockerService, db *database.DB, logger *slog.Logger, broadcastFn EventBroadcaster) *SchedulerService {
	if logger == nil {
		logger = slog.Default()
	}
	return &SchedulerService{
		docker:      docker,
		db:          db,
		logger:      logger.With("component", "scheduler"),
		broadcastFn: broadcastFn,
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

	s.ticker = time.NewTicker(interval)
	s.done = make(chan struct{})

	go func() {
		s.logger.Info("Scheduler started", "interval", interval)
		for {
			select {
			case <-s.ticker.C:
				s.runCycle(context.Background())
			case <-s.done:
				s.logger.Info("Scheduler stopped")
				return
			}
		}
	}()
}

func (s *SchedulerService) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *SchedulerService) RunScan(ctx context.Context) ([]models.CachedUpdate, error) {
	if !s.mu.TryLock() {
		return nil, fmt.Errorf("scan already in progress")
	}
	defer s.mu.Unlock()

	s.scanning = true
	defer func() { s.scanning = false }()

	results, err := s.docker.CheckForUpdates(ctx, s.db)
	if err != nil {
		if dbErr := s.db.SetSetting("update_scan_last_error", err.Error()); dbErr != nil {
			s.logger.Error("Failed to record scan error", "error", dbErr)
		}
		s.logger.Error("Scan failed", "error", err)
		return nil, err
	}

	var cachedUpdates []models.CachedUpdate
	now := time.Now().Format(time.RFC3339)
	for _, r := range results {
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
			LocalDigest:   "",
			RemoteDigest:  "",
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
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.scanning = false
			s.mu.Unlock()
		}()

		results, err := s.docker.CheckForUpdates(context.Background(), s.db)
		if err != nil {
			if dbErr := s.db.SetSetting("update_scan_last_error", err.Error()); dbErr != nil {
				s.logger.Error("Failed to record scan error", "error", dbErr)
			}
			s.logger.Error("Background scan failed", "error", err)
			if s.broadcastFn != nil {
				s.broadcastFn(models.StackEvent{Type: "update_scan_failed", Timestamp: time.Now()})
			}
			return
		}

		var cachedUpdates []models.CachedUpdate
		now := time.Now().Format(time.RFC3339)
		for _, r := range results {
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
				LocalDigest:   "",
				RemoteDigest:  "",
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

		s.logger.Info("Background scan completed", "updates_found", len(cachedUpdates))

		if s.broadcastFn != nil {
			s.broadcastFn(models.StackEvent{Type: "update_scan_complete", Timestamp: time.Now()})
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
		if p.TargetType == "container" {
			containerPolicies[p.TargetID] = p
		} else if p.TargetType == "stack" {
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

		result, err := s.docker.UpdateContainer(ctx, update.ContainerID, s.db)
		if err != nil {
			failed++
			if err := s.db.UpdateUpdateHistory(historyID, map[string]interface{}{
				"status":        "failed",
				"error_message": err.Error(),
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
			continue
		}

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

		policy.ConsecutiveFailures = 0
		policy.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := s.db.UpsertAutoUpdatePolicy(policy); err != nil {
			s.logger.Error("Failed to reset policy failures", "error", err)
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
