//go:build integration

package truth

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
)

// TestResolveContainerImage_Integration pulls alpine:3.20, creates a container
// from it, calls ResolveContainerImage with a real *client.Client, and asserts
// that the returned values are populated and consistent with docker inspect.
//
// Run with: go test -tags=integration ./internal/truth/ -run TestResolveContainerImage_Integration
func TestResolveContainerImage_Integration(t *testing.T) {
	const (
		ref          = "alpine:3.20"
		containerRef = "truth-test-resolve-" // will append test ID
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Pull the image first so we have a known local copy.
	t.Logf("Pulling %s ...", ref)
	pullOut, err := exec.CommandContext(ctx, "docker", "pull", ref).CombinedOutput()
	if err != nil {
		t.Fatalf("docker pull %s: %v\n%s", ref, err, pullOut)
	}

	// Create a non-running container so we can inspect it without starting it.
	containerName := fmt.Sprintf("%s%d", containerRef, time.Now().UnixNano())
	createOut, err := exec.CommandContext(ctx, "docker", "create", "--name", containerName, ref).CombinedOutput()
	if err != nil {
		t.Fatalf("docker create %s: %v\n%s", ref, err, createOut)
	}
	containerID := strings.TrimSpace(string(createOut))
	t.Logf("Created container %s (%s)", containerName, containerID[:12])

	// Always remove the container when the test exits.
	t.Cleanup(func() {
		out, rmErr := exec.Command("docker", "rm", "-f", containerID).CombinedOutput()
		if rmErr != nil {
			t.Logf("cleanup: docker rm -f %s: %v\n%s", containerID[:12], rmErr, out)
		}
	})

	// Build a real *client.Client.
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("creating docker client: %v", err)
	}

	// Exercise the function under test.
	imageRef, repoDigests, imageID, err := ResolveContainerImage(ctx, cli, containerID)
	if err != nil {
		t.Fatalf("ResolveContainerImage: %v", err)
	}

	t.Logf("imageRef     = %s", imageRef)
	t.Logf("imageID      = %s", imageID[:16])
	t.Logf("repoDigests  = %v", repoDigests)

	// Validate that imageRef is non-empty and plausible.
	if imageRef == "" {
		t.Error("imageRef is empty")
	}
	if !strings.Contains(imageRef, "alpine") {
		t.Errorf("imageRef %q does not mention alpine", imageRef)
	}

	// Validate imageID is a sha256 ID.
	if !strings.HasPrefix(imageID, "sha256:") {
		t.Errorf("imageID %q does not look like a sha256 ID", imageID)
	}

	// Validate repoDigests are populated (alpine is a well-known registry image).
	if len(repoDigests) == 0 {
		t.Error("repoDigests is empty; expected at least one entry for a pulled image")
	}
	for _, rd := range repoDigests {
		if !strings.Contains(rd, "@sha256:") {
			t.Errorf("repoDigest %q does not look like a valid digest reference", rd)
		}
	}

	// Cross-check with docker inspect to ensure consistency.
	inspectOut, err := exec.CommandContext(ctx, "docker", "inspect", containerID).Output()
	if err != nil {
		t.Fatalf("docker inspect %s: %v", containerID[:12], err)
	}
	var inspectResults []struct {
		Image  string `json:"Image"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(inspectOut, &inspectResults); err != nil {
		t.Fatalf("parsing docker inspect output: %v", err)
	}
	if len(inspectResults) == 0 {
		t.Fatal("docker inspect returned no results")
	}

	gotDockerImageID := inspectResults[0].Image
	if imageID != gotDockerImageID {
		t.Errorf("imageID mismatch: ResolveContainerImage=%q docker inspect=%q", imageID, gotDockerImageID)
	}
}
