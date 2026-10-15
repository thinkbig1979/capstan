package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// hostSpaceSnapshotTTL bounds how stale a served summary can be. DiskUsage
// walks every volume directory and is the slowest Docker read Capstan makes,
// so the sidebar badge, header pill, tiles and alert share one call per TTL
// however many tabs poll.
const hostSpaceSnapshotTTL = 30 * time.Second

const composeProjectLabel = "com.docker.compose.project"

// HostFreeSpaceTimeout bounds a free-space run. The handler runs it on a
// context of its own (safe-defaults rule 15), so this is what ends it.
const HostFreeSpaceTimeout = 15 * time.Minute

// notCandidateReason is the skipped reason for a requested ID that is not in
// the candidate set rebuilt under the lock.
const notCandidateReason = "no longer removable: in use, owned by a stack, or gone"

// builtInNetworks are created by Docker itself and never listed for removal.
// ingress and docker_gwbridge are swarm's; they carry no compose label and can
// have no attached container, so the label and attachment rules alone would
// list them.
var builtInNetworks = map[string]bool{
	"bridge":          true,
	"host":            true,
	"none":            true,
	"ingress":         true,
	"docker_gwbridge": true,
}

// hostSpaceDocker is the slice of the Docker client HostSpaceService reads,
// declared on the consumer side so the candidate rules are testable with a
// fake. *client.Client satisfies it.
type hostSpaceDocker interface {
	DiskUsage(ctx context.Context, options dockertypes.DiskUsageOptions) (dockertypes.DiskUsage, error)
	NetworkList(ctx context.Context, options network.ListOptions) ([]network.Summary, error)
	networkInspector

	// The removals FreeSpace makes, one object at a time.
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
	ImageRemove(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error)
	NetworkRemove(ctx context.Context, networkID string) error
	VolumeRemove(ctx context.Context, volumeID string, force bool) error
	BuildCachePrune(ctx context.Context, opts build.CachePruneOptions) (*build.CachePruneReport, error)
}

// hostSpaceStacks is the slice of *database.DB that says which compose
// projects belong to a stack.
type hostSpaceStacks interface {
	ListStacks() ([]models.Stack, error)
}

// HostSpaceService computes what Docker keeps on disk and what can be removed
// without touching anything a stack uses or owns. Summary is served from a
// snapshot up to hostSpaceSnapshotTTL old; Candidates always reads fresh,
// because the user is about to delete from it.
type HostSpaceService struct {
	docker hostSpaceDocker
	stacks hostSpaceStacks
	now    func() time.Time
	// refs resolves what the stacks' compose files name; fetch withholds
	// every referenced object from the candidates (agent-os-3rr3).
	refs stackReferenceResolver

	// fetching is a one-slot semaphore held for a whole fetch, so concurrent
	// callers make one Docker read between them. A channel rather than a mutex
	// so a caller whose request is cancelled stops waiting.
	fetching chan struct{}

	// mu guards snap and gen. It is never held across a Docker call, so
	// Invalidate does not wait for a fetch in flight.
	mu   sync.Mutex
	snap *hostSpaceSnapshot
	// gen is bumped by Invalidate. A fetch that started under an older gen
	// does not store its result: it may predate the change being reported.
	gen uint64
}

type hostSpaceSnapshot struct {
	summary    HostSummary
	candidates HostCandidates
}

// NewHostSpaceService returns nil when Docker is unavailable, so main.go can
// skip the setter rather than box a nil service into the handler's interface.
func NewHostSpaceService(docker *DockerService, stacks hostSpaceStacks) *HostSpaceService {
	if docker == nil || docker.client == nil {
		return nil
	}
	svc := newHostSpaceService(docker.client, stacks, time.Now)
	if docker.config != nil && docker.config.DataDir != "" {
		// The same global.env buildComposeArgs hands compose.
		svc.refs = composeStackReferences(filepath.Join(docker.config.DataDir, "global.env"))
	}
	return svc
}

func newHostSpaceService(docker hostSpaceDocker, stacks hostSpaceStacks, now func() time.Time) *HostSpaceService {
	return &HostSpaceService{
		docker:   docker,
		stacks:   stacks,
		now:      now,
		refs:     composeStackReferences(""),
		fetching: make(chan struct{}, 1),
	}
}

// Summary returns the per-category figures, from the snapshot when it is
// younger than hostSpaceSnapshotTTL. A Docker or stacks read error is
// returned as is and never cached, so it is never served as a zero summary.
func (s *HostSpaceService) Summary(ctx context.Context) (*HostSummary, error) {
	snap, err := s.snapshot(ctx, false)
	if err != nil {
		return nil, err
	}
	summary := snap.summary
	return &summary, nil
}

// Candidates returns the removable items from a fresh read, and refreshes the
// snapshot Summary serves with it.
func (s *HostSpaceService) Candidates(ctx context.Context) (*HostCandidates, error) {
	snap, err := s.snapshot(ctx, true)
	if err != nil {
		return nil, err
	}
	candidates := snap.candidates
	return &candidates, nil
}

// Invalidate drops the snapshot, so the next Summary reads Docker again.
// Anything that removes Docker objects calls it.
func (s *HostSpaceService) Invalidate() {
	s.mu.Lock()
	s.snap = nil
	s.gen++
	s.mu.Unlock()
}

func (s *HostSpaceService) snapshot(ctx context.Context, fresh bool) (*hostSpaceSnapshot, error) {
	select {
	case s.fetching <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-s.fetching }()

	// Checked after taking the slot, so callers that queued behind a fetch
	// reuse its result instead of each reading Docker again.
	s.mu.Lock()
	if !fresh && s.snap != nil && s.now().Sub(s.snap.summary.MeasuredAt) < hostSpaceSnapshotTTL {
		snap := s.snap
		s.mu.Unlock()
		return snap, nil
	}
	gen := s.gen
	s.mu.Unlock()

	snap, err := s.fetch(ctx)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.gen == gen {
		s.snap = snap
	}
	s.mu.Unlock()
	return snap, nil
}

func (s *HostSpaceService) fetch(ctx context.Context) (*hostSpaceSnapshot, error) {
	stacks, err := s.stacks.ListStacks()
	if err != nil {
		return nil, fmt.Errorf("listing stacks: %w", err)
	}
	owned := make(map[string]bool, len(stacks))
	for _, st := range stacks {
		if st.ProjectName != "" {
			owned[st.ProjectName] = true
		}
	}

	refs := s.refs(ctx, stacks)

	du, err := s.docker.DiskUsage(ctx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading Docker disk usage: %w", err)
	}
	nets, err := s.docker.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}

	totals := summarizeDiskUsage(du)
	snap := &hostSpaceSnapshot{
		summary: HostSummary{MeasuredAt: s.now().UTC()},
		candidates: HostCandidates{
			Images:         []HostCandidateItem{},
			Containers:     []HostCandidateItem{},
			Volumes:        []HostCandidateItem{},
			Networks:       []HostCandidateItem{},
			WithheldStacks: refs.withheld,
		},
	}
	sum, cand := &snap.summary, &snap.candidates

	sum.Images.Bytes = totals.Images
	for _, img := range du.Images {
		if img == nil {
			continue
		}
		sum.Images.Count++
		if img.Containers > 0 {
			sum.Images.InUse++
		}
		if !imageIsCandidate(img) || refs.withholdsImage(img) {
			continue
		}
		size := imageReclaimableSize(img)
		sum.Images.ReclaimableCount++
		if size != nil {
			sum.Images.ReclaimableBytes += *size
		}
		_, dangling := danglingRepository(img.RepoTags)
		cand.Images = append(cand.Images, HostCandidateItem{ID: img.ID, Label: imageLabel(img), Size: size, Created: knownUnix(img.Created), Dangling: dangling})
	}

	sum.BuildCache.Bytes = totals.BuildCache
	for _, rec := range du.BuildCache {
		if rec == nil {
			continue
		}
		sum.BuildCache.Count++
		if !buildCacheIsCandidate(rec) {
			continue
		}
		sum.BuildCache.ReclaimableBytes += rec.Size
		cand.BuildCache.Count++
		cand.BuildCache.Bytes += rec.Size
	}

	for _, ctr := range du.Containers {
		if ctr == nil {
			continue
		}
		sum.Containers.Count++
		switch {
		case ctr.State == container.StateRunning:
			sum.Containers.Running++
		case containerIsStopped(ctr.State):
			sum.Containers.Stopped++
		}
		if !containerIsCandidate(ctr) || refs.withholdsContainer(ctr) {
			continue
		}
		item := HostCandidateItem{ID: ctr.ID, Label: containerLabel(ctr), Detail: ctr.Status, Created: knownUnix(ctr.Created)}
		if ctr.SizeRw >= 0 {
			size := ctr.SizeRw
			item.Size = &size
			sum.Containers.ReclaimableBytes += size
		}
		sum.Containers.ReclaimableCount++
		cand.Containers = append(cand.Containers, item)
	}

	sum.Volumes.Bytes = totals.Volumes
	sum.Volumes.SizeKnown = totals.VolumesSizeKnown
	for _, vol := range du.Volumes {
		if vol == nil {
			continue
		}
		sum.Volumes.Count++
		if vol.UsageData != nil && vol.UsageData.RefCount > 0 {
			sum.Volumes.Attached++
		}
		if !volumeIsCandidate(vol, owned) || refs.withholdsVolume(vol) {
			continue
		}
		item := HostCandidateItem{ID: vol.Name, Label: vol.Name, Detail: vol.Labels[composeProjectLabel], Created: volumeCreated(vol)}
		if vol.UsageData.Size >= 0 {
			size := vol.UsageData.Size
			item.Size = &size
			sum.Volumes.ReclaimableBytes += size
		}
		sum.Volumes.ReclaimableCount++
		cand.Volumes = append(cand.Volumes, item)
	}

	for _, n := range nets {
		sum.Networks.Count++
		builtIn := builtInNetworks[n.Name]
		stackOwned := isStackOwned(n.Labels, owned)
		switch {
		case builtIn:
			sum.Networks.BuiltIn++
		case stackOwned:
			sum.Networks.StackOwned++
		}
		if builtIn || stackOwned || refs.withholdsNetwork(n) {
			continue
		}
		// NetworkList leaves Containers empty, so the count comes from an
		// inspect. A failed inspect falls back to -1, never to the list's 0:
		// a network whose attachments are unknown is not listed.
		if networkContainerCount(ctx, s.docker, n.ID, -1) != 0 {
			continue
		}
		sum.Networks.ReclaimableCount++
		cand.Networks = append(cand.Networks, HostCandidateItem{ID: n.ID, Label: n.Name, Detail: n.Driver, Created: knownTime(n.Created)})
	}

	sum.Disk.Total = totals.Total
	sum.Disk.VolumesSizeKnown = totals.VolumesSizeKnown
	sum.Disk.Reclaimable = sum.Images.ReclaimableBytes + sum.BuildCache.ReclaimableBytes + sum.Containers.ReclaimableBytes
	sum.Disk.ReclaimableAll = sum.Disk.Reclaimable + sum.Volumes.ReclaimableBytes
	sum.Disk.InUse = max(0, sum.Disk.Total-sum.Disk.Reclaimable)
	return snap, nil
}

// isStackOwned: the compose project label names a stack in the stacks table,
// running or not. A stopped or downed stack still owns its volumes and networks.
func isStackOwned(labels map[string]string, owned map[string]bool) bool {
	project := labels[composeProjectLabel]
	return project != "" && owned[project]
}

// imageIsCandidate: no container, running or stopped, uses the image. Docker
// reports -1 when it did not count, which is not 0.
func imageIsCandidate(img *image.Summary) bool {
	return img.Containers == 0
}

// imageReclaimableSize is Size minus SharedSize, the bytes only this image
// holds. nil when Docker did not compute SharedSize (-1).
func imageReclaimableSize(img *image.Summary) *int64 {
	if img.SharedSize < 0 || img.Size < img.SharedSize {
		return nil
	}
	size := img.Size - img.SharedSize
	return &size
}

// knownUnix is a Docker Unix creation time, nil when Docker sent none (0).
func knownUnix(sec int64) *int64 {
	if sec <= 0 {
		return nil
	}
	return &sec
}

func knownTime(t time.Time) *int64 {
	if t.IsZero() {
		return nil
	}
	return knownUnix(t.Unix())
}

// volumeCreated parses the volume's RFC 3339 CreatedAt; empty or malformed is
// unknown.
func volumeCreated(vol *volume.Volume) *int64 {
	t, err := time.Parse(time.RFC3339, vol.CreatedAt)
	if err != nil {
		return nil
	}
	return knownTime(t)
}

// imageLabel follows candidateLabel in DockerCleanupCard.tsx: the first real
// tag, else the start of the ID.
func imageLabel(img *image.Summary) string {
	for _, tag := range img.RepoTags {
		if tag != "" && tag != "<none>:<none>" {
			return tag
		}
	}
	id := strings.TrimPrefix(img.ID, "sha256:")
	return id[:min(19, len(id))]
}

func buildCacheIsCandidate(rec *build.CacheRecord) bool {
	return !rec.InUse && !rec.Shared
}

// containerIsStopped is an allow-list, so removing, paused, restarting and any
// state Docker adds later are never treated as leftovers.
func containerIsStopped(state container.ContainerState) bool {
	switch state {
	case container.StateExited, container.StateCreated, container.StateDead:
		return true
	}
	return false
}

// containerIsCandidate: a stopped one-off container. Any compose project label
// excludes it, whether or not a stack here manages that project: compose
// recreates its containers, and a stopped one may be a stack mid-maintenance.
func containerIsCandidate(ctr *container.Summary) bool {
	if !containerIsStopped(ctr.State) {
		return false
	}
	_, labelled := ctr.Labels[composeProjectLabel]
	return !labelled
}

func containerLabel(ctr *container.Summary) string {
	if len(ctr.Names) > 0 {
		return strings.TrimPrefix(ctr.Names[0], "/")
	}
	return ctr.ID[:min(12, len(ctr.ID))]
}

// volumeIsCandidate: no container references it, Docker counted that
// (RefCount -1 or no usage data is unknown, not unused), and no stack owns it.
func volumeIsCandidate(vol *volume.Volume, owned map[string]bool) bool {
	if vol.UsageData == nil || vol.UsageData.RefCount != 0 {
		return false
	}
	return !isStackOwned(vol.Labels, owned)
}

// FreeSpace removes the requested items that are still candidates. The caller
// holds the exclusive stack turn, so the candidate set rebuilt here cannot go
// stale before the removals: a requested ID outside it was in use, stack-owned
// or gone by the time the lock was taken, and is skipped, never removed.
//
// Every removal passes force=false, so Docker itself refuses anything in use.
// Images are removed without PruneChildren: an untagged parent was not listed,
// so it is not this run's to remove. Order: containers first (which can free
// images, but those are not added to this run), images, networks, volumes,
// then build cache.
//
// An error means nothing was removed: the candidate read failed.
func (s *HostSpaceService) FreeSpace(ctx context.Context, req HostFreeSpaceRequest) (*HostFreeSpaceResult, error) {
	snap, err := s.snapshot(ctx, true)
	if err != nil {
		return nil, err
	}
	// Anything removed below changes the summary; drop the snapshot whatever
	// happens from here on.
	defer s.Invalidate()
	before := snap.summary.Disk.Total

	res := &HostFreeSpaceResult{
		Images:     newCategoryResult(),
		Containers: newCategoryResult(),
		Volumes:    newCategoryResult(),
		Networks:   newCategoryResult(),
	}
	cand := snap.candidates

	removeEach(req.Containers, cand.Containers, &res.Containers, func(id string) error {
		return s.docker.ContainerRemove(ctx, id, container.RemoveOptions{Force: false})
	})
	removeEach(req.Images, cand.Images, &res.Images, func(id string) error {
		resp, err := s.docker.ImageRemove(ctx, id, image.RemoveOptions{Force: false, PruneChildren: false})
		if err != nil {
			return err
		}
		for _, r := range resp {
			if r.Deleted != "" {
				return nil
			}
		}
		return errors.New("only untagged; the image is still referenced")
	})
	removeEach(req.Networks, cand.Networks, &res.Networks, func(id string) error {
		return s.docker.NetworkRemove(ctx, id)
	})
	removeEach(req.Volumes, cand.Volumes, &res.Volumes, func(id string) error {
		return s.docker.VolumeRemove(ctx, id, false)
	})

	if req.BuildCache {
		res.BuildCache.Requested = true
		if cand.BuildCache.Count == 0 {
			res.BuildCache.Skipped = "no unused build cache"
		} else {
			// All=false spares shared records, matching buildCacheIsCandidate.
			// INFERRED from BuildKit's prune (the non-all filter skips shared
			// records), not run against a daemon.
			report, err := s.docker.BuildCachePrune(ctx, build.CachePruneOptions{All: false})
			switch {
			case err != nil:
				res.BuildCache.Failed = err.Error()
			case report != nil:
				res.BuildCache.Removed = len(report.CachesDeleted)
				res.BuildCache.SpaceReclaimed = int64(min(report.SpaceReclaimed, math.MaxInt64))
			}
		}
	}

	after, err := s.docker.DiskUsage(ctx, dockertypes.DiskUsageOptions{})
	if err != nil {
		slog.Warn("Free-space run: could not measure Docker disk use afterwards", "error", err)
		return res, nil
	}
	// Floored at 0: something outside Capstan can grow Docker's use while
	// the run is in progress.
	freed := max(0, before-summarizeDiskUsage(after).Total)
	res.SpaceReclaimed = &freed
	return res, nil
}

func newCategoryResult() HostFreeSpaceCategoryResult {
	return HostFreeSpaceCategoryResult{
		Removed: []string{},
		Failed:  []HostFreeSpaceItemIssue{},
		Skipped: []HostFreeSpaceItemIssue{},
	}
}

// removeEach removes every requested ID that is in candidates, once each, and
// records each ID in exactly one of out's lists.
func removeEach(requested []string, candidates []HostCandidateItem, out *HostFreeSpaceCategoryResult, remove func(id string) error) {
	eligible := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		eligible[c.ID] = true
	}
	seen := make(map[string]bool, len(requested))
	for _, id := range requested {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !eligible[id] {
			out.Skipped = append(out.Skipped, HostFreeSpaceItemIssue{ID: id, Reason: notCandidateReason})
			continue
		}
		if err := remove(id); err != nil {
			out.Failed = append(out.Failed, HostFreeSpaceItemIssue{ID: id, Reason: err.Error()})
			continue
		}
		out.Removed = append(out.Removed, id)
	}
}
