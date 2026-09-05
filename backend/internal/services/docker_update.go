package services

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
// composeUpdateStrategy is how an update is applied to one container: through
// the compose project that owns it, through the standalone recreate path, or —
// when the stacks table cannot be read — not at all.
type composeUpdateStrategy int

const (
	// updateViaStandalone recreates the container directly. Correct for a
	// container carrying no compose labels, and for one whose compose project
	// name is genuinely absent from the stacks table.
	updateViaStandalone composeUpdateStrategy = iota
	// updateViaCompose applies the update through the owning stack's compose file.
	updateViaCompose
	// updateRefused means the stacks table could not be read, so which of the two
	// above is correct is UNKNOWN. Both apply paths write, and the standalone one
	// recreates the container, so guessing here recreates a compose-managed
	// container with the wrong strategy on the strength of a read that failed
	// (agent-os-g482).
	updateRefused
)

// lookupStackByProject resolves the stack that owns a compose project name,
// discriminating "this project name is not in the stacks table" from "the
// stacks table could not be read".
//
// database.DB.GetStackByProjectName returns the bare Scan error
// (database/stacks.go:127-138), so an absent row arrives as sql.ErrNoRows.
// Every caller here used to test only `err == nil`, which cannot tell that
// apart from a closed or locked database, and so answered a DB fault with "not
// a compose stack" (agent-os-g482 — the same softening as agent-os-l42o).
//
// Absence is not a fault: a missing row returns (nil, nil), so the caller's
// pre-existing not-found behaviour is preserved byte-for-byte. A nil db or an
// empty projectName likewise return (nil, nil), reproducing the guards this
// replaced.
func lookupStackByProject(db DashboardDB, projectName string) (*models.Stack, error) {
	if db == nil || projectName == "" {
		return nil, nil
	}
	stack, err := db.GetStackByProjectName(projectName)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("reading stack for compose project %q: %w", projectName, err)
	}
	return stack, nil
}

// resolveUpdateStrategy decides how UpdateContainer and UpdateContainerStreaming
// apply an update to one container, given the compose labels read off it.
//
// A container is updated through compose only when it carries BOTH labels, a db
// is available, and that project name resolves to a stack. Anything genuinely
// absent is standalone. A stacks table that cannot be READ is neither: it
// returns updateRefused with the cause, because the caller is about to write.
func resolveUpdateStrategy(db DashboardDB, projectName, serviceName string) (composeUpdateStrategy, *models.Stack, error) {
	if db == nil || projectName == "" || serviceName == "" {
		return updateViaStandalone, nil, nil
	}
	stack, err := lookupStackByProject(db, projectName)
	if err != nil {
		return updateRefused, nil, err
	}
	if stack == nil {
		return updateViaStandalone, nil, nil
	}
	return updateViaCompose, stack, nil
}

// refusedUpdateReason is the ActionResult reason both apply paths carry when
// resolveUpdateStrategy refuses. A constant so the two paths cannot drift; it is
// unexported and no caller matches on it, so it is a shared literal, not an API.
const refusedUpdateReason = "cannot determine update strategy: the stacks table could not be read"

// logRefusedUpdate emits the single ERROR line that accompanies an
// updateRefused. It is shared by UpdateContainer and UpdateContainerStreaming so
// the two write paths cannot drift, and — the reason it is a function at all —
// so the line itself is reachable from a unit test. Neither apply path is: both
// call s.client.ContainerInspect before reaching this branch, and
// DockerService.client is a concrete *client.Client (docker.go:54), not an
// interface, so they cannot run without a live daemon. Their end-to-end coverage
// is internal/integrationtest, behind the `integration` build tag, which
// `go test ./...` does not run.
//
// cause is the wrapped driver error from lookupStackByProject. Nothing here is
// decrypted, so unlike backup_config.go there is no reason to withhold it.
func logRefusedUpdate(containerID, projectName, serviceName string, cause error) {
	slog.Error("Refusing container update: cannot determine whether this container is compose-managed",
		"container", containerID, "project", projectName, "service", serviceName, "cause", cause)
}

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

	// One ERROR per call, not per container — see the stackErr branch below.
	stackLookupFailed := false

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
		stack, stackErr := lookupStackByProject(db, projectName)
		switch {
		case stackErr != nil:
			// agent-os-g482. Not a write, but not a display either: this StackID
			// is what scheduler.go:821-822 looks up in stackPolicies to decide
			// whether a stack-scoped auto-update policy applies. An unreadable
			// stacks table leaves it empty, so the scheduler skips the container
			// (scheduler.go:824-828) — fail-closed, but silently, which is what
			// this line exists to stop. Logged once per call, not once per
			// container: a dead database faults every iteration of this loop.
			if !stackLookupFailed {
				stackLookupFailed = true
				slog.Error("Cannot resolve compose stacks while checking for updates; affected containers are reported with no stack id and the scheduler will skip their stack-scoped auto-update policies",
					"project", projectName, "cause", stackErr)
			}
		case stack != nil:
			stackID = stack.ID
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
	// Set by the standalone paths, which recreate the container and so change its
	// id. Empty after a compose apply, where the id is re-resolved by label below.
	recreatedID := ""
	strategy, stack, stackErr := resolveUpdateStrategy(db, projectName, serviceName)
	switch strategy {
	case updateRefused:
		// agent-os-g482. The stacks table is unreadable, so compose-managed and
		// standalone are indistinguishable. The old code fell through to the
		// standalone path, which RECREATES the container — the wrong strategy for
		// a compose-managed one, applied silently. Refuse instead: an update not
		// applied is recoverable, a container recreated outside its compose
		// project is not.
		logRefusedUpdate(containerID, projectName, serviceName, stackErr)
		return models.UpdateResult{DurationMs: time.Since(start).Milliseconds()},
			truth.Failed(refusedUpdateReason, stackErr)
	case updateViaCompose:
		applyErr = s.updateComposeContainer(ctx, *stack, serviceName, wasRunning)
	default:
		recreatedID, applyErr = s.updateStandaloneContainer(ctx, inspect, wasRunning)
	}

	durationMs := time.Since(start).Milliseconds()

	if applyErr != nil {
		return models.UpdateResult{DurationMs: durationMs},
			truth.Failed("update apply failed", applyErr)
	}

	// Verify image advancement against the container that exists NOW, which is not
	// the one we were handed: both apply paths replace it.
	//
	// The standalone path reports the id it created (agent-os-ekmk). Deriving it
	// here instead used to leave newContainerID as the pre-update id whenever the
	// container carried no compose labels — that container had just been removed,
	// so the inspect below failed and a successful update was reported as failed.
	//
	// Compose recreates under its own naming, so there the id is re-resolved by
	// label. The compose branch is checked first because a compose container whose
	// stack could not be resolved takes the standalone APPLY path while still being
	// findable by label.
	newContainerID := containerID
	if projectName != "" && serviceName != "" {
		newContainerID = s.findComposeContainer(ctx, projectName, serviceName, containerID)
	} else if recreatedID != "" {
		newContainerID = recreatedID
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
	pullCmd := execCommandContext(ctx, "docker", pullArgs...)
	pullCmd.Dir = stack.Directory
	pullCmd.Env = dockerEnv()
	if output, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("compose pull failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	upArgs := s.buildComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	upCmd := execCommandContext(ctx, "docker", upArgs...)
	upCmd.Dir = stack.Directory
	upCmd.Env = dockerEnv()
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
// It returns the id of the container it created. That return value is load-bearing:
// the recreate gives the container a NEW id, and the caller's post-update
// verification has to inspect that one. Before agent-os-ekmk this returned only an
// error, the caller kept inspecting the pre-update id, and every successful
// standalone update was reported to the user as failed.
func (s *DockerService) updateStandaloneContainer(ctx context.Context, inspect container.InspectResponse, wasRunning bool) (string, error) {
	imageRef := inspect.Config.Image

	reader, err := s.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return "", fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()

	// Decode the stream — surfaces errorDetail/error messages (finding #3 fix).
	if pullErr := truth.DrainPullStream(reader, nil); pullErr != nil {
		return "", fmt.Errorf("pulling image %s: %w", imageRef, pullErr)
	}

	if wasRunning {
		if err := s.client.ContainerStop(ctx, inspect.ID, container.StopOptions{}); err != nil {
			return "", fmt.Errorf("stopping container: %w", err)
		}
	}

	if err := s.client.ContainerRemove(ctx, inspect.ID, container.RemoveOptions{}); err != nil {
		return "", fmt.Errorf("removing container: %w", err)
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
		return "", fmt.Errorf("creating container: %w", err)
	}

	if wasRunning {
		if err := s.client.ContainerStart(ctx, newContainer.ID, container.StartOptions{}); err != nil {
			return newContainer.ID, fmt.Errorf("starting container: %w", err)
		}
	}

	return newContainer.ID, nil
}

// streamComposeCmd runs a docker compose command and streams each output line
// via emit. Both stdout and stderr are merged. Returns the combined output for
// error messages.
func streamComposeCmd(ctx context.Context, args []string, dir string, stream LogLineStream, emit func(LogLine)) error {
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd := execCommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	cmd.Env = dockerEnv()

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
	// See UpdateContainer: only the standalone paths recreate the container and so
	// know its new id (agent-os-ekmk).
	recreatedID := ""
	strategy, stack, stackErr := resolveUpdateStrategy(db, projectName, serviceName)
	switch strategy {
	case updateRefused:
		// See UpdateContainer: the same refusal, for the same reason
		// (agent-os-g482). This is the site the bead was filed on — its receiver
		// was sErr, not err, which is why the family's usual sweep missed it.
		logRefusedUpdate(containerID, projectName, serviceName, stackErr)
		return models.UpdateResult{OldDigest: oldImageID, DurationMs: time.Since(start).Milliseconds()},
			truth.Failed(refusedUpdateReason, stackErr)
	case updateViaCompose:
		applyErr = s.updateComposeContainerStreaming(ctx, *stack, serviceName, wasRunning, emit, setStatus)
	default:
		recreatedID, applyErr = s.updateStandaloneContainerStreaming(ctx, inspect, wasRunning, emit, setStatus)
	}

	durationMs := time.Since(start).Milliseconds()

	if applyErr != nil {
		return models.UpdateResult{OldDigest: oldImageID, DurationMs: durationMs},
			truth.Failed("update apply failed", applyErr)
	}

	// Verify image advancement against the container that exists now — see the
	// same block in UpdateContainer for why the standalone id has to be carried
	// out of the apply rather than re-derived (agent-os-ekmk).
	newContainerID := containerID
	if projectName != "" && serviceName != "" {
		newContainerID = s.findComposeContainer(ctx, projectName, serviceName, containerID)
	} else if recreatedID != "" {
		newContainerID = recreatedID
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

// It returns the id of the recreated container, for the same reason as
// updateStandaloneContainer above (agent-os-ekmk).
//
// updateStandaloneContainerStreaming pulls the image via the Docker SDK and
// decodes the pull stream via truth.DrainPullStream (finding #3 fix), then
// recreates the container.
func (s *DockerService) updateStandaloneContainerStreaming(
	ctx context.Context,
	inspect container.InspectResponse,
	wasRunning bool,
	emit func(LogLine),
	setStatus func(Status),
) (string, error) {
	imageRef := inspect.Config.Image

	setStatus(StatusPulling)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Pulling " + imageRef, Stream: StreamStatus})

	reader, err := s.client.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return "", fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()

	// Decode the stream: surface pull progress to the WS log AND detect
	// errorDetail/error messages (finding #3 fix: replaces io.Copy(io.Discard)).
	if pullErr := truth.DrainPullStream(reader, func(line string) {
		emit(LogLine{Ts: time.Now().UTC(), Text: line, Stream: StreamStdout})
	}); pullErr != nil {
		return "", fmt.Errorf("pulling image %s: %w", imageRef, pullErr)
	}

	emit(LogLine{Ts: time.Now().UTC(), Text: "Pull complete", Stream: StreamStdout})

	setStatus(StatusRecreating)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Recreating " + strings.TrimPrefix(inspect.Name, "/"), Stream: StreamStatus})

	if wasRunning {
		if err := s.client.ContainerStop(ctx, inspect.ID, container.StopOptions{}); err != nil {
			return "", fmt.Errorf("stopping container: %w", err)
		}
		emit(LogLine{Ts: time.Now().UTC(), Text: "Container stopped", Stream: StreamStdout})
	}

	if err := s.client.ContainerRemove(ctx, inspect.ID, container.RemoveOptions{}); err != nil {
		return "", fmt.Errorf("removing container: %w", err)
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
		return "", fmt.Errorf("creating container: %w", err)
	}
	emit(LogLine{Ts: time.Now().UTC(), Text: "New container created", Stream: StreamStdout})

	if wasRunning {
		if err := s.client.ContainerStart(ctx, newContainer.ID, container.StartOptions{}); err != nil {
			return newContainer.ID, fmt.Errorf("starting container: %w", err)
		}
		emit(LogLine{Ts: time.Now().UTC(), Text: "Container started", Stream: StreamStdout})
	}

	return newContainer.ID, nil
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
