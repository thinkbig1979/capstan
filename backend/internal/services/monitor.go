package services

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
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
					var statsJSON types.StatsJSON
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

func (s *MonitorService) ListenEvents(ctx context.Context) (<-chan models.StackEvent, error) {
	eventChan := make(chan models.StackEvent, 100)

	dockerEvents, errChan := s.client.Events(ctx, types.EventsOptions{
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

				if projectName == "" {
					stackEvent := models.StackEvent{
						Type:        "container_event",
						StackID:     "",
						ContainerID: containerID,
						Event:       action,
						Timestamp:   time.Unix(event.Time, 0),
					}
					if action == "start" || action == "stop" || action == "die" || action == "destroy" {
						select {
						case eventChan <- stackEvent:
						case <-ctx.Done():
							return
						}
					}
					continue
				}

				var stackID string
				if s.db != nil {
					stack, err := s.db.GetStackByProjectName(projectName)
					if err != nil {
						slog.Debug("Stack not found for project", "project", projectName, "error", err)
						continue
					}
					stackID = stack.ID
				} else {
					stackID = projectName
				}

				stackEvent := models.StackEvent{
					Type:        "container_event",
					StackID:     stackID,
					ContainerID: containerID,
					Event:       action,
					Timestamp:   time.Unix(event.Time, 0),
				}

				if action == "start" || action == "restart" {
					stackEvent.Type = "stack_status"
					stackEvent.Status = "running"
				} else if action == "stop" || action == "die" || action == "kill" {
					stackEvent.Type = "stack_status"
					stackEvent.Status = "stopped"
				} else if action == "pause" {
					stackEvent.Type = "stack_status"
					stackEvent.Status = "paused"
				} else if action == "unpause" {
					stackEvent.Type = "stack_status"
					stackEvent.Status = "running"
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
