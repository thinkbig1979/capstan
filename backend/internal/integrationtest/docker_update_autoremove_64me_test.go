//go:build integration

package integrationtest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_UpdateContainer_Standalone_64me_AutoRemove (agent-os-64me): stopping a
// container started with --rm makes Docker remove it and its anonymous volumes,
// so the recreate named a volume that was gone and the daemon made a new, empty
// one. The update is now refused before anything is pulled or stopped.
//
// "refused" is the bead's case; "named volume still updates" is the control: a
// --rm container whose data lives in a named volume loses nothing on stop, so
// it still updates.
//
// Every object this test creates is named or collected here and removed by name.
func Test_UpdateContainer_Standalone_64me_AutoRemove(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	run := func(t *testing.T, name string, vol string) (id string) {
		t.Helper()
		volumes := map[string]bool{}
		leftover := "^/" + name + "-capstan-old-"
		// Cleanups run last-registered first: containers, then their volumes.
		t.Cleanup(func() {
			for v := range volumes {
				_ = exec.Command("docker", "volume", "rm", v).Run()
			}
		})
		t.Cleanup(func() {
			_ = exec.Command("docker", "rm", "-f", name).Run()
			out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name="+leftover).Output()
			for _, id := range strings.Fields(string(out)) {
				_ = exec.Command("docker", "rm", "-f", id).Run()
			}
		})

		out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-v", vol,
			updateTestImage, "sleep", "600").CombinedOutput()
		require.NoError(t, err, "docker run: %s", strings.TrimSpace(string(out)))
		id = strings.TrimSpace(string(out))

		out, err = exec.Command("docker", "inspect", "-f",
			`{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}} {{end}}{{end}}`, name).CombinedOutput()
		require.NoError(t, err, strings.TrimSpace(string(out)))
		for _, v := range strings.Fields(string(out)) {
			volumes[v] = true
		}

		out, err = exec.Command("docker", "exec", name, "sh", "-c", "echo keep > /data/f").CombinedOutput()
		require.NoError(t, err, "writing /data/f: %s", strings.TrimSpace(string(out)))
		return id
	}

	readBack := func(t *testing.T, name string) {
		t.Helper()
		out, err := exec.Command("docker", "exec", name, "cat", "/data/f").CombinedOutput()
		require.NoError(t, err, "/data/f is readable: %s", strings.TrimSpace(string(out)))
		assert.Equal(t, "keep", strings.TrimSpace(string(out)))
	}

	svc := newDockerService(t, t.TempDir())

	t.Run("refused", func(t *testing.T) {
		name := fmt.Sprintf("capstan-it-64me-anon-%d", time.Now().UnixNano())
		id := run(t, name, "/data")

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		_, ar := svc.UpdateContainer(ctx, id, nil)
		cancel()

		require.Equal(t, "failed", string(ar.Outcome), "%s (%v)", ar.Reason, ar.Err)
		require.Error(t, ar.Err)
		assert.Contains(t, ar.Err.Error(), "--rm")

		out, err := exec.Command("docker", "inspect", "-f", "{{.Id}} {{.State.Running}}", name).CombinedOutput()
		require.NoError(t, err, "the original container still exists: %s", strings.TrimSpace(string(out)))
		assert.Equal(t, id+" true", strings.TrimSpace(string(out)), "the original container still runs")
		readBack(t, name)
	})

	t.Run("named volume still updates", func(t *testing.T) {
		name := fmt.Sprintf("capstan-it-64me-named-%d", time.Now().UnixNano())
		id := run(t, name, name+"-vol:/data")

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		_, ar := svc.UpdateContainer(ctx, id, nil)
		cancel()

		require.NotEqual(t, "failed", string(ar.Outcome), "%s (%v)", ar.Reason, ar.Err)
		out, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).CombinedOutput()
		require.NoError(t, err, "the container exists: %s", strings.TrimSpace(string(out)))
		assert.NotEqual(t, id, strings.TrimSpace(string(out)), "the update recreated the container")
		readBack(t, name)
	})
}
