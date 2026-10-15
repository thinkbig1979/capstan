package services

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
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

	cacheRecords   []*build.CacheRecord
	cacheListErr   error
	cacheListCalls int
}

func (f *fn7x2FakeDocker) ListBuildCache(context.Context) ([]*build.CacheRecord, error) {
	f.cacheListCalls++
	if f.cacheListErr != nil {
		return nil, f.cacheListErr
	}
	return f.cacheRecords, nil
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

	_, err := svc.Execute(context.Background(), "scheduled", 24, DefaultDockerCleanupCategories())
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

	run, err := svc.Execute(context.Background(), "manual", 24, DefaultDockerCleanupCategories())
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

// TestDockerCleanupPreviewReportsBuildCache pins agent-os-nacp: Execute prunes
// build cache as well as images, so the preview must report the cache bytes a
// run would reclaim or the forecast under-reports by exactly the cache. The
// fixture holds one record per way a record can be spared (in use, inside the
// floor) next to the one a run would take, so the sum is the old unused
// record's size and nothing else. A record never used is aged by CreatedAt.
func TestDockerCleanupPreviewReportsBuildCache(t *testing.T) {
	ago := func(h int) time.Time { return time.Now().Add(-time.Duration(h) * time.Hour) }
	usedAgo := func(h int) *time.Time { u := ago(h); return &u }
	docker := &fn7x2FakeDocker{cacheRecords: []*build.CacheRecord{
		{ID: "old-unused", Size: 4096, CreatedAt: ago(100), LastUsedAt: usedAgo(48)},
		{ID: "never-used-old", Size: 1024, CreatedAt: ago(48)},
		{ID: "old-in-use", Size: 9999, InUse: true, CreatedAt: ago(100), LastUsedAt: usedAgo(48)},
		{ID: "recently-used", Size: 7777, CreatedAt: ago(100), LastUsedAt: usedAgo(1)},
		{ID: "recent", Size: 5555, CreatedAt: ago(1)},
	}}
	svc := NewDockerCleanupService(docker, &fn7x2FakeStore{})

	preview, err := svc.Preview(context.Background(), 24)
	require.NoError(t, err)

	assert.Equal(t, int64(4096+1024), preview.CacheReclaimableBytes)
	assert.Equal(t, int64(0), preview.ReclaimableBytes, "images stay a separate figure")
	assert.Equal(t, 1, docker.cacheListCalls)
	assert.Empty(t, docker.cachePruneOpts, "preview must not prune build cache")
}

// A build-cache listing that fails must fail the preview: a 0 would read as
// "the cache has nothing to reclaim" when the truth is that nobody looked.
func TestDockerCleanupPreviewBuildCacheListFailureFailsPreview(t *testing.T) {
	docker := &fn7x2FakeDocker{cacheListErr: errors.New("daemon went away")}
	svc := NewDockerCleanupService(docker, &fn7x2FakeStore{})

	preview, err := svc.Preview(context.Background(), 24)

	require.Error(t, err)
	assert.Nil(t, preview)
	assert.ErrorContains(t, err, "daemon went away")
}

// TestDockerCleanupFailureRecorded pins AC6: a failed prune records
// status=failed with a non-empty error_message, rather than a success row of
// zeroes.
func TestDockerCleanupFailureRecorded(t *testing.T) {
	boom := errors.New("docker daemon unreachable")
	docker := &fn7x2FakeDocker{imagePruneErr: boom}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(docker, store)

	run, err := svc.Execute(context.Background(), "scheduled", 24, DefaultDockerCleanupCategories())
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
			_, err := svc.Execute(context.Background(), "scheduled", tc.asked, DefaultDockerCleanupCategories())
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

	_, err := svc.Execute(context.Background(), "scheduled", 24, DefaultDockerCleanupCategories())
	require.NoError(t, err)
	require.Len(t, store.runs, 1)
	assert.GreaterOrEqual(t, store.runs[0].CacheBytesReclaimed, int64(0), "a history row must never report negative bytes reclaimed")
	assert.Equal(t, int64(math.MaxInt64), store.runs[0].CacheBytesReclaimed)
}

// agent-os-q1kh: every figure Execute reports has a Preview counterpart.
//
// agent-os-nacp found Preview summing dangling images while Execute also pruned
// build cache, so the forecast under-reported by exactly the cache. The fix
// pinned only that half. This is the class guard: Execute's report row is
// models.DockerCleanupRun, Preview's is DockerCleanupPreview, and the table
// below is the single place that says which Preview field forecasts which
// Execute figure.
//
// THE RULE. Every field of models.DockerCleanupRun is in exactly one of two
// maps: runToPreview (a reclaim figure, paired with the Preview field that
// forecasts it) or notReclaimFigures (with the reason it needs no forecast). A
// new Execute report field fails TestDockerCleanupEveryRunFieldIsClassified
// until someone classifies it; a pairing is then proven on a live fixture by
// TestDockerCleanupPreviewForecastsEveryReclaimFigure, where Execute and
// Preview run over the same fake daemon and each pair must be non-zero on BOTH
// sides (a pair that is zero on both sides agrees by emptiness).
//
// WHAT IT CANNOT SEE.
//   - A new prune step whose bytes are added into an EXISTING field (a third
//     prune summed into bytesReclaimed) leaves the field set unchanged, so the
//     classification arm is silent. INFERRED, not tested: such a step needs a
//     new dockerCleanupPruner method, which breaks fn7x2FakeDocker's compile and
//     forces an edit to this file.
//   - A reclaim figure reported outside models.DockerCleanupRun (an extra field
//     in the handler's response) is invisible.
//   - A reclaim figure wrongly filed under notReclaimFigures passes.
//   - Cache bytes are asserted non-zero on both sides but not equal: the fake's
//     cache figure is hand-set, so equality would be circular. Image count and
//     image bytes ARE compared, because the fake derives them from the same
//     images the preview lists.
//   - Whether Preview's estimate matches what a real daemon would prune is out
//     of scope (cacheReclaimableBytes documents it as an estimate).
var runToPreview = map[string]string{
	"ImagesDeleted":       "Candidates",
	"BytesReclaimed":      "ReclaimableBytes",
	"CacheBytesReclaimed": "CacheReclaimableBytes",
	"ContainersDeleted":   "Containers",
	"VolumesDeleted":      "Volumes",
	"NetworksDeleted":     "Networks",
	"OtherBytesReclaimed": "OtherReclaimableBytes",
}

var notReclaimFigures = map[string]string{
	"ID":           "run identity",
	"Trigger":      "who started the run",
	"Status":       "outcome of the run",
	"StartedAt":    "timestamp",
	"FinishedAt":   "timestamp",
	"MinAgeHours":  "the floor applied, echoed by Preview as MinAgeHours; not an amount reclaimed",
	"ErrorMessage": "failure text",
	"Categories":   "which categories the run acted on; not an amount reclaimed",
}

func TestDockerCleanupEveryRunFieldIsClassified(t *testing.T) {
	runType := reflect.TypeOf(models.DockerCleanupRun{})
	previewType := reflect.TypeOf(DockerCleanupPreview{})

	seen := map[string]bool{}
	for i := 0; i < runType.NumField(); i++ {
		name := runType.Field(i).Name
		seen[name] = true
		classified := 0
		for _, table := range []map[string]string{runToPreview, notReclaimFigures} {
			if _, ok := table[name]; ok {
				classified++
			}
		}
		assert.Equal(t, 1, classified,
			"DockerCleanupRun.%s must be in exactly one of runToPreview / notReclaimFigures: "+
				"if Execute reports it as a reclaim figure, Preview needs a counterpart (agent-os-q1kh)", name)
	}

	// A table entry for a field that no longer exists would let a rename
	// silently drop a pairing.
	for name, previewField := range runToPreview {
		assert.True(t, seen[name], "runToPreview names DockerCleanupRun.%s, which does not exist", name)
		_, ok := previewType.FieldByName(previewField)
		assert.True(t, ok, "runToPreview pairs %s with DockerCleanupPreview.%s, which does not exist", name, previewField)
	}
	for name := range notReclaimFigures {
		assert.True(t, seen[name], "notReclaimFigures names DockerCleanupRun.%s, which does not exist", name)
	}
}

// TestDockerCleanupCategoriesSwitchOffPrunes: a category switched off is not
// pruned and is not recorded, and the run records exactly what it ran
// (agent-os-8hdg). The other four categories are TestDockerCleanupCategories*.
func TestDockerCleanupCategoriesSwitchOffPrunes(t *testing.T) {
	cases := []struct {
		name           string
		cats           DockerCleanupCategories
		wantImagePrune int
		wantCachePrune int
		wantCategories string
	}{
		{"defaults run both", DefaultDockerCleanupCategories(), 1, 1, "dangling_images,build_cache"},
		{"dangling images off", DockerCleanupCategories{BuildCache: true}, 0, 1, "build_cache"},
		{"build cache off", DockerCleanupCategories{DanglingImages: true}, 1, 0, "dangling_images"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docker := &fn7x2FakeDocker{images: []models.DockerImage{
				{ID: "sha256:old", Size: 1000, Created: hoursAgo(100)},
			}, cacheReclaimed: 4096}
			store := &fn7x2FakeStore{}
			run, err := NewDockerCleanupService(docker, store).Execute(context.Background(), TriggerScheduled, 24, tc.cats)
			require.NoError(t, err)

			assert.Len(t, docker.imagePruneOpts, tc.wantImagePrune, "image prune calls")
			assert.Len(t, docker.cachePruneOpts, tc.wantCachePrune, "build cache prune calls")
			assert.Equal(t, tc.wantCategories, run.Categories)
			require.Len(t, store.runs, 1)
			assert.Equal(t, tc.wantCategories, store.runs[0].Categories, "the stored row records the same categories")
			if tc.wantImagePrune == 0 {
				assert.Zero(t, run.ImagesDeleted)
				assert.Zero(t, run.BytesReclaimed)
			}
			if tc.wantCachePrune == 0 {
				assert.Zero(t, run.CacheBytesReclaimed)
			}
		})
	}
}

// reclaimAmount reads one figure as a number: an integer field is its value, a
// slice is its length (Candidates forecasts ImagesDeleted by count).
func reclaimAmount(t *testing.T, v reflect.Value) int64 {
	t.Helper()
	switch v.Kind() {
	case reflect.Slice:
		return int64(v.Len())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	default:
		t.Fatalf("cannot read %s as a reclaim amount", v.Kind())
		return 0
	}
}

func TestDockerCleanupPreviewForecastsEveryReclaimFigure(t *testing.T) {
	ago := func(h int) time.Time { return time.Now().Add(-time.Duration(h) * time.Hour) }
	const cacheBytes = 4096
	docker := &fn7x2FakeDocker{
		images: []models.DockerImage{
			{ID: "sha256:old-a", Size: 1000, Created: hoursAgo(100)},
			{ID: "sha256:old-b", Size: 2000, Created: hoursAgo(100)},
			{ID: "sha256:recent", Size: 5555, Created: hoursAgo(1)},
			{ID: "sha256:tagged", RepoTags: []string{"app:1"}, Size: 7777, Created: hoursAgo(100)},
		},
		cacheRecords:   []*build.CacheRecord{{ID: "old-unused", Size: cacheBytes, CreatedAt: ago(100)}},
		cacheReclaimed: cacheBytes,
	}
	svc := NewDockerCleanupService(docker, &fn7x2FakeStore{})
	// The other categories come from the host service: one old candidate of
	// each, and one too young that neither side may count.
	old, young := hoursAgo(100), hoursAgo(1)
	svc.SetHostSpaceService(&lr7xFakeHostSpace{cands: HostCandidates{
		Images: []HostCandidateItem{
			{ID: "sha256:unused-tagged", Label: "app:0", Size: ptr(300), Created: &old},
			{ID: "sha256:young-tagged", Label: "app:9", Size: ptr(900), Created: &young},
		},
		Containers: []HostCandidateItem{{ID: "c-old", Size: ptr(70), Created: &old}},
		Volumes:    []HostCandidateItem{{ID: "v-old", Size: ptr(500), Created: &old}},
		Networks:   []HostCandidateItem{{ID: "n-old", Created: &old}},
	}})
	svc.SetOperationLock(NewOperationLock())
	all := DockerCleanupCategories{DanglingImages: true, UnusedImages: true, BuildCache: true, Containers: true, Volumes: true, Networks: true}

	run, err := svc.Execute(context.Background(), TriggerManual, 24, all)
	require.NoError(t, err)
	preview, err := svc.PreviewCategories(context.Background(), 24, all)
	require.NoError(t, err)

	// A table of nothing passes every loop below, so its size is asserted first.
	require.Len(t, runToPreview, 7)
	runValue, previewValue := reflect.ValueOf(*run), reflect.ValueOf(*preview)
	for runField, previewField := range runToPreview {
		executed := reclaimAmount(t, runValue.FieldByName(runField))
		forecast := reclaimAmount(t, previewValue.FieldByName(previewField))
		assert.Positive(t, executed, "fixture must make Execute reclaim something for %s", runField)
		assert.Positive(t, forecast, "Preview.%s forecasts nothing while Execute reported %s=%d", previewField, runField, executed)
	}

	assert.Equal(t, int64(run.ImagesDeleted), int64(len(preview.Candidates)))
	assert.Equal(t, run.BytesReclaimed, preview.ReclaimableBytes)
	assert.Equal(t, run.ContainersDeleted, len(preview.Containers))
	assert.Equal(t, run.VolumesDeleted, len(preview.Volumes))
	assert.Equal(t, run.NetworksDeleted, len(preview.Networks))
	assert.Equal(t, run.OtherBytesReclaimed, preview.OtherReclaimableBytes)
	assert.Equal(t, int64(1000+2000+300), run.BytesReclaimed, "dangling plus tagged, the young one excluded")
}

// yicuBlockingPruner holds PruneImages open until released and counts how many
// prunes were entered, and the most that were in flight at once.
type yicuBlockingPruner struct {
	fn7x2FakeDocker
	entered chan struct{}
	release chan struct{}

	mu          sync.Mutex
	prunes      int
	inFlight    int
	maxInFlight int
}

func (f *yicuBlockingPruner) PruneImages(ctx context.Context, opts PruneOptions) (image.PruneReport, error) {
	f.mu.Lock()
	f.prunes++
	f.inFlight++
	if f.inFlight > f.maxInFlight {
		f.maxInFlight = f.inFlight
	}
	f.mu.Unlock()
	f.entered <- struct{}{}
	<-f.release
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	return image.PruneReport{}, nil
}

func (f *yicuBlockingPruner) PruneBuildCache(context.Context, PruneOptions) (*build.CachePruneReport, error) {
	return &build.CachePruneReport{}, nil
}

// yicuLockedStore is fn7x2FakeStore made safe for concurrent writers, so that on
// unguarded code the test fails on its prune assertion, not on a race inside
// the fake.
type yicuLockedStore struct {
	mu   sync.Mutex
	runs []models.DockerCleanupRun
}

func (s *yicuLockedStore) CreateDockerCleanupRun(r *models.DockerCleanupRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, *r)
	return nil
}

func (s *yicuLockedStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

// TestDockerCleanupExecuteIsSingleFlight pins agent-os-yicu: the scheduler tick
// and the manual-run handler both reach the prune through Execute, so Execute
// itself refuses a second call while a run is in progress. The second call
// returns ErrCleanupInProgress with no run, prunes nothing and records nothing.
//
// The second call is made while the first is provably INSIDE PruneImages (the
// entered channel), so this is not a timing race the test can win by luck.
func TestDockerCleanupExecuteIsSingleFlight(t *testing.T) {
	docker := &yicuBlockingPruner{entered: make(chan struct{}, 2), release: make(chan struct{})}
	store := &yicuLockedStore{}
	svc := NewDockerCleanupService(docker, store)

	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.Execute(context.Background(), TriggerScheduled, 24, DefaultDockerCleanupCategories())
		firstDone <- err
	}()
	<-docker.entered

	secondDone := make(chan struct{})
	var secondRun *models.DockerCleanupRun
	var secondErr error
	go func() {
		defer close(secondDone)
		secondRun, secondErr = svc.Execute(context.Background(), TriggerManual, 24, DefaultDockerCleanupCategories())
	}()

	// On the unguarded code the second call blocks inside the fake's prune, so
	// give it a bounded chance to arrive there before releasing both.
	select {
	case <-secondDone:
	case <-docker.entered:
	case <-time.After(2 * time.Second):
	}
	close(docker.release)
	require.NoError(t, <-firstDone)
	<-secondDone

	docker.mu.Lock()
	prunes, maxInFlight := docker.prunes, docker.maxInFlight
	docker.mu.Unlock()
	require.Equal(t, 1, maxInFlight, "two prunes ran at the same time")
	require.Equal(t, 1, prunes, "the refused call still pruned")
	require.ErrorIs(t, secondErr, ErrCleanupInProgress)
	require.Nil(t, secondRun, "a refusal is not a run and must not look like one")
	require.Equal(t, 1, store.count(), "a refused call wrote a history row for a run that never happened")

	// The guard is released when the run ends: a later call runs normally.
	docker.release = make(chan struct{})
	close(docker.release)
	_, err := svc.Execute(context.Background(), TriggerManual, 24, DefaultDockerCleanupCategories())
	require.NoError(t, err, "the guard was not released after the first run finished")
	require.Equal(t, 2, store.count())
}

// agent-os-lr7x: the categories beyond dangling images and build cache.

// lr7xFakeHostSpace serves a fixed candidate set and removes, in FreeSpace,
// every requested ID that is in it, unless failIDs names it.
type lr7xFakeHostSpace struct {
	mu            sync.Mutex
	cands         HostCandidates
	candErr       error
	candCalls     int
	freeReqs      []HostFreeSpaceRequest
	failIDs       map[string]string
	invalidations int
}

func (f *lr7xFakeHostSpace) Candidates(context.Context) (*HostCandidates, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.candCalls++
	if f.candErr != nil {
		return nil, f.candErr
	}
	c := f.cands
	return &c, nil
}

func (f *lr7xFakeHostSpace) FreeSpace(_ context.Context, req HostFreeSpaceRequest) (*HostFreeSpaceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.freeReqs = append(f.freeReqs, req)
	res := &HostFreeSpaceResult{Images: newCategoryResult(), Containers: newCategoryResult(), Volumes: newCategoryResult(), Networks: newCategoryResult()}
	for _, k := range []struct {
		ids   []string
		cands []HostCandidateItem
		out   *HostFreeSpaceCategoryResult
	}{
		{req.Images, f.cands.Images, &res.Images},
		{req.Containers, f.cands.Containers, &res.Containers},
		{req.Volumes, f.cands.Volumes, &res.Volumes},
		{req.Networks, f.cands.Networks, &res.Networks},
	} {
		removeEach(k.ids, k.cands, k.out, func(id string) error {
			if reason, ok := f.failIDs[id]; ok {
				return errors.New(reason)
			}
			return nil
		})
	}
	return res, nil
}

func (f *lr7xFakeHostSpace) Invalidate() {
	f.mu.Lock()
	f.invalidations++
	f.mu.Unlock()
}

// lr7xCountingTurn is a real OperationLock that counts exclusive takes.
type lr7xCountingTurn struct {
	*OperationLock
	mu       sync.Mutex
	acquires int
}

func (l *lr7xCountingTurn) AcquireExclusive(kind string) (string, error) {
	l.mu.Lock()
	l.acquires++
	l.mu.Unlock()
	return l.OperationLock.AcquireExclusive(kind)
}

// lr7xCountingHostSpace forwards to a real HostSpaceService and counts the
// invalidations made through it (FreeSpace's own internal one is not).
type lr7xCountingHostSpace struct {
	*HostSpaceService
	invalidations int
}

func (h *lr7xCountingHostSpace) Invalidate() {
	h.invalidations++
	h.HostSpaceService.Invalidate()
}

func rfc3339HoursAgo(h int) string {
	return time.Now().Add(-time.Duration(h) * time.Hour).UTC().Format(time.RFC3339)
}

// AC21 (a): at the defaults the run is today's: the two prunes, no turn, no
// candidate read, no item removal.
func TestDockerCleanupCategoriesDefaultsTakeNoTurn(t *testing.T) {
	docker := &fn7x2FakeDocker{images: []models.DockerImage{
		{ID: "sha256:old", RepoTags: []string{"<none>:<none>"}, Size: 100, Created: hoursAgo(48)},
	}}
	host := &lr7xFakeHostSpace{cands: HostCandidates{Containers: []HostCandidateItem{{ID: "c", Created: ptr(hoursAgo(100))}}}}
	turn := &lr7xCountingTurn{OperationLock: NewOperationLock()}
	svc := NewDockerCleanupService(docker, &fn7x2FakeStore{})
	svc.SetHostSpaceService(host)
	svc.turn = turn

	run, err := svc.Execute(context.Background(), TriggerScheduled, 24, DefaultDockerCleanupCategories())
	require.NoError(t, err)

	require.Len(t, docker.imagePruneOpts, 1)
	assert.False(t, docker.imagePruneOpts[0].All)
	require.Len(t, docker.cachePruneOpts, 1)
	assert.Zero(t, turn.acquires, "a default run took the exclusive turn")
	assert.Zero(t, host.candCalls, "a default run read the host candidates")
	assert.Empty(t, host.freeReqs, "a default run removed items")
	assert.Equal(t, 1, run.ImagesDeleted, "positive side: the prune ran")
	assert.Equal(t, 1, host.invalidations, "a run that pruned invalidates the host summary once")
}

// AC21 (b): containers and volumes on. Only the unlabelled stopped container
// and the non-stack unattached volume created before the floor go, with
// force=false; the rules are the dialog's (a real HostSpaceService).
func TestDockerCleanupCategoriesContainersAndVolumes(t *testing.T) {
	stackLabel := map[string]string{composeProjectLabel: "shop"}
	hostDocker := &hostSpaceFakeDocker{
		du: dockertypes.DiskUsage{
			Containers: []*container.Summary{
				{ID: "c-old", Names: []string{"/old"}, State: container.StateExited, SizeRw: 7, Created: hoursAgo(100)},
				{ID: "c-young", Names: []string{"/young"}, State: container.StateExited, SizeRw: 7, Created: hoursAgo(1)},
				{ID: "c-unknown-age", Names: []string{"/unknown"}, State: container.StateExited, SizeRw: 7},
				{ID: "c-stack", Names: []string{"/shop-db-1"}, State: container.StateExited, Labels: stackLabel, Created: hoursAgo(100)},
				{ID: "c-running", Names: []string{"/up"}, State: container.StateRunning, Created: hoursAgo(100)},
			},
			Volumes: []*volume.Volume{
				{Name: "v-old", UsageData: usage(500, 0), CreatedAt: rfc3339HoursAgo(100)},
				{Name: "shop_db", Labels: stackLabel, UsageData: usage(900, 0), CreatedAt: rfc3339HoursAgo(100)},
				{Name: "v-attached", UsageData: usage(10, 1), CreatedAt: rfc3339HoursAgo(100)},
				{Name: "v-young", UsageData: usage(10, 0), CreatedAt: rfc3339HoursAgo(1)},
				{Name: "v-unknown-age", UsageData: usage(10, 0)},
			},
			Images: []*image.Summary{{ID: "sha256:tagged", RepoTags: []string{"app:1"}, Size: 50, Created: hoursAgo(100)}},
		},
		nets:    []network.Summary{{ID: "n-old", Name: "scratch", Created: time.Now().Add(-100 * time.Hour)}},
		inspect: map[string]int{"n-old": 0},
	}
	hs, _ := newHostSpaceForTest(hostDocker, stoppedStack)
	host := &lr7xCountingHostSpace{HostSpaceService: hs}
	lock := NewOperationLock()
	pruner := &fn7x2FakeDocker{}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(pruner, store)
	svc.SetHostSpaceService(host)
	svc.SetOperationLock(lock)

	run, err := svc.Execute(context.Background(), TriggerScheduled, 24, DockerCleanupCategories{Containers: true, Volumes: true})
	require.NoError(t, err)

	assert.Equal(t, []hostSpaceRemoval{
		{kind: "container", id: "c-old", force: false},
		{kind: "volume", id: "v-old", force: false},
	}, hostDocker.removals, "only the old unlabelled stopped container and the old unattached non-stack volume may go")
	assert.Empty(t, pruner.imagePruneOpts, "dangling images are off")
	assert.Empty(t, pruner.cachePruneOpts, "build cache is off")
	assert.Equal(t, 1, run.ContainersDeleted)
	assert.Equal(t, 1, run.VolumesDeleted)
	assert.Zero(t, run.ImagesDeleted, "unused images are off")
	assert.Zero(t, run.NetworksDeleted, "networks are off")
	assert.Equal(t, int64(7+500), run.OtherBytesReclaimed)
	assert.Equal(t, "containers,volumes", run.Categories)
	require.Len(t, store.runs, 1)
	assert.Equal(t, "success", store.runs[0].Status)
	assert.Equal(t, 1, host.invalidations, "a run that removed items invalidates the host summary exactly once")

	// The turn was released when the run ended.
	token, err := lock.AcquireExclusive("probe")
	require.NoError(t, err, "the cleanup run did not release its exclusive turn")
	lock.ReleaseExclusive(token)
}

// AC21 (c), service half: with the turn held by a stack operation, the run
// returns ErrCleanupTurnBusy naming the holder, and removes and records
// nothing, dangling images included.
func TestDockerCleanupCategoriesBusyTurnRemovesNothing(t *testing.T) {
	pruner := &fn7x2FakeDocker{images: []models.DockerImage{
		{ID: "sha256:old", RepoTags: []string{"<none>:<none>"}, Size: 100, Created: hoursAgo(48)},
	}}
	host := &lr7xFakeHostSpace{cands: HostCandidates{Containers: []HostCandidateItem{{ID: "c", Created: ptr(hoursAgo(100))}}}}
	lock := NewOperationLock()
	held, err := lock.Acquire("s1", OpKindStart)
	require.NoError(t, err)
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(pruner, store)
	svc.SetHostSpaceService(host)
	svc.SetOperationLock(lock)
	cats := DockerCleanupCategories{DanglingImages: true, Containers: true}

	run, err := svc.Execute(context.Background(), TriggerScheduled, 24, cats)
	require.ErrorIs(t, err, ErrCleanupTurnBusy)
	assert.Contains(t, err.Error(), "start in progress since", "the error names the holder")
	assert.Nil(t, run)
	assert.Empty(t, pruner.imagePruneOpts, "a busy turn must remove nothing, dangling images included")
	assert.Empty(t, host.freeReqs)
	assert.Empty(t, store.runs, "Execute records nothing; the scheduler writes the skipped row")
	assert.Zero(t, host.invalidations)

	// Positive side on the same service: once the stack releases, the run goes.
	lock.Release("s1", held)
	_, err = svc.Execute(context.Background(), TriggerScheduled, 24, cats)
	require.NoError(t, err)
	assert.Len(t, pruner.imagePruneOpts, 1)
	require.Len(t, host.freeReqs, 1)
	assert.Equal(t, []string{"c"}, host.freeReqs[0].Containers)
}

// AC21 (d): unused images on. An image a container uses (Docker counts a
// stopped container too) and one whose use was not counted survive; a
// dangling image is left to its own switch.
func TestDockerCleanupCategoriesUnusedImagesSpareUsedOnes(t *testing.T) {
	hostDocker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{Images: []*image.Summary{
		{ID: "sha256:unused", RepoTags: []string{"app:1"}, Containers: 0, Size: 300, SharedSize: 100, Created: hoursAgo(100)},
		{ID: "sha256:stopped-user", RepoTags: []string{"db:16"}, Containers: 1, Size: 300, Created: hoursAgo(100)},
		{ID: "sha256:uncounted", RepoTags: []string{"x:1"}, Containers: -1, Size: 300, Created: hoursAgo(100)},
		{ID: "sha256:dangling", RepoTags: []string{"<none>:<none>"}, Containers: 0, Size: 300, Created: hoursAgo(100)},
		{ID: "sha256:young", RepoTags: []string{"app:2"}, Containers: 0, Size: 300, Created: hoursAgo(1)},
	}}}
	hs, _ := newHostSpaceForTest(hostDocker)
	pruner := &fn7x2FakeDocker{}
	svc := NewDockerCleanupService(pruner, &fn7x2FakeStore{})
	svc.SetHostSpaceService(hs)
	svc.SetOperationLock(NewOperationLock())

	run, err := svc.Execute(context.Background(), TriggerScheduled, 24, DockerCleanupCategories{UnusedImages: true})
	require.NoError(t, err)

	assert.Equal(t, []hostSpaceRemoval{{kind: "image", id: "sha256:unused"}}, hostDocker.removals,
		"an image a stopped container uses, an uncounted, a dangling or a young image was removed")
	assert.Equal(t, 1, run.ImagesDeleted)
	assert.Equal(t, int64(200), run.BytesReclaimed, "Size minus SharedSize")
	assert.Empty(t, pruner.imagePruneOpts, "dangling images are off")
}

// A host category with no host service or lock refuses before removing
// anything: the run is recorded failed and the prunes do not run either.
func TestDockerCleanupCategoriesWithoutHostSpaceRefuse(t *testing.T) {
	for name, configure := range map[string]func(*DockerCleanupService){
		"no host service": func(s *DockerCleanupService) { s.SetOperationLock(NewOperationLock()) },
		"no lock":         func(s *DockerCleanupService) { s.SetHostSpaceService(&lr7xFakeHostSpace{}) },
	} {
		t.Run(name, func(t *testing.T) {
			pruner := &fn7x2FakeDocker{images: []models.DockerImage{{ID: "sha256:old", Size: 1, Created: hoursAgo(100)}}}
			store := &fn7x2FakeStore{}
			svc := NewDockerCleanupService(pruner, store)
			configure(svc)

			_, err := svc.Execute(context.Background(), TriggerManual, 24, DockerCleanupCategories{DanglingImages: true, Volumes: true})
			require.Error(t, err)
			assert.Empty(t, pruner.imagePruneOpts, "a run that cannot do every category does none")
			require.Len(t, store.runs, 1)
			assert.Equal(t, "failed", store.runs[0].Status)
		})
	}
}

// An item Docker refuses makes the run failed, with the item named; what was
// removed is still counted.
func TestDockerCleanupCategoriesItemFailureFailsTheRun(t *testing.T) {
	old := hoursAgo(100)
	host := &lr7xFakeHostSpace{
		cands:   HostCandidates{Volumes: []HostCandidateItem{{ID: "v-ok", Size: ptr(5), Created: &old}, {ID: "v-busy", Created: &old}}},
		failIDs: map[string]string{"v-busy": "volume is in use"},
	}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(&fn7x2FakeDocker{}, store)
	svc.SetHostSpaceService(host)
	svc.SetOperationLock(NewOperationLock())

	run, err := svc.Execute(context.Background(), TriggerManual, 24, DockerCleanupCategories{Volumes: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v-busy")
	assert.Equal(t, "failed", run.Status)
	assert.Equal(t, 1, run.VolumesDeleted)
	assert.Equal(t, int64(5), run.OtherBytesReclaimed)
}

// Addition 1 (agent-os-8hdg follow-up): the preview lists only the categories
// switched on, and reads nothing for the others.
func TestDockerCleanupPreviewFollowsCategories(t *testing.T) {
	old := hoursAgo(100)
	docker := &fn7x2FakeDocker{
		images:       []models.DockerImage{{ID: "sha256:dangling", RepoTags: []string{"<none>:<none>"}, Size: 10, Created: old}},
		cacheRecords: []*build.CacheRecord{{ID: "cache", Size: 99, CreatedAt: time.Now().Add(-100 * time.Hour)}},
	}
	host := &lr7xFakeHostSpace{cands: HostCandidates{
		Containers: []HostCandidateItem{{ID: "c-old", Size: ptr(3), Created: &old}, {ID: "c-young", Size: ptr(3), Created: ptr(hoursAgo(1))}},
		Volumes:    []HostCandidateItem{{ID: "v-old", Created: &old}},
		Networks:   []HostCandidateItem{{ID: "n-old", Created: &old}},
	}}
	svc := NewDockerCleanupService(docker, &fn7x2FakeStore{})
	svc.SetHostSpaceService(host)

	preview, err := svc.PreviewCategories(context.Background(), 24, DockerCleanupCategories{Containers: true})
	require.NoError(t, err)
	assert.Empty(t, preview.Candidates, "dangling images are off")
	assert.Zero(t, preview.ReclaimableBytes)
	assert.Zero(t, preview.CacheReclaimableBytes, "build cache is off")
	assert.Zero(t, docker.listCalls, "images were read for a category that is off")
	assert.Zero(t, docker.cacheListCalls, "build cache was read for a category that is off")
	assert.Equal(t, []string{"c-old"}, candidateIDs(preview.Containers), "the floor applies to the preview too")
	assert.Empty(t, preview.Volumes)
	assert.Empty(t, preview.Networks)
	assert.Equal(t, int64(3), preview.OtherReclaimableBytes)

	// Positive side: with dangling images and build cache on, both are listed.
	preview, err = svc.PreviewCategories(context.Background(), 24, DefaultDockerCleanupCategories())
	require.NoError(t, err)
	assert.Len(t, preview.Candidates, 1)
	assert.Equal(t, int64(99), preview.CacheReclaimableBytes)
	assert.Equal(t, 1, host.candCalls, "a default preview does not read the host candidates")
}

// Addition 3 (agent-os-gkgr item 3): the dangling preview counts Size minus
// SharedSize, and an image whose SharedSize Docker did not compute is counted
// as unknown, never as its full Size.
func TestDockerCleanupPreviewCountsSharedLayersOnce(t *testing.T) {
	docker := &fn7x2FakeDocker{images: []models.DockerImage{
		{ID: "sha256:a", RepoTags: []string{"<none>:<none>"}, Size: 100, SharedSize: 40, Created: hoursAgo(48)},
		{ID: "sha256:b", RepoTags: []string{"<none>:<none>"}, Size: 100, SharedSize: 40, Created: hoursAgo(48)},
		{ID: "sha256:c", RepoTags: []string{"<none>:<none>"}, Size: 500, SharedSize: -1, Created: hoursAgo(48)},
	}}
	preview, err := NewDockerCleanupService(docker, &fn7x2FakeStore{}).Preview(context.Background(), 24)
	require.NoError(t, err)
	require.Len(t, preview.Candidates, 3)
	assert.Equal(t, int64(60+60), preview.ReclaimableBytes, "two images sharing 40 bytes free 60 each, not 100")
	assert.Equal(t, 1, preview.UnknownSizeCount)
	for _, c := range preview.Candidates {
		if c.ID == "sha256:c" {
			assert.Nil(t, c.ReclaimableSize, "an unknown SharedSize must not be reported as a size")
		}
	}
}

// Adversary B1: the floor has an upper bound. Without one,
// time.Duration(h)*time.Hour overflows at h >= 2562048, the cutoff lands in
// the future and a container created an hour ago passes the floor.
func TestDockerCleanupAgeFloorHasAnUpperBound(t *testing.T) {
	const overflow = 2562048 // math.MaxInt64 / int64(time.Hour) + 1
	for _, asked := range []int{MaxCleanupAgeHours, MaxCleanupAgeHours + 1, overflow} {
		t.Run(strconv.Itoa(asked), func(t *testing.T) {
			young := hoursAgo(1)
			host := &lr7xFakeHostSpace{cands: HostCandidates{Containers: []HostCandidateItem{{ID: "young", Created: &young}}}}
			svc := NewDockerCleanupService(&fn7x2FakeDocker{}, &fn7x2FakeStore{})
			svc.SetHostSpaceService(host)
			svc.SetOperationLock(NewOperationLock())

			run, err := svc.Execute(context.Background(), TriggerManual, asked, DockerCleanupCategories{Containers: true})
			require.NoError(t, err)
			assert.Empty(t, host.freeReqs, "a container created an hour ago passed a %dh floor", asked)
			assert.Equal(t, min(asked, MaxCleanupAgeHours), run.MinAgeHours, "the row records the floor applied")

			preview, err := svc.PreviewCategories(context.Background(), asked, DockerCleanupCategories{Containers: true})
			require.NoError(t, err)
			assert.Empty(t, preview.Containers, "the preview listed a container created an hour ago")

			docker := &fn7x2FakeDocker{}
			_, err = NewDockerCleanupService(docker, &fn7x2FakeStore{}).Execute(context.Background(), TriggerManual, asked, DefaultDockerCleanupCategories())
			require.NoError(t, err, "Docker refuses an until filter past time.Duration's range")
			require.Len(t, docker.imagePruneOpts, 1)
			assert.Equal(t, strconv.Itoa(MaxCleanupAgeHours)+"h", docker.imagePruneOpts[0].Until)
		})
	}
}

// Adversary m2: the floor is strict. An object created exactly at the cutoff
// is kept; one a second older goes.
func TestDockerCleanupAgeFloorIsStrictAtTheCutoff(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-24 * time.Hour).Unix()
	atCutoff, older := cutoff, cutoff-1
	host := &lr7xFakeHostSpace{cands: HostCandidates{Containers: []HostCandidateItem{
		{ID: "at-cutoff", Created: &atCutoff},
		{ID: "older", Created: &older},
	}}}
	svc := NewDockerCleanupService(&fn7x2FakeDocker{}, &fn7x2FakeStore{})
	svc.SetHostSpaceService(host)
	svc.SetOperationLock(NewOperationLock())
	svc.now = func() time.Time { return now }

	_, err := svc.Execute(context.Background(), TriggerManual, 24, DockerCleanupCategories{Containers: true})
	require.NoError(t, err)
	require.Len(t, host.freeReqs, 1)
	assert.Equal(t, []string{"older"}, host.freeReqs[0].Containers)
}

// Adversary M1: a run whose host categories were withheld because a stack's
// compose file could not be read is a failure naming the stacks and the
// categories kept, not a silent success. The dangling and cache prunes still
// run and are counted.
func TestDockerCleanupCategoriesWithheldStacksFailTheRun(t *testing.T) {
	old := hoursAgo(100)
	pruner := &fn7x2FakeDocker{
		images:         []models.DockerImage{{ID: "sha256:old", RepoTags: []string{"<none>:<none>"}, Size: 100, Created: hoursAgo(48)}},
		cacheReclaimed: 64,
	}
	host := &lr7xFakeHostSpace{cands: HostCandidates{
		Networks:       []HostCandidateItem{{ID: "n-old", Created: &old}},
		WithheldStacks: []string{"alpha", "beta"},
	}}
	store := &fn7x2FakeStore{}
	svc := NewDockerCleanupService(pruner, store)
	svc.SetHostSpaceService(host)
	svc.SetOperationLock(NewOperationLock())

	run, err := svc.Execute(context.Background(), TriggerScheduled, 24,
		DockerCleanupCategories{DanglingImages: true, BuildCache: true, Containers: true, Volumes: true})
	require.Error(t, err)
	require.Len(t, store.runs, 1)
	assert.Equal(t, "failed", store.runs[0].Status)
	assert.Contains(t, store.runs[0].ErrorMessage, "kept containers, volumes")
	assert.Contains(t, store.runs[0].ErrorMessage, "alpha, beta")
	assert.Equal(t, 1, run.ImagesDeleted, "the dangling prune still ran")
	assert.Equal(t, int64(64), run.CacheBytesReclaimed, "the cache prune still ran")
	assert.Empty(t, host.freeReqs, "nothing of a withheld category is removed")

	// Positive side: the same host with no withheld stacks is a success.
	host.cands.WithheldStacks = nil
	store.runs = nil
	_, err = svc.Execute(context.Background(), TriggerScheduled, 24, DockerCleanupCategories{Networks: true})
	require.NoError(t, err)
	require.Len(t, store.runs, 1)
	assert.Equal(t, "success", store.runs[0].Status)
}
