package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

type MonitorService struct {
	client *client.Client
	db     Database
}

type Database interface {
	GetStackByProjectName(projectName string) (*models.Stack, error)
}

func NewMonitorService(dockerClient *client.Client) *MonitorService {
	return &MonitorService{
		client: dockerClient,
	}
}

func NewMonitorServiceWithDB(dockerClient *client.Client, db Database) *MonitorService {
	return &MonitorService{
		client: dockerClient,
		db:     db,
	}
}

func (s *MonitorService) StreamStats(ctx context.Context, containerIDs []string) (<-chan []models.ContainerMetrics, error) {
	// main.go leaves the Docker client nil when it could not be constructed
	// (e.g. DOCKER_HOST pointing at nothing). Refuse before the goroutine
	// starts: a panic in there is not caught by gin's RecoveryMiddleware and
	// takes the process down (agent-os-xay).
	if s == nil || s.client == nil {
		return nil, ErrDockerUnavailable
	}

	statsChan := make(chan []models.ContainerMetrics, 10)

	if len(containerIDs) == 0 {
		close(statsChan)
		return statsChan, nil
	}

	go func() {
		defer close(statsChan)

		var wg sync.WaitGroup
		containerChans := make([]chan models.ContainerMetrics, len(containerIDs))
		for i := range containerChans {
			containerChans[i] = make(chan models.ContainerMetrics, 10)
		}

		for i, containerID := range containerIDs {
			wg.Add(1)
			i := i
			containerID := containerID
			ch := containerChans[i]

			go func() {
				defer wg.Done()
				defer close(ch)

				stats, err := s.client.ContainerStats(ctx, containerID, true)
				if err != nil {
					slog.Error("Failed to stream stats", "container", containerID, "error", err)
					return
				}
				defer stats.Body.Close()

				decoder := json.NewDecoder(stats.Body)

				// Network and block I/O counters are cumulative; we convert them
				// to per-second rates using the delta against the previous sample.
				var (
					prevNetRx, prevNetTx      float64
					prevBlkRead, prevBlkWrite float64
					prevSampleTime            time.Time
					havePrevSample            bool
				)

				for {
					var statsJSON container.StatsResponse
					if err := decoder.Decode(&statsJSON); err != nil {
						break
					}

					cpuPercent := calculateCPUPercent(&statsJSON)
					memPercent, memUsage, memLimit := calculateMemPercent(&statsJSON)
					cumNetRx, cumNetTx := calculateNetwork(&statsJSON)
					cumBlkRead, cumBlkWrite := calculateBlockIO(&statsJSON)
					memSwap := calculateMemSwap(&statsJSON)
					pids := getPids(&statsJSON)

					// Derive per-second throughput from the cumulative counters.
					sampleTime := statsJSON.Read
					if sampleTime.IsZero() {
						sampleTime = time.Now()
					}
					var netRx, netTx, blockRead, blockWrite float64
					if havePrevSample {
						if dt := sampleTime.Sub(prevSampleTime).Seconds(); dt > 0 {
							netRx = perSecondRate(cumNetRx-prevNetRx, dt)
							netTx = perSecondRate(cumNetTx-prevNetTx, dt)
							blockRead = perSecondRate(cumBlkRead-prevBlkRead, dt)
							blockWrite = perSecondRate(cumBlkWrite-prevBlkWrite, dt)
						}
					}
					prevNetRx, prevNetTx = cumNetRx, cumNetTx
					prevBlkRead, prevBlkWrite = cumBlkRead, cumBlkWrite
					prevSampleTime = sampleTime
					havePrevSample = true

					containerName := containerID
					if len(containerID) > 12 {
						containerName = containerID[:12]
					}
					if len(statsJSON.Name) > 0 {
						containerName = strings.TrimPrefix(statsJSON.Name, "/")
					}

					metrics := models.ContainerMetrics{
						ContainerID: containerID,
						Name:        containerName,
						CPUPercent:  cpuPercent,
						MemUsage:    memUsage,
						MemLimit:    memLimit,
						MemPercent:  memPercent,
						NetRx:       netRx,
						NetTx:       netTx,
						BlockRead:   blockRead,
						BlockWrite:  blockWrite,
						MemSwap:     memSwap,
						Pids:        pids,
					}

					select {
					case ch <- metrics:
					case <-ctx.Done():
						return
					}
				}
			}()
		}

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		lastMetrics := make(map[string]models.ContainerMetrics)
		metricsMu := sync.Mutex{}

		for _, ch := range containerChans {
			go func(c <-chan models.ContainerMetrics) {
				for metrics := range c {
					metricsMu.Lock()
					lastMetrics[metrics.ContainerID] = metrics
					metricsMu.Unlock()
				}
			}(ch)
		}

		go func() {
			wg.Wait()
			metricsMu.Lock()
			for id := range lastMetrics {
				delete(lastMetrics, id)
			}
			metricsMu.Unlock()
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				metricsMu.Lock()
				batch := make([]models.ContainerMetrics, 0, len(lastMetrics))
				for _, m := range lastMetrics {
					batch = append(batch, m)
				}
				metricsMu.Unlock()

				select {
				case statsChan <- batch:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return statsChan, nil
}

// unassociatedStackEvent is the event emitted for a container that belongs to no
// stack we can name. It is the shape the label-less (non-compose) path has always
// used: a plain container_event with an empty StackID, and only for the four
// lifecycle actions that carry signal on their own. The other six (restart,
// pause, unpause, kill, create, rename) are only meaningful as a stack status
// transition, so without a stack they are still dropped — unchanged behaviour.
func unassociatedStackEvent(action, containerID string, ts time.Time) (models.StackEvent, bool) {
	switch action {
	case "start", "stop", "die", "destroy":
		return models.StackEvent{
			Type:        "container_event",
			StackID:     "",
			ContainerID: containerID,
			Event:       action,
			Timestamp:   ts,
		}, true
	default:
		return models.StackEvent{}, false
	}
}

// stackEventFor maps one Docker container event to the StackEvent to broadcast,
// reporting ok=false when the event carries nothing worth broadcasting.
//
// agent-os-91u2. The stack lookup used to be `if err != nil { slog.Debug(...);
// continue }`, which merged two unrelated outcomes: "this compose project has no
// stacks row" — routine, for a project started outside Capstan or not yet
// scanned — and "the stacks table cannot be read". Both dropped the event
// entirely, and the only trace was DEBUG, which is off in production. So a
// container going down was invisible, and a broken database looked exactly like
// a container Capstan does not manage.
//
// The two are now separated, and NEITHER drops the event:
//
//   - sql.ErrNoRows: not an error. Emit unassociated, log at DEBUG.
//   - any other error: a real read fault. Emit unassociated as well, and log at
//     ERROR. The container really did start or die; that fact does not depend on
//     the database, and a database fault is the worst moment for the fleet to go
//     dark. The log level is what distinguishes the two, which is the whole
//     point — dropping telemetry because a container has no stack and dropping it
//     because the database is broken must not look the same.
func (s *MonitorService) stackEventFor(action, containerID, projectName string, ts time.Time) (models.StackEvent, bool) {
	if projectName == "" {
		return unassociatedStackEvent(action, containerID, ts)
	}

	// Without a database this service reports the compose project name as the
	// stack id, unchanged.
	stackID := projectName
	if s.db != nil {
		stack, err := s.db.GetStackByProjectName(projectName)
		switch {
		case err == nil:
			stackID = stack.ID
		case errors.Is(err, sql.ErrNoRows):
			slog.Debug("No stack row for compose project; emitting the container event without a stack association",
				"project", projectName)
			return unassociatedStackEvent(action, containerID, ts)
		default:
			slog.Error("Failed to read the stack for a compose project; emitting the container event without a stack association, so this container's events are no longer attributed to its stack",
				"project", projectName, "error", err)
			return unassociatedStackEvent(action, containerID, ts)
		}
	}

	stackEvent := models.StackEvent{
		Type:        "container_event",
		StackID:     stackID,
		ContainerID: containerID,
		Event:       action,
		Timestamp:   ts,
	}

	switch action {
	case "start", "restart", "unpause":
		stackEvent.Type = "stack_status"
		stackEvent.Status = "running"
	case "stop", "die", "kill":
		stackEvent.Type = "stack_status"
		stackEvent.Status = "stopped"
	case "pause":
		stackEvent.Type = "stack_status"
		stackEvent.Status = "paused"
	}

	return stackEvent, true
}

func (s *MonitorService) ListenEvents(ctx context.Context) (<-chan models.StackEvent, error) {
	if s == nil || s.client == nil {
		return nil, ErrDockerUnavailable
	}

	eventChan := make(chan models.StackEvent, 100)

	dockerEvents, errChan := s.client.Events(ctx, events.ListOptions{
		Filters: filters.NewArgs(
			filters.KeyValuePair{
				Key:   "type",
				Value: "container",
			},
		),
	})

	go func() {
		defer close(eventChan)

		for {
			select {
			case event := <-dockerEvents:
				if event.Type != "container" {
					continue
				}

				containerID := event.Actor.ID
				action := string(event.Action)

				slog.Debug("Docker container event", "action", action, "container", containerID[:12])

				switch action {
				case "start", "stop", "die", "kill", "destroy", "restart", "pause", "unpause", "create", "rename":
				default:
					continue
				}

				containerLabels := event.Actor.Attributes
				projectName := containerLabels["com.docker.compose.project"]

				stackEvent, ok := s.stackEventFor(action, containerID, projectName, time.Unix(event.Time, 0))
				if !ok {
					continue
				}

				select {
				case eventChan <- stackEvent:
				case <-ctx.Done():
					return
				}

			case err := <-errChan:
				if err != nil {
					slog.Error("Docker event error", "error", err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

func (s *MonitorService) GetContainersForStack(ctx context.Context, projectName string) ([]string, error) {
	if s == nil || s.client == nil {
		return nil, ErrDockerUnavailable
	}

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "com.docker.compose.project="+projectName)

	containers, err := s.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})

	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(containers))
	for _, c := range containers {
		ids = append(ids, c.ID)
	}

	return ids, nil
}
