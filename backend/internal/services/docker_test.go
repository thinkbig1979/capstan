package services

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/docker/docker/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerService_buildComposeArgs(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	args := service.buildComposeArgs(stack, "up", []string{"-d"})

	assert.Contains(t, args, "compose")
	assert.Contains(t, args, "up")
	assert.Contains(t, args, "-d")
	assert.Contains(t, args, "-f")
	assert.Contains(t, args, "compose.yaml")
	assert.Contains(t, args, "-p")
	assert.Contains(t, args, "test-stack-default")
	assert.Contains(t, args, "--env-file")
	assert.Contains(t, args, ".env")
}

func TestDockerService_buildComposeArgs_WithGlobalEnv(t *testing.T) {
	tempDir := t.TempDir()
	dataDir := t.TempDir()

	globalEnvPath := filepath.Join(dataDir, "global.env")
	err := os.WriteFile(globalEnvPath, []byte("TEST=value\n"), 0644)
	require.NoError(t, err)

	cfg := &config.Config{StacksDir: tempDir, DataDir: dataDir}
	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	args := service.buildComposeArgs(stack, "up", []string{"-d"})

	envFileCount := 0
	for i, arg := range args {
		if arg == "--env-file" && i+1 < len(args) {
			envFileCount++
		}
	}

	assert.Equal(t, 2, envFileCount, "Should include both global and stack env files")
}

func TestDockerService_buildComposeArgs_WithoutEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     "",
		ProjectName: "test-stack-default",
	}

	args := service.buildComposeArgs(stack, "up", []string{"-d"})

	stackEnvPresent := false
	for i, arg := range args {
		if arg == "--env-file" && i+1 < len(args) {
			if args[i+1] == ".env" {
				stackEnvPresent = true
			}
		}
	}

	assert.False(t, stackEnvPresent, "Should not include --env-file for empty EnvFile")
}

func TestDockerService_ValidateName(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	tests := []struct {
		name    string
		wantErr bool
	}{
		{"valid-stack-name", false},
		{"Valid_Stack.Name", false},
		{"stack:with:colons", false},
		{"stack-with-dashes", false},
		{"stack123", false},
		{"invalid name", true},
		{"invalid@name", true},
		{"", true},
		{"invalid/name", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := service.ValidateName(tt.name)
			if tt.wantErr {
				assert.Error(t, err)
				var appErr *models.AppError
				require.ErrorAs(t, err, &appErr)
				assert.Equal(t, models.ErrValidation, appErr.Code)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCalculateCPUPercent(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.CPUStats.CPUUsage.TotalUsage = 10000000
	stats.CPUStats.CPUUsage.PercpuUsage = []uint64{5000000, 5000000}
	stats.PreCPUStats.CPUUsage.TotalUsage = 5000000
	stats.PreCPUStats.CPUUsage.PercpuUsage = []uint64{2500000, 2500000}
	stats.CPUStats.SystemUsage = 100000000
	stats.PreCPUStats.SystemUsage = 50000000
	stats.CPUStats.OnlineCPUs = 2

	percent := calculateCPUPercent(stats)
	assert.Greater(t, percent, 0.0)
	assert.LessOrEqual(t, percent, 100.0)
}

func TestCalculateCPUPercent_ZeroDelta(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.CPUStats.CPUUsage.TotalUsage = 1000000
	stats.PreCPUStats.CPUUsage.TotalUsage = 1000000
	stats.CPUStats.SystemUsage = 10000000
	stats.PreCPUStats.SystemUsage = 10000000
	stats.CPUStats.OnlineCPUs = 1

	percent := calculateCPUPercent(stats)
	assert.Equal(t, 0.0, percent)
}

func TestCalculateMemPercent(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.MemoryStats.Usage = 100000000
	stats.MemoryStats.Limit = 1000000000
	stats.MemoryStats.Stats = map[string]uint64{
		"cache": 10000000,
	}

	percent, usage, limit := calculateMemPercent(stats)

	assert.Greater(t, percent, 0.0)
	assert.LessOrEqual(t, percent, 100.0)
	assert.Equal(t, 90000000.0, usage)
	assert.Equal(t, 1000000000.0, limit)
}

func TestCalculateMemPercent_ZeroLimit(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.MemoryStats.Usage = 100000000
	stats.MemoryStats.Limit = 0
	stats.MemoryStats.Stats = map[string]uint64{
		"cache": 10000000,
	}

	percent, _, _ := calculateMemPercent(stats)
	assert.Equal(t, 0.0, percent)
}

func TestCalculateNetwork(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.Networks = map[string]types.NetworkStats{
		"eth0": {
			RxBytes: 1000000,
			TxBytes: 500000,
		},
		"eth1": {
			RxBytes: 2000000,
			TxBytes: 1000000,
		},
	}

	rx, tx := calculateNetwork(stats)

	assert.Equal(t, 3000000.0, rx)
	assert.Equal(t, 1500000.0, tx)
}

func TestCalculateNetwork_Empty(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.Networks = map[string]types.NetworkStats{}

	rx, tx := calculateNetwork(stats)

	assert.Equal(t, 0.0, rx)
	assert.Equal(t, 0.0, tx)
}

func TestCalculateBlockIO(t *testing.T) {
	stats := &types.StatsJSON{}
	stats.BlkioStats.IoServiceBytesRecursive = []types.BlkioStatEntry{
		{Op: "read", Value: 1000000},
		{Op: "Read", Value: 2000000},
		{Op: "write", Value: 500000},
		{Op: "Write", Value: 1000000},
	}

	read, write := calculateBlockIO(stats)

	assert.Equal(t, 3000000.0, read)
	assert.Equal(t, 1500000.0, write)
}

func TestParsePorts(t *testing.T) {
	tests := []struct {
		input    string
		expected []models.PortBinding
	}{
		{
			"0.0.0.0:8080->80/tcp",
			[]models.PortBinding{
				{Host: "0.0.0.0:8080", Container: "80/tcp", Protocol: "tcp"},
			},
		},
		{
			"0.0.0.0:8080->80/tcp, 0.0.0.0:5432->5432/tcp",
			[]models.PortBinding{
				{Host: "0.0.0.0:8080", Container: "80/tcp", Protocol: "tcp"},
				{Host: "0.0.0.0:5432", Container: "5432/tcp", Protocol: "tcp"},
			},
		},
		{
			"",
			[]models.PortBinding{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ports := parsePorts(tt.input)
			assert.Equal(t, tt.expected, ports)
		})
	}
}

func TestDockerService_Start_FailsWithoutDocker(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	_, err := service.Start(stack)
	assert.Error(t, err)
}

func TestDockerService_Stop_FailsWithoutDocker(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	_, err := service.Stop(stack)
	assert.Error(t, err)
}

func TestDockerService_Restart_FailsWithoutDocker(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	_, err := service.Restart(stack)
	assert.Error(t, err)
}

func TestDockerService_Pull_FailsWithoutDocker(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	_, err := service.Pull(stack)
	assert.Error(t, err)
}

func TestDockerService_Delete_FailsWithoutDocker(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	_, err := service.Delete(stack)
	assert.Error(t, err)
}

func TestDockerService_Status_InvalidProject(t *testing.T) {
	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}

	service := &DockerService{config: cfg}

	stack := models.Stack{
		ID:          filepath.Base(tempDir) + "~test-stack:default",
		Directory:   tempDir,
		ComposeFile: "compose.yaml",
		EnvFile:     ".env",
		ProjectName: "test-stack-default",
	}

	status, containers, err := service.Status(stack)

	assert.NoError(t, err)
	assert.Equal(t, "unknown", status)
	assert.Nil(t, containers)
}

// fakeNetworkInspector lets us exercise networkContainerCount without a daemon.
type fakeNetworkInspector struct {
	containers map[string]int // networkID -> attached container count
	err        error
}

func (f fakeNetworkInspector) NetworkInspect(_ context.Context, networkID string, _ types.NetworkInspectOptions) (types.NetworkResource, error) {
	if f.err != nil {
		return types.NetworkResource{}, f.err
	}
	res := types.NetworkResource{ID: networkID, Containers: map[string]types.EndpointResource{}}
	for i := 0; i < f.containers[networkID]; i++ {
		res.Containers[fmt.Sprintf("c%d", i)] = types.EndpointResource{}
	}
	return res, nil
}

// TestNetworkContainerCount reproduces FLAG 2 (D-3): NetworkList never populates
// the Containers map, so the list-reported count is always 0 and the in-use delete
// guard is inert. networkContainerCount must inspect each network to get the real
// attachment count, and fall back to the list value only when inspect fails.
func TestNetworkContainerCount(t *testing.T) {
	ctx := context.Background()

	// Real attachments revealed by inspect, even though the list reported 0.
	inspector := fakeNetworkInspector{containers: map[string]int{"net-busy": 3, "net-empty": 0}}
	assert.Equal(t, 3, networkContainerCount(ctx, inspector, "net-busy", 0))
	assert.Equal(t, 0, networkContainerCount(ctx, inspector, "net-empty", 0))

	// Inspect failure: fall back to whatever the list reported.
	failing := fakeNetworkInspector{err: errors.New("inspect boom")}
	assert.Equal(t, 7, networkContainerCount(ctx, failing, "net-x", 7))
}

// TestBuildStackStatuses locks in the FLAG 1 follow-up: live stack status +
// per-stack containers are derived from a single ContainerList snapshot bucketed
// by compose project, replacing the per-stack `docker compose ps` subprocess
// fan-out in GET /stacks. Status mirrors the old Status(): all-running ->
// "running", non-empty-but-not-all-running -> "partial". A project with no
// containers is simply absent from the map (the caller maps that to stopped/error).
func TestBuildStackStatuses(t *testing.T) {
	snapshot := []models.DashboardContainerInfo{
		{ProjectName: "web", ID: "a1", Name: "web-1", Image: "nginx", State: "running", Status: "Up 2 minutes", Health: "healthy", Ports: []models.PortBinding{{Host: "0.0.0.0:80", Container: "80/tcp", Protocol: "tcp"}}},
		{ProjectName: "web", ID: "a2", Name: "web-2", Image: "nginx", State: "running", Status: "Up 2 minutes"},
		{ProjectName: "api", ID: "b1", Name: "api-1", Image: "go", State: "running", Status: "Up 1 hour"},
		{ProjectName: "api", ID: "b2", Name: "api-2", Image: "go", State: "exited", Status: "Exited (0)"},
		{ProjectName: "idle", ID: "c1", Name: "idle-1", Image: "busybox", State: "exited", Status: "Exited (0)"},
		{ProjectName: "", ID: "x1", Name: "orphan", State: "running"}, // no compose project -> ignored
	}

	got := BuildStackStatuses(snapshot)

	// Only the three labelled projects are present; the unlabelled container is ignored.
	assert.Len(t, got, 3)
	_, hasOrphan := got[""]
	assert.False(t, hasOrphan, "containers without a compose project are ignored")

	// web: every container running -> "running".
	require.Contains(t, got, "web")
	assert.Equal(t, "running", got["web"].Status)
	require.Len(t, got["web"].Containers, 2)

	// api: one running, one exited -> "partial".
	assert.Equal(t, "partial", got["api"].Status)
	assert.Len(t, got["api"].Containers, 2)

	// idle: only an exited container -> "partial" (mirrors Status(): non-empty,
	// not all running; "stopped" is reserved for zero containers).
	assert.Equal(t, "partial", got["idle"].Status)

	// Field parity: every models.Container field is carried from the snapshot.
	web1 := got["web"].Containers[0]
	assert.Equal(t, "a1", web1.ID)
	assert.Equal(t, "web-1", web1.Name)
	assert.Equal(t, "nginx", web1.Image)
	assert.Equal(t, "running", web1.State)
	assert.Equal(t, "Up 2 minutes", web1.Status)
	assert.Equal(t, "healthy", web1.Health)
	require.Len(t, web1.Ports, 1)
	assert.Equal(t, "0.0.0.0:80", web1.Ports[0].Host)
}

// TestBuildStackStatuses_SharedProject documents that multiple stacks sharing a
// compose project name (a messy dev scan, e.g. several docker/ folders -> project
// "docker") each resolve to that project's containers. This mirrors the current
// /stacks double-count; in production project names are unique per stack.
func TestBuildStackStatuses_SharedProject(t *testing.T) {
	snapshot := []models.DashboardContainerInfo{
		{ProjectName: "docker", ID: "d1", State: "running"},
		{ProjectName: "docker", ID: "d2", State: "exited"},
	}
	got := BuildStackStatuses(snapshot)
	assert.Equal(t, "partial", got["docker"].Status)
	assert.Len(t, got["docker"].Containers, 2)
}

func TestBuildStackStatuses_Empty(t *testing.T) {
	assert.Empty(t, BuildStackStatuses(nil))
}

// TestSelectUpdates pins the update-detection decision after the multi-arch fix.
// The old code ran `docker manifest inspect --verbose`, which returns a JSON array
// for multi-arch images, then unmarshalled it into a single struct — failing and
// silently skipping every multi-arch image, so nothing was ever reported. The new
// path compares the local RepoDigest against a remote index digest per ref.
func TestSelectUpdates(t *testing.T) {
	candidates := []updateCandidate{
		// Remote differs from local -> a real update (the multi-arch case the old
		// code never surfaced; mirrors postgres:16-alpine local 93d5 vs remote 16bc).
		{info: models.ContainerUpdateInfo{ContainerName: "db", ImageRef: "postgres:16-alpine"}, localDigest: "sha256:93d5"},
		// Remote equals local -> up to date, not reported.
		{info: models.ContainerUpdateInfo{ContainerName: "web", ImageRef: "nginx:latest"}, localDigest: "sha256:aaaa"},
		// Remote digest unavailable (fetch failed) -> must NOT be reported as an
		// update, so a registry hiccup never produces a false positive.
		{info: models.ContainerUpdateInfo{ContainerName: "cache", ImageRef: "redis:7"}, localDigest: "sha256:bbbb"},
	}
	remote := map[string]string{
		"postgres:16-alpine": "sha256:16bc",
		"nginx:latest":       "sha256:aaaa",
		// redis:7 intentionally absent.
	}

	got := selectUpdates(candidates, remote)

	require.Len(t, got, 1)
	assert.Equal(t, "postgres:16-alpine", got[0].ImageRef)
	assert.Equal(t, "db", got[0].ContainerName)
}
