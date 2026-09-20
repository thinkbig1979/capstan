package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

// Hoisted out of ValidateName: the pattern is a literal, so compiling it per
// call bought nothing and left an error return nobody could act on
// (agent-os-qyg7.1). MustCompile fails at init if the pattern is ever broken,
// which is the only way it could fail.
var dockerNameRe = regexp.MustCompile(`^[a-zA-Z0-9._:-]+$`)

// ErrDockerUnavailable is returned by every DockerService method when the
// service itself is nil — main leaves dockerService nil when the daemon was
// unreachable at startup (see cmd/server/main.go).
//
// Design decision (agent-os-xay): the guard lives on the RECEIVER, not on the
// ~40 call sites, and there is deliberately no no-op DockerService type.
//
//   - Go allows calling a pointer-receiver method on a nil pointer; only the
//     dereference panics. A `if s == nil` guard at the top of each method
//     therefore gives the same "uniform, impossible to forget" behaviour a no-op
//     implementation would, without a 40-method interface and a second
//     implementation to keep in sync forever.
//   - It is also the only shape that survives the typed-nil-in-an-interface
//     trap: a nil *DockerService stored in an interface is a NON-nil interface
//     value, so consumer-side interfaces (handlers.stackDocker,
//     services.dockerStopper) that receive the concrete pointer from main.go
//     cannot detect the outage with `!= nil`, and a no-op type would never be
//     substituted in at those seams.
//   - Precedent: Ping already did exactly this.
//
// Handlers map this sentinel to 503 DOCKER_UNAVAILABLE via respondDockerErr.
var ErrDockerUnavailable = errors.New("docker daemon unreachable")

// dockerUnavailableReason is the operator-facing phrasing of ErrDockerUnavailable,
// used where the outage is reported as a reason string rather than an error:
// truth.ActionResult reasons and streamed error frames.
const dockerUnavailableReason = "Docker daemon unreachable: the server started without a usable Docker connection"

type DockerService struct {
	config *config.Config
	client *client.Client
	// updateClient, when non-nil, overrides the Docker API the container-update
	// apply paths call (see containerUpdateAPI and updateAPI in
	// docker_update.go). Production leaves it nil and gets client; unit tests
	// inject a fake so UpdateContainer and UpdateContainerStreaming can be
	// driven without a live daemon (agent-os-bx43).
	updateClient containerUpdateAPI
	// statusFn, when non-nil, overrides Status during lifecycle settling.
	// Production leaves it nil; unit tests inject a scripted snapshot
	// sequence so pollUntilSettled runs against real code.
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

// Ping reports whether the Docker daemon is reachable, honouring ctx's deadline.
//
// It is the readiness probe's dependency check. Ping is a single cheap
// round-trip, unlike the GetContainerList("") the old inline /health handler ran
// every 30 seconds.
//
// The nil receiver and nil client are handled rather than dereferenced: main
// leaves dockerService nil when the daemon was unreachable at startup, and a nil
// *DockerService stored in an interface is a non-nil interface value — calling
// through it would panic instead of reporting the outage it exists to report.
func (s *DockerService) Ping(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("docker client not initialized: %w", ErrDockerUnavailable)
	}
	if _, err := s.client.Ping(ctx); err != nil {
		return err
	}
	return nil
}

func (s *DockerService) Logs(stack models.Stack, tail int) (string, error) {
	if s == nil {
		return "", ErrDockerUnavailable
	}

	args := s.buildComposeArgs(stack, "logs", []string{"--tail", fmt.Sprintf("%d", tail), "--timestamps"})

	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommand("docker", args...)
	cmd.Dir = stack.Directory
	cmd.Env = dockerEnv()

	// STDOUT AND STDERR ARE READ SEPARATELY, and only stdout is returned
	// (agent-os-pc4o). This was cmd.CombinedOutput(), which points the child's
	// stdout and stderr at ONE pipe, so anything written to stderr by a command
	// that EXITS 0 arrived inside the body below. Same class as agent-os-vwi7
	// (git.go) and agent-os-sl9z (docker_lifecycle.go's `compose ps`).
	//
	// The body is NOT free text for a human to read, which is why
	// agent-os-vwi7's sweep dispositioned this site as a weaker sibling and was
	// wrong to. handlers/logs.go hands it straight to parseLogLines, which
	// splits on newlines and builds a LogLine{Container,Timestamp,Message} per
	// line via strings.SplitN(line, "|", 2) — returned as JSON and filtered on
	// Container. Three outcomes, all with exit code 0 throughout and no error
	// value anywhere on the path for any gate in this repository to catch:
	//
	//	a stderr line CONTAINING A PIPE becomes a log entry ATTRIBUTED TO A
	//	  CONTAINER THAT DOES NOT EXIST — logfmt and shell-pipeline advice
	//	  produce pipes routinely (msg="use compose ps | jq").
	//	a stderr write with NO TRAILING NEWLINE is glued onto the front of the
	//	  next real row. The joined line still holds that row's pipe, so it is
	//	  not skipped: an entry the container really printed is RE-KEYED under a
	//	  fabricated name and vanishes from its own ?container= filter.
	//	a stderr line with NO PIPE hits parseLogLine's `len(parts) < 2` guard
	//	  and is discarded, so merging it means it is neither shown nor logged.
	//
	// parseLogLine is deliberately left alone: its nil return on a pipe-less
	// line is pinned by handlers/logs_test.go's "line without pipe" case, the
	// same intentional skip-a-bad-line behaviour parseComposePSOutput has. The
	// STREAMING path for this feature never had the defect — handlers/logs.go's
	// buildLogsCmd is read with StdoutPipe() and leaves cmd.Stderr nil — which
	// is exactly what made the non-streaming half easy to clear by mistake.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Unwrapped, as before: handlers/logs.go logs this error and maps it through
	// respondDockerErr, so wrapping it here would change an operator-visible
	// string for no gain. cmd.Stderr is assigned either way, so the
	// *exec.ExitError's own .Stderr stays nil exactly as it did.
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// A zero-exit diagnostic is kept out of the parsed body and recorded here
	// instead. DEBUG rather than WARN, for the same reason as gitCommandWithCreds
	// and Status: the commonest cause is an ordinary plugin or wrapper that
	// prints, so a louder level would turn every healthy install into a standing
	// log line. The branch is skipped entirely when stderr is empty.
	if diag := strings.TrimSpace(stderr.String()); diag != "" {
		slog.Debug("docker compose logs wrote to stderr but exited 0; diagnostic kept out of the returned log body",
			"project", stack.ProjectName, "directory", stack.Directory, "stderr", trimOutput(diag))
	}

	return stdout.String(), nil
}

func (s *DockerService) GetContainerList(projectName string) ([]models.Container, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

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
	if s == nil {
		return nil, ErrDockerUnavailable
	}

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
			var statsJSON container.StatsResponse
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

func (s *DockerService) buildComposeArgs(stack models.Stack, subcommand string, extraArgs []string) []string {
	args := []string{"compose"}

	// os.IsNotExist is the ONLY stat answer that means "absent". Every other
	// error means "could not find out", and dropping --env-file on one of those
	// ASSERTS an absence we never established: the stack came up without the
	// operator's global environment, silently, with nothing logged
	// (agent-os-d5ff). It is the discriminator ScanAll already uses on this
	// very file (scanner.go:498-499, `hasGlobalEnv = !os.IsNotExist(err)`).
	//
	// On a fault we hand compose the configured path anyway and let the
	// component that can actually read the file decide. That is a refusal
	// rather than a shrug: `docker compose --env-file <unstattable path>` exits
	// 1 naming the cause — "stat …/global.env: not a directory" — so the
	// command stops without buildComposeArgs needing an error return it does
	// not have. It is also right in the one case a Capstan-side refusal would
	// get wrong: a stat fault that has cleared by the time compose runs.
	//
	// Measured on the compose that `docker compose version` reports as
	// "Docker Compose version v5.5.1", on both `config` and `ps`. Quoting the
	// command because the first version recorded here was wrong: it was the
	// ENGINE version from `docker version --format '{{.Server.Version}}'`
	// (26.1.5+dfsg1) mislabelled as compose's.
	//
	// Absence stays silent. `--env-file` on a merely missing path ALSO exits 1
	// ("couldn't find env file: …"), so passing it unconditionally would break
	// every healthy install that has no global.env.
	globalEnvPath := s.config.DataDir + "/global.env"
	if _, statErr := os.Stat(globalEnvPath); !os.IsNotExist(statErr) {
		if statErr != nil {
			slog.Error("Could not determine whether the global env file exists; passing it to compose rather than starting the stack without it",
				"file", globalEnvPath, "error", statErr)
		}
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

// ValidateName is the one exported method with no nil-receiver guard, and
// deliberately so: it validates a string and never touches the receiver, so
// reporting a Docker outage from it would be a lie.
func (s *DockerService) ValidateName(name string) error {
	if !dockerNameRe.MatchString(name) {
		return models.NewAppError(400, models.ErrValidation, "Invalid name format")
	}
	return nil
}

func calculateCPUPercent(stats *container.StatsResponse) float64 {
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

func calculateMemPercent(stats *container.StatsResponse) (float64, float64, float64) {
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

func calculateMemSwap(stats *container.StatsResponse) float64 {
	if stats.MemoryStats.Stats == nil {
		return 0
	}
	swapUsage, ok := stats.MemoryStats.Stats["swap"]
	if !ok {
		return 0
	}
	return float64(swapUsage)
}

func getPids(stats *container.StatsResponse) uint64 {
	return stats.PidsStats.Current
}

func calculateNetwork(stats *container.StatsResponse) (float64, float64) {
	var netRx, netTx uint64

	for _, network := range stats.Networks {
		netRx += network.RxBytes
		netTx += network.TxBytes
	}

	return float64(netRx), float64(netTx)
}

func calculateBlockIO(stats *container.StatsResponse) (float64, float64) {
	var read, write uint64

	for _, stat := range stats.BlkioStats.IoServiceBytesRecursive {
		switch stat.Op {
		case "read", "Read":
			read += stat.Value
		case "write", "Write":
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
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	containers, err := s.client.ContainerList(ctx, container.ListOptions{All: true, Size: true})
	if err != nil {
		return nil, err
	}

	result := make([]models.DashboardContainerInfo, 0, len(containers))

	// One ERROR per call, not per container — see the stackErr branch below.
	stackLookupFailed := false

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

		// agent-os-g482, revised by agent-os-yrgn. A failed read still DEFAULTS
		// rather than refuses: this is the dashboard poll, and a dashboard that
		// fails because one table is briefly unreadable is worse than one drawn
		// without stack associations.
		//
		// What changed is that it no longer defaults SILENTLY into a state the
		// consumer cannot tell from a real answer. StackID is no longer
		// display-only -- ContainersOverviewTab.tsx now routes on it, rendering a
		// compose container with no stack row as standalone -- and an empty
		// StackID has two causes. Carrying only the empty string would make an
		// unreadable stacks table indistinguishable from "genuinely not a
		// stack", which is precisely g482's P2 defect on a second path.
		//
		// The ERROR fires once per call, not once per container: a dashboard
		// silently missing every stack association is indistinguishable from a
		// host with no stacks, but this is a poll.
		assoc, stackErr := resolveDashboardStackAssociation(db, projectName)
		if stackErr != nil && !stackLookupFailed {
			stackLookupFailed = true
			slog.Error("Cannot resolve compose stacks for the container list; containers are reported with no stack id",
				"project", projectName, "cause", stackErr)
		}

		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		restartCount := 0

		health := ""

		info := models.DashboardContainerInfo{
			ID:                c.ID,
			Name:              name,
			Image:             c.Image,
			State:             c.State,
			Status:            c.Status,
			Health:            health,
			Ports:             ports,
			StackID:           assoc.StackID,
			StackLookupFailed: assoc.LookupFailed,
			ProjectName:       projectName,
			RestartCount:      restartCount,
			Created:           time.Unix(c.Created, 0),
			DiskSize:          c.SizeRw,
			ImageSize:         c.SizeRootFs,
		}

		if c.State == "running" {
			inspect, err := s.client.ContainerInspect(ctx, c.ID) //geterrors:ignore best-effort enrichment of one row of a container list: a failed inspect leaves the optional fields zero rather than failing the whole list
			if err == nil {
				if inspect.State != nil && inspect.State.StartedAt != "" {
					if t, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil { //geterrors:ignore a daemon-reported StartedAt in an unexpected layout leaves info.StartedAt at Go's ZERO TIME, and DashboardContainerInfo.StartedAt (models/models.go:169) carries no omitempty -- so it serialises as "0001-01-01T00:00:00Z", a zero timestamp on the wire and NOT an absent field. Kept because failing an entire container list over one unparseable field is worse; what a consumer displays is deliberately not asserted here, having not been measured
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
	if s == nil {
		return 0, ErrDockerUnavailable
	}

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
	if s == nil {
		return nil, ErrDockerUnavailable
	}

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
	if s == nil {
		return nil, ErrDockerUnavailable
	}

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

// dashboardStackAssociation is how one container's compose project resolved
// against the stacks table for the dashboard.
//
// LookupFailed is not a redundant encoding of an empty StackID. An empty StackID
// has two causes and they are different states: the project is genuinely not a
// stack -- an ordinary not-found, which resolveUpdateStrategy already treats as
// standalone -- or the stacks table could not be READ, which that function
// refuses outright rather than guessing (agent-os-g482: answering a failed read
// with "not a compose stack" recreated compose-managed containers down the
// standalone apply path). The dashboard defaults instead of refusing because it
// is only a read, but it must still report WHICH of the two happened, or its
// consumer cannot avoid reinstating the same defect.
type dashboardStackAssociation struct {
	StackID      string
	LookupFailed bool
}

// resolveDashboardStackAssociation is the per-container decision
// GetAllContainersWithDetails makes, extracted so it can be tested without a
// Docker daemon: DockerService.client is a concrete *client.Client, not an
// interface, so the loop that calls this cannot be driven from a unit test at
// all. Same constraint, and deliberately the same shape, as
// resolveUpdateStrategy in docker_update.go.
//
// It returns the error as well as the flag so the caller keeps its existing
// once-per-call log line; the flag is what reaches the wire.
func resolveDashboardStackAssociation(db DashboardDB, projectName string) (dashboardStackAssociation, error) {
	stack, err := lookupStackByProject(db, projectName)
	switch {
	case err != nil:
		return dashboardStackAssociation{LookupFailed: true}, err
	case stack != nil:
		return dashboardStackAssociation{StackID: stack.ID}, nil
	}
	return dashboardStackAssociation{}, nil
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
//
// No nil-receiver guard of its own: its first statement delegates to
// GetAllContainersWithDetails, which returns ErrDockerUnavailable for a nil
// receiver.
func (s *DockerService) GetStackStatuses(ctx context.Context, db DashboardDB) (map[string]LiveStatus, error) {
	containers, err := s.GetAllContainersWithDetails(ctx, db)
	if err != nil {
		return nil, err
	}
	return BuildStackStatuses(containers), nil
}

func (s *DockerService) StartContainer(ctx context.Context, containerID string) error {
	if s == nil {
		return ErrDockerUnavailable
	}

	return s.client.ContainerStart(ctx, containerID, container.StartOptions{})
}

func (s *DockerService) StopContainer(ctx context.Context, containerID string) error {
	if s == nil {
		return ErrDockerUnavailable
	}

	return s.client.ContainerStop(ctx, containerID, container.StopOptions{})
}

func (s *DockerService) InspectContainer(ctx context.Context, containerID string) (container.InspectResponse, error) {
	if s == nil {
		return container.InspectResponse{}, ErrDockerUnavailable
	}

	return s.client.ContainerInspect(ctx, containerID)
}

func (s *DockerService) RestartContainer(ctx context.Context, containerID string) error {
	if s == nil {
		return ErrDockerUnavailable
	}

	return s.client.ContainerRestart(ctx, containerID, container.StopOptions{})
}
