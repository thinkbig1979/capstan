package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	dockernet "github.com/docker/docker/api/types/network"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// updateCandidate pairs a container's pre-built update metadata with the
// imageRef and local repo digest that were resolved from the SAME repository
// entry so that selectUpdates compares apples to apples.
type updateCandidate struct {
	info        models.ContainerUpdateInfo
	localDigest string // truth.LocalRepoDigest(imageRef, repoDigests)
}

// selectUpdates returns the candidates whose remote index digest differs from
// the local one. A ref absent from remoteDigests (its fetch failed) is skipped
// rather than reported, so a registry hiccup never produces a phantom update.
func selectUpdates(candidates []updateCandidate, remoteDigests map[string]string) []models.ContainerUpdateInfo {
	var result []models.ContainerUpdateInfo
	for _, c := range candidates {
		remote, ok := remoteDigests[c.info.ImageRef]
		if !ok || remote == "" {
			continue
		}
		if remote != c.localDigest {
			c.info.RemoteDigest = remote
			result = append(result, c.info)
		}
	}
	return result
}

// CheckForUpdates lists all containers, resolves each one's local repo digest
// via truth.LocalRepoDigest (repo-matched, not [0]), fetches the remote index
// digest via truth.RemoteRegistryDigest (version-independent, fixes the
// buildx --format phantom-update bug: audit finding #1), and returns only
// containers where local != remote.
//
// If a candidate's remote fetch errors or the local digest cannot be resolved
// for the imageRef, the candidate is skipped — never emitted as a phantom update.
func (s *DockerService) CheckForUpdates(ctx context.Context, db DashboardDB) ([]models.ContainerUpdateInfo, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	containers, err := s.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	uniqueRefs := make(map[string]struct{})
	var candidates []updateCandidate

	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}

		imgInspect, err := s.client.ImageInspect(ctx, c.ImageID)
		if err != nil {
			continue
		}

		// Pick a symbolic tag — skip <none> and bare digest refs.
		var imageRef string
		for _, tag := range imgInspect.RepoTags {
			if tag != "" && tag != "<none>:<none>" && tag != "<none>" && !strings.HasPrefix(tag, "@sha256:") {
				imageRef = tag
				break
			}
		}
		if imageRef == "" {
			continue
		}

		// Resolve local digest repo-matched to imageRef (finding #1 fix):
		// truth.LocalRepoDigest picks the RepoDigest whose repository component
		// matches imageRef, not blindly [0].
		localDigest, ok := truth.LocalRepoDigest(imageRef, imgInspect.RepoDigests)
		if !ok {
			// No matching local digest — cannot do a meaningful comparison; skip.
			continue
		}

		uniqueRefs[imageRef] = struct{}{}

		var stackID string
		projectName := c.Labels["com.docker.compose.project"]
		serviceName := c.Labels["com.docker.compose.service"]
		if db != nil && projectName != "" {
			stack, err := db.GetStackByProjectName(projectName)
			if err == nil && stack != nil {
				stackID = stack.ID
			}
		}

		candidates = append(candidates, updateCandidate{
			localDigest: localDigest,
			info: models.ContainerUpdateInfo{
				ContainerID:   c.ID,
				ContainerName: name,
				Image:         imageRef,
				ImageRef:      imageRef,
				State:         c.State,
				StackID:       stackID,
				ProjectName:   projectName,
				ServiceName:   serviceName,
				IsCompose:     projectName != "",
				LocalDigest:   localDigest,
			},
		})
	}

	// Fetch each unique tag's remote index digest concurrently via
	// truth.RemoteRegistryDigest, which is version-independent (hashes --raw
	// bytes; falls back to parsing the Digest: line). A failed fetch leaves
	// the ref out of the map so selectUpdates skips it gracefully.
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	remoteDigests := make(map[string]string)

	for ref := range uniqueRefs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			digest, err := truth.RemoteRegistryDigest(fetchCtx, ref)
			if err != nil || digest == "" {
				slog.Debug("Failed to fetch remote image digest", "image", ref, "error", err)
				return
			}

			mu.Lock()
			remoteDigests[ref] = digest
			mu.Unlock()
		}(ref)
	}
	wg.Wait()

	return selectUpdates(candidates, remoteDigests), nil
}

// UpdateContainer performs a non-streaming update of a single container and
// returns a truth.ActionResult:
//   - success   → the running image digest actually advanced (finding #2 fix)
//   - no_change → pull+recreate succeeded but the image is unchanged
//   - failed    → any error during pull, recreate, or post-verify
//
// Pull stream errors (auth failures, manifest-unknown) are decoded via
// truth.DrainPullStream and surface as failed, not success (finding #3 fix).
func (s *DockerService) UpdateContainer(ctx context.Context, containerID string, db DashboardDB) (models.UpdateResult, truth.ActionResult) {
	if s == nil {
		return models.UpdateResult{}, truth.Failed(dockerUnavailableReason, ErrDockerUnavailable)
	}

	// Capture the pre-update image ID so we can verify advancement afterward.
	imageRef, _, oldImageID, err := truth.ResolveContainerImage(ctx, s.client, containerID)
	if err != nil {
		return models.UpdateResult{}, truth.Failed("could not inspect container before update", err)
	}

	start := time.Now()

	inspect, err := s.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return models.UpdateResult{}, truth.Failed("could not inspect container", err)
	}

	wasRunning := inspect.State != nil && inspect.State.Running
	projectName := ""
	serviceName := ""
	if inspect.Config != nil && inspect.Config.Labels != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
		serviceName = inspect.Config.Labels["com.docker.compose.service"]
	}

	var applyErr error
	if projectName != "" && serviceName != "" && db != nil {
		stack, err := db.GetStackByProjectName(projectName)
		if err == nil && stack != nil {
			applyErr = s.updateComposeContainer(ctx, *stack, serviceName, wasRunning)
		} else {
			applyErr = s.updateStandaloneContainer(ctx, inspect, wasRunning)
		}
	} else {
		applyErr = s.updateStandaloneContainer(ctx, inspect, wasRunning)
	}

	durationMs := time.Since(start).Milliseconds()

	if applyErr != nil {
		return models.UpdateResult{DurationMs: durationMs},
			truth.Failed("update apply failed", applyErr)
	}

	// Verify image advancement: find the new container ID after compose recreate
	// (compose may give it a new ID) then call ContainerImageAdvanced.
	newContainerID := containerID
	if projectName != "" && serviceName != "" {
		newContainerID = s.findComposeContainer(ctx, projectName, serviceName, containerID)
	}

	advanced, newImageID, err := truth.ContainerImageAdvanced(ctx, s.client, newContainerID, oldImageID)
	if err != nil {
		// Post-verify failed — we can't confirm the outcome. Report failed.
		return models.UpdateResult{OldDigest: oldImageID, DurationMs: durationMs},
			truth.Failed("post-update container inspect failed", err)
	}

	result := models.UpdateResult{
		OldDigest:  oldImageID,
		NewDigest:  newImageID,
		DurationMs: durationMs,
	}

	if advanced {
		// Resolve new digest for details (best-effort).
		newImg, imgErr := s.client.ImageInspect(ctx, newImageID)
		newDigestStr := ""
		if imgErr == nil {
			newDigestStr, _ = truth.LocalRepoDigest(imageRef, newImg.RepoDigests)
		}
		shortID := newImageID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		return result, truth.Success(
			"Updated to "+shortID,
			truth.KV("newDigest", newDigestStr),
			truth.KV("oldImageID", oldImageID),
			truth.KV("newImageID", newImageID),
		)
	}

	return result, truth.NoChange("Already up to date")
}

// findComposeContainer looks for a running container with the given
// compose project+service labels and returns its ID. Falls back to
// originalID if not found.
func (s *DockerService) findComposeContainer(ctx context.Context, projectName, serviceName, fallbackID string) string {
	fa := filters.NewArgs()
	fa.Add("label", "com.docker.compose.project="+projectName)
	fa.Add("label", "com.docker.compose.service="+serviceName)
	containers, err := s.client.ContainerList(ctx, container.ListOptions{All: true, Filters: fa})
	if err != nil || len(containers) == 0 {
		return fallbackID
	}
	return containers[0].ID
}

func (s *DockerService) updateComposeContainer(ctx context.Context, stack models.Stack, serviceName string, wasRunning bool) error {
	pullArgs := s.buildComposeArgs(stack, "pull", []string{"--", serviceName})
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	pullCmd := exec.CommandContext(ctx, "docker", pullArgs...)
	pullCmd.Dir = stack.Directory
	if output, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compose pull failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	upArgs := s.buildComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
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

// updateStandaloneContainer pulls the image, decoding the stream via
// truth.DrainPullStream so that auth/manifest errors are surfaced (finding #3).
func (s *DockerService) updateStandaloneContainer(ctx context.Context, inspect container.InspectResponse, wasRunning bool) error {
	imageRef := inspect.Config.Image

	reader, err := s.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()

	// Decode the stream — surfaces errorDetail/error messages (finding #3 fix).
	if pullErr := truth.DrainPullStream(reader, nil); pullErr != nil {
		return fmt.Errorf("pulling image %s: %w", imageRef, pullErr)
	}

	if wasRunning {
		if err := s.client.ContainerStop(ctx, inspect.ID, container.StopOptions{}); err != nil {
			return fmt.Errorf("stopping container: %w", err)
		}
	}

	if err := s.client.ContainerRemove(ctx, inspect.ID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}

	name := strings.TrimPrefix(inspect.Name, "/")

	var netConfig *dockernet.NetworkingConfig
	if inspect.NetworkSettings != nil {
		netConfig = &dockernet.NetworkingConfig{
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

// streamComposeCmd runs a docker compose command and streams each output line
// via emit. Both stdout and stderr are merged. Returns the combined output for
// error messages.
func streamComposeCmd(ctx context.Context, args []string, dir string, stream LogLineStream, emit func(LogLine)) error {
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	var wg sync.WaitGroup
	scanPipe := func(r io.Reader, s LogLineStream) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(r)
			for scanner.Scan() {
				text := scanner.Text()
				emit(LogLine{Ts: time.Now().UTC(), Text: text, Stream: s})
			}
		}()
	}
	scanPipe(stdout, StreamStdout)
	scanPipe(stderr, StreamStderr)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("command failed: %w", err)
	}
	return nil
}

// UpdateContainerStreaming performs a streaming update of a single container,
// calling emit for each output line and setStatus at each lifecycle phase.
// Returns the UpdateResult and a truth.ActionResult (finding #2 fix: success
// only when the image digest advanced).
//
// Pull stream errors on the standalone path are decoded via DrainPullStream
// (finding #3 fix).
func (s *DockerService) UpdateContainerStreaming(
	ctx context.Context,
	containerID string,
	db DashboardDB,
	emit func(LogLine),
	setStatus func(Status),
) (models.UpdateResult, truth.ActionResult) {
	if s == nil {
		return models.UpdateResult{}, truth.Failed(dockerUnavailableReason, ErrDockerUnavailable)
	}

	// Capture the pre-update image ID.
	imageRef, _, oldImageID, err := truth.ResolveContainerImage(ctx, s.client, containerID)
	if err != nil {
		return models.UpdateResult{}, truth.Failed("could not inspect container before update", err)
	}

	start := time.Now()

	inspect, err := s.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return models.UpdateResult{}, truth.Failed("could not inspect container", err)
	}

	wasRunning := inspect.State != nil && inspect.State.Running
	projectName := ""
	serviceName := ""
	if inspect.Config != nil && inspect.Config.Labels != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
		serviceName = inspect.Config.Labels["com.docker.compose.service"]
	}

	var applyErr error
	if projectName != "" && serviceName != "" && db != nil {
		stack, sErr := db.GetStackByProjectName(projectName)
		if sErr == nil && stack != nil {
			applyErr = s.updateComposeContainerStreaming(ctx, *stack, serviceName, wasRunning, emit, setStatus)
		} else {
			applyErr = s.updateStandaloneContainerStreaming(ctx, inspect, wasRunning, emit, setStatus)
		}
	} else {
		applyErr = s.updateStandaloneContainerStreaming(ctx, inspect, wasRunning, emit, setStatus)
	}

	durationMs := time.Since(start).Milliseconds()

	if applyErr != nil {
		return models.UpdateResult{OldDigest: oldImageID, DurationMs: durationMs},
			truth.Failed("update apply failed", applyErr)
	}

	// Verify image advancement.
	newContainerID := containerID
	if projectName != "" && serviceName != "" {
		newContainerID = s.findComposeContainer(ctx, projectName, serviceName, containerID)
	}

	advanced, newImageID, err := truth.ContainerImageAdvanced(ctx, s.client, newContainerID, oldImageID)
	if err != nil {
		return models.UpdateResult{OldDigest: oldImageID, DurationMs: durationMs},
			truth.Failed("post-update container inspect failed", err)
	}

	result := models.UpdateResult{
		OldDigest:  oldImageID,
		NewDigest:  newImageID,
		DurationMs: durationMs,
	}

	if advanced {
		newImg, imgErr := s.client.ImageInspect(ctx, newImageID)
		newDigestStr := ""
		if imgErr == nil {
			newDigestStr, _ = truth.LocalRepoDigest(imageRef, newImg.RepoDigests)
		}
		shortID := newImageID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		return result, truth.Success(
			"Updated to "+shortID,
			truth.KV("newDigest", newDigestStr),
			truth.KV("oldImageID", oldImageID),
			truth.KV("newImageID", newImageID),
		)
	}

	return result, truth.NoChange("Already up to date")
}

func (s *DockerService) updateComposeContainerStreaming(
	ctx context.Context,
	stack models.Stack,
	serviceName string,
	wasRunning bool,
	emit func(LogLine),
	setStatus func(Status),
) error {
	imageRef := serviceName

	setStatus(StatusPulling)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Pulling " + imageRef, Stream: StreamStatus})

	pullArgs := s.buildComposeArgs(stack, "pull", []string{"--", serviceName})
	if err := streamComposeCmd(ctx, pullArgs, stack.Directory, StreamStdout, emit); err != nil {
		return fmt.Errorf("compose pull failed: %w", err)
	}

	setStatus(StatusRecreating)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Recreating " + serviceName, Stream: StreamStatus})

	upArgs := s.buildComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	if err := streamComposeCmd(ctx, upArgs, stack.Directory, StreamStdout, emit); err != nil {
		return fmt.Errorf("compose up failed: %w", err)
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

// updateStandaloneContainerStreaming pulls the image via the Docker SDK and
// decodes the pull stream via truth.DrainPullStream (finding #3 fix), then
// recreates the container.
func (s *DockerService) updateStandaloneContainerStreaming(
	ctx context.Context,
	inspect container.InspectResponse,
	wasRunning bool,
	emit func(LogLine),
	setStatus func(Status),
) error {
	imageRef := inspect.Config.Image

	setStatus(StatusPulling)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Pulling " + imageRef, Stream: StreamStatus})

	reader, err := s.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()

	// Decode the stream: surface pull progress to the WS log AND detect
	// errorDetail/error messages (finding #3 fix: replaces io.Copy(io.Discard)).
	if pullErr := truth.DrainPullStream(reader, func(line string) {
		emit(LogLine{Ts: time.Now().UTC(), Text: line, Stream: StreamStdout})
	}); pullErr != nil {
		return fmt.Errorf("pulling image %s: %w", imageRef, pullErr)
	}

	emit(LogLine{Ts: time.Now().UTC(), Text: "Pull complete", Stream: StreamStdout})

	setStatus(StatusRecreating)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Recreating " + strings.TrimPrefix(inspect.Name, "/"), Stream: StreamStatus})

	if wasRunning {
		if err := s.client.ContainerStop(ctx, inspect.ID, container.StopOptions{}); err != nil {
			return fmt.Errorf("stopping container: %w", err)
		}
		emit(LogLine{Ts: time.Now().UTC(), Text: "Container stopped", Stream: StreamStdout})
	}

	if err := s.client.ContainerRemove(ctx, inspect.ID, container.RemoveOptions{}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}
	emit(LogLine{Ts: time.Now().UTC(), Text: "Old container removed", Stream: StreamStdout})

	name := strings.TrimPrefix(inspect.Name, "/")

	var netConfig *dockernet.NetworkingConfig
	if inspect.NetworkSettings != nil {
		netConfig = &dockernet.NetworkingConfig{
			EndpointsConfig: inspect.NetworkSettings.Networks,
		}
	}

	newContainer, err := s.client.ContainerCreate(ctx, inspect.Config, inspect.HostConfig, netConfig, nil, name)
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	emit(LogLine{Ts: time.Now().UTC(), Text: "New container created", Stream: StreamStdout})

	if wasRunning {
		if err := s.client.ContainerStart(ctx, newContainer.ID, container.StartOptions{}); err != nil {
			return fmt.Errorf("starting container: %w", err)
		}
		emit(LogLine{Ts: time.Now().UTC(), Text: "Container started", Stream: StreamStdout})
	}

	return nil
}

// UpdateComposeServiceStreaming updates a single compose service for a stack,
// streaming output via emit and advancing status via setStatus. Returns a
// truth.ActionResult (success only on verified image advancement) and the old/
// new image IDs for history persistence.
func (s *DockerService) UpdateComposeServiceStreaming(
	ctx context.Context,
	stack models.Stack,
	serviceName string,
	emit func(LogLine),
	setStatus func(Status),
) (oldImageID, newImageID string, durationMs int64, ar truth.ActionResult) {
	if s == nil {
		ar = truth.Failed(dockerUnavailableReason, ErrDockerUnavailable)
		return
	}

	start := time.Now()

	// Find the pre-update container.
	filterArgs := filters.NewArgs()
	filterArgs.Add("label", "com.docker.compose.project="+stack.ProjectName)
	filterArgs.Add("label", "com.docker.compose.service="+serviceName)
	containers, listErr := s.client.ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
	if listErr != nil || len(containers) == 0 {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("could not find container for service "+serviceName, listErr)
		return
	}

	preContainerID := containers[0].ID
	// Resolve image ref and old image ID for post-apply comparison.
	imageRef, _, oldImgID, resolveErr := truth.ResolveContainerImage(ctx, s.client, preContainerID)
	if resolveErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("could not inspect container for "+serviceName, resolveErr)
		return
	}
	oldImageID = oldImgID

	setStatus(StatusPulling)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Pulling " + serviceName, Stream: StreamStatus})

	pullArgs := s.buildComposeArgs(stack, "pull", []string{"--", serviceName})
	if pullErr := streamComposeCmd(ctx, pullArgs, stack.Directory, StreamStdout, emit); pullErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("compose pull failed", pullErr)
		return
	}

	setStatus(StatusRecreating)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Recreating " + serviceName, Stream: StreamStatus})

	upArgs := s.buildComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	if upErr := streamComposeCmd(ctx, upArgs, stack.Directory, StreamStdout, emit); upErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("compose up failed", upErr)
		return
	}

	// Verify image advancement.
	postContainerID := s.findComposeContainer(ctx, stack.ProjectName, serviceName, preContainerID)
	advanced, newImgID, verifyErr := truth.ContainerImageAdvanced(ctx, s.client, postContainerID, oldImgID)
	newImageID = newImgID
	durationMs = time.Since(start).Milliseconds()

	if verifyErr != nil {
		ar = truth.Failed("post-update container inspect failed", verifyErr)
		return
	}

	if advanced {
		newImg, imgErr := s.client.ImageInspect(ctx, newImgID)
		newDigestStr := ""
		if imgErr == nil {
			newDigestStr, _ = truth.LocalRepoDigest(imageRef, newImg.RepoDigests)
		}
		shortID := newImgID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		ar = truth.Success(
			"Updated to "+shortID,
			truth.KV("newDigest", newDigestStr),
		)
		return
	}

	ar = truth.NoChange("Already up to date")
	return
}
