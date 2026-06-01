package services

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

type DockerService struct {
	config *config.Config
	client *client.Client
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

func (s *DockerService) Start(stack models.Stack) (*models.CommandResult, error) {
	args := s.buildComposeArgs(stack, "up", []string{"-d"})

	cmd := exec.Command("docker", args...)
	cmd.Dir = stack.Directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, models.NewAppErrorWithDetails(500, models.ErrDockerOperation, "Failed to start stack", string(output))
	}

	return &models.CommandResult{
		ExitCode: 0,
		Stdout:   string(output),
		Stderr:   "",
	}, nil
}

func (s *DockerService) Stop(stack models.Stack) (*models.CommandResult, error) {
	args := s.buildComposeArgs(stack, "down", nil)

	cmd := exec.Command("docker", args...)
	cmd.Dir = stack.Directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, models.NewAppErrorWithDetails(500, models.ErrDockerOperation, "Failed to stop stack", string(output))
	}

	return &models.CommandResult{
		ExitCode: 0,
		Stdout:   string(output),
		Stderr:   "",
	}, nil
}

func (s *DockerService) Restart(stack models.Stack) (*models.CommandResult, error) {
	_, err := s.Stop(stack)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	backoff := 100 * time.Millisecond
waitLoop:
	for {
		select {
		case <-ctx.Done():
			break waitLoop
		default:
		}
		status, _, sErr := s.Status(stack)
		if sErr != nil || status == "stopped" {
			break waitLoop
		}
		time.Sleep(backoff)
		backoff = backoff * 2
		if backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}

	return s.Start(stack)
}

func (s *DockerService) Pull(stack models.Stack) (*models.CommandResult, error) {
	args := s.buildComposeArgs(stack, "pull", nil)

	cmd := exec.Command("docker", args...)
	cmd.Dir = stack.Directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, models.NewAppErrorWithDetails(500, models.ErrDockerOperation, "Failed to pull images", string(output))
	}

	return &models.CommandResult{
		ExitCode: 0,
		Stdout:   string(output),
		Stderr:   "",
	}, nil
}

type StreamLine struct {
	Type    string `json:"type"`
	Line    string `json:"line,omitempty"`
	Success bool   `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *DockerService) RunStreaming(ctx context.Context, stack models.Stack, subcommand string, extraArgs []string) <-chan StreamLine {
	out := make(chan StreamLine, 100)

	go func() {
		defer close(out)

		args := s.buildComposeArgs(stack, subcommand, extraArgs)
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Dir = stack.Directory

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			out <- StreamLine{Type: "error", Error: fmt.Sprintf("Failed to create pipe: %v", err)}
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			out <- StreamLine{Type: "error", Error: fmt.Sprintf("Failed to create stderr pipe: %v", err)}
			return
		}

		if err := cmd.Start(); err != nil {
			out <- StreamLine{Type: "error", Error: fmt.Sprintf("Failed to start command: %v", err)}
			return
		}

		done := make(chan struct{})
		go func() {
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" {
					out <- StreamLine{Type: "data", Line: line}
				}
			}
			if err := scanner.Err(); err != nil {
				out <- StreamLine{Type: "error", Error: err.Error()}
			}
			done <- struct{}{}
		}()
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.TrimSpace(line) != "" {
					out <- StreamLine{Type: "data", Line: line}
				}
			}
			if err := scanner.Err(); err != nil {
				out <- StreamLine{Type: "error", Error: err.Error()}
			}
			done <- struct{}{}
		}()

		<-done
		<-done

		err = cmd.Wait()
		if err != nil {
			out <- StreamLine{Type: "done", Success: false, Error: fmt.Sprintf("Command failed: %v", err)}
		} else {
			out <- StreamLine{Type: "done", Success: true}
		}
	}()

	return out
}

func (s *DockerService) Delete(stack models.Stack) (*models.CommandResult, error) {
	args := s.buildComposeArgs(stack, "down", []string{"-v"})

	cmd := exec.Command("docker", args...)
	cmd.Dir = stack.Directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, models.NewAppErrorWithDetails(500, models.ErrDockerOperation, "Failed to delete stack", string(output))
	}

	return &models.CommandResult{
		ExitCode: 0,
		Stdout:   string(output),
		Stderr:   "",
	}, nil
}

func (s *DockerService) Status(stack models.Stack) (string, []models.Container, error) {
	args := s.buildComposeArgs(stack, "ps", []string{"--format", "json"})

	cmd := exec.Command("docker", args...)
	cmd.Dir = stack.Directory

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown", nil, nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	containers := make([]models.Container, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		var composePS struct {
			ID      string `json:"ID"`
			Name    string `json:"Name"`
			Service string `json:"Service"`
			State   string `json:"State"`
			Health  string `json:"Health"`
			Image   string `json:"Image"`
			Ports   string `json:"Ports"`
		}

		if err := json.Unmarshal([]byte(line), &composePS); err != nil {
			continue
		}

		ports := parsePorts(composePS.Ports)

		container := models.Container{
			ID:     composePS.ID,
			Name:   composePS.Name,
			Image:  composePS.Image,
			State:  composePS.State,
			Status: composePS.State,
			Ports:  ports,
			Health: composePS.Health,
		}

		containers = append(containers, container)
	}

	status := "running"
	if len(containers) == 0 {
		status = "stopped"
	} else {
		for _, c := range containers {
			if c.State != "running" {
				status = "partial"
				break
			}
		}
	}

	return status, containers, nil
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

	if systemDelta > 0.0 && cpuDelta > 0.0 {
		cpuPercent = (cpuDelta / systemDelta) * float64(len(stats.CPUStats.CPUUsage.PercpuUsage)) * 100.0
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

func (s *DockerService) ListImages(ctx context.Context) ([]models.DockerImage, error) {
	images, err := s.client.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	result := make([]models.DockerImage, 0, len(images))
	for _, img := range images {
		repoTags := img.RepoTags
		if repoTags == nil {
			repoTags = []string{"<none>"}
		}

		result = append(result, models.DockerImage{
			ID:         img.ID,
			RepoTags:   repoTags,
			Size:       img.Size,
			Created:    img.Created,
			Containers: int(img.Containers),
		})
	}

	return result, nil
}

func (s *DockerService) ListVolumes(ctx context.Context) ([]models.DockerVolume, error) {
	volumes, err := s.client.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	result := make([]models.DockerVolume, 0, len(volumes.Volumes))
	for _, vol := range volumes.Volumes {
		if vol == nil {
			continue
		}

		var stack string
		if vol.Labels != nil {
			stack = vol.Labels["com.docker.compose.project"]
		}

		var usageData int64
		if vol.UsageData != nil {
			usageData = vol.UsageData.Size
		}

		result = append(result, models.DockerVolume{
			Name:       vol.Name,
			Driver:     vol.Driver,
			Mountpoint: vol.Mountpoint,
			Size:       usageData,
			Created:    vol.CreatedAt,
			Stack:      stack,
		})
	}

	return result, nil
}

func (s *DockerService) ListNetworks(ctx context.Context) ([]models.DockerNetwork, error) {
	networks, err := s.client.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}

	result := make([]models.DockerNetwork, 0, len(networks))
	for _, net := range networks {
		var stack string
		var labelStrs []string
		if net.Labels != nil {
			stack = net.Labels["com.docker.compose.project"]
			for k, v := range net.Labels {
				labelStrs = append(labelStrs, k+"="+v)
			}
		}

		// NetworkList does not populate net.Containers, so inspect each network to
		// get the real attachment count that the in-use delete guard relies on.
		containerCount := networkContainerCount(ctx, s.client, net.ID, len(net.Containers))

		result = append(result, models.DockerNetwork{
			ID:         net.ID,
			Name:       net.Name,
			Driver:     net.Driver,
			Scope:      net.Scope,
			Internal:   net.Internal,
			Containers: containerCount,
			Labels:     labelStrs,
			Created:    net.Created.Format(time.RFC3339),
			Stack:      stack,
		})
	}

	return result, nil
}

// networkInspector is the subset of the Docker client used to resolve real
// per-network container attachment counts (NetworkList leaves them empty).
type networkInspector interface {
	NetworkInspect(ctx context.Context, networkID string, options types.NetworkInspectOptions) (types.NetworkResource, error)
}

// networkContainerCount returns the number of containers attached to a network.
// NetworkList does not populate the Containers map, so we inspect the network;
// on inspect failure we fall back to whatever the list reported (typically 0).
func networkContainerCount(ctx context.Context, inspector networkInspector, networkID string, listFallback int) int {
	inspected, err := inspector.NetworkInspect(ctx, networkID, types.NetworkInspectOptions{})
	if err != nil {
		return listFallback
	}
	return len(inspected.Containers)
}

func (s *DockerService) DeleteContainer(ctx context.Context, containerID string, force bool) error {
	return s.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force})
}

func (s *DockerService) PruneContainers(ctx context.Context) (types.ContainersPruneReport, error) {
	return s.client.ContainersPrune(ctx, filters.NewArgs())
}

func (s *DockerService) DeleteImage(ctx context.Context, imageID string, force bool) ([]image.DeleteResponse, error) {
	return s.client.ImageRemove(ctx, imageID, image.RemoveOptions{Force: force})
}

func (s *DockerService) PruneImages(ctx context.Context) (types.ImagesPruneReport, error) {
	return s.client.ImagesPrune(ctx, filters.NewArgs())
}

func (s *DockerService) DeleteVolume(ctx context.Context, volumeName string, force bool) error {
	return s.client.VolumeRemove(ctx, volumeName, force)
}

func (s *DockerService) PruneVolumes(ctx context.Context) (types.VolumesPruneReport, error) {
	return s.client.VolumesPrune(ctx, filters.NewArgs())
}

func (s *DockerService) DeleteNetwork(ctx context.Context, networkID string) error {
	return s.client.NetworkRemove(ctx, networkID)
}

func (s *DockerService) CreateNetwork(ctx context.Context, name string, opts types.NetworkCreate) (string, error) {
	resp, err := s.client.NetworkCreate(ctx, name, opts)
	if err != nil {
		return "", fmt.Errorf("creating network: %w", err)
	}
	return resp.ID, nil
}

func (s *DockerService) PruneNetworks(ctx context.Context) (types.NetworksPruneReport, error) {
	return s.client.NetworksPrune(ctx, filters.NewArgs())
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

func (s *DockerService) ListBuildCache(ctx context.Context) ([]*types.BuildCache, error) {
	du, err := s.client.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return nil, err
	}
	return du.BuildCache, nil
}

func (s *DockerService) PruneBuildCache(ctx context.Context) (*types.BuildCachePruneReport, error) {
	return s.client.BuildCachePrune(ctx, types.BuildCachePruneOptions{All: true})
}

func (s *DockerService) CheckForUpdates(ctx context.Context, db DashboardDB) ([]models.ContainerUpdateInfo, error) {
	containers, err := s.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	type imgInfo struct {
		ref          string
		localDigest  string
		remoteDigest string
		checked      bool
	}

	imageMap := make(map[string]*imgInfo)

	type cInfo struct {
		id          string
		name        string
		imageID     string
		imageRef    string
		localDigest string
		state       string
		stackID     string
		project     string
		service     string
		isCompose   bool
	}
	var allContainers []cInfo

	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		imgInspect, _, err := s.client.ImageInspectWithRaw(ctx, c.ImageID)
		if err != nil {
			continue
		}

		var imageRef string
		for _, tag := range imgInspect.RepoTags {
			if tag != "<none>" && !strings.Contains(tag, "@sha256:") {
				imageRef = tag
				break
			}
		}
		if imageRef == "" {
			continue
		}

		var localDigest string
		for _, rd := range imgInspect.RepoDigests {
			if idx := strings.LastIndex(rd, "@"); idx >= 0 {
				localDigest = rd[idx+1:]
				break
			}
		}
		if localDigest == "" {
			continue
		}

		if _, exists := imageMap[imageRef]; !exists {
			imageMap[imageRef] = &imgInfo{
				ref:         imageRef,
				localDigest: localDigest,
			}
		}

		var stackID string
		projectName := c.Labels["com.docker.compose.project"]
		serviceName := c.Labels["com.docker.compose.service"]
		if db != nil && projectName != "" {
			stack, err := db.GetStackByProjectName(projectName)
			if err == nil && stack != nil {
				stackID = stack.ID
			}
		}

		allContainers = append(allContainers, cInfo{
			id:          c.ID,
			name:        name,
			imageID:     c.ImageID,
			imageRef:    imageRef,
			localDigest: localDigest,
			state:       c.State,
			stackID:     stackID,
			project:     projectName,
			service:     serviceName,
			isCompose:   projectName != "",
		})
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for ref, info := range imageMap {
		wg.Add(1)
		go func(ref string, info *imgInfo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			manifestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			output, err := exec.CommandContext(manifestCtx, "docker", "manifest", "inspect", "--verbose", ref).Output()
			if err != nil {
				slog.Debug("Failed to inspect manifest", "image", ref, "error", err)
				return
			}

			var manifest struct {
				Descriptor struct {
					Digest string `json:"digest"`
				} `json:"Descriptor"`
			}
			if err := json.Unmarshal(output, &manifest); err != nil {
				slog.Debug("Failed to parse manifest", "image", ref, "error", err)
				return
			}

			if manifest.Descriptor.Digest == "" {
				return
			}

			mu.Lock()
			info.remoteDigest = manifest.Descriptor.Digest
			info.checked = true
			mu.Unlock()
		}(ref, info)
	}
	wg.Wait()

	var result []models.ContainerUpdateInfo
	for _, c := range allContainers {
		info, ok := imageMap[c.imageRef]
		if !ok || !info.checked {
			continue
		}
		if info.remoteDigest != c.localDigest {
			result = append(result, models.ContainerUpdateInfo{
				ContainerID:   c.id,
				ContainerName: c.name,
				Image:         c.imageRef,
				ImageRef:      c.imageRef,
				State:         c.state,
				StackID:       c.stackID,
				ProjectName:   c.project,
				ServiceName:   c.service,
				IsCompose:     c.isCompose,
			})
		}
	}

	return result, nil
}

func (s *DockerService) UpdateContainer(ctx context.Context, containerID string, db DashboardDB) (models.UpdateResult, error) {
	inspect, err := s.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return models.UpdateResult{}, fmt.Errorf("inspecting container: %w", err)
	}

	oldDigest := inspect.Image
	start := time.Now()

	wasRunning := inspect.State != nil && inspect.State.Running
	projectName := ""
	serviceName := ""
	if inspect.Config != nil && inspect.Config.Labels != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
		serviceName = inspect.Config.Labels["com.docker.compose.service"]
	}

	var updateErr error
	if projectName != "" && serviceName != "" && db != nil {
		stack, err := db.GetStackByProjectName(projectName)
		if err == nil && stack != nil {
			updateErr = s.updateComposeContainer(ctx, *stack, serviceName, wasRunning)
		} else {
			updateErr = s.updateStandaloneContainer(ctx, inspect, wasRunning)
		}
	} else {
		updateErr = s.updateStandaloneContainer(ctx, inspect, wasRunning)
	}

	durationMs := time.Since(start).Milliseconds()

	if updateErr != nil {
		return models.UpdateResult{
			OldDigest:  oldDigest,
			DurationMs: durationMs,
		}, updateErr
	}

	newDigest := oldDigest
	newInspect, inspectErr := s.client.ContainerInspect(ctx, containerID)
	if inspectErr == nil {
		newDigest = newInspect.Image
	}

	return models.UpdateResult{
		OldDigest:  oldDigest,
		NewDigest:  newDigest,
		DurationMs: durationMs,
	}, nil
}

func (s *DockerService) updateComposeContainer(ctx context.Context, stack models.Stack, serviceName string, wasRunning bool) error {
	pullArgs := s.buildComposeArgs(stack, "pull", []string{serviceName})
	pullCmd := exec.CommandContext(ctx, "docker", pullArgs...)
	pullCmd.Dir = stack.Directory
	if output, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compose pull failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	upArgs := s.buildComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", serviceName})
	upCmd := exec.CommandContext(ctx, "docker", upArgs...)
	upCmd.Dir = stack.Directory
	if output, err := upCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compose up failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	if !wasRunning {
		time.Sleep(3 * time.Second)
		filterArgs := filters.NewArgs()
		filterArgs.Add("label", "com.docker.compose.project="+stack.ProjectName)
		filterArgs.Add("label", "com.docker.compose.service="+serviceName)
		filterArgs.Add("status", "running")

		containers, err := s.client.ContainerList(ctx, container.ListOptions{Filters: filterArgs})
		if err != nil {
			return fmt.Errorf("finding new container to stop: %w", err)
		}
		for _, c := range containers {
			if err := s.client.ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				slog.Error("Failed to stop recreated container", "id", c.ID, "error", err)
			}
		}
	}

	return nil
}

func (s *DockerService) updateStandaloneContainer(ctx context.Context, inspect types.ContainerJSON, wasRunning bool) error {
	imageRef := inspect.Config.Image

	reader, err := s.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	if wasRunning {
		if err := s.client.ContainerStop(ctx, inspect.ID, container.StopOptions{}); err != nil {
			return fmt.Errorf("stopping container: %w", err)
		}
	}

	if err := s.client.ContainerRemove(ctx, inspect.ID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}

	name := strings.TrimPrefix(inspect.Name, "/")

	var netConfig *network.NetworkingConfig
	if inspect.NetworkSettings != nil {
		netConfig = &network.NetworkingConfig{
			EndpointsConfig: inspect.NetworkSettings.Networks,
		}
	}

	newContainer, err := s.client.ContainerCreate(ctx, inspect.Config, inspect.HostConfig, netConfig, nil, name)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}

	if wasRunning {
		if err := s.client.ContainerStart(ctx, newContainer.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting container: %w", err)
		}
	}

	return nil
}
