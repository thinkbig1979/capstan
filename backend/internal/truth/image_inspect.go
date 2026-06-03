package truth

import (
	"context"
	"fmt"
	"strings"

	dockertypes "github.com/docker/docker/api/types"
)

// ImageInspector is the subset of the Docker client required by the image
// inspection helpers. It extends ContainerInspector with the ability to
// inspect image metadata so both the update and lifecycle domains can share
// identical image-identity and image-advancement checks.
//
// *client.Client satisfies this interface directly; tests use a hand-rolled
// stub.
type ImageInspector interface {
	ContainerInspect(ctx context.Context, containerID string) (dockertypes.ContainerJSON, error)
	ImageInspectWithRaw(ctx context.Context, imageID string) (dockertypes.ImageInspect, []byte, error)
}

// ResolveContainerImage inspects a container and its underlying image, then
// returns the canonical image reference, the image's RepoDigests, and the
// image content-addressable ID.
//
// The returned imageRef is chosen as follows:
//  1. The first RepoTag from the image inspect that is neither "<none>:<none>"
//     nor a bare digest reference ("@sha256:…").
//  2. If no suitable RepoTag exists, falls back to the container's
//     Config.Image (the ref the container was created with).
//
// These three values give a domain everything it needs to call
// truth.ImageUpToDate(ctx, imageRef, repoDigests) and to compare imageIDs
// before and after an apply operation.
func ResolveContainerImage(ctx context.Context, cli ImageInspector, containerID string) (imageRef string, repoDigests []string, imageID string, err error) {
	ctr, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", nil, "", fmt.Errorf("inspecting container %s: %w", containerID, err)
	}

	// ContainerJSONBase.Image holds the content-addressable image ID.
	imageID = ctr.Image

	// ContainerJSON.Config.Image holds the symbolic ref used at create time.
	configRef := ""
	if ctr.Config != nil {
		configRef = ctr.Config.Image
	}

	img, _, err := cli.ImageInspectWithRaw(ctx, imageID)
	if err != nil {
		return "", nil, "", fmt.Errorf("inspecting image %s (container %s): %w", imageID, containerID, err)
	}

	repoDigests = img.RepoDigests

	// Pick the best symbolic ref from the image's RepoTags.
	imageRef = pickImageRef(img.RepoTags, configRef)

	return imageRef, repoDigests, imageID, nil
}

// pickImageRef selects the most useful symbolic image reference from a list of
// RepoTags. It skips placeholder tags ("<none>:<none>", bare "@sha256:…") and
// returns the first acceptable entry. If none qualify, it returns the
// fallback (typically the container's Config.Image).
func pickImageRef(repoTags []string, fallback string) string {
	for _, tag := range repoTags {
		if tag == "" || tag == "<none>:<none>" || tag == "<none>" {
			continue
		}
		if strings.HasPrefix(tag, "@sha256:") {
			continue
		}
		return tag
	}
	return fallback
}

// ContainerImageAdvanced reports whether the image running inside a container
// has advanced relative to a previously recorded image ID. It is the
// canonical "did the apply actually change the running image" check that
// closes the false-success path described in audit finding #2.
//
//   - advanced is true when the container's current image ID differs from
//     oldImageID.
//   - newImageID is always the container's current image ID (even when
//     advanced is false), so callers can store it for the next comparison.
//   - err is non-nil if the container inspect fails; errors are never swallowed.
func ContainerImageAdvanced(ctx context.Context, cli ImageInspector, containerID, oldImageID string) (advanced bool, newImageID string, err error) {
	ctr, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, "", fmt.Errorf("inspecting container %s: %w", containerID, err)
	}
	newImageID = ctr.Image
	return newImageID != oldImageID, newImageID, nil
}
