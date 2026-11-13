package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/gin-gonic/gin"
	"github.com/thinkbig1979/capstan/backend/internal/database"
	"github.com/thinkbig1979/capstan/backend/internal/errdefs"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// updateScanner is the narrow seam checkUpdates needs from a scheduler: start
// a background scan and report whether one is in flight. *services.SchedulerService
// satisfies it structurally, so production wiring is unchanged. Tests substitute
// a fake to drive scanStartIsBenign's unreached branch through the router: a real
// SchedulerService's StartBackgroundScan can only ever return ErrScanInProgress,
// ErrSchedulerStopping, or nil (see scheduler.go's StartBackgroundScan), so there
// is no way to reach checkUpdates' 500 branch through the concrete type alone
// (agent-os-10hb).
type updateScanner interface {
	StartBackgroundScan() error
	IsScanning() bool
}

// dockerCleanupService is the slice of *services.DockerCleanupService the
// cleanup routes need. Declared here on the consumer side so the preview and
// run handlers are testable without a Docker daemon.
type dockerCleanupService interface {
	PreviewCategories(ctx context.Context, minAgeHours int, categories services.DockerCleanupCategories) (*services.DockerCleanupPreview, error)
	Execute(ctx context.Context, trigger string, minAgeHours int, categories services.DockerCleanupCategories) (*models.DockerCleanupRun, error)
}

// dockerCleanupArmer is the slice of *services.DockerCleanupSchedulerService the
// policy PUT needs: re-arm the tick from the freshly stored policy, so an
// operator who opts in does not have to restart the process. NextRunAt is what
// the policy GET and PUT report as nextRunAt.
type dockerCleanupArmer interface {
	StartFromPolicy()
	NextRunAt() time.Time
}

// resourcePruner is the slice of *services.DockerService the four cross-stack
// prune routes need, so the stack turn they take can be tested without a Docker
// daemon. Kept apart from containerActionDocker: a prune is not a
// single-container action. Every method on it removes objects of any stack, so
// the guard (TestContainerLockGuard_EveryContainerPruneTakesTheExclusiveTurnFirst)
// derives its method set from this interface: a prune added here that
// is called without the gate fails that test.
type resourcePruner interface {
	PruneContainers(ctx context.Context, opts services.PruneOptions) (container.PruneReport, error)
	PruneVolumes(ctx context.Context, opts services.PruneOptions) (volume.PruneReport, error)
	PruneNetworks(ctx context.Context, opts services.PruneOptions) (network.PruneReport, error)
	PruneImages(ctx context.Context, opts services.PruneOptions) (image.PruneReport, error)
}

// volumePruneScoper reads what a default volume prune removes on this daemon
// (agent-os-ny0c). Kept off resourcePruner: the exclusive-gate guard reads
// every method there as a removal, and this one only reads.
type volumePruneScoper interface {
	VolumePruneRemovesAllUnused(ctx context.Context) (bool, error)
}

// resourceDeleter is the slice of *services.DockerService the single network,
// volume and image delete routes need (agent-os-qags.33). Probed against a real
// daemon: a Created container does not hold its network, a volume compose has
// created and not yet attached is deletable, and an unused image is deletable
// between a pull and the up that needs it. Like resourcePruner, the exclusive-gate
// guard derives its method set from this interface, so a delete added here that
// is called without the gate fails it. DeleteContainer is not here: it is a
// single-container action behind lockContainerStack.
type resourceDeleter interface {
	DeleteImage(ctx context.Context, imageID string, force bool) ([]image.DeleteResponse, error)
	DeleteVolume(ctx context.Context, volumeName string, force bool) error
	DeleteNetwork(ctx context.Context, networkID string) error
}

// containerActionDocker is the slice of *services.DockerService the
// single-container action routes need: the inspect that finds the container's
// stack, and the four mutations. Declared on the consumer side so the lock
// those routes take can be tested without a Docker daemon. A nil
// *DockerService boxed into it is a non-nil interface value, which is fine
// here: every one of these methods guards a nil receiver and returns
// services.ErrDockerUnavailable.
type containerActionDocker interface {
	InspectContainer(ctx context.Context, containerID string) (container.InspectResponse, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string) error
	RestartContainer(ctx context.Context, containerID string) error
	DeleteContainer(ctx context.Context, containerID string, force bool) error
}

type ResourcesHandler struct {
	docker *services.DockerService
	// containerOps is docker, seen through containerActionDocker. Set by the
	// constructor; tests replace it with a fake.
	containerOps containerActionDocker
	// pruner is docker, seen through resourcePruner; nil (handlers built as
	// struct literals) means use docker.
	pruner resourcePruner
	// deleter is docker, seen through resourceDeleter; nil means use docker.
	deleter    resourceDeleter
	db         *database.DB
	scheduler  updateScanner
	jobManager *services.UpdateJobManager
	actionLog  *services.ActionLogger
	// cleanup and cleanupArmer are injected by setters rather than through a
	// constructor parameter: both are nil on a Docker-less host (see
	// cmd/server/main.go), every cleanup handler nil-checks them, and adding
	// parameters to the two constructors would churn every existing caller for
	// no gain.
	cleanup      dockerCleanupService
	cleanupArmer dockerCleanupArmer
	// opLock is the per-stack operation lock the manual update handlers take,
	// so an update cannot interleave with a lifecycle op or backup on the same
	// stack (agent-os-a1ye.4). Injected by SetOperationLock like the two
	// above; nil (test-built handlers) means no locking.
	opLock *services.OperationLock
	// hostSpace serves the Docker host summary and cleanup candidates and
	// runs free-space. Injected by SetHostSpaceService; nil on a Docker-less
	// host, and the host routes then answer 503.
	hostSpace hostSpaceService
	// pruneScope is docker, seen through volumePruneScoper; nil means use docker.
	pruneScope volumePruneScoper
}

// SetOperationLock installs the per-stack operation lock shared with the
// stacks, operations and backup paths. main.go passes the same instance.
func (h *ResourcesHandler) SetOperationLock(l *services.OperationLock) {
	h.opLock = l
}

// SetCleanupService installs the Docker cleanup service. Nil-checked on the
// concrete pointer by the caller, for the typed-nil reason NewResourcesHandler's
// doc comment gives: boxing a nil *DockerCleanupService into this interface would
// produce a NON-nil interface value and the handlers' nil checks would pass.
func (h *ResourcesHandler) SetCleanupService(svc dockerCleanupService) {
	h.cleanup = svc
}

// SetCleanupScheduler installs the cleanup scheduler, so a policy PUT can re-arm
// the tick. Same typed-nil discipline as SetCleanupService.
func (h *ResourcesHandler) SetCleanupScheduler(s dockerCleanupArmer) {
	h.cleanupArmer = s
}

// NewResourcesHandler delegates to NewResourcesHandlerWithJobManager (nil
// jobManager) so the nil-check below has exactly one copy to keep correct.
// Only tests call it; production (cmd/server/main.go) calls
// NewResourcesHandlerWithJobManager.
//
// Both constructors keep *services.SchedulerService as their parameter type
// (production callers, e.g. cmd/server/main.go, are unaffected) and nil-check
// the concrete pointer before assigning it to the interface field. That order
// matters: main.go can pass a typed-nil *SchedulerService when Docker is
// unavailable, and boxing a typed nil pointer into an interface value directly
// produces a NON-nil interface (the classic typed-nil trap) — h.scheduler != nil
// would then be true and StartBackgroundScan/IsScanning would run on a nil
// receiver, which panics (both dereference s.mu with no nil-receiver guard).
// Checking scheduler != nil on the still-concrete parameter avoids that.
func NewResourcesHandler(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService) *ResourcesHandler {
	return NewResourcesHandlerWithJobManager(docker, db, scheduler, nil)
}

func NewResourcesHandlerWithJobManager(docker *services.DockerService, db *database.DB, scheduler *services.SchedulerService, jobManager *services.UpdateJobManager) *ResourcesHandler {
	h := &ResourcesHandler{docker: docker, containerOps: docker, pruner: docker, deleter: docker, db: db, jobManager: jobManager, actionLog: services.NewActionLogger(db)}
	if scheduler != nil {
		h.scheduler = scheduler
	}
	return h
}

func (h *ResourcesHandler) RegisterRoutes(r *gin.RouterGroup) {
	r.GET("/resources/images", h.listImages)
	r.DELETE("/resources/images/:id", h.deleteImage)
	r.POST("/resources/images/prune", h.pruneImages)
	r.GET("/resources/images/prune-impact", h.getImagePruneImpact)

	r.GET("/resources/containers", h.listContainers)
	r.GET("/resources/containers/:id/inspect", h.inspectContainer)
	r.POST("/resources/containers/:id/start", h.startContainer)
	r.POST("/resources/containers/:id/stop", h.stopContainer)
	r.POST("/resources/containers/:id/restart", h.restartContainer)
	r.DELETE("/resources/containers/:id", h.deleteContainer)
	r.POST("/resources/containers/prune", h.pruneContainers)
	r.GET("/resources/containers/prune-impact", h.getContainerPruneImpact)

	r.GET("/resources/updates", h.checkUpdates)
	r.POST("/resources/containers/:id/update", h.updateContainer)
	r.POST("/resources/stacks/:id/update", h.updateStack)

	r.GET("/resources/updates/jobs", h.listUpdateJobs)
	r.GET("/resources/updates/jobs/:jobId", h.getUpdateJob)

	r.GET("/resources/updates/history", h.getUpdateHistory)
	r.DELETE("/resources/updates/history", h.clearUpdateHistory)

	r.GET("/resources/auto-update/policies", h.listAutoUpdatePolicies)
	r.PUT("/resources/auto-update/policies/:targetType/:targetId", h.upsertAutoUpdatePolicy)
	r.DELETE("/resources/auto-update/policies/:targetType/:targetId", h.deleteAutoUpdatePolicy)

	r.GET("/resources/volumes", h.listVolumes)
	r.DELETE("/resources/volumes/:name", h.deleteVolume)
	r.POST("/resources/volumes/prune", h.pruneVolumes)
	r.GET("/resources/volumes/prune-impact", h.getVolumePruneImpact)
	r.GET("/resources/volumes/prune-scope", h.getVolumePruneScope)

	r.GET("/resources/networks", h.listNetworks)
	r.POST("/resources/networks", h.createNetwork)
	r.DELETE("/resources/networks/:id", h.deleteNetwork)
	r.POST("/resources/networks/prune", h.pruneNetworks)
	r.GET("/resources/networks/prune-impact", h.getNetworkPruneImpact)

	r.GET("/resources/build-cache", h.listBuildCache)
	r.POST("/resources/build-cache/prune", h.pruneBuildCache)

	// Docker host page (agent-os-p8sm). Handlers in host_space.go.
	r.GET("/resources/host/summary", h.getHostSummary)
	r.GET("/resources/host/candidates", h.getHostCandidates)
	r.POST("/resources/host/free-space", h.freeSpace)

	// Scheduled Docker cleanup (agent-os-fn7x.3). Handlers in docker_cleanup.go.
	r.GET("/resources/cleanup/policy", h.getCleanupPolicy)
	r.PUT("/resources/cleanup/policy", h.updateCleanupPolicy)
	r.POST("/resources/cleanup/preview", h.previewCleanup)
	r.POST("/resources/cleanup/run", h.runCleanup)
	r.GET("/resources/cleanup/history", h.getCleanupHistory)
}

func (h *ResourcesHandler) listImages(c *gin.Context) {
	images, err := h.docker.ListImages(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list images", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list images")
		return
	}
	c.JSON(http.StatusOK, gin.H{"images": images})
}

func (h *ResourcesHandler) listContainers(c *gin.Context) {
	containers, err := h.docker.GetAllContainersWithDetails(c.Request.Context(), h.db)
	if err != nil {
		slog.Error("Failed to list containers", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list containers")
		return
	}
	c.JSON(http.StatusOK, gin.H{"containers": containers})
}

// lockContainerStack takes the operation lock of the managed stack that owns
// container id, so a single-container action cannot interleave with a backup
// (which stops the stack), a restore or a compose up on it (agent-os-w1cj).
// The owner is found the way updateContainer finds it: the container's compose
// project label, looked up in the stacks table. A container with no label, or
// whose project no stack here manages, takes no lock.
//
// On ok=false the response is written: dockerFail renders an inspect error the
// way the action renders its own Docker error, a busy stack answers 409, and a
// stacks table that cannot be read answers 500, because managed and standalone
// are then indistinguishable and acting unlocked is the unsafe guess.
func (h *ResourcesHandler) lockContainerStack(c *gin.Context, id string, dockerFail func(error)) (release func(), ok bool) {
	inspect, err := h.containerOps.InspectContainer(c.Request.Context(), id)
	if err != nil {
		dockerFail(err)
		return nil, false
	}
	projectName := ""
	if inspect.Config != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
	}
	if projectName == "" {
		return func() {}, true
	}
	stack, err := h.db.GetStackByProjectName(projectName)
	switch {
	case errors.Is(err, errdefs.ErrNotFound):
		return func() {}, true
	case errors.Is(err, errdefs.ErrAmbiguous):
		refuseAmbiguousStack(c, err)
		return nil, false
	case err != nil:
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to look up the stack that owns this container", err))
		return nil, false
	}
	return acquireStackLock(c, h.opLock, stack.ID, services.OpKindContainer)
}

// refuseAmbiguousStack answers 409 AMBIGUOUS_STACK for a container whose
// compose project name more than one stack carries: whose lock to take, and
// which stack an update goes through, cannot be told, so nothing is done
// (agent-os-z91e.19, owner decision D25). err's text names every stack.
func refuseAmbiguousStack(c *gin.Context, err error) {
	handleError(c, models.NewAppErrorWithCause(http.StatusConflict, models.ErrAmbiguousStack,
		err.Error()+"; give each stack its own compose project name", err))
}

func (h *ResourcesHandler) startContainer(c *gin.Context) {
	id := c.Param("id")
	fail := func(err error) {
		slog.Error("Failed to start container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to start container")
	}
	release, ok := h.lockContainerStack(c, id, fail)
	if !ok {
		return
	}
	defer release()
	if err := h.containerOps.StartContainer(c.Request.Context(), id); err != nil {
		fail(err)
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_start", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container started"})
}

func (h *ResourcesHandler) stopContainer(c *gin.Context) {
	id := c.Param("id")
	fail := func(err error) {
		slog.Error("Failed to stop container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to stop container")
	}
	release, ok := h.lockContainerStack(c, id, fail)
	if !ok {
		return
	}
	defer release()
	if err := h.containerOps.StopContainer(c.Request.Context(), id); err != nil {
		fail(err)
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_stop", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container stopped"})
}

func (h *ResourcesHandler) restartContainer(c *gin.Context) {
	id := c.Param("id")
	fail := func(err error) {
		slog.Error("Failed to restart container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to restart container")
	}
	release, ok := h.lockContainerStack(c, id, fail)
	if !ok {
		return
	}
	defer release()
	if err := h.containerOps.RestartContainer(c.Request.Context(), id); err != nil {
		fail(err)
		return
	}
	BroadcastEvent(models.StackEvent{Type: "resource_changed", Event: "container_restart", ContainerID: id, Timestamp: time.Now()})
	c.JSON(http.StatusOK, gin.H{"message": "Container restarted"})
}

func (h *ResourcesHandler) inspectContainer(c *gin.Context) {
	id := c.Param("id")
	inspect, err := h.docker.InspectContainer(c.Request.Context(), id)
	if err != nil {
		slog.Error("Failed to inspect container", "id", id, "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "DOCKER_OPERATION", "Failed to inspect container")
		return
	}

	// A session that has not unlocked env values must not read them here
	// either: Config.Env holds every value the container started with,
	// interpolated .env secrets included, and Cmd, Args, labels and the
	// healthcheck can hold them too (agent-os-yvlh).
	if !envUnlocked(c) {
		secrets, ok := h.inspectSecrets(c, inspect)
		if !ok {
			return
		}
		maskContainerInspect(&inspect, secrets)
	}

	formatted, err := json.MarshalIndent(inspect, "", "  ")
	if err != nil {
		slog.Error("Failed to format inspect output", "id", id, "error", err)
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to format inspect output", err))
		return
	}

	c.Data(http.StatusOK, "application/json", formatted)
}

// inspectSecrets are the secret values of the stack that owns the inspected
// container, found by its compose project label as lockContainerStack finds
// it. A container no stack here manages has none: the name and flag passes
// still apply. When the owner cannot be told (two stacks share the project,
// or the stacks table cannot be read) the response is written and ok is
// false: masking with the wrong set, or none, would fail open.
func (h *ResourcesHandler) inspectSecrets(c *gin.Context, inspect container.InspectResponse) ([]string, bool) {
	projectName := ""
	if inspect.Config != nil {
		projectName = inspect.Config.Labels["com.docker.compose.project"]
	}
	if projectName == "" {
		return nil, true
	}
	stack, err := h.db.GetStackByProjectName(projectName)
	switch {
	case errors.Is(err, errdefs.ErrNotFound):
		return nil, true
	case errors.Is(err, errdefs.ErrAmbiguous):
		refuseAmbiguousStack(c, err)
		return nil, false
	case err != nil:
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to look up the stack that owns this container", err))
		return nil, false
	}
	secrets, err := h.docker.ComposeSecretValues(*stack)
	if err != nil {
		handleError(c, models.NewAppErrorWithCause(http.StatusInternalServerError, "INTERNAL_ERROR",
			"Failed to read the secrets of the stack that owns this container", err))
		return nil, false
	}
	return secrets, true
}

// maskContainerInspect masks, for a locked session, every string the stack's
// compose file or its running service can put in the inspect document, with
// the masker's value pass (secrets) and its name and flag passes
// (agent-os-yvlh): Path, Args, Name, the security labels, State.Error, the
// healthcheck output, and every string value under Config, HostConfig,
// Mounts and NetworkSettings (review r3: Mounts[].Source served the bind
// path HostConfig.Binds starred). Left as Docker sent them, because the
// daemon generates them: ID, Created, Image (a digest), the
// resolv/hostname/hosts/log paths, Driver, Platform, ExecIDs, GraphDriver,
// the sizes, State's status and timestamps, and ImageManifestDescriptor.
func maskContainerInspect(inspect *container.InspectResponse, secrets []string) {
	mask := func(s *string) { *s, _ = services.MaskSecrets(*s, "", secrets) }
	if inspect.ContainerJSONBase != nil {
		mask(&inspect.Path)
		inspect.Args = services.MaskArgs(inspect.Args, secrets)
		for _, s := range []*string{&inspect.Name, &inspect.AppArmorProfile, &inspect.MountLabel, &inspect.ProcessLabel} {
			mask(s)
		}
		if inspect.State != nil {
			mask(&inspect.State.Error)
			if inspect.State.Health != nil {
				for _, r := range inspect.State.Health.Log {
					if r != nil {
						mask(&r.Output)
					}
				}
			}
		}
	}
	if inspect.Config != nil {
		// Env first, by its own rule: an entry with no "=" is masked whole.
		inspect.Config.Env = maskContainerEnv(inspect.Config.Env, secrets)
		maskEveryString(reflect.ValueOf(inspect.Config).Elem(), secrets)
	}
	if inspect.ContainerJSONBase != nil && inspect.HostConfig != nil {
		maskEveryString(reflect.ValueOf(inspect.HostConfig).Elem(), secrets)
	}
	maskEveryString(reflect.ValueOf(inspect.Mounts), secrets)
	if inspect.NetworkSettings != nil {
		maskEveryString(reflect.ValueOf(inspect.NetworkSettings).Elem(), secrets)
	}
}

// maskEveryString masks every string VALUE reachable from v, in place,
// rather than a named list of fields (review r2 N1: LogConfig.Config held a
// splunk-token and a loki-url with credentials, and the next Docker field
// would too): a []string as an argv (MaskArgs), a string map entry by its
// key's name, else by its value, and every other string by MaskSecrets. Map
// KEYS are not masked (they are option names, port specs and container
// paths, and renaming a key could merge two entries). Unexported fields and
// interfaces are skipped; the inspect types hold strings in neither.
func maskEveryString(v reflect.Value, secrets []string) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			maskEveryString(v.Elem(), secrets)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				maskEveryString(f, secrets)
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			args := make([]string, v.Len())
			for i := range args {
				args[i] = v.Index(i).String()
			}
			for i, a := range services.MaskArgs(args, secrets) {
				v.Index(i).SetString(a)
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			maskEveryString(v.Index(i), secrets)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			nv := reflect.New(v.Type().Elem()).Elem()
			nv.Set(v.MapIndex(k))
			if k.Kind() == reflect.String && nv.Kind() == reflect.String && services.IsSensitiveEnvVar(k.String(), nv.String()) {
				nv.SetString("***")
			} else {
				maskEveryString(nv, secrets)
			}
			v.SetMapIndex(k, nv)
		}
	case reflect.String:
		masked, _ := services.MaskSecrets(v.String(), "", secrets)
		v.SetString(masked)
	}
}

// maskContainerEnv masks a container's KEY=value entries the way the locked
// env views do: the value of every entry IsSensitiveEnvVar matches becomes
// "***", and every other entry goes through MaskSecrets (the stack's secret
// values verbatim, KEY=value and flag tokens, URL credentials). An entry with
// no "=" cannot be classified and is masked whole: fail closed.
func maskContainerEnv(env, secrets []string) []string {
	if env == nil {
		return nil
	}
	out := make([]string, len(env))
	for i, e := range env {
		k, v, ok := strings.Cut(e, "=")
		switch {
		case !ok:
			out[i] = "***"
		case services.IsSensitiveEnvVar(k, v):
			out[i] = k + "=***"
		default:
			masked, _ := services.MaskSecrets(v, "", secrets)
			out[i] = k + "=" + masked
		}
	}
	return out
}

func (h *ResourcesHandler) listVolumes(c *gin.Context) {
	volumes, err := h.docker.ListVolumes(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list volumes", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list volumes")
		return
	}
	c.JSON(http.StatusOK, gin.H{"volumes": volumes})
}

func (h *ResourcesHandler) listNetworks(c *gin.Context) {
	networks, err := h.docker.ListNetworks(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list networks", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list networks")
		return
	}
	c.JSON(http.StatusOK, gin.H{"networks": networks})
}

// toBuildCacheEntries maps the Docker SDK records onto our own response type.
func toBuildCacheEntries(records []*build.CacheRecord) []BuildCacheEntry {
	entries := make([]BuildCacheEntry, 0, len(records))
	for _, r := range records {
		if r == nil {
			continue
		}
		entries = append(entries, BuildCacheEntry{
			ID:          r.ID,
			Parents:     r.Parents,
			Type:        r.Type,
			Description: r.Description,
			InUse:       r.InUse,
			Shared:      r.Shared,
			Size:        r.Size,
			CreatedAt:   r.CreatedAt,
			LastUsedAt:  r.LastUsedAt,
			UsageCount:  r.UsageCount,
		})
	}
	return entries
}

func (h *ResourcesHandler) listBuildCache(c *gin.Context) {
	records, err := h.docker.ListBuildCache(c.Request.Context())
	if err != nil {
		slog.Error("Failed to list build cache", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list build cache")
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": toBuildCacheEntries(records)})
}

// getVolumePruneScope tells the Volumes page whether the default volume prune
// removes every unused volume on this daemon, so it can warn about that prune
// too (agent-os-ny0c). A failed read is an error, never "anonymous only".
func (h *ResourcesHandler) getVolumePruneScope(c *gin.Context) {
	var scoper volumePruneScoper = h.docker
	if h.pruneScope != nil {
		scoper = h.pruneScope
	}
	all, err := scoper.VolumePruneRemovesAllUnused(c.Request.Context())
	if err != nil {
		slog.Error("Failed to read the Docker API version for the volume prune", "error", err)
		respondDockerErr(c, err, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check what a volume prune removes")
		return
	}
	c.JSON(http.StatusOK, services.VolumePruneScope{RemovesAllUnused: all})
}
