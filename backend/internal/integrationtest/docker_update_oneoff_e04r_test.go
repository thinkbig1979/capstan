//go:build integration

package integrationtest

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// Test_UpdateContainer_Compose_e04r_LeavesOneOffRunning (agent-os-e04r): the
// compose update of a service that was not running stops what `compose up`
// started. It used to stop a `docker compose run --rm` one-off of the same
// service too, and Docker then removed the one-off and its anonymous volume.
//
// Every object this test creates is named or collected here and removed by name;
// the stack itself is taken down by NewTempStack's cleanup.
func Test_UpdateContainer_Compose_e04r_LeavesOneOffRunning(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	composeYAML := "services:\n  web:\n    image: " + updateTestImage +
		"\n    command: [\"sleep\",\"600\"]\n    restart: \"no\"\n    volumes:\n      - /data\n"
	dir, project, cleanup := NewTempStack(t, composeYAML)
	defer cleanup()

	oneOff := project + "-oneoff"
	var oneOffVol string
	// Cleanups run last-registered first: the one-off, then its volume.
	t.Cleanup(func() {
		if oneOffVol != "" {
			_ = exec.Command("docker", "volume", "rm", oneOffVol).Run()
		}
	})
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", oneOff).Run() })

	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		require.NoError(t, err, "docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		return strings.TrimSpace(string(out))
	}

	// The service container exists but is stopped: the update's !wasRunning path.
	RunCompose(t, dir, project, "create", "web")
	serviceID := strings.TrimSpace(RunCompose(t, dir, project, "ps", "-aq", "web"))
	require.NotEmpty(t, serviceID)

	RunCompose(t, dir, project, "run", "--rm", "-d", "--name", oneOff, "web", "sleep", "600")
	oneOffVol = docker("inspect", "-f", `{{range .Mounts}}{{if eq .Destination "/data"}}{{.Name}}{{end}}{{end}}`, oneOff)
	require.NotEmpty(t, oneOffVol)
	docker("exec", oneOff, "sh", "-c", "echo keep > /data/f")

	svc := newDockerService(t, dir)
	db := stackByProject{project: project, stack: &models.Stack{
		ID: project + ":default", Directory: dir, ComposeFile: "compose.yaml", ProjectName: project,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	_, ar := svc.UpdateContainer(ctx, serviceID, db)
	cancel()
	require.NotEqual(t, "failed", string(ar.Outcome), "%s (%v)", ar.Reason, ar.Err)

	AssertContainerState(t, dir, project, "web", false)
	assert.Equal(t, "true", docker("inspect", "-f", "{{.State.Running}}", oneOff), "the one-off still runs")
	assert.Equal(t, "keep", docker("exec", oneOff, "cat", "/data/f"), "the one-off's volume kept its data")
}
