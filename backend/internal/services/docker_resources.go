package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// PruneOptions carries the optional flags a prune action can apply. Not every
// field is meaningful for every resource (e.g. Docker's volume prune has no
// "until" filter, and container prune has no "all"); handlers only populate the
// fields that apply and the methods below ignore the rest.
type PruneOptions struct {
	// All removes everything unused, not just dangling/anonymous (docker `-a`).
	All bool
	// Until restricts pruning to objects created before the given age, expressed
	// as a Go duration string (e.g. "24h"). Empty means no age filter.
	Until string
}

func (s *DockerService) ListImages(ctx context.Context) ([]models.DockerImage, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	// SharedSize lets the cleanup preview count only the bytes removing an
	// image frees. A daemon too old to compute it reports -1 (unknown).
	images, err := s.client.ImageList(ctx, image.ListOptions{SharedSize: true})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	// ImageList always reports Containers as -1: the client has no parameter
	// that asks for the count. DiskUsage (docker system df) computes it, keyed
	// by ID, as ListVolumes does for sizes. A failure degrades to "unknown".
	var usage *dockertypes.DiskUsage
	if du, duErr := s.client.DiskUsage(ctx, dockertypes.DiskUsageOptions{
		Types: []dockertypes.DiskUsageObject{dockertypes.ImageObject},
	}); duErr == nil {
		usage = &du
	} else {
		slog.Warn("Failed to compute image container counts; counts unavailable", "error", duErr)
	}

	return toDockerImages(images, usage), nil
}

// toDockerImages builds the served image list, taking each image's container
// count from usage. A nil usage (the read failed), an image missing from it
// (created between the two calls) or a negative count leaves ContainersKnown
// false and Containers 0, never a count the UI could read as "unused".
func toDockerImages(images []image.Summary, usage *dockertypes.DiskUsage) []models.DockerImage {
	counts := make(map[string]int64)
	if usage != nil {
		for _, img := range usage.Images {
			if img != nil && img.Containers >= 0 {
				counts[img.ID] = img.Containers
			}
		}
	}

	result := make([]models.DockerImage, 0, len(images))
	for _, img := range images {
		repoTags := img.RepoTags
		if repoTags == nil {
			repoTags = []string{"<none>"}
		}

		count, known := counts[img.ID]
		result = append(result, models.DockerImage{
			ID:              img.ID,
			RepoTags:        repoTags,
			Size:            img.Size,
			SharedSize:      img.SharedSize,
			Created:         img.Created,
			Containers:      int(count),
			ContainersKnown: known,
		})
	}

	return result
}

func (s *DockerService) DeleteImage(ctx context.Context, imageID string, force bool) ([]image.DeleteResponse, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	return s.client.ImageRemove(ctx, imageID, image.RemoveOptions{Force: force})
}

func (s *DockerService) PruneImages(ctx context.Context, opts PruneOptions) (image.PruneReport, error) {
	if s == nil {
		return image.PruneReport{}, ErrDockerUnavailable
	}

	f := filters.NewArgs()
	// Docker's default prune only removes dangling (untagged) images. dangling=false
	// widens it to all unused images (the `docker image prune -a` behaviour).
	if opts.All {
		f.Add("dangling", "false")
	}
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.ImagesPrune(ctx, f)
}

func (s *DockerService) ListVolumes(ctx context.Context) ([]models.DockerVolume, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	volumes, err := s.client.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	// VolumeList does not populate UsageData, so size comes back as zero and there is no way
	// to tell which volumes are in use. DiskUsage (docker system df) computes both, keyed by
	// name. It is heavier, so a failure here degrades to "unknown" rather than erroring.
	usageByName := make(map[string]volumeUsage)
	if du, duErr := s.client.DiskUsage(ctx, dockertypes.DiskUsageOptions{}); duErr == nil {
		for _, v := range du.Volumes {
			if v != nil {
				usageByName[v.Name] = volumeUsageOf(v)
			}
		}
	} else {
		slog.Warn("Failed to compute volume disk usage; size and in-use state unavailable", "error", duErr)
	}

	result := make([]models.DockerVolume, 0, len(volumes.Volumes))
	for _, vol := range volumes.Volumes {
		if vol == nil {
			continue
		}

		var stack string
		if vol.Labels != nil {
			stack = vol.Labels["com.docker.compose.project"]
		}

		// A volume missing from the snapshot (failed read, or created between the
		// two calls) gets the zero volumeUsage: both halves unknown.
		u := usageByName[vol.Name]

		result = append(result, models.DockerVolume{
			Name:       vol.Name,
			Driver:     vol.Driver,
			Mountpoint: vol.Mountpoint,
			Size:       u.size,
			SizeKnown:  u.sizeKnown,
			InUse:      u.refCount > 0,
			InUseKnown: u.refCountKnown,
			Created:    vol.CreatedAt,
			Stack:      stack,
		})
	}

	return result, nil
}

// volumeUsage is a volume's disk usage with Docker's "not computed" sentinel
// resolved: a known flag false means the matching value is 0 and means nothing.
type volumeUsage struct {
	size          int64
	sizeKnown     bool
	refCount      int64
	refCountKnown bool
}

// volumeUsageOf reads a volume's UsageData. Docker leaves UsageData nil, or
// reports Size or RefCount as -1, when it did not compute them; copied through,
// -1 renders as a size and a RefCount of -1 reads as "not in use", which made an
// unchecked volume look safe to delete (agent-os-gkgr).
func volumeUsageOf(v *volume.Volume) volumeUsage {
	var u volumeUsage
	if v == nil || v.UsageData == nil {
		return u
	}
	if v.UsageData.Size >= 0 {
		u.size, u.sizeKnown = v.UsageData.Size, true
	}
	if v.UsageData.RefCount >= 0 {
		u.refCount, u.refCountKnown = v.UsageData.RefCount, true
	}
	return u
}

func (s *DockerService) DeleteVolume(ctx context.Context, volumeName string, force bool) error {
	if s == nil {
		return ErrDockerUnavailable
	}

	return s.client.VolumeRemove(ctx, volumeName, force)
}

func (s *DockerService) PruneVolumes(ctx context.Context, opts PruneOptions) (volume.PruneReport, error) {
	if s == nil {
		return volume.PruneReport{}, ErrDockerUnavailable
	}

	f := filters.NewArgs()
	// Default prune only removes anonymous volumes. all=true widens it to every
	// unused volume (the `docker volume prune -a` behaviour).
	if opts.All {
		f.Add("all", "true")
	}
	return s.client.VolumesPrune(ctx, f)
}

func (s *DockerService) ListNetworks(ctx context.Context) ([]models.DockerNetwork, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	networks, err := s.client.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}

	result := make([]models.DockerNetwork, 0, len(networks))
	for _, net := range networks {
		var stack string
		// Non-nil so an unlabelled network (bridge, host, none) sends []
		// (agent-os-e5pr).
		labelStrs := []string{}
		if net.Labels != nil {
			stack = net.Labels["com.docker.compose.project"]
			for k, v := range net.Labels {
				labelStrs = append(labelStrs, k+"="+v)
			}
		}

		// NetworkList does not populate net.Containers, so inspect each network to
		// get the real attachment count that the in-use delete guard relies on. A
		// failed inspect is unknown, never the list's empty 0 (agent-os-gkgr).
		containerCount, containersKnown := networkContainerCount(ctx, s.client, net.ID)

		result = append(result, models.DockerNetwork{
			ID:              net.ID,
			Name:            net.Name,
			Driver:          net.Driver,
			Scope:           net.Scope,
			Internal:        net.Internal,
			Containers:      containerCount,
			ContainersKnown: containersKnown,
			Labels:          labelStrs,
			Created:         net.Created.Format(time.RFC3339),
			Stack:           stack,
		})
	}

	return result, nil
}

// networkInspector is the subset of the Docker client used to resolve real
// per-network container attachment counts (NetworkList leaves them empty).
type networkInspector interface {
	NetworkInspect(ctx context.Context, networkID string, options network.InspectOptions) (network.Inspect, error)
}

// networkContainerCount returns the number of containers attached to a network.
// NetworkList does not populate the Containers map, so we inspect the network.
// known is false when the inspect failed: the count is then 0 and means
// nothing, so a caller cannot read a failure as "nothing attached" without
// ignoring the flag (agent-os-m84q).
func networkContainerCount(ctx context.Context, inspector networkInspector, networkID string) (count int, known bool) {
	inspected, err := inspector.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err != nil {
		return 0, false
	}
	return len(inspected.Containers), true
}

func (s *DockerService) DeleteNetwork(ctx context.Context, networkID string) error {
	if s == nil {
		return ErrDockerUnavailable
	}

	return s.client.NetworkRemove(ctx, networkID)
}

func (s *DockerService) CreateNetwork(ctx context.Context, name string, opts network.CreateOptions) (string, error) {
	if s == nil {
		return "", ErrDockerUnavailable
	}

	resp, err := s.client.NetworkCreate(ctx, name, opts)
	if err != nil {
		return "", fmt.Errorf("creating network: %w", err)
	}
	return resp.ID, nil
}

func (s *DockerService) PruneNetworks(ctx context.Context, opts PruneOptions) (network.PruneReport, error) {
	if s == nil {
		return network.PruneReport{}, ErrDockerUnavailable
	}

	f := filters.NewArgs()
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.NetworksPrune(ctx, f)
}

func (s *DockerService) DeleteContainer(ctx context.Context, containerID string, force bool) error {
	if s == nil {
		return ErrDockerUnavailable
	}

	return s.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force})
}

func (s *DockerService) PruneContainers(ctx context.Context, opts PruneOptions) (container.PruneReport, error) {
	if s == nil {
		return container.PruneReport{}, ErrDockerUnavailable
	}

	f := filters.NewArgs()
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.ContainersPrune(ctx, f)
}

func (s *DockerService) ListBuildCache(ctx context.Context) ([]*build.CacheRecord, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	du, err := s.client.DiskUsage(ctx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return nil, err
	}
	return du.BuildCache, nil
}

func (s *DockerService) PruneBuildCache(ctx context.Context, opts PruneOptions) (*build.CachePruneReport, error) {
	if s == nil {
		return nil, ErrDockerUnavailable
	}

	f := filters.NewArgs()
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.BuildCachePrune(ctx, build.CachePruneOptions{All: opts.All, Filters: f})
}
