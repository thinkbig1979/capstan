package services

import (
	"context"
	"fmt"
	"math"
	"strings"
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

// dockerCleanupPruner is the slice of DockerService this service needs, declared
// here on the consumer side so tests can drive the cleanup paths without a
// daemon. DockerService is a concrete struct with an unexported client, so a
// test cannot otherwise observe the PruneOptions it builds -- and those options
// are the entire safety property.
type dockerCleanupPruner interface {
	ListImages(ctx context.Context) ([]models.DockerImage, error)
	PruneImages(ctx context.Context, opts PruneOptions) (image.PruneReport, error)
	PruneBuildCache(ctx context.Context, opts PruneOptions) (*build.CachePruneReport, error)
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
}

func NewDockerCleanupService(docker dockerCleanupPruner, store dockerCleanupStore) *DockerCleanupService {
	return &DockerCleanupService{docker: docker, store: store}
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

// Preview reports what a run would remove without removing anything. It calls
// no prune method -- that is asserted, not merely intended.
func (s *DockerCleanupService) Preview(ctx context.Context, minAgeHours int) (*DockerCleanupPreview, error) {
	floor := clampCleanupAgeHours(minAgeHours)
	images, err := s.docker.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing images for cleanup preview: %w", err)
	}

	// The same boundary the Until filter applies: created strictly before this
	// instant. Computed once so every candidate is judged against one cutoff
	// rather than against a clock that moves during the loop.
	cutoff := time.Now().Add(-time.Duration(floor) * time.Hour).Unix()

	preview := &DockerCleanupPreview{
		Candidates:  []DockerCleanupCandidate{},
		MinAgeHours: floor,
	}
	for _, img := range images {
		repo, dangling := danglingRepository(img.RepoTags)
		if !dangling || img.Created >= cutoff {
			continue
		}
		preview.Candidates = append(preview.Candidates, DockerCleanupCandidate{
			ID:         img.ID,
			Repository: repo,
			Size:       img.Size,
			Created:    img.Created,
		})
		preview.ReclaimableBytes += img.Size
	}
	return preview, nil
}

// Execute prunes dangling images and build cache under the age floor and
// records the run. trigger is "scheduled" or "manual".
//
// A run that fails partway is recorded with whatever it had already reclaimed
// rather than with zeroes: the bytes are gone either way, and a history row
// claiming zero would misreport the disk.
func (s *DockerCleanupService) Execute(ctx context.Context, trigger string, minAgeHours int) (*models.DockerCleanupRun, error) {
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
	}

	imgReport, err := s.docker.PruneImages(ctx, opts)
	if err != nil {
		return s.record(run, fmt.Errorf("pruning dangling images: %w", err))
	}
	run.ImagesDeleted = len(imgReport.ImagesDeleted)
	run.BytesReclaimed = reclaimedBytes(imgReport.SpaceReclaimed)

	cacheReport, err := s.docker.PruneBuildCache(ctx, opts)
	if err != nil {
		return s.record(run, fmt.Errorf("pruning build cache: %w", err))
	}
	if cacheReport != nil {
		run.CacheBytesReclaimed = reclaimedBytes(cacheReport.SpaceReclaimed)
	}
	return s.record(run, nil)
}

// record finalises the run row and stores it. It is the single exit from
// Execute so that no path can return without a history row.
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
