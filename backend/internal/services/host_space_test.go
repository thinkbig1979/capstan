package services

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
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

// hostSpaceFakeDocker serves a fixed DiskUsage and network list. inspect maps a
// network ID to its attached-container count; an ID missing from it fails the
// inspect. block, when set, holds DiskUsage until it is closed.
type hostSpaceFakeDocker struct {
	mu        sync.Mutex
	du        dockertypes.DiskUsage
	duErr     error
	nets      []network.Summary
	netErr    error
	inspect   map[string]int
	block     chan struct{}
	entered   chan struct{}
	diskCalls int
}

func (f *hostSpaceFakeDocker) DiskUsage(_ context.Context, _ dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error) {
	f.mu.Lock()
	f.diskCalls++
	block, entered := f.block, f.entered
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if block != nil {
		<-block
	}
	return f.du, f.duErr
}

func (f *hostSpaceFakeDocker) NetworkList(_ context.Context, _ network.ListOptions) ([]network.Summary, error) {
	return f.nets, f.netErr
}

func (f *hostSpaceFakeDocker) NetworkInspect(_ context.Context, id string, _ network.InspectOptions) (network.Inspect, error) {
	n, ok := f.inspect[id]
	if !ok {
		return network.Inspect{}, errors.New("inspect failed")
	}
	attached := make(map[string]network.EndpointResource, n)
	for i := 0; i < n; i++ {
		attached[string(rune('a'+i))] = network.EndpointResource{}
	}
	return network.Inspect{Containers: attached}, nil
}

func (f *hostSpaceFakeDocker) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.diskCalls
}

type hostSpaceFakeStacks struct {
	stacks []models.Stack
	err    error
}

func (f *hostSpaceFakeStacks) ListStacks() ([]models.Stack, error) { return f.stacks, f.err }

type hostSpaceClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *hostSpaceClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *hostSpaceClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// stoppedStack is a Capstan stack that is not running: it still owns its
// volumes and networks.
var stoppedStack = models.Stack{ID: "s1", ProjectName: "shop", Status: "stopped"}

func newHostSpaceForTest(docker *hostSpaceFakeDocker, stacks ...models.Stack) (*HostSpaceService, *hostSpaceClock) {
	clock := &hostSpaceClock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	return newHostSpaceService(docker, &hostSpaceFakeStacks{stacks: stacks}, clock.now), clock
}

func candidateIDs(items []HostCandidateItem) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	sort.Strings(ids)
	return ids
}

func usage(size, refs int64) *volume.UsageData {
	return &volume.UsageData{Size: size, RefCount: refs}
}

func ptr(v int64) *int64 { return &v }

// (a) An image any container uses is never a candidate; Docker's -1 "not
// counted" is not 0.
func TestHostSpaceImages(t *testing.T) {
	docker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{Images: []*image.Summary{
		{ID: "sha256:used", RepoTags: []string{"app:1"}, Containers: 1, Size: 500, SharedSize: 100},
		{ID: "sha256:unused", RepoTags: []string{"app:0"}, Containers: 0, Size: 300, SharedSize: 100},
		{ID: "sha256:uncounted", Containers: -1, Size: 50, SharedSize: 0},
		{ID: "sha256:0123456789abcdef0123456789", RepoTags: []string{"<none>:<none>"}, Containers: 0, Size: 40, SharedSize: -1},
	}}}
	svc, _ := newHostSpaceForTest(docker)

	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"sha256:0123456789abcdef0123456789", "sha256:unused"}, candidateIDs(cand.Images),
		"an image used by a container, or whose use Docker did not count (-1), was listed")

	byID := map[string]HostCandidateItem{}
	for _, it := range cand.Images {
		byID[it.ID] = it
	}
	assert.Equal(t, HostCandidateItem{ID: "sha256:unused", Label: "app:0", Size: ptr(200)}, byID["sha256:unused"],
		"size is Size minus SharedSize")
	assert.Equal(t, HostCandidateItem{ID: "sha256:0123456789abcdef0123456789", Label: "0123456789abcdef012"}, byID["sha256:0123456789abcdef0123456789"],
		"an untagged image is labelled by its short ID, and SharedSize -1 leaves the size unknown")

	sum, err := svc.Summary(context.Background())
	require.NoError(t, err)
	assert.Equal(t, HostImageSummary{Count: 4, InUse: 1, ReclaimableCount: 2, ReclaimableBytes: 200}, sum.Images)
}

// (b) A stopped container with any compose project label is never a
// candidate, whether or not a stack here manages that project.
func TestHostSpaceContainers(t *testing.T) {
	compose := func(project string) map[string]string { return map[string]string{composeProjectLabel: project} }
	docker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{Containers: []*container.Summary{
		{ID: "exited-other-project", State: container.StateExited, Labels: compose("not-ours"), SizeRw: 1},
		{ID: "exited-stack", State: container.StateExited, Labels: compose("shop"), SizeRw: 1},
		{ID: "exited-empty-label", State: container.StateExited, Labels: compose(""), SizeRw: 1},
		{ID: "exited-oneoff", Names: []string{"/leftover"}, State: container.StateExited, Status: "Exited (0) 3 days ago", SizeRw: 7},
		{ID: "created-oneoff", State: container.StateCreated, SizeRw: 0},
		{ID: "dead-oneoff", State: container.StateDead, SizeRw: 2},
		{ID: "running-oneoff", State: container.StateRunning, SizeRw: 1},
		{ID: "paused-oneoff", State: container.StatePaused, SizeRw: 1},
		{ID: "restarting-oneoff", State: container.StateRestarting, SizeRw: 1},
		{ID: "removing-oneoff", State: container.StateRemoving, SizeRw: 1},
	}}}
	svc, _ := newHostSpaceForTest(docker, stoppedStack)

	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"created-oneoff", "dead-oneoff", "exited-oneoff"}, candidateIDs(cand.Containers),
		"a compose-labelled container, or one that is not stopped, was listed")

	for _, it := range cand.Containers {
		if it.ID == "exited-oneoff" {
			assert.Equal(t, HostCandidateItem{ID: "exited-oneoff", Label: "leftover", Size: ptr(7), Detail: "Exited (0) 3 days ago"}, it)
		}
	}

	sum, err := svc.Summary(context.Background())
	require.NoError(t, err)
	assert.Equal(t, HostContainerSummary{Count: 10, Running: 1, Stopped: 6, ReclaimableCount: 3, ReclaimableBytes: 9}, sum.Containers)
}

// (c) A volume owned by a stack (stack stopped), or whose reference count
// Docker did not compute, is never a candidate.
func TestHostSpaceVolumes(t *testing.T) {
	docker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{Volumes: []*volume.Volume{
		{Name: "shop_db", Labels: map[string]string{composeProjectLabel: "shop"}, UsageData: usage(1000, 0)},
		{Name: "loose", UsageData: usage(20, 0)},
		{Name: "other_data", Labels: map[string]string{composeProjectLabel: "not-ours"}, UsageData: usage(30, 0)},
		{Name: "unknown-refs", UsageData: usage(40, -1)},
		{Name: "unknown-size", UsageData: usage(-1, 0)},
		{Name: "no-usage"},
		{Name: "attached", UsageData: usage(50, 1)},
	}}}
	svc, _ := newHostSpaceForTest(docker, stoppedStack)

	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"loose", "other_data", "unknown-size"}, candidateIDs(cand.Volumes),
		"a stack-owned, attached or uncounted volume was listed")

	for _, it := range cand.Volumes {
		switch it.ID {
		case "other_data":
			assert.Equal(t, HostCandidateItem{ID: "other_data", Label: "other_data", Size: ptr(30), Detail: "not-ours"}, it)
		case "unknown-size":
			assert.Nil(t, it.Size, "a -1 size was sent as a measurement")
		}
	}

	sum, err := svc.Summary(context.Background())
	require.NoError(t, err)
	assert.Equal(t, HostVolumeSummary{Count: 7, Attached: 1, Bytes: 1140, SizeKnown: false, ReclaimableCount: 3, ReclaimableBytes: 50}, sum.Volumes)
}

// (d) Built-in and stack-owned networks are never candidates, and neither is
// one whose inspect failed (attachments unknown).
func TestHostSpaceNetworks(t *testing.T) {
	docker := &hostSpaceFakeDocker{
		nets: []network.Summary{
			{ID: "n-bridge", Name: "bridge"},
			{ID: "n-host", Name: "host"},
			{ID: "n-none", Name: "none"},
			{ID: "n-ingress", Name: "ingress"},
			{ID: "n-gw", Name: "docker_gwbridge"},
			{ID: "n-stack", Name: "shop_default", Labels: map[string]string{composeProjectLabel: "shop"}},
			{ID: "n-free", Name: "scratch", Driver: "bridge"},
			{ID: "n-other", Name: "other_default", Driver: "bridge", Labels: map[string]string{composeProjectLabel: "not-ours"}},
			{ID: "n-attached", Name: "busy"},
			{ID: "n-uninspectable", Name: "mystery"},
		},
		inspect: map[string]int{
			"n-bridge": 0, "n-host": 0, "n-none": 0, "n-ingress": 0, "n-gw": 0,
			"n-stack": 0, "n-free": 0, "n-other": 0, "n-attached": 1,
		},
	}
	svc, _ := newHostSpaceForTest(docker, stoppedStack)

	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"n-free", "n-other"}, candidateIDs(cand.Networks),
		"a built-in, stack-owned, attached or uninspectable network was listed")
	for _, it := range cand.Networks {
		if it.ID == "n-free" {
			assert.Equal(t, HostCandidateItem{ID: "n-free", Label: "scratch", Detail: "bridge"}, it)
		}
	}

	sum, err := svc.Summary(context.Background())
	require.NoError(t, err)
	assert.Equal(t, HostNetworkSummary{Count: 10, BuiltIn: 5, StackOwned: 1, ReclaimableCount: 2}, sum.Networks)
}

// (e) The headline is what one click frees: images, build cache and
// containers. Volumes only join reclaimableAll.
func TestHostSpaceReclaimableTotals(t *testing.T) {
	docker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{
		LayersSize: 900,
		Images:     []*image.Summary{{ID: "sha256:unused", Containers: 0, Size: 150, SharedSize: 50}},
		BuildCache: []*build.CacheRecord{
			{ID: "free", Size: 10},
			{ID: "in-use", Size: 5, InUse: true},
			{ID: "shared", Size: 7, Shared: true},
		},
		Containers: []*container.Summary{{ID: "leftover", State: container.StateExited, SizeRw: 3}},
		Volumes:    []*volume.Volume{{Name: "loose", UsageData: usage(1000, 0)}},
	}}
	svc, _ := newHostSpaceForTest(docker)

	sum, err := svc.Summary(context.Background())
	require.NoError(t, err)
	// total = 900 layers + 3 container + 1000 volume + 22 cache
	assert.Equal(t, HostDiskSummary{Total: 1925, InUse: 1925 - 113, Reclaimable: 113, ReclaimableAll: 1113, VolumesSizeKnown: true}, sum.Disk)
	assert.Equal(t, HostBuildCacheSummary{Count: 3, Bytes: 22, ReclaimableBytes: 10}, sum.BuildCache)
	assert.Equal(t, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), sum.MeasuredAt)

	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)
	assert.Equal(t, HostBuildCacheCandidates{Count: 1, Bytes: 10}, cand.BuildCache, "in-use or shared cache was counted")
}

// (f) A failed read is an error, never a zero summary, and is not cached.
func TestHostSpaceReadErrorIsNotAZeroSummary(t *testing.T) {
	boom := errors.New("daemon said no")
	cases := map[string]func(*hostSpaceFakeDocker, *hostSpaceFakeStacks){
		"disk usage": func(d *hostSpaceFakeDocker, _ *hostSpaceFakeStacks) { d.duErr = boom },
		"networks":   func(d *hostSpaceFakeDocker, _ *hostSpaceFakeStacks) { d.netErr = boom },
		"stacks":     func(_ *hostSpaceFakeDocker, s *hostSpaceFakeStacks) { s.err = boom },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			docker := &hostSpaceFakeDocker{du: dockertypes.DiskUsage{LayersSize: 10}}
			stacks := &hostSpaceFakeStacks{}
			breakIt(docker, stacks)
			svc := newHostSpaceService(docker, stacks, time.Now)

			sum, err := svc.Summary(context.Background())
			require.ErrorIs(t, err, boom)
			assert.Nil(t, sum)
			cand, err := svc.Candidates(context.Background())
			require.ErrorIs(t, err, boom)
			assert.Nil(t, cand)

			// The other side: once the read recovers, the next call answers,
			// so the failure was not stored as the snapshot.
			docker.duErr, docker.netErr, stacks.err = nil, nil, nil
			sum, err = svc.Summary(context.Background())
			require.NoError(t, err)
			assert.Equal(t, int64(10), sum.Disk.Total)
		})
	}
}

func TestHostSpaceSnapshotCache(t *testing.T) {
	docker := &hostSpaceFakeDocker{}
	svc, clock := newHostSpaceForTest(docker)
	ctx := context.Background()

	_, err := svc.Summary(ctx)
	require.NoError(t, err)
	clock.advance(hostSpaceSnapshotTTL - time.Second)
	_, err = svc.Summary(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, docker.calls(), "a summary younger than the TTL read Docker again")

	clock.advance(time.Second)
	_, err = svc.Summary(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, docker.calls(), "a summary TTL old was served from the snapshot")

	svc.Invalidate()
	_, err = svc.Summary(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, docker.calls(), "Summary after Invalidate served the old snapshot")

	_, err = svc.Candidates(ctx)
	require.NoError(t, err)
	assert.Equal(t, 4, docker.calls(), "Candidates was served from the snapshot")

	// Candidates' fresh read becomes the snapshot Summary serves.
	docker.du = dockertypes.DiskUsage{LayersSize: 77}
	_, err = svc.Candidates(ctx)
	require.NoError(t, err)
	sum, err := svc.Summary(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, docker.calls())
	assert.Equal(t, int64(77), sum.Disk.Total)
}

// A fetch that was in flight when Invalidate ran must not become the snapshot:
// it may predate the removal that triggered the Invalidate.
func TestHostSpaceInvalidateDuringFetchIsNotStored(t *testing.T) {
	docker := &hostSpaceFakeDocker{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	svc, _ := newHostSpaceForTest(docker)

	done := make(chan error, 1)
	go func() {
		_, err := svc.Summary(context.Background())
		done <- err
	}()
	<-docker.entered
	svc.Invalidate()
	close(docker.block)
	require.NoError(t, <-done)

	docker.mu.Lock()
	docker.block, docker.entered = nil, nil
	docker.mu.Unlock()
	_, err := svc.Summary(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, docker.calls(), "the pre-Invalidate fetch was stored and served")
}

// Callers that queue behind a fetch reuse its result: one Docker read.
// If the second caller has not reached the queue when the first finishes, it
// finds the fresh snapshot and still makes no read, so this cannot go red by
// timing, only by a missing re-check.
func TestHostSpaceQueuedCallersShareOneRead(t *testing.T) {
	docker := &hostSpaceFakeDocker{block: make(chan struct{}), entered: make(chan struct{}, 2)}
	svc, _ := newHostSpaceForTest(docker)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Summary(context.Background())
			assert.NoError(t, err)
		}()
	}
	<-docker.entered
	time.Sleep(50 * time.Millisecond) // let the others queue
	close(docker.block)
	wg.Wait()
	assert.Equal(t, 1, docker.calls())
}

func TestHostSpaceWaitingCallerLeavesOnCancel(t *testing.T) {
	docker := &hostSpaceFakeDocker{block: make(chan struct{}), entered: make(chan struct{}, 1)}
	svc, _ := newHostSpaceForTest(docker)
	first := make(chan error, 1)
	go func() {
		_, err := svc.Summary(context.Background())
		first <- err
	}()
	<-docker.entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Summary(ctx)
	require.ErrorIs(t, err, context.Canceled)
	close(docker.block)
	require.NoError(t, <-first)
}

func TestHostSpaceEmptyCategoriesSendArrays(t *testing.T) {
	svc, _ := newHostSpaceForTest(&hostSpaceFakeDocker{})
	cand, err := svc.Candidates(context.Background())
	require.NoError(t, err)
	body, err := json.Marshal(cand)
	require.NoError(t, err)
	assert.JSONEq(t, `{"images":[],"buildCache":{"count":0,"bytes":0},"containers":[],"volumes":[],"networks":[]}`, string(body))
}

func TestHostSpaceNilWithoutDocker(t *testing.T) {
	assert.Nil(t, NewHostSpaceService(nil, &hostSpaceFakeStacks{}))
}
