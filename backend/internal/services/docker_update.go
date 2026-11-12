package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	dockernet "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"

	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
)

// containerUpdateAPI is the subset of *client.Client that UpdateContainer and
// UpdateContainerStreaming reach, directly or through the apply and verify
// helpers they call. It exists so both apply paths can be driven from a unit
// test with a fake (agent-os-bx43): DockerService.client is a concrete
// *client.Client (docker.go:54), so before this the only executable coverage of
// either path was internal/integrationtest, behind the `integration` build tag,
// which `go test ./...` does not run — and a mutation that rewired only the
// call sites, leaving resolveUpdateStrategy correct, survived every test in
// this package.
//
// It is deliberately the eight methods those two paths touch, not the whole
// client surface: docker.go and docker_resources.go keep calling the concrete
// client, so this stays small enough to read in one screen.
//
// ImageInspect keeps client.ImageInspectOption's variadic parameter for two
// reasons: *client.Client satisfies this interface with its real signature and
// needs no adapter, and a containerUpdateAPI is then assignable to
// truth.ImageInspector (truth/image_inspect.go:22). That assignability is load
// bearing, not incidental — truth.ResolveContainerImage is handed the client at
// docker_update.go's first Docker touch on each apply path, BEFORE any
// ContainerInspect call of its own, so a seam that covered only the
// s.client.<method> sites would still hand the real client to truth and never
// reach the strategy switch.
type containerUpdateAPI interface {
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
	ImageInspect(ctx context.Context, imageID string, opts ...client.ImageInspectOption) (image.InspectResponse, error)
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error)
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *dockernet.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerRename(ctx context.Context, containerID, newContainerName string) error
	ContainerUpdate(ctx context.Context, containerID string, updateConfig container.UpdateConfig) (container.UpdateResponse, error)
}

// updateAPI returns the Docker API the container-update paths call: the fake a
// test injected, or the real client. Production leaves updateClient nil, so the
// shipped behaviour is the concrete client exactly as before.
func (s *DockerService) updateAPI() containerUpdateAPI {
	if s.updateClient != nil {
		return s.updateClient
	}
	return s.client
}

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
// (database/stacks.go:127-138), so an absent row arrives as errdefs.ErrNotFound.
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
	case errors.Is(err, errdefs.ErrNotFound):
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
// so the line itself is reachable from a unit test.
//
// Both apply paths are now reachable too: they call Docker through
// containerUpdateAPI, so a fake drives them to this branch without a daemon
// (agent-os-bx43, docker_update_apply_fake_test.go). Before that they could
// only run against internal/integrationtest, behind the `integration` build
// tag, which `go test ./...` does not run — which is why this function was
// carved out in the first place.
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

	containers, err := s.updateAPI().ContainerList(ctx, container.ListOptions{All: true})
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
		rowLookupFailed := false

		imgInspect, err := s.updateAPI().ImageInspect(ctx, c.ImageID)
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
			// Flagged on the row too (agent-os-zt0h), so the Updates tab does not
			// present "could not tell" as "not managed by Capstan".
			rowLookupFailed = true
			// agent-os-g482. Not a write, but not a display either: this StackID
			// is what `policy, hasPolicy = stackPolicies[update.StackID]`
			// (scheduler.go:821-822) looks up to decide whether a stack-scoped
			// auto-update policy applies. An unreadable stacks table leaves it
			// empty, so the scheduler skips the container at its
			// `if !hasPolicy { skipped++; continue }` (scheduler.go:826-828) —
			// fail-closed, but silently, which is what
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
				// Shown verbatim, never parsed, as on DashboardContainerInfo.
				StackLookupFailed:  rowLookupFailed,
				ComposeWorkingDir:  c.Labels["com.docker.compose.project.working_dir"],
				ComposeConfigFiles: c.Labels["com.docker.compose.project.config_files"],
				LocalDigest:        localDigest,
			},
		})
	}

	// Fetch each unique tag's remote index digest concurrently via
	// truth.RemoteRegistryDigest, which is version-independent (hashes --raw
	// bytes; falls back to parsing the Digest: line). A failed fetch leaves
	// the ref out of the map so selectUpdates skips it gracefully.
	remoteDigests := fetchRemoteDigests(ctx, uniqueRefs)

	return selectUpdates(candidates, remoteDigests), nil
}

// fetchRemoteDigests resolves each unique image ref's remote index digest,
// five at a time, and returns only the refs that resolved. A ref missing from
// the result is one selectUpdates must skip.
//
// It is a free function rather than inline in CheckForUpdates because it is the
// TIGHTEST TESTABLE UNIT for agent-os-rb6f: DockerService.client is a concrete
// *client.Client (docker.go:54), so CheckForUpdates itself cannot be driven
// without a live daemon, while truth.RemoteRegistryDigest is already a package
// var and can be swapped. Same reasoning as resolveUpdateStrategy under
// agent-os-g482.
//
// THE DISCRIMINATION IT EXISTS FOR (agent-os-rb6f). A fetch that FAILED and a
// registry that ANSWERED with no digest used to share one `err != nil || digest
// == ""` branch, logged at DEBUG, which is off in production. A persistent
// registry fault — an expired credential, a network policy change, a rate limit
// — was therefore indistinguishable from "you are up to date", which is the most
// reassuring possible message to show an operator whose containers are in fact
// drifting. The transient case self-heals on the next scan; the persistent one
// never announces itself.
//
// WHY THE FAULT REPORT IS AGGREGATED, and why this departs from the bead's
// literal "at least INFO per image": this runs once per ref per scan, and a
// locally built image has no registry to answer for it, so a per-image warning
// fires forever for every such image. An operator who learns to ignore a
// recurring warning has no signal left when a real registry fault appears. One
// line per scan, only when something actually failed, keeps the signal.
func fetchRemoteDigests(ctx context.Context, uniqueRefs map[string]struct{}) map[string]string {
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	remoteDigests := make(map[string]string)
	var unreadable []string

	for ref := range uniqueRefs {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			digest, err := truth.RemoteRegistryDigest(fetchCtx, ref)
			if err != nil {
				slog.Debug("Failed to fetch remote image digest", "image", ref, "error", err)
				mu.Lock()
				unreadable = append(unreadable, ref)
				mu.Unlock()
				return
			}
			if digest == "" {
				// The registry answered and reported no digest. Not a fault,
				// so it must not be counted as one.
				slog.Debug("Registry reported no remote digest for image", "image", ref)
				return
			}

			mu.Lock()
			remoteDigests[ref] = digest
			mu.Unlock()
		}(ref)
	}
	wg.Wait()

	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		slog.Warn("Could not determine whether an update exists for some images; their registry digest could not be fetched, so they are reported as having no update available",
			"unreadable", len(unreadable), "checked", len(uniqueRefs), "images", strings.Join(unreadable, ", "))
	}

	return remoteDigests
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
	imageRef, _, oldImageID, err := truth.ResolveContainerImage(ctx, s.updateAPI(), containerID)
	if err != nil {
		return models.UpdateResult{}, truth.Failed("could not inspect container before update", err)
	}

	start := time.Now()

	inspect, err := s.updateAPI().ContainerInspect(ctx, containerID)
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
		recreatedID, applyErr = s.recreateStandaloneContainer(ctx, inspect, wasRunning, func(LogLine) {}, func(Status) {})
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

	advanced, newImageID, err := truth.ContainerImageAdvanced(ctx, s.updateAPI(), newContainerID, oldImageID)
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
		newImg, imgErr := s.updateAPI().ImageInspect(ctx, newImageID) //geterrors:ignore best-effort, per the comment above: a failed ImageInspect leaves newDigestStr "" and it is attached as truth.KV("newDigest", "") to an ALREADY-VERIFIED success, so the update outcome is unaffected; what a consumer displays is deliberately not asserted here, having not been measured
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
	containers, err := s.updateAPI().ContainerList(ctx, container.ListOptions{All: true, Filters: fa})
	if err != nil {
		// A list fault is NOT "no such container": both used to return
		// fallbackID silently, and fallbackID is the PRE-update id, which
		// compose has just removed. ContainerImageAdvanced then fails on a
		// container that no longer exists and a successful update is reported
		// as "post-update container inspect failed" — a wrong cause, with the
		// real one nowhere in the logs (agent-os-rb6f).
		slog.Warn("Could not list compose containers to resolve the post-update container; falling back to the pre-update id, so this update's outcome may be reported wrongly",
			"project", projectName, "service", serviceName, "error", err)
		return fallbackID
	}
	if len(containers) == 0 {
		return fallbackID
	}
	return containers[0].ID
}

// updateComposeContainer's errors carry compose's output redacted but at full
// length (agent-os-zrm2): a long pull prints progress first and its real error
// last, so trimOutput's first-500-bytes cut would drop the diagnosis.
func (s *DockerService) updateComposeContainer(ctx context.Context, stack models.Stack, serviceName string, wasRunning bool) error {
	// Both built before anything runs, so a refusal leaves the service untouched.
	pullArgs, err := s.mutatingComposeArgs(stack, "pull", []string{"--", serviceName})
	if err != nil {
		return err
	}
	upArgs, err := s.mutatingComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	if err != nil {
		return err
	}
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	pullCmd, pullCtx, cancelPull := commandWithDeadline(ctx, s.composeTimeout(), "docker", pullArgs...)
	defer cancelPull()
	pullCmd.Dir = stack.Directory
	pullCmd.Env = dockerEnv()
	if output, err := pullCmd.CombinedOutput(); err != nil {
		err = timeoutError(pullCtx, err, "docker compose pull")
		return fmt.Errorf("compose pull failed: %s: %w", strings.TrimSpace(s.redactComposeOutputFor(stack, string(output))), err)
	}

	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	upCmd, upCtx, cancelUp := commandWithDeadline(ctx, s.composeTimeout(), "docker", upArgs...)
	defer cancelUp()
	upCmd.Dir = stack.Directory
	upCmd.Env = dockerEnv()
	if output, err := upCmd.CombinedOutput(); err != nil {
		err = timeoutError(upCtx, err, "docker compose up")
		return fmt.Errorf("compose up failed: %s: %w", strings.TrimSpace(s.redactComposeOutputFor(stack, string(output))), err)
	}

	if !wasRunning {
		time.Sleep(3 * time.Second)
		filterArgs := filters.NewArgs()
		filterArgs.Add("label", "com.docker.compose.project="+stack.ProjectName)
		filterArgs.Add("label", "com.docker.compose.service="+serviceName)
		filterArgs.Add("status", "running")

		containers, err := s.updateAPI().ContainerList(ctx, container.ListOptions{Filters: filterArgs})
		if err != nil {
			return fmt.Errorf("finding new container to stop: %w", err)
		}
		for _, c := range containers {
			if err := s.updateAPI().ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				slog.Error("Failed to stop recreated container", "id", c.ID, "error", err)
			}
		}
	}

	return nil
}

// standaloneRollbackTimeout bounds each cleanup step after the new container's
// create or start: the restore of the old container after a failure, and the
// removal of the old one after a success. Those steps run on a context
// detached from the update's, because the update's deadline ending is one of
// the ways the create or start fails, or can end just after the start
// succeeded, and a step on that ended context would fail at once: no container
// at all after a failure, a leftover old one after a success (agent-os-ohiy).
const standaloneRollbackTimeout = time.Minute

// oldContainerSuffix is appended to the previous container's name while its
// replacement is created. Nothing in code looks a leftover up by it; it is
// there so an operator who sees one can tell what it is.
const oldContainerSuffix = "-capstan-old-"

// withoutImageDefaults returns a copy of a container's Config with every value
// it inherited from its old image's defaults removed, so the new image's
// defaults apply at create (agent-os-ohiy). A container's inspected Config is
// the image's Config with the user's settings merged over it; recreating from
// it verbatim pinned the old image's Env, Cmd, Entrypoint, labels and the rest,
// so a new image version's changed defaults never took effect.
//
// The cost, accepted: a value the user set explicitly that happens to equal the
// old image's default cannot be told apart from the default, so it is removed
// and the new image's value applies to it too.
//
// Cmd is dropped only together with Entrypoint. Docker clears the image's Cmd
// when a container sets its own Entrypoint, so dropping a default Cmd beside a
// custom Entrypoint would start the container with no command.
//
// An exposed port the container publishes (a HostConfig.PortBindings key) is
// kept even when the old image declared it: the CLI writes a published port
// into ExposedPorts too, so it is the user's as much as the image's, and a new
// image that no longer EXPOSEs it must not lose the binding.
func withoutImageDefaults(cfg *container.Config, hostCfg *container.HostConfig, oldImage image.InspectResponse) *container.Config {
	out := *cfg
	img := oldImage.Config
	if img == nil {
		return &out
	}

	if len(cfg.Env) > 0 {
		out.Env = slices.DeleteFunc(slices.Clone(cfg.Env), func(e string) bool {
			return slices.Contains(img.Env, e)
		})
	}
	if slices.Equal(cfg.Entrypoint, img.Entrypoint) {
		out.Entrypoint = nil
		if slices.Equal(cfg.Cmd, img.Cmd) {
			out.Cmd = nil
		}
	}
	out.Labels = dropKeys(cfg.Labels, func(k string) bool {
		v, ok := img.Labels[k]
		return ok && v == cfg.Labels[k]
	})
	published := map[string]bool{}
	if hostCfg != nil {
		for p := range hostCfg.PortBindings {
			published[string(p)] = true
		}
	}
	out.ExposedPorts = dropKeys(cfg.ExposedPorts, func(p string) bool {
		_, ok := img.ExposedPorts[p]
		return ok && !published[p]
	})
	out.Volumes = dropKeys(cfg.Volumes, func(v string) bool {
		_, ok := img.Volumes[v]
		return ok
	})
	if cfg.WorkingDir == img.WorkingDir {
		out.WorkingDir = ""
	}
	if cfg.User == img.User {
		out.User = ""
	}
	if cfg.StopSignal == img.StopSignal {
		out.StopSignal = ""
	}
	if slices.Equal(cfg.Shell, img.Shell) {
		out.Shell = nil
	}
	if slices.Equal(cfg.OnBuild, img.OnBuild) {
		out.OnBuild = nil
	}
	if c, h := cfg.Healthcheck, img.Healthcheck; c != nil && h != nil &&
		slices.Equal(c.Test, h.Test) && c.Interval == h.Interval &&
		c.Timeout == h.Timeout && c.StartPeriod == h.StartPeriod &&
		c.StartInterval == h.StartInterval && c.Retries == h.Retries {
		out.Healthcheck = nil
	}
	return &out
}

// dropKeys returns a copy of m without the keys drop reports, or nil for nil.
func dropKeys[K ~string, V any](m map[K]V, drop func(string) bool) map[K]V {
	if m == nil {
		return nil
	}
	out := maps.Clone(m)
	maps.DeleteFunc(out, func(k K, _ V) bool { return drop(string(k)) })
	return out
}

// recreateStandaloneContainer updates a container no compose project manages:
// it pulls the image, decoding the stream via truth.DrainPullStream so that
// auth/manifest errors are surfaced (finding #3), and recreates the container
// from the new image. It returns the id of the container it created. That
// return value is load-bearing: the recreate gives the container a NEW id, and
// the caller's post-update verification has to inspect that one (agent-os-ekmk).
//
// One function for both apply paths; emit and setStatus may be no-ops. There
// used to be two copies, one streaming and one not (agent-os-ohiy).
//
// The old container is kept, renamed, until the new one has started, and is
// put back if the create or the start fails, so a failure at any step leaves a
// container. It used to be removed first, and a failed create left none.
//
// A standalone container has no stack, so no stack lock covers it, which leaves
// a window between the update's stop and its rename in which a container prune
// can remove the stopped container first (agent-os-qags.20). That not-found is
// forgiven only when this update's own stop succeeded: the stop proves the
// container existed a moment ago, and a prune only removes stopped containers.
// The recreate then runs from the captured inspect, with nothing to restore. For
// a container that was not running nothing proves it was not deleted on
// purpose, and recreating it would bring it back, so that not-found stays an
// error, as does any other rename failure.
func (s *DockerService) recreateStandaloneContainer(
	ctx context.Context,
	inspect container.InspectResponse,
	wasRunning bool,
	emit func(LogLine),
	setStatus func(Status),
) (string, error) {
	api := s.updateAPI()
	say := func(stream Stream, text string) {
		emit(LogLine{Ts: time.Now().UTC(), Text: text, Stream: stream})
	}
	imageRef := inspect.Config.Image
	name := strings.TrimPrefix(inspect.Name, "/")

	setStatus(StatusPulling)
	say(StreamStatus, "==> Pulling "+imageRef)

	reader, err := api.ImagePull(ctx, imageRef, image.PullOptions{})
	if err != nil {
		return "", fmt.Errorf("pulling image: %w", err)
	}
	defer reader.Close()

	if pullErr := truth.DrainPullStream(reader, func(line string) { say(StreamStdout, line) }); pullErr != nil {
		return "", fmt.Errorf("pulling image %s: %w", imageRef, pullErr)
	}
	say(StreamStdout, "Pull complete")

	// Read before anything is stopped: without the old image's defaults the
	// new container could only be created from the old ones, which is the
	// defect, so an unreadable old image fails the update with the old
	// container untouched.
	oldImage, err := api.ImageInspect(ctx, inspect.Image)
	if err != nil {
		return "", fmt.Errorf("inspecting the current image: %w", err)
	}
	config := withoutImageDefaults(inspect.Config, inspect.HostConfig, oldImage)

	setStatus(StatusRecreating)
	say(StreamStatus, "==> Recreating "+name)

	if wasRunning {
		if err := api.ContainerStop(ctx, inspect.ID, container.StopOptions{}); err != nil {
			return "", fmt.Errorf("stopping container: %w", err)
		}
		say(StreamStdout, "Container stopped")
	}

	// kept is the old container's id while it is renamed aside; empty when a
	// prune removed it first.
	kept := inspect.ID
	keptName := name + oldContainerSuffix + shortContainerID(inspect.ID)
	if err := api.ContainerRename(ctx, inspect.ID, keptName); err != nil {
		if !wasRunning || !cerrdefs.IsNotFound(err) {
			s.resumeOld(ctx, inspect.ID, wasRunning, say)
			return "", fmt.Errorf("renaming container: %w", err)
		}
		kept = ""
	} else {
		say(StreamStdout, "Old container kept as "+keptName)
	}

	var netConfig *dockernet.NetworkingConfig
	if inspect.NetworkSettings != nil {
		netConfig = &dockernet.NetworkingConfig{
			EndpointsConfig: inspect.NetworkSettings.Networks,
		}
	}

	newContainer, err := api.ContainerCreate(ctx, config, inspect.HostConfig, netConfig, nil, name)
	if err != nil {
		err = fmt.Errorf("creating container: %w", err)
		return "", errors.Join(err, s.restoreOld(ctx, "", kept, name, wasRunning, say))
	}
	say(StreamStdout, "New container created")

	if wasRunning {
		if err := api.ContainerStart(ctx, newContainer.ID, container.StartOptions{}); err != nil {
			err = fmt.Errorf("starting container: %w", err)
			return "", errors.Join(err, s.restoreOld(ctx, newContainer.ID, kept, name, wasRunning, say))
		}
		say(StreamStdout, "Container started")
	}

	if kept != "" {
		s.removeOld(ctx, kept, keptName, say)
	}

	return newContainer.ID, nil
}

// removeOld removes the old container once its replacement has started, on the
// same detached deadline as the rollback: the update's own context can end
// right after the start (agent-os-ohiy).
//
// The update has happened; a leftover old container does not undo it, so a
// failed remove is a warning, not a failure. The leftover is stopped, but one
// with a restart policy of "always" starts again when the Docker daemon
// restarts, beside its replacement: it can take a published port first, or run
// as a second writer on the same volumes. So its restart policy is set to "no"
// before the warning, best effort.
func (s *DockerService) removeOld(ctx context.Context, kept, keptName string, say func(Stream, string)) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), standaloneRollbackTimeout)
	defer cancel()
	api := s.updateAPI()

	err := api.ContainerRemove(rctx, kept, container.RemoveOptions{})
	if err == nil {
		say(StreamStdout, "Old container removed")
		return
	}
	if cerrdefs.IsNotFound(err) {
		// A prune took the stopped, set-aside container first: it is gone,
		// which is what this step wanted. Not a leftover.
		say(StreamStdout, "Old container already removed")
		return
	}
	restart := "its restart policy is now \"no\", so it stays stopped"
	if _, uerr := api.ContainerUpdate(rctx, kept, container.UpdateConfig{
		RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
	}); uerr != nil {
		restart = "its restart policy could not be cleared (" + uerr.Error() + "), so a Docker restart may start it beside the new one"
	}
	slog.Warn("Standalone update: the old container could not be removed; remove it by hand",
		"container", keptName, "id", kept, "error", err, "restart_policy", restart)
	say(StreamStderr, "Warning: the old container "+keptName+" could not be removed ("+err.Error()+"); "+restart+"; remove it by hand")
}

// restoreOld undoes a failed recreate: it removes the new container (if one was
// created), gives the old one back its name and starts it again if it was
// running. It runs on its own deadline, detached from ctx; see
// standaloneRollbackTimeout. kept is empty when there is no old container to
// restore. It returns what could not be undone, or nil.
//
// Every step is tried even when an earlier one failed: an old container
// started under its set-aside name still serves, which beats leaving nothing
// running because the new container could not be removed or the name was taken
// meanwhile (agent-os-ohiy).
func (s *DockerService) restoreOld(ctx context.Context, newID, kept, name string, wasRunning bool, say func(Stream, string)) error {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), standaloneRollbackTimeout)
	defer cancel()
	api := s.updateAPI()

	var errs []error
	newMayRun := false
	if newID != "" {
		if err := api.ContainerRemove(rctx, newID, container.RemoveOptions{Force: true}); err != nil {
			errs = append(errs, fmt.Errorf("rollback: removing the new container: %w", err))
			newMayRun = newContainerMayRun(rctx, api, newID)
		}
	}
	if kept == "" {
		return errors.Join(errs...)
	}
	if err := api.ContainerRename(rctx, kept, name); err != nil {
		errs = append(errs, fmt.Errorf("rollback: renaming the old container back to %s: %w", name, err))
	} else {
		say(StreamStdout, "Old container restored")
	}
	switch {
	case wasRunning && newMayRun:
		// The new container's start can fail on the client side after the
		// daemon started it (the update's context ended mid-request), and it
		// could not be removed. Starting the old one beside it would run two
		// copies on the same volumes, so it stays stopped for a person to
		// decide.
		errs = append(errs, fmt.Errorf("rollback: the new container %s may still be running and could not be removed, so the old container %s was left stopped; manual action needed", shortContainerID(newID), shortContainerID(kept)))
	case wasRunning:
		if err := api.ContainerStart(rctx, kept, container.StartOptions{}); err != nil {
			errs = append(errs, fmt.Errorf("rollback: starting the old container: %w", err))
		} else {
			say(StreamStdout, "Old container started")
		}
	}
	return errors.Join(errs...)
}

// newContainerMayRun reports whether a new container that could not be removed
// might be running. Only "gone" or "inspected and not running" count as safe;
// an inspect that fails any other way is treated as running.
func newContainerMayRun(ctx context.Context, api containerUpdateAPI, id string) bool {
	info, err := api.ContainerInspect(ctx, id)
	if cerrdefs.IsNotFound(err) {
		return false
	}
	if err != nil {
		return true // unknown: assume it runs
	}
	if info.ContainerJSONBase == nil || info.State == nil {
		return true
	}
	return info.State.Running
}

// resumeOld starts the old container again when the update stopped it and
// then could not set it aside. Same detached deadline as restoreOld. A failure
// is logged: the rename error is what the caller reports.
func (s *DockerService) resumeOld(ctx context.Context, id string, wasRunning bool, say func(Stream, string)) {
	if !wasRunning {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), standaloneRollbackTimeout)
	defer cancel()
	if err := s.updateAPI().ContainerStart(rctx, id, container.StartOptions{}); err != nil {
		slog.Warn("Standalone update: could not start the old container again", "id", id, "error", err)
		return
	}
	say(StreamStdout, "Old container started")
}

// shortContainerID is the 12-character form Docker shows.
func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// streamComposeCmd runs a docker compose command and streams each output line
// via emit, stdout and stderr each tagged with its own stream. It returns nil on
// success, or an error wrapping cmd.Wait's exit status; the output itself only
// reaches the caller through emit.
//
// The command is bounded by timeout as well as ctx (agent-os-z91e.20). Its
// output goes through io.Pipe writers rather than StdoutPipe/StderrPipe: exec
// then runs its own copy goroutines, which cmd.Wait bounds with WaitDelay, so
// Wait can be called first. With StdoutPipe the scanners had to finish before
// Wait, and a grandchild holding the pipe open after the kill kept them, and so
// the update job, running until it exited on its own.
//
// Each line passes redactComposeOutput with secrets, which the caller reads once
// with composeSecrets for the whole update (agent-os-sdbr): the lines reach the
// update job's log, which the API serves.
func streamComposeCmd(ctx context.Context, timeout time.Duration, args []string, dir string, secrets []string, emit func(LogLine)) error {
	//nolint:gosec // explicit argv, not a shell string — see README.md "Command execution and file access"
	cmd, cmdCtx, cancel := commandWithDeadline(ctx, timeout, "docker", args...)
	defer cancel()
	cmd.Dir = dir
	cmd.Env = dockerEnv()

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	var wg sync.WaitGroup
	scanPipe := func(r io.Reader, s Stream) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner := bufio.NewScanner(r)
			for scanner.Scan() {
				text := scanner.Text()
				emit(LogLine{Ts: time.Now().UTC(), Text: redactComposeOutput(text, secrets), Stream: s})
			}
			// Keep draining after a scan error (a line over the scanner's limit):
			// exec's copy goroutine blocks on a writer nobody reads.
			_, _ = io.Copy(io.Discard, r) //nolint:errcheck // Drain only: the pipe reader's only error is the writer closing, which is the end of the stream.
		}()
	}
	scanPipe(stdoutR, StreamStdout)
	scanPipe(stderrR, StreamStderr)

	waitErr := cmd.Wait()
	// Wait has returned, so exec's copy goroutines are done writing: closing the
	// writers is what ends the scanners.
	_ = stdoutW.Close()
	_ = stderrW.Close()
	wg.Wait()

	if waitErr != nil {
		return fmt.Errorf("command failed: %w", timeoutError(cmdCtx, waitErr, "docker compose"))
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
	imageRef, _, oldImageID, err := truth.ResolveContainerImage(ctx, s.updateAPI(), containerID)
	if err != nil {
		return models.UpdateResult{}, truth.Failed("could not inspect container before update", err)
	}

	start := time.Now()

	inspect, err := s.updateAPI().ContainerInspect(ctx, containerID)
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
		recreatedID, applyErr = s.recreateStandaloneContainer(ctx, inspect, wasRunning, emit, setStatus)
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

	advanced, newImageID, err := truth.ContainerImageAdvanced(ctx, s.updateAPI(), newContainerID, oldImageID)
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
		newImg, imgErr := s.updateAPI().ImageInspect(ctx, newImageID) //geterrors:ignore as at the streaming-container site above: the digest is detail on a verified success
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

	// Both built before anything runs, so a refusal leaves the service untouched.
	pullArgs, err := s.mutatingComposeArgs(stack, "pull", []string{"--", serviceName})
	if err != nil {
		return err
	}
	upArgs, err := s.mutatingComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	if err != nil {
		return err
	}

	setStatus(StatusPulling)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Pulling " + imageRef, Stream: StreamStatus})

	secrets := s.composeSecrets(stack)
	if err := streamComposeCmd(ctx, s.composeTimeout(), pullArgs, stack.Directory, secrets, emit); err != nil {
		return fmt.Errorf("compose pull failed: %w", err)
	}

	setStatus(StatusRecreating)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Recreating " + serviceName, Stream: StreamStatus})

	if err := streamComposeCmd(ctx, s.composeTimeout(), upArgs, stack.Directory, secrets, emit); err != nil {
		return fmt.Errorf("compose up failed: %w", err)
	}

	if !wasRunning {
		time.Sleep(3 * time.Second)
		filterArgs := filters.NewArgs()
		filterArgs.Add("label", "com.docker.compose.project="+stack.ProjectName)
		filterArgs.Add("label", "com.docker.compose.service="+serviceName)
		filterArgs.Add("status", "running")

		containers, err := s.updateAPI().ContainerList(ctx, container.ListOptions{Filters: filterArgs})
		if err != nil {
			return fmt.Errorf("finding new container to stop: %w", err)
		}
		for _, c := range containers {
			if err := s.updateAPI().ContainerStop(ctx, c.ID, container.StopOptions{}); err != nil {
				slog.Error("Failed to stop recreated container", "id", c.ID, "error", err)
			}
		}
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
	containers, listErr := s.updateAPI().ContainerList(ctx, container.ListOptions{All: true, Filters: filterArgs})
	// Split rather than merged (agent-os-qyg7.2). A docker daemon that could
	// not be reached and a service that has no container are different facts,
	// and only one of them is about the service. The caller shows ar.Reason to
	// the user and records ar.Err only when it is non-nil
	// (handlers/updates.go), so under the merged branch an unreachable daemon
	// reached the update history as "could not find container for service X".
	if listErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("could not list containers for service "+serviceName, listErr)
		return
	}
	if len(containers) == 0 {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("could not find container for service "+serviceName, nil)
		return
	}

	preContainerID := containers[0].ID
	// Resolve image ref and old image ID for post-apply comparison.
	imageRef, _, oldImgID, resolveErr := truth.ResolveContainerImage(ctx, s.updateAPI(), preContainerID)
	if resolveErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("could not inspect container for "+serviceName, resolveErr)
		return
	}
	oldImageID = oldImgID

	// Both built before the pull, so a refusal changes nothing and announces
	// no pull (agent-os-z91e.38).
	pullArgs, refuseErr := s.mutatingComposeArgs(stack, "pull", []string{"--", serviceName})
	if refuseErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = refusedCompose(refuseErr)
		return
	}
	upArgs, refuseErr := s.mutatingComposeArgs(stack, "up", []string{"-d", "--force-recreate", "--no-deps", "--", serviceName})
	if refuseErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = refusedCompose(refuseErr)
		return
	}

	setStatus(StatusPulling)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Pulling " + serviceName, Stream: StreamStatus})

	secrets := s.composeSecrets(stack)
	if pullErr := streamComposeCmd(ctx, s.composeTimeout(), pullArgs, stack.Directory, secrets, emit); pullErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("compose pull failed", pullErr)
		return
	}

	setStatus(StatusRecreating)
	emit(LogLine{Ts: time.Now().UTC(), Text: "==> Recreating " + serviceName, Stream: StreamStatus})

	if upErr := streamComposeCmd(ctx, s.composeTimeout(), upArgs, stack.Directory, secrets, emit); upErr != nil {
		durationMs = time.Since(start).Milliseconds()
		ar = truth.Failed("compose up failed", upErr)
		return
	}

	// Verify image advancement.
	postContainerID := s.findComposeContainer(ctx, stack.ProjectName, serviceName, preContainerID)
	advanced, newImgID, verifyErr := truth.ContainerImageAdvanced(ctx, s.updateAPI(), postContainerID, oldImgID)
	newImageID = newImgID
	durationMs = time.Since(start).Milliseconds()

	if verifyErr != nil {
		ar = truth.Failed("post-update container inspect failed", verifyErr)
		return
	}

	if advanced {
		newImg, imgErr := s.updateAPI().ImageInspect(ctx, newImgID) //geterrors:ignore as at the two sites above: the digest is detail on a verified success
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
