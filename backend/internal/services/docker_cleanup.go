package services

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/google/uuid"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// MinCleanupAgeHours is the hard floor on the cleanup age filter, below which
// this service refuses to go regardless of what a caller asks for.
//
// It exists because the age floor is the feature's ONLY retention mechanism
// (the spec rejected "keep last N per repository", measured: dangling images
// are overwhelmingly <none>:<none> with no repository to group by). A caller
// passing 0 would produce Until="0h", which does not mean "no filter" -- it
// means "created before now", i.e. everything. The safety property must not be
// reachable by passing a zero value, so cleanupPruneOptions clamps rather than
// trusting its argument.
const MinCleanupAgeHours = 1

// ErrCleanupInProgress means another cleanup run holds the single-flight guard.
// It means "not now", not "something failed": nothing was pruned and no history
// row was written, because no run happened (agent-os-yicu).
var ErrCleanupInProgress = errors.New("a Docker cleanup is already running")

// ErrCleanupTurnBusy means a run that needed the exclusive stack turn found it
// held. Nothing was removed and no history row was written: the scheduler
// records a 'skipped' row, the manual route answers 409. The wrapping error's
// text names the holder.
var ErrCleanupTurnBusy = errors.New("the Docker cleanup could not take its turn")

// dockerCleanupPruner is the slice of DockerService this service needs, declared
// here on the consumer side so tests can drive the cleanup paths without a
// daemon. DockerService is a concrete struct with an unexported client, so a
// test cannot otherwise observe the PruneOptions it builds -- and those options
// are the entire safety property.
type dockerCleanupPruner interface {
	ListImages(ctx context.Context) ([]models.DockerImage, error)
	ListBuildCache(ctx context.Context) ([]*build.CacheRecord, error)
	PruneImages(ctx context.Context, opts PruneOptions) (image.PruneReport, error)
	PruneBuildCache(ctx context.Context, opts PruneOptions) (*build.CachePruneReport, error)
}

// cleanupHostSpace is the slice of *HostSpaceService a run of the categories
// beyond dangling images and build cache uses. The candidate rules (stack
// ownership, compose references, in-use checks) are HostSpaceService's, the
// same ones the Free-up-space dialog applies; this service adds only the age
// floor.
type cleanupHostSpace interface {
	Candidates(ctx context.Context) (*HostCandidates, error)
	FreeSpace(ctx context.Context, req HostFreeSpaceRequest) (*HostFreeSpaceResult, error)
	Invalidate()
}

// cleanupTurn is the slice of *OperationLock a run takes its exclusive turn on.
type cleanupTurn interface {
	AcquireExclusive(kind string) (string, error)
	ReleaseExclusive(token string)
}

// dockerCleanupStore is the slice of *database.DB this service needs.
type dockerCleanupStore interface {
	CreateDockerCleanupRun(r *models.DockerCleanupRun) error
}

// DockerCleanupService prunes dangling images and build cache under an age
// floor, and records what each run reclaimed.
type DockerCleanupService struct {
	docker dockerCleanupPruner
	store  dockerCleanupStore

	// mu guards running, the single-flight flag Execute takes for the whole run.
	// It lives here, not in either caller, because the scheduler tick and the
	// manual-run handler both reach the prune only through Execute: a guard in
	// one caller left the other free to start a second prune (agent-os-yicu).
	mu      sync.Mutex
	running bool

	// hostSpace and turn are set once by main.go before the scheduler is
	// armed or a route is served, and never changed. Nil means a run of the
	// host categories refuses.
	hostSpace cleanupHostSpace
	turn      cleanupTurn
}

func NewDockerCleanupService(docker dockerCleanupPruner, store dockerCleanupStore) *DockerCleanupService {
	return &DockerCleanupService{docker: docker, store: store}
}

// SetHostSpaceService installs the candidate source and remover for unused
// tagged images, containers, volumes and networks, and the summary cache a
// run invalidates. main.go calls it only with a non-nil service: a typed nil
// would make the nil check below pass.
func (s *DockerCleanupService) SetHostSpaceService(h cleanupHostSpace) {
	s.hostSpace = h
}

// SetOperationLock installs the lock whose exclusive turn a run of the host
// categories takes, so it never removes an object a stack operation is
// creating.
func (s *DockerCleanupService) SetOperationLock(l *OperationLock) {
	if l == nil {
		return
	}
	s.turn = l
}

// usesHostSpace reports whether a run of these categories goes through
// HostSpaceService and the exclusive turn. Dangling images and build cache
// alone never do: a dangling image is untagged, so no pull in flight can be
// waiting on it.
func (c DockerCleanupCategories) usesHostSpace() bool {
	return c.UnusedImages || c.Containers || c.Volumes || c.Networks
}

// cleanupPruneOptions is the ONLY place this service builds PruneOptions, so
// there is exactly one line that could ever set All true and one test can pin
// it. All is the `docker image prune -a` behaviour, which deletes tagged images
// no container is running; on 2026-09-18 that set included the rollback image
// for a release deployed 30 minutes earlier. It is not offered, not behind a
// flag, and not a phase-1 simplification.
//
// Until is Docker's age filter and was measured two-sided with a firing control
// (spec probe P2): until=24h removes only images created BEFORE now minus 24h,
// so a dangling image seconds old survives it.
func cleanupPruneOptions(minAgeHours int) PruneOptions {
	return PruneOptions{
		All:   false,
		Until: fmt.Sprintf("%dh", clampCleanupAgeHours(minAgeHours)),
	}
}

// reclaimedBytes converts a Docker byte count to the int64 the history row
// stores.
//
// Overflow is not reachable from a real daemon -- int64 tops out at ~9.2
// exabytes -- but an UNCHECKED conversion wraps NEGATIVE, and a history row
// reporting negative bytes reclaimed is a worse lie than one reporting a
// saturated maximum. gosec flags the bare conversion (G115); this is the check
// it asks for, not a suppression of it.
func reclaimedBytes(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func clampCleanupAgeHours(h int) int {
	if h < MinCleanupAgeHours {
		return MinCleanupAgeHours
	}
	return h
}

// danglingRepository reports whether an image is dangling and, if it carries a
// repository, which one.
//
// BOTH dangling forms exist and the order of the checks below is load-bearing:
// "<none>:<none>" (locally built, superseded) also ends in ":<none>", so a
// suffix test applied first would report its repository as "<none>". The spec's
// P3 probe records the same trap from the shell side, where a ':<none>$' grep
// matched both forms and the two counters agreed for the wrong reason.
func danglingRepository(repoTags []string) (string, bool) {
	if len(repoTags) == 0 {
		return "", true
	}
	if len(repoTags) != 1 {
		// More than one tag means at least one real tag, so not dangling.
		return "", false
	}
	switch repoTags[0] {
	case "<none>", "<none>:<none>":
		// ListImages substitutes ["<none>"] for a nil RepoTags; Docker itself
		// reports ["<none>:<none>"]. Both are the fully-untagged form.
		return "", true
	}
	if repo, ok := strings.CutSuffix(repoTags[0], ":<none>"); ok {
		// Registry-pulled and superseded: the repo digest is retained, so the
		// repository name survives and the operator can be told which it was.
		return repo, true
	}
	return "", false
}

// cacheReclaimableBytes estimates what PruneBuildCache would free under the same
// age floor: records not in use whose last use (creation, if never used) is
// strictly before the cutoff.
//
// It is an estimate, and says so. Docker applies the until filter and decides
// what counts as unused inside the daemon; this mirrors that from the listing
// and does not reproduce record types the daemon may also spare. INFERRED from
// the prune options (All=false, until=<floor>h), not tested against a daemon:
// a prune cannot be run read-only.
func cacheReclaimableBytes(records []*build.CacheRecord, cutoff time.Time) int64 {
	var total int64
	for _, rec := range records {
		if rec == nil || rec.InUse {
			continue
		}
		last := rec.CreatedAt
		if rec.LastUsedAt != nil {
			last = *rec.LastUsedAt
		}
		if !last.Before(cutoff) {
			continue
		}
		total += rec.Size
	}
	return total
}

// Preview reports what a run would remove without removing anything. It calls
// no prune method -- that is asserted, not merely intended.
//
// The build-cache figure comes from a second Docker read. If that read fails
// the preview fails: a 0 would read as "no cache to reclaim" when nobody looked.
//
// Preview is the forecast at the default categories; PreviewCategories is the
// one that follows a policy.
func (s *DockerCleanupService) Preview(ctx context.Context, minAgeHours int) (*DockerCleanupPreview, error) {
	return s.PreviewCategories(ctx, minAgeHours, DefaultDockerCleanupCategories())
}

// PreviewCategories reports what a run of these categories would remove. A
// category switched off lists nothing and reads nothing from Docker.
func (s *DockerCleanupService) PreviewCategories(ctx context.Context, minAgeHours int, categories DockerCleanupCategories) (*DockerCleanupPreview, error) {
	floor := clampCleanupAgeHours(minAgeHours)
	// The same boundary the Until filter applies: created strictly before this
	// instant. Computed once so every candidate is judged against one cutoff
	// rather than against a clock that moves during the loop.
	cutoffTime := time.Now().Add(-time.Duration(floor) * time.Hour)
	cutoff := cutoffTime.Unix()

	preview := &DockerCleanupPreview{
		Candidates:  []DockerCleanupCandidate{},
		Containers:  []HostCandidateItem{},
		Volumes:     []HostCandidateItem{},
		Networks:    []HostCandidateItem{},
		MinAgeHours: floor,
	}

	if categories.DanglingImages {
		images, err := s.docker.ListImages(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing images for cleanup preview: %w", err)
		}
		for _, img := range images {
			repo, dangling := danglingRepository(img.RepoTags)
			if !dangling || img.Created >= cutoff {
				continue
			}
			size := reclaimableImageSize(img.Size, img.SharedSize)
			preview.Candidates = append(preview.Candidates, DockerCleanupCandidate{
				ID:              img.ID,
				Repository:      repo,
				Size:            img.Size,
				ReclaimableSize: size,
				Created:         img.Created,
			})
			preview.addImageBytes(size)
		}
	}

	if categories.BuildCache {
		records, err := s.docker.ListBuildCache(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing build cache for cleanup preview: %w", err)
		}
		preview.CacheReclaimableBytes = cacheReclaimableBytes(records, cutoffTime)
	}

	if categories.usesHostSpace() {
		if s.hostSpace == nil {
			return nil, errCleanupHostSpaceMissing
		}
		cands, err := s.hostSpace.Candidates(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing cleanup candidates: %w", err)
		}
		picked := pickCleanupCandidates(cands, categories, cutoff)
		preview.WithheldStacks = cands.WithheldStacks
		for _, it := range picked.images {
			// A tagged image's Size is what removing it frees (the
			// dialog's Size minus SharedSize), 0 when unknown;
			// ReclaimableSize is omitted then, which is the signal.
			preview.Candidates = append(preview.Candidates, DockerCleanupCandidate{
				ID:              it.ID,
				Tag:             it.Label,
				Size:            derefOr(it.Size, 0),
				ReclaimableSize: it.Size,
				Created:         derefOr(it.Created, 0),
			})
			preview.addImageBytes(it.Size)
		}
		preview.Containers = append(preview.Containers, picked.containers...)
		preview.Volumes = append(preview.Volumes, picked.volumes...)
		preview.Networks = append(preview.Networks, picked.networks...)
		for _, it := range append(append([]HostCandidateItem{}, picked.containers...), picked.volumes...) {
			if it.Size == nil {
				preview.UnknownSizeCount++
				continue
			}
			preview.OtherReclaimableBytes += *it.Size
		}
	}
	return preview, nil
}

func (p *DockerCleanupPreview) addImageBytes(size *int64) {
	if size == nil {
		p.UnknownSizeCount++
		return
	}
	p.ReclaimableBytes += *size
}

// reclaimableImageSize is Size minus SharedSize, the bytes only this image
// holds, or nil when Docker did not compute SharedSize (-1). It never falls
// back to Size: counting shared layers once per image overstates what a run
// frees. imageReclaimableSize (host_space.go) applies the same rule.
func reclaimableImageSize(size, shared int64) *int64 {
	if shared < 0 || size < shared {
		return nil
	}
	v := size - shared
	return &v
}

func derefOr(v *int64, fallback int64) int64 {
	if v == nil {
		return fallback
	}
	return *v
}

var errCleanupHostSpaceMissing = errors.New("removing unused tagged images, containers, volumes or networks needs the Docker host service, which is not available")

// cleanupPick is what a run of the host categories removes: the candidates of
// each switched-on category old enough to pass the floor.
type cleanupPick struct {
	images, containers, volumes, networks []HostCandidateItem
}

func (p cleanupPick) empty() bool {
	return len(p.images)+len(p.containers)+len(p.volumes)+len(p.networks) == 0
}

// pickCleanupCandidates applies the categories and the age floor to the
// dialog's candidates. The floor keeps an object unless its creation time is
// known and strictly before cutoff: an object of unknown age is kept. Unused
// images are the tagged ones only; dangling images belong to their own
// switch and the prune path.
func pickCleanupCandidates(c *HostCandidates, categories DockerCleanupCategories, cutoff int64) cleanupPick {
	old := func(items []HostCandidateItem, keep func(HostCandidateItem) bool) []HostCandidateItem {
		out := []HostCandidateItem{}
		for _, it := range items {
			if it.Created != nil && *it.Created < cutoff && keep(it) {
				out = append(out, it)
			}
		}
		return out
	}
	every := func(HostCandidateItem) bool { return true }
	var p cleanupPick
	if categories.UnusedImages {
		p.images = old(c.Images, func(it HostCandidateItem) bool { return !it.Dangling })
	}
	if categories.Containers {
		p.containers = old(c.Containers, every)
	}
	if categories.Volumes {
		p.volumes = old(c.Volumes, every)
	}
	if categories.Networks {
		p.networks = old(c.Networks, every)
	}
	return p
}

// Execute prunes dangling images and build cache under the age floor and
// records the run. trigger is "scheduled" or "manual".
//
// Each category runs only when it is switched on, and the run records exactly
// the categories it ran. Dangling images and build cache are Docker prunes
// under the until filter. Unused tagged images, containers, volumes and
// networks come from HostSpaceService's candidates (the Free-up-space
// dialog's rules), are kept unless created before the floor, and are removed
// one by one with force=false, all under the exclusive stack turn (taken for
// the whole run, and only when one of those four is on). A busy turn returns
// ErrCleanupTurnBusy with nothing removed and nothing recorded.
//
// A run that fails partway is recorded with whatever it had already reclaimed
// rather than with zeroes: the bytes are gone either way, and a history row
// claiming zero would misreport the disk.
//
// Single-flight: while a run is in progress, a second call returns
// (nil, ErrCleanupInProgress) at once, prunes nothing and records nothing. It
// refuses rather than waits so the manual caller can say so instead of hanging,
// and because a prune queued behind another reclaims next to nothing.
func (s *DockerCleanupService) Execute(ctx context.Context, trigger string, minAgeHours int, categories DockerCleanupCategories) (*models.DockerCleanupRun, error) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil, ErrCleanupInProgress
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	// The exclusive turn is taken before anything is removed and held for
	// the whole run, so a busy turn removes nothing at all.
	if categories.usesHostSpace() && s.turn != nil {
		token, err := s.turn.AcquireExclusive(OpKindDockerCleanup)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCleanupTurnBusy, err)
		}
		defer s.turn.ReleaseExclusive(token)
	}
	// Whatever is removed below changes the host summary; one invalidation
	// covers the prunes and the per-item removals alike (agent-os-yyat).
	if s.hostSpace != nil {
		defer s.hostSpace.Invalidate()
	}

	opts := cleanupPruneOptions(minAgeHours)
	run := &models.DockerCleanupRun{
		ID:      uuid.New().String(),
		Trigger: trigger,
		Status:  "success",
		// UTC, second precision. CreateDockerCleanupRun normalises through
		// canonicalTimestamp as well (agent-os-fn7x.8), so this is the correct
		// spelling arriving at a layer that also enforces it -- not a second
		// normaliser.
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
		MinAgeHours: clampCleanupAgeHours(minAgeHours),
		Categories:  executedCategories(categories),
	}

	// Refuse before removing anything: a run that cannot do every category
	// it was asked for does none of them.
	if categories.usesHostSpace() && (s.hostSpace == nil || s.turn == nil) {
		return s.record(run, errCleanupHostSpaceMissing)
	}

	if categories.DanglingImages {
		imgReport, err := s.docker.PruneImages(ctx, opts)
		if err != nil {
			return s.record(run, fmt.Errorf("pruning dangling images: %w", err))
		}
		run.ImagesDeleted = len(imgReport.ImagesDeleted)
		run.BytesReclaimed = reclaimedBytes(imgReport.SpaceReclaimed)
	}

	if categories.BuildCache {
		cacheReport, err := s.docker.PruneBuildCache(ctx, opts)
		if err != nil {
			return s.record(run, fmt.Errorf("pruning build cache: %w", err))
		}
		if cacheReport != nil {
			run.CacheBytesReclaimed = reclaimedBytes(cacheReport.SpaceReclaimed)
		}
	}

	if categories.usesHostSpace() {
		return s.record(run, s.removeHostCandidates(ctx, run, categories, minAgeHours))
	}
	return s.record(run, nil)
}

// removeHostCandidates removes the old-enough candidates of the host
// categories through FreeSpace, which rebuilds the candidate set under the
// turn this run holds and removes each with force=false, and adds what it
// removed to run. An item Docker refused is a failure of the run.
func (s *DockerCleanupService) removeHostCandidates(ctx context.Context, run *models.DockerCleanupRun, categories DockerCleanupCategories, minAgeHours int) error {
	cutoff := time.Now().Add(-time.Duration(clampCleanupAgeHours(minAgeHours)) * time.Hour).Unix()
	cands, err := s.hostSpace.Candidates(ctx)
	if err != nil {
		return fmt.Errorf("listing cleanup candidates: %w", err)
	}
	pick := pickCleanupCandidates(cands, categories, cutoff)
	if pick.empty() {
		return nil
	}

	ids := func(items []HostCandidateItem) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	res, err := s.hostSpace.FreeSpace(ctx, HostFreeSpaceRequest{
		Images:     ids(pick.images),
		Containers: ids(pick.containers),
		Volumes:    ids(pick.volumes),
		Networks:   ids(pick.networks),
	})
	if err != nil {
		return fmt.Errorf("removing cleanup candidates: %w", err)
	}

	sizes := map[string]*int64{}
	for _, items := range [][]HostCandidateItem{pick.images, pick.containers, pick.volumes} {
		for _, it := range items {
			sizes[it.ID] = it.Size
		}
	}
	for _, id := range res.Images.Removed {
		run.ImagesDeleted++
		run.BytesReclaimed += derefOr(sizes[id], 0)
	}
	for _, id := range res.Containers.Removed {
		run.ContainersDeleted++
		run.OtherBytesReclaimed += derefOr(sizes[id], 0)
	}
	for _, id := range res.Volumes.Removed {
		run.VolumesDeleted++
		run.OtherBytesReclaimed += derefOr(sizes[id], 0)
	}
	run.NetworksDeleted = len(res.Networks.Removed)

	var failed []string
	for kind, cat := range map[string]HostFreeSpaceCategoryResult{
		"image": res.Images, "container": res.Containers, "volume": res.Volumes, "network": res.Networks,
	} {
		for _, f := range cat.Failed {
			failed = append(failed, fmt.Sprintf("%s %s: %s", kind, f.ID, f.Reason))
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		return fmt.Errorf("%d item(s) could not be removed: %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

// executedCategories is the docker_cleanup_runs.categories value for a run:
// the categories Execute acts on, in the column's fixed order.
func executedCategories(c DockerCleanupCategories) string {
	var names []string
	for _, cat := range []struct {
		on   bool
		name string
	}{
		{c.DanglingImages, CleanupCategoryDanglingImages},
		{c.UnusedImages, CleanupCategoryUnusedImages},
		{c.BuildCache, CleanupCategoryBuildCache},
		{c.Containers, CleanupCategoryContainers},
		{c.Volumes, CleanupCategoryVolumes},
		{c.Networks, CleanupCategoryNetworks},
	} {
		if cat.on {
			names = append(names, cat.name)
		}
	}
	return strings.Join(names, ",")
}

// record finalises the run row and stores it. It is the single exit from
// Execute once a run has started, so no run can return without a history row;
// the single-flight refusal returns before a run exists.
func (s *DockerCleanupService) record(run *models.DockerCleanupRun, runErr error) (*models.DockerCleanupRun, error) {
	finished := time.Now().UTC().Format(time.RFC3339)
	run.FinishedAt = &finished
	if runErr != nil {
		run.Status = "failed"
		run.ErrorMessage = runErr.Error()
	}

	if err := s.store.CreateDockerCleanupRun(run); err != nil {
		// The prune already happened; losing the record is a separate failure
		// and must not be reported as a clean run.
		if runErr != nil {
			return run, fmt.Errorf("%w (and recording the run failed: %v)", runErr, err)
		}
		return run, fmt.Errorf("recording cleanup run: %w", err)
	}
	return run, runErr
}
