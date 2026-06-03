package truth

import (
	"context"
	"fmt"

	dockertypes "github.com/docker/docker/api/types"
)

// ContainerInspector is the subset of the Docker client used by the verify
// helpers. Defined as an interface so tests can stub it without a real daemon.
type ContainerInspector interface {
	ContainerInspect(ctx context.Context, containerID string) (dockertypes.ContainerJSON, error)
}

// ContainerRunning reports whether the container with the given ID is currently
// in the running state. It returns an error if the inspect call fails.
func ContainerRunning(ctx context.Context, cli ContainerInspector, id string) (bool, error) {
	info, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return false, fmt.Errorf("inspecting container %s: %w", id, err)
	}
	if info.State == nil {
		return false, nil
	}
	return info.State.Running, nil
}

// ContainerHealthy reports whether the container with the given ID is in the
// "healthy" state. It also reports whether the container has a healthcheck
// configured at all, so callers can distinguish "no healthcheck" from
// "unhealthy".
//
// Returns:
//   - healthy: true only when State.Health.Status == "healthy"
//   - hasHealthcheck: false when there is no healthcheck (State.Health is nil
//     or Status == "none")
//   - err: non-nil if the inspect call fails
func ContainerHealthy(ctx context.Context, cli ContainerInspector, id string) (healthy bool, hasHealthcheck bool, err error) {
	info, inspectErr := cli.ContainerInspect(ctx, id)
	if inspectErr != nil {
		return false, false, fmt.Errorf("inspecting container %s: %w", id, inspectErr)
	}
	if info.State == nil || info.State.Health == nil {
		return false, false, nil
	}
	status := info.State.Health.Status
	if status == dockertypes.NoHealthcheck || status == "" {
		return false, false, nil
	}
	return status == dockertypes.Healthy, true, nil
}

// ResourceAbsent calls checkExists and returns true when the resource no
// longer exists. It is a small inversion helper that makes post-delete
// verification read clearly:
//
//	absent, err := truth.ResourceAbsent(func() (bool, error) { return imageExists(ctx, cli, id) })
func ResourceAbsent(checkExists func() (bool, error)) (bool, error) {
	exists, err := checkExists()
	if err != nil {
		return false, err
	}
	return !exists, nil
}
