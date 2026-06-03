package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

type DockerService struct {
	config *config.Config
	client *client.Client
	// statusFn, when non-nil, overrides statusVerified during lifecycle
	// settling. Production leaves it nil; unit tests inject a scripted
	// snapshot sequence so pollUntilSettled runs against real code.
	statusFn func(models.Stack) (string, []models.Container, error)
}

func NewDockerService(cfg *config.Config) (*DockerService, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker unavailable: %w", err)
	}

	return &DockerService{
		config: cfg,
		client: cli,
	}, nil
}

func (s *DockerService) Logs(stack models.Stack, tail int) (string, error) {
	args := s.buildComposeArgs(stack, "logs", []string{"--tail", fmt.Sprintf("%d", tail), "--timestamps"})

	cmd := exec.Command("docker", args...)
	cmd.Dir = stack.Directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}

	return string(output), nil
}

func (s *DockerService) GetContainerList(projectName string) ([]models.Container, error) {
	ctx := context.Background()

	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "com.docker.compose.project="+projectName)

	containers, err := s.client.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filterArgs,
	})

	if err != nil {
		return nil, err
	}

	result := make([]models.Container, 0, len(containers))

	for _, c := range containers {
		ports := make([]models.PortBinding, 0)
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				ports = append(ports, models.PortBinding{
					Host:      fmt.Sprintf("%s:%d", p.IP, p.PublicPort),
					Container: fmt.Sprintf("%d/%s", p.PrivatePort, p.Type),
					Protocol:  p.Type,
				})
			}
		}

		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		container := models.Container{
			ID:     c.ID,
			Name:   name,
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
			Ports:  ports,
		}

		result = append(result, container)
	}

	return result, nil
}

func (s *DockerService) GetContainerStats(ctx context.Context, containerID string) (<-chan models.ContainerMetrics, error) {
	statsChan := make(chan models.ContainerMetrics, 10)

	go func() {
		defer close(statsChan)

		stats, err := s.client.ContainerStats(ctx, containerID, true)
		if err != nil {
			return
		}
		defer stats.Body.Close()

		decoder := json.NewDecoder(stats.Body)

		for {
			var statsJSON types.StatsJSON
			if err := decoder.Decode(&statsJSON); err != nil {
				break
			}

			cpuPercent := calculateCPUPercent(&statsJSON)
			memPercent, memUsage, memLimit := calculateMemPercent(&statsJSON)
			netRx, netTx := calculateNetwork(&statsJSON)
			blockRead, blockWrite := calculateBlockIO(&statsJSON)
			memSwap := calculateMemSwap(&statsJSON)
			pids := getPids(&statsJSON)

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
			case statsChan <- metrics:
			case <-ctx.Done():
				return
			}
		}
	}()

	return statsChan, nil
}

func (s *DockerService) ListenEvents(ctx context.Context) (<-chan models.DockerEvent, error) {
	eventChan := make(chan models.DockerEvent, 100)

	dockerEvents, errChan := s.client.Events(ctx, types.EventsOptions{})

	go func() {
		defer close(eventChan)

		for {
			select {
			case event := <-dockerEvents:
				if event.Type == "container" {
					containerID := event.Actor.ID
					action := string(event.Action)

					dockerEvent := models.DockerEvent{
						ContainerID: containerID,
						Action:      action,
						Type:        "container",
						Timestamp:   time.Unix(event.Time, 0),
					}

					select {
					case eventChan <- dockerEvent:
					case <-ctx.Done():
						return
					}
				}
			case err := <-errChan:
				slog.Error("Docker event error", "error", err)
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	return eventChan, nil
}

func (s *DockerService) buildComposeArgs(stack models.Stack, subcommand string, extraArgs []string) []string {
	args := []string{"compose"}

	globalEnvPath := s.config.DataDir + "/global.env"
	if _, err := os.Stat(globalEnvPath); err == nil {
		args = append(args, "--env-file", globalEnvPath)
	}

	if stack.EnvFile != "" {
		args = append(args, "--env-file", stack.EnvFile)
	}

	args = append(args, "-f", stack.ComposeFile)
	args = append(args, "-p", stack.ProjectName)
	args = append(args, subcommand)
	args = append(args, extraArgs...)

	return args
}

func (s *DockerService) ValidateName(name string) error {
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9._:-]+$`, name)
	if !matched {
		return models.NewAppError(400, models.ErrValidation, "Invalid name format")
	}
	return nil
}

func calculateCPUPercent(stats *types.StatsJSON) float64 {
	cpuPercent := 0.0
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)

	// OnlineCPUs is populated on cgroup v2, where PercpuUsage is empty; fall back
	// to the per-CPU slice length for older daemons / cgroup v1. Without this the
	// multiplier is 0 on cgroup v2 hosts and CPU usage always reports 0%.
	cpuCount := float64(stats.CPUStats.OnlineCPUs)
	if cpuCount == 0 {
		cpuCount = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * cpuCount * 100.0
	}

	return cpuPercent
}

func calculateMemPercent(stats *types.StatsJSON) (float64, float64, float64) {
	memPercent := 0.0
	var cache uint64
	if stats.MemoryStats.Stats != nil {
		cache = stats.MemoryStats.Stats["cache"]
	}
	memUsage := float64(stats.MemoryStats.Usage - cache)
	memLimit := float64(stats.MemoryStats.Limit)

	if memLimit > 0 {
		memPercent = (memUsage / memLimit) * 100.0
	}

	return memPercent, memUsage, memLimit
}

func calculateMemSwap(stats *types.StatsJSON) float64 {
	if stats.MemoryStats.Stats == nil {
		return 0
	}
	swapUsage, ok := stats.MemoryStats.Stats["swap"]
	if !ok {
		return 0
	}
	return float64(swapUsage)
}

func getPids(stats *types.StatsJSON) uint64 {
	return stats.PidsStats.Current
}

func calculateNetwork(stats *types.StatsJSON) (float64, float64) {
	var netRx, netTx uint64

	for _, network := range stats.Networks {
		netRx += network.RxBytes
		netTx += network.TxBytes
	}

	return float64(netRx), float64(netTx)
}

func calculateBlockIO(stats *types.StatsJSON) (float64, float64) {
	var read, write uint64

	for _, stat := range stats.BlkioStats.IoServiceBytesRecursive {
		if stat.Op == "read" || stat.Op == "Read" {
			read += stat.Value
		} else if stat.Op == "write" || stat.Op == "Write" {
			write += stat.Value
		}
	}

	return float64(read), float64(write)
}

// perSecondRate converts a cumulative-counter delta over the given duration into
// a per-second rate. Negative deltas (counter resets on container restart) and
// non-positive durations clamp to zero.
func perSecondRate(delta, seconds float64) float64 {
	if delta <= 0 || seconds <= 0 {
		return 0
	}
	return delta / seconds
}

func parsePorts(portsStr string) []models.PortBinding {
	ports := make([]models.PortBinding, 0)
	if portsStr == "" {
		return ports
	}

	re := regexp.MustCompile(`(?P<host>\d+(?:\.\d+){3}:\d+)->(?P<container>\d+)/(?P<protocol>tcp|udp)`)
	matches := re.FindAllStringSubmatch(portsStr, -1)

	for _, match := range matches {
		if len(match) == 4 {
			ports = append(ports, models.PortBinding{
				Host:      match[1],
				Container: match[2] + "/" + match[3],
				Protocol:  match[3],
			})
		}
	}

	return ports
}

func (s *DockerService) GetAllContainersWithDetails(ctx context.Context, db DashboardDB) ([]models.DashboardContainerInfo, error) {
	containers, err := s.client.ContainerList(ctx, container.ListOptions{All: true, Size: true})
	if err != nil {
		return nil, err
	}

	result := make([]models.DashboardContainerInfo, 0, len(containers))

	for _, c := range containers {
		projectName := c.Labels["com.docker.compose.project"]

		ports := make([]models.PortBinding, 0)
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				ports = append(ports, models.PortBinding{
					Host:      fmt.Sprintf("%s:%d", p.IP, p.PublicPort),
					Container: fmt.Sprintf("%d/%s", p.PrivatePort, p.Type),
					Protocol:  p.Type,
				})
			}
		}

		var stackID string
		if db != nil && projectName != "" {
			stack, err := db.GetStackByProjectName(projectName)
			if err == nil && stack != nil {
				stackID = stack.ID
			}
		}

		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		restartCount := 0

		health := ""

		info := models.DashboardContainerInfo{
			ID:           c.ID,
			Name:         name,
			Image:        c.Image,
			State:        c.State,
			Status:       c.Status,
			Health:       health,
			Ports:        ports,
			StackID:      stackID,
			ProjectName:  projectName,
			RestartCount: restartCount,
			Created:      time.Unix(c.Created, 0),
			DiskSize:     c.SizeRw,
			ImageSize:    c.SizeRootFs,
		}

		if c.State == "running" {
			inspect, err := s.client.ContainerInspect(ctx, c.ID)
			if err == nil {
				if inspect.State != nil && inspect.State.StartedAt != "" {
					if t, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
						info.StartedAt = t
					}
				}
				if inspect.State != nil {
					info.RestartCount = inspect.RestartCount
					if inspect.State.Health != nil {
						info.Health = inspect.State.Health.Status
					}
				}
				if inspect.SizeRw != nil {
					info.DiskSize = *inspect.SizeRw
				}
				if inspect.SizeRootFs != nil {
					info.ImageSize = *inspect.SizeRootFs
				}
			}
		}

		result = append(result, info)
	}

	return result, nil
}

func (s *DockerService) GetImageDiskUsage(ctx context.Context) (int64, error) {
	images, err := s.client.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return 0, err
	}

	var total int64
	for _, img := range images {
		total += img.Size
	}

	return total, nil
}

type DiskUsageBreakdown struct {
	Images     int64 `json:"images"`
	Containers int64 `json:"containers"`
	Volumes    int64 `json:"volumes"`
	BuildCache int64 `json:"buildCache"`
	Total      int64 `json:"total"`
}

func (s *DockerService) GetDiskUsage(ctx context.Context) (*DiskUsageBreakdown, error) {
	du, err := s.client.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting disk usage: %w", err)
	}

	var imagesTotal, containersTotal, volumesTotal, buildCacheTotal int64
	for _, img := range du.Images {
		imagesTotal += img.Size
	}
	for _, ctr := range du.Containers {
		containersTotal += ctr.SizeRw
	}
	for _, vol := range du.Volumes {
		volumesTotal += vol.UsageData.Size
	}
	for _, bc := range du.BuildCache {
		buildCacheTotal += bc.Size
	}

	return &DiskUsageBreakdown{
		Images:     imagesTotal,
		Containers: containersTotal,
		Volumes:    volumesTotal,
		BuildCache: buildCacheTotal,
		Total:      imagesTotal + containersTotal + volumesTotal + buildCacheTotal,
	}, nil
}

func (s *DockerService) GetRunningContainerIDs(ctx context.Context) ([]string, error) {
	filterArgs := filters.NewArgs()
	filterArgs.Add("status", "running")

	containers, err := s.client.ContainerList(ctx, container.ListOptions{
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

type DashboardDB interface {
	GetStackByProjectName(projectName string) (*models.Stack, error)
}

// LiveStatus is a stack's live state derived from the shared container snapshot:
// a status string plus the project's reconstructed container list.
type LiveStatus struct {
	Status     string
	Containers []models.Container
}

// BuildStackStatuses buckets a single container snapshot by compose project and
// derives each project's live status and container list — no Docker calls and no
// per-stack `docker compose ps`. Status mirrors Status(): "running" when every
// container in the project is running, "partial" when the project has containers
// but not all are running. A project with no containers is simply absent from the
// returned map; it cannot reproduce Status()'s "unknown" (which means `compose
// ps` itself errored on an unreadable dir / invalid file — a condition container
// labels can't reveal), so the caller decides between "stopped" and "error" for
// absent projects. Multiple stacks sharing a project name each resolve to that
// project's containers (mirroring current /stacks behavior); production project
// names are unique per stack so this is moot there.
func BuildStackStatuses(containers []models.DashboardContainerInfo) map[string]LiveStatus {
	byProject := make(map[string][]models.Container)
	allRunning := make(map[string]bool)

	for _, c := range containers {
		if c.ProjectName == "" {
			continue
		}
		if _, seen := allRunning[c.ProjectName]; !seen {
			allRunning[c.ProjectName] = true
		}
		if c.State != "running" {
			allRunning[c.ProjectName] = false
		}
		byProject[c.ProjectName] = append(byProject[c.ProjectName], models.Container{
			ID:     c.ID,
			Name:   c.Name,
			Image:  c.Image,
			State:  c.State,
			Status: c.Status,
			Ports:  c.Ports,
			Health: c.Health,
		})
	}

	result := make(map[string]LiveStatus, len(byProject))
	for project, list := range byProject {
		status := "partial"
		if allRunning[project] {
			status = "running"
		}
		result[project] = LiveStatus{Status: status, Containers: list}
	}
	return result
}

// GetStackStatuses fetches a single container snapshot (one ContainerList) and
// returns live status per compose project, replacing the per-stack `docker
// compose ps` subprocess fan-out the stack list used to run (O(1) Docker call
// instead of O(N) process spawns).
func (s *DockerService) GetStackStatuses(ctx context.Context, db DashboardDB) (map[string]LiveStatus, error) {
	containers, err := s.GetAllContainersWithDetails(ctx, db)
	if err != nil {
		return nil, err
	}
	return BuildStackStatuses(containers), nil
}

func (s *DockerService) StartContainer(ctx context.Context, containerID string) error {
	return s.client.ContainerStart(ctx, containerID, container.StartOptions{})
}

func (s *DockerService) StopContainer(ctx context.Context, containerID string) error {
	return s.client.ContainerStop(ctx, containerID, container.StopOptions{})
}

func (s *DockerService) InspectContainer(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	return s.client.ContainerInspect(ctx, containerID)
}

func (s *DockerService) RestartContainer(ctx context.Context, containerID string) error {
	return s.client.ContainerRestart(ctx, containerID, container.StopOptions{})
}
