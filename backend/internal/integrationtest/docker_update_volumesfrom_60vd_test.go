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

// Test_UpdateContainer_Standalone_60vd_VolumesFrom (agent-os-60vd): a --rm
// container whose volumes come from --volumes-from loses nothing on stop, named
// or anonymous, because the source container still holds them. The 64me
// refusal used to fire for it anyway. It now updates, and the data is still
// there in the recreated container.
//
// Every object this test creates is named or collected here and removed by name.
func Test_UpdateContainer_Standalone_60vd_VolumesFrom(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	stamp := time.Now().UnixNano()
	src := fmt.Sprintf("capstan-it-60vd-src-%d", stamp)
	name := fmt.Sprintf("capstan-it-60vd-rm-%d", stamp)
	namedVol := src + "-vol"
	volumes := map[string]bool{namedVol: true}

	// Cleanups run last-registered first: containers, then their volumes.
	t.Cleanup(func() {
		for v := range volumes {
			_ = exec.Command("docker", "volume", "rm", v).Run()
		}
	})
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "name=^/"+name+"-capstan-old-").Output()
		for _, id := range strings.Fields(string(out)) {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
		_ = exec.Command("docker", "rm", "-f", src).Run()
	})

	docker := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", args...).CombinedOutput()
		require.NoError(t, err, "docker %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		return strings.TrimSpace(string(out))
	}

	docker("create", "--name", src, "-v", namedVol+":/named", "-v", "/anon", updateTestImage, "true")
	for _, v := range strings.Fields(docker("inspect", "-f",
		`{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}} {{end}}{{end}}`, src)) {
		volumes[v] = true
	}
	id := docker("run", "-d", "--rm", "--name", name, "--volumes-from", src, updateTestImage, "sleep", "600")
	docker("exec", name, "sh", "-c", "echo keep > /named/f && echo keep > /anon/f")

	svc := newDockerService(t, t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	_, ar := svc.UpdateContainer(ctx, id, nil)
	cancel()

	require.NotEqual(t, "failed", string(ar.Outcome), "%s (%v)", ar.Reason, ar.Err)
	assert.NotEqual(t, id, docker("inspect", "-f", "{{.Id}}", name), "the update recreated the container")
	assert.Equal(t, "keep\nkeep", docker("exec", name, "sh", "-c", "cat /named/f /anon/f"),
		"both volumes from the source kept their data")
}
