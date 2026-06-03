package services

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
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
	images, err := s.client.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	result := make([]models.DockerImage, 0, len(images))
	for _, img := range images {
		repoTags := img.RepoTags
		if repoTags == nil {
			repoTags = []string{"<none>"}
		}

		result = append(result, models.DockerImage{
			ID:         img.ID,
			RepoTags:   repoTags,
			Size:       img.Size,
			Created:    img.Created,
			Containers: int(img.Containers),
		})
	}

	return result, nil
}

func (s *DockerService) DeleteImage(ctx context.Context, imageID string, force bool) ([]image.DeleteResponse, error) {
	return s.client.ImageRemove(ctx, imageID, image.RemoveOptions{Force: force})
}

func (s *DockerService) PruneImages(ctx context.Context, opts PruneOptions) (dockertypes.ImagesPruneReport, error) {
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
	volumes, err := s.client.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing volumes: %w", err)
	}

	// VolumeList does not populate UsageData, so size comes back as zero and there is no way
	// to tell which volumes are in use. DiskUsage (docker system df) computes both, keyed by
	// name. It is heavier, so a failure here degrades to "size unknown" rather than erroring.
	type volUsage struct {
		size     int64
		refCount int64
	}
	usageByName := make(map[string]volUsage)
	usageComputed := false
	if du, duErr := s.client.DiskUsage(ctx, dockertypes.DiskUsageOptions{}); duErr == nil {
		usageComputed = true
		for _, v := range du.Volumes {
			if v == nil || v.UsageData == nil {
				continue
			}
			usageByName[v.Name] = volUsage{size: v.UsageData.Size, refCount: v.UsageData.RefCount}
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

		u, hasUsage := usageByName[vol.Name]

		result = append(result, models.DockerVolume{
			Name:       vol.Name,
			Driver:     vol.Driver,
			Mountpoint: vol.Mountpoint,
			Size:       u.size,
			SizeKnown:  usageComputed && hasUsage,
			InUse:      u.refCount > 0,
			Created:    vol.CreatedAt,
			Stack:      stack,
		})
	}

	return result, nil
}

func (s *DockerService) DeleteVolume(ctx context.Context, volumeName string, force bool) error {
	return s.client.VolumeRemove(ctx, volumeName, force)
}

func (s *DockerService) PruneVolumes(ctx context.Context, opts PruneOptions) (dockertypes.VolumesPruneReport, error) {
	f := filters.NewArgs()
	// Default prune only removes anonymous volumes. all=true widens it to every
	// unused volume (the `docker volume prune -a` behaviour).
	if opts.All {
		f.Add("all", "true")
	}
	return s.client.VolumesPrune(ctx, f)
}

func (s *DockerService) ListNetworks(ctx context.Context) ([]models.DockerNetwork, error) {
	networks, err := s.client.NetworkList(ctx, dockertypes.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing networks: %w", err)
	}

	result := make([]models.DockerNetwork, 0, len(networks))
	for _, net := range networks {
		var stack string
		var labelStrs []string
		if net.Labels != nil {
			stack = net.Labels["com.docker.compose.project"]
			for k, v := range net.Labels {
				labelStrs = append(labelStrs, k+"="+v)
			}
		}

		// NetworkList does not populate net.Containers, so inspect each network to
		// get the real attachment count that the in-use delete guard relies on.
		containerCount := networkContainerCount(ctx, s.client, net.ID, len(net.Containers))

		result = append(result, models.DockerNetwork{
			ID:         net.ID,
			Name:       net.Name,
			Driver:     net.Driver,
			Scope:      net.Scope,
			Internal:   net.Internal,
			Containers: containerCount,
			Labels:     labelStrs,
			Created:    net.Created.Format(time.RFC3339),
			Stack:      stack,
		})
	}

	return result, nil
}

// networkInspector is the subset of the Docker client used to resolve real
// per-network container attachment counts (NetworkList leaves them empty).
type networkInspector interface {
	NetworkInspect(ctx context.Context, networkID string, options dockertypes.NetworkInspectOptions) (dockertypes.NetworkResource, error)
}

// networkContainerCount returns the number of containers attached to a network.
// NetworkList does not populate the Containers map, so we inspect the network;
// on inspect failure we fall back to whatever the list reported (typically 0).
func networkContainerCount(ctx context.Context, inspector networkInspector, networkID string, listFallback int) int {
	inspected, err := inspector.NetworkInspect(ctx, networkID, dockertypes.NetworkInspectOptions{})
	if err != nil {
		return listFallback
	}
	return len(inspected.Containers)
}

func (s *DockerService) DeleteNetwork(ctx context.Context, networkID string) error {
	return s.client.NetworkRemove(ctx, networkID)
}

func (s *DockerService) CreateNetwork(ctx context.Context, name string, opts dockertypes.NetworkCreate) (string, error) {
	resp, err := s.client.NetworkCreate(ctx, name, opts)
	if err != nil {
		return "", fmt.Errorf("creating network: %w", err)
	}
	return resp.ID, nil
}

func (s *DockerService) PruneNetworks(ctx context.Context, opts PruneOptions) (dockertypes.NetworksPruneReport, error) {
	f := filters.NewArgs()
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.NetworksPrune(ctx, f)
}

func (s *DockerService) DeleteContainer(ctx context.Context, containerID string, force bool) error {
	return s.client.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: force})
}

func (s *DockerService) PruneContainers(ctx context.Context, opts PruneOptions) (dockertypes.ContainersPruneReport, error) {
	f := filters.NewArgs()
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.ContainersPrune(ctx, f)
}

func (s *DockerService) ListBuildCache(ctx context.Context) ([]*dockertypes.BuildCache, error) {
	du, err := s.client.DiskUsage(ctx, dockertypes.DiskUsageOptions{})
	if err != nil {
		return nil, err
	}
	return du.BuildCache, nil
}

func (s *DockerService) PruneBuildCache(ctx context.Context, opts PruneOptions) (*dockertypes.BuildCachePruneReport, error) {
	f := filters.NewArgs()
	if opts.Until != "" {
		f.Add("until", opts.Until)
	}
	return s.client.BuildCachePrune(ctx, dockertypes.BuildCachePruneOptions{All: opts.All, Filters: f})
}
