//go:build integration

package integrationtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// Test_UpdateComposeService_fjd6_OldImageFromServiceNotOneOff (agent-os-fjd6):
// the stack service update took the first container with the service's
// project and service labels as the pre-update container. A `docker compose
// run` one-off carries the same labels and the daemon lists newest first, so
// a one-off started after the service container was read for the old image.
//
// The one-off runs a different image so the two are told apart: it is started
// while compose.yaml names a local image built here, and compose.yaml is then
// put back. The one-off is still a real compose one-off, labelled by compose.
//
// Every object this test creates is named here and removed by name; the stack
// itself is taken down by NewTempStack's cleanup.
func Test_UpdateComposeService_fjd6_OldImageFromServiceNotOneOff(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	serviceYAML := func(image string) string {
		return "services:\n  web:\n    image: " + image +
			"\n    command: [\"sleep\",\"600\"]\n    restart: \"no\"\n"
	}
	dir, project, cleanup := NewTempStack(t, serviceYAML(updateTestImage))
	defer cleanup()

	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		require.NoError(t, err, "docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		return strings.TrimSpace(string(out))
	}
	writeCompose := func(yaml string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o600))
	}

	otherImage := fmt.Sprintf("capstan-it-fjd6-%d", time.Now().UnixNano())
	oneOff := project + "-oneoff"
	// Cleanups run last-registered first: the one-off, then its image.
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", otherImage).Run() })
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", oneOff).Run() })

	build := exec.Command("docker", "build", "-q", "-t", otherImage, "-")
	build.Stdin = strings.NewReader("FROM " + updateTestImage + "\nLABEL capstan-it-fjd6=1\n")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "docker build: %s", out)

	RunCompose(t, dir, project, "up", "-d", "web")
	serviceID := strings.TrimSpace(RunCompose(t, dir, project, "ps", "-q", "web"))
	require.NotEmpty(t, serviceID)
	serviceImage := docker("inspect", "-f", "{{.Image}}", serviceID)

	// Newer than the service container, so the daemon lists it first.
	time.Sleep(1100 * time.Millisecond)
	writeCompose(serviceYAML(otherImage))
	RunCompose(t, dir, project, "run", "-d", "--name", oneOff, "web", "sleep", "600")
	writeCompose(serviceYAML(updateTestImage))
	require.NotEqual(t, serviceImage, docker("inspect", "-f", "{{.Image}}", oneOff),
		"the fixture needs the one-off on a different image")
	require.Equal(t, "True", docker("inspect", "-f", `{{index .Config.Labels "com.docker.compose.oneoff"}}`, oneOff))

	svc := newDockerService(t, dir)
	stack := models.Stack{ID: project + ":default", Directory: dir, ComposeFile: "compose.yaml", ProjectName: project}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	oldImageID, _, _, ar := svc.UpdateComposeServiceStreaming(ctx, stack, "web",
		func(services.LogLine) {}, func(services.Status) {})
	cancel()
	require.NotEqual(t, "failed", string(ar.Outcome), "%s (%v)", ar.Reason, ar.Err)

	assert.Equal(t, serviceImage, oldImageID, "the old image is the service container's, not the one-off's")
	assert.Equal(t, "true", docker("inspect", "-f", "{{.State.Running}}", oneOff), "the one-off is left running")
}
