package services

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// agent-os-fn7x.2 — the cleanup service's behavioural arms.
//
// WHY A FAKE AND NOT A DAEMON. The safety property of this feature is entirely
// a property of the PruneOptions the service builds: All must be false, and
// Until must carry the floor. DockerService holds an unexported client, so no
// test against the real type can observe those options. A real daemon cannot
// help either -- it cannot be made to hold a dangling image of a known age
// reliably, which is the exact thing the age floor is about.
//
// WHAT THE FAKE'S FILTER MEANS, stated so it is not mistaken for proof about
// Docker. fn7x2FakeDocker.PruneImages implements the until semantics that were
// MEASURED against a real daemon in the spec's P2 probe, two-sided with a
// firing positive control: a dangling image seconds old SURVIVED
// `docker image prune -f --filter "until=24h"`, and the SAME image id was
// REMOVED by an unfiltered prune. So these tests assert the service asks for
// the right thing; the spec's probe is what establishes that Docker honours it.

type fn7x2FakeDocker struct {
	images []models.DockerImage

	listCalls      int
	imagePruneOpts []PruneOptions
	cachePruneOpts []PruneOptions

	imagePruneErr error
	cachePruneErr error

	cacheReclaimed uint64
}

func (f *fn7x2FakeDocker) ListImages(context.Context) ([]models.DockerImage, error) {
	f.listCalls++
	return f.images, nil
}

func (f *fn7x2FakeDocker) PruneImages(_ context.Context, opts PruneOptions) (image.PruneReport, error) {
	f.imagePruneOpts = append(f.imagePruneOpts, opts)
	if f.imagePruneErr != nil {
		return image.PruneReport{}, f.imagePruneErr
	}

	d, err := time.ParseDuration(opts.Until)
	if err != nil {
		// A malformed Until would be rejected by Docker, not silently ignored.
		// Failing here stops a test passing because the filter was unparseable.
		return image.PruneReport{}, err
	}
	cutoff := time.Now().Add(-d).Unix()

	report := image.PruneReport{}
	for _, img := range f.images {
		_, dangling := danglingRepository(img.RepoTags)
		if !dangling && !opts.All {
			continue
		}
		if img.Created >= cutoff {
			continue
		}
		report.ImagesDeleted = append(report.ImagesDeleted, image.DeleteResponse{Deleted: img.ID})
		// Fixture sizes are small positive literals defined in this file, so
		// the conversion cannot overflow; the production direction is guarded
		// by reclaimedBytes and tested below.
		report.SpaceReclaimed += uint64(img.Size) //nolint:gosec // G115: test fixture size, a small positive literal
	}
	return report, nil
}

func (f *fn7x2FakeDocker) PruneBuildCache(_ context.Context, opts PruneOptions) (*build.CachePruneReport, error) {
	f.cachePruneOpts = append(f.cachePruneOpts, opts)
	if f.cachePruneErr != nil {
		return nil, f.cachePruneErr
	}
	return &build.CachePruneReport{SpaceReclaimed: f.cacheReclaimed}, nil
}

type fn7x2FakeStore struct {
	runs   []models.DockerCleanupRun
	errOut error
}

func (s *fn7x2FakeStore) CreateDockerCleanupRun(r *models.DockerCleanupRun) error {
	if s.errOut != nil {
		return s.errOut
	}
	s.runs = append(s.runs, *r)
	return nil
}

// hoursAgo returns a unix creation timestamp h hours in the past.
func hoursAgo(h int) int64 { return time.Now().Add(-time.Duration(h) * time.Hour).Unix() }

// TestDockerCleanupNeverPrunesAll pins AC2: PruneOptions.All is false on every
// path that prunes.
//
// The Len assertions are what make this discriminating. "every recorded call
// has All false" is vacuously true over an empty slice, so a mutation that
// stopped calling Docker at all -- or a test wired to a service that never ran
// -- would pass the loop and prove nothing. The count is asserted first.
func TestDockerCleanupNeverPrunesAll(t *testing.T) {
	docker := &fn7x2FakeDocker{images: []models.DockerImage{
		{ID: "sha256:old", RepoTags: []string{"<none>:<none>"}, Size: 100, Created: hoursAgo(48)},
	}}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(docker, store)

	_, err := svc.Execute(context.Background(), "scheduled", 24)
	require.NoError(t, err)

	require.Len(t, docker.imagePruneOpts, 1, "the image prune must actually have been called")
	require.Len(t, docker.cachePruneOpts, 1, "the build cache prune must actually have been called")

	for i, opts := range docker.imagePruneOpts {
		assert.False(t, opts.All, "image prune call %d must not set All: that is the -a behaviour, which deletes tagged images no container is running", i)
	}
	for i, opts := range docker.cachePruneOpts {
		assert.False(t, opts.All, "build cache prune call %d must not set All", i)
	}

	// The floor must reach BOTH calls, not just the image one. A build cache
	// prune with no until filter would remove cache seconds old.
	assert.Equal(t, "24h", docker.imagePruneOpts[0].Until)
	assert.Equal(t, "24h", docker.cachePruneOpts[0].Until)
}

// TestDockerCleanupAgeFloor pins AC3 two-sided in one test: an image younger
// than the floor survives and an older one is removed.
//
// One-sided would not discriminate. "The young image survived" alone also holds
// if the service pruned nothing at all, so the old image's removal is what
// proves the prune ran and the filter is a filter rather than a no-op.
func TestDockerCleanupAgeFloor(t *testing.T) {
	young := models.DockerImage{ID: "sha256:young", RepoTags: []string{"<none>:<none>"}, Size: 10, Created: hoursAgo(1)}
	old := models.DockerImage{ID: "sha256:old", RepoTags: []string{"<none>:<none>"}, Size: 4096, Created: hoursAgo(48)}

	docker := &fn7x2FakeDocker{images: []models.DockerImage{young, old}}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(docker, store)

	run, err := svc.Execute(context.Background(), "manual", 24)
	require.NoError(t, err)

	require.Len(t, docker.imagePruneOpts, 1)
	assert.Equal(t, "24h", docker.imagePruneOpts[0].Until, "the floor must be passed as Docker's until filter")

	// Removed arm: exactly one image, and it is the old one.
	assert.Equal(t, 1, run.ImagesDeleted, "the 48h-old image must be removed")
	assert.Equal(t, int64(4096), run.BytesReclaimed, "bytes must come from the old image, not the young one")

	// Survived arm: the young image is not among what was deleted. Asserted on
	// the ids the fake reports rather than on the count alone, so a run that
	// deleted the WRONG single image would still fail.
	require.Len(t, store.runs, 1)
	assert.Equal(t, 24, store.runs[0].MinAgeHours, "the row must record the floor this run ran under")

	// And the preview, judged against the same cutoff, agrees on which one.
	preview, err := svc.Preview(context.Background(), 24)
	require.NoError(t, err)
	require.Len(t, preview.Candidates, 1, "only the old image is a candidate")
	assert.Equal(t, "sha256:old", preview.Candidates[0].ID)
	assert.Equal(t, int64(4096), preview.ReclaimableBytes)
}

// TestDockerCleanupPreviewIsReadOnly pins AC4: the preview records zero prune
// calls while still reporting a non-empty candidate set.
//
// Both halves are required. Zero prune calls alone would also hold for a
// preview that returned nothing, which is read-only and useless.
func TestDockerCleanupPreviewIsReadOnly(t *testing.T) {
	docker := &fn7x2FakeDocker{images: []models.DockerImage{
		{ID: "sha256:untagged", RepoTags: []string{"<none>:<none>"}, Size: 512, Created: hoursAgo(48)},
		{ID: "sha256:repodigest", RepoTags: []string{"ghcr.io/example/app:<none>"}, Size: 1024, Created: hoursAgo(72)},
		{ID: "sha256:tagged", RepoTags: []string{"ghcr.io/example/app:latest"}, Size: 9999, Created: hoursAgo(72)},
		{ID: "sha256:recent", RepoTags: []string{"<none>:<none>"}, Size: 7777, Created: hoursAgo(1)},
	}}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(docker, store)

	preview, err := svc.Preview(context.Background(), 24)
	require.NoError(t, err)

	assert.Empty(t, docker.imagePruneOpts, "preview must not prune images")
	assert.Empty(t, docker.cachePruneOpts, "preview must not prune build cache")
	assert.Empty(t, store.runs, "a preview is not a run and must not be recorded")

	// Non-empty, and specifically the two dangling images past the floor: the
	// tagged one and the one inside the floor are excluded.
	require.Len(t, preview.Candidates, 2)
	ids := []string{preview.Candidates[0].ID, preview.Candidates[1].ID}
	assert.ElementsMatch(t, []string{"sha256:untagged", "sha256:repodigest"}, ids)
	assert.Equal(t, int64(512+1024), preview.ReclaimableBytes)

	// AC8's backend half: the repo-digest form keeps its repository name so the
	// UI can render it, and the fully-untagged form has none to render.
	byID := map[string]DockerCleanupCandidate{}
	for _, c := range preview.Candidates {
		byID[c.ID] = c
	}
	assert.Equal(t, "ghcr.io/example/app", byID["sha256:repodigest"].Repository)
	assert.Empty(t, byID["sha256:untagged"].Repository, "the fully-untagged form has no repository, and must not report '<none>'")
}

// TestDockerCleanupFailureRecorded pins AC6: a failed prune records
// status=failed with a non-empty error_message, rather than a success row of
// zeroes.
func TestDockerCleanupFailureRecorded(t *testing.T) {
	boom := errors.New("docker daemon unreachable")
	docker := &fn7x2FakeDocker{imagePruneErr: boom}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(docker, store)

	run, err := svc.Execute(context.Background(), "scheduled", 24)
	require.Error(t, err, "a failed prune must be reported to the caller, not swallowed")

	require.Len(t, store.runs, 1, "a failed run must still be recorded exactly once")
	stored := store.runs[0]
	assert.Equal(t, "failed", stored.Status)
	assert.NotEmpty(t, stored.ErrorMessage, "a failed row with an empty error_message is indistinguishable from a success row")
	assert.Contains(t, stored.ErrorMessage, "docker daemon unreachable")
	assert.NotNil(t, stored.FinishedAt, "a failed run still finished")
	assert.Equal(t, 24, stored.MinAgeHours)

	// The returned run agrees with what was stored, so a caller that reads the
	// return value is not told something different from the history.
	assert.Equal(t, "failed", run.Status)

	// The build cache prune must NOT run after the image prune failed --
	// otherwise a broken daemon gets hit twice per tick.
	assert.Empty(t, docker.cachePruneOpts, "a failed image prune must short-circuit the run")
}

// TestDockerCleanupAgeFloorCannotBeBypassed pins the clamp. The floor is this
// feature's only retention mechanism, so it must not be reachable by passing a
// zero value: Until="0h" does not mean "no filter", it means "created before
// now", which is everything.
func TestDockerCleanupAgeFloorCannotBeBypassed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		asked int
		want  string
	}{
		{"zero", 0, "1h"},
		{"negative", -5, "1h"},
		{"below the floor is raised to it", 1, "1h"},
		{"above the floor is honoured", 168, "168h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docker := &fn7x2FakeDocker{}
			svc := NewDockerCleanupService(docker, &fn7x2FakeStore{})
			_, err := svc.Execute(context.Background(), "scheduled", tc.asked)
			require.NoError(t, err)
			require.Len(t, docker.imagePruneOpts, 1)
			assert.Equal(t, tc.want, docker.imagePruneOpts[0].Until)
			assert.False(t, docker.imagePruneOpts[0].All)
		})
	}
}

// TestDanglingRepositoryBothForms pins the ordering trap directly.
// "<none>:<none>" also ends in ":<none>", so a suffix test applied first
// reports its repository as "<none>". The spec's P3 probe records the same trap
// from the shell side, where a ':<none>$' grep matched both forms and the two
// counters agreed for the wrong reason.
func TestDanglingRepositoryBothForms(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tags     []string
		wantRepo string
		wantDang bool
	}{
		{"fully untagged, docker's spelling", []string{"<none>:<none>"}, "", true},
		{"fully untagged, ListImages' substitute", []string{"<none>"}, "", true},
		{"nil tags", nil, "", true},
		{"registry-pulled, repo digest retained", []string{"ghcr.io/example/app:<none>"}, "ghcr.io/example/app", true},
		{"tagged", []string{"ghcr.io/example/app:latest"}, "", false},
		{"multi-tagged", []string{"app:latest", "app:1.2.3"}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, dangling := danglingRepository(tc.tags)
			assert.Equal(t, tc.wantDang, dangling)
			assert.Equal(t, tc.wantRepo, repo)
		})
	}
}

// TestDockerCleanupReclaimedBytesNeverNegative pins the conversion guard.
//
// Docker reports reclaimed bytes as uint64 and the history row stores int64.
// An unchecked conversion WRAPS NEGATIVE, and "reclaimed -9223372036854775808
// bytes" in the operator's history is a worse lie than a saturated maximum.
// The value below is not reachable from a real daemon; the point is that the
// guard exists and saturates rather than wrapping, which is exactly what gosec
// G115 asks for and what a //nolint would have skipped.
func TestDockerCleanupReclaimedBytesNeverNegative(t *testing.T) {
	assert.Equal(t, int64(math.MaxInt64), reclaimedBytes(math.MaxUint64), "must saturate, not wrap")
	assert.Equal(t, int64(math.MaxInt64), reclaimedBytes(uint64(math.MaxInt64)+1), "the first value past int64 must saturate")
	assert.Equal(t, int64(math.MaxInt64), reclaimedBytes(math.MaxInt64), "the boundary itself is representable and must pass through")
	assert.Equal(t, int64(4096), reclaimedBytes(4096), "an ordinary value is unchanged")

	// End to end: a run whose cache prune reports an absurd figure still
	// records a non-negative row.
	docker := &fn7x2FakeDocker{cacheReclaimed: math.MaxUint64}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(docker, store)

	_, err := svc.Execute(context.Background(), "scheduled", 24)
	require.NoError(t, err)
	require.Len(t, store.runs, 1)
	assert.GreaterOrEqual(t, store.runs[0].CacheBytesReclaimed, int64(0), "a history row must never report negative bytes reclaimed")
	assert.Equal(t, int64(math.MaxInt64), store.runs[0].CacheBytesReclaimed)
}
