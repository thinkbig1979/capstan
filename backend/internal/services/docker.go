package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/docker-manager/backend/internal/config"
	"github.com/docker-manager/backend/internal/models"
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

	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
		status, _, err := s.Status(stack)
		if err != nil || status == "stopped" {
			break
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
	args := s.buildComposeArgs(stack, "logs", []string{"--tail", fmt.Sprintf("%d", tail)})

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

		container := models.Container{
			ID:     c.ID,
			Name:   strings.TrimPrefix(c.Names[0], "/"),
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

			containerName := containerID[:12]
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

	globalEnvPath := s.config.StacksDir + "/global.env"
	if _, err := exec.Command("test", "-f", globalEnvPath).CombinedOutput(); err == nil {
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

func calculateMemPercent(stats *types.StatsJSON) (float64, string, string) {
	memPercent := 0.0
	memUsage := float64(stats.MemoryStats.Usage - stats.MemoryStats.Stats["cache"])
	memLimit := float64(stats.MemoryStats.Limit)

	if memLimit > 0 {
		memPercent = (memUsage / memLimit) * 100.0
	}

	return memPercent, formatBytes(memUsage), formatBytes(memLimit)
}

func calculateNetwork(stats *types.StatsJSON) (string, string) {
	var netRx, netTx uint64

	for _, network := range stats.Networks {
		netRx += network.RxBytes
		netTx += network.TxBytes
	}

	return formatBytes(float64(netRx)), formatBytes(float64(netTx))
}

func calculateBlockIO(stats *types.StatsJSON) (string, string) {
	var read, write uint64

	for _, stat := range stats.BlkioStats.IoServiceBytesRecursive {
		if stat.Op == "read" || stat.Op == "Read" {
			read += stat.Value
		} else if stat.Op == "write" || stat.Op == "Write" {
			write += stat.Value
		}
	}

	return formatBytes(float64(read)), formatBytes(float64(write))
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

func formatBytes(bytes float64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.0f B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", bytes/float64(div), "KMGTPE"[exp])
}
