//go:build integration

package integrationtest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_UpdateContainer_Standalone_KeepsAnonymousVolumeData (agent-os-3mox): a
// standalone update recreated the container from its HostConfig verbatim, which
// names no source for an anonymous volume, so the daemon gave the new container
// fresh, empty volumes and left the old ones dangling for the next volume prune.
// Every update hit it, including one where the image was already current.
//
// Three cases on one container: `-v /data` (Config.Volumes), an anonymous
// `--mount type=volume,target=/mnt2` (a source-less HostConfig.Mounts entry),
// and the named-volume control, which always survived. A second update checks
// that the carried mounts do not collide with themselves on the next recreate.
// A named volume at a destination typed with a trailing slash ("/slash/") is
// stored that way in the HostConfig and reported cleaned in the mounts; a
// carry that compares the two raw mounts it twice and every update is refused.
//
// The image-declared VOLUME case is covered by the fake-daemon test in
// services: updateTestImage declares no VOLUME, and an image built here could
// not be pulled by the update.
//
// Every object this test creates is named or collected here and removed by
// name; the anonymous volumes have hex names, so they are read from inspect.
func Test_UpdateContainer_Standalone_KeepsAnonymousVolumeData(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, updateTestImage)

	name := fmt.Sprintf("capstan-it-3mox-%d", time.Now().UnixNano())
	named := name + "-named"
	slash := name + "-slash"
	leftover := "^/" + name + "-capstan-old-"

	var mu sync.Mutex
	volumes := map[string]bool{named: true, slash: true}
	collect := func() {
		out, err := exec.Command("docker", "inspect", "-f",
			`{{range .Mounts}}{{if eq .Type "volume"}}{{.Name}} {{end}}{{end}}`, name).CombinedOutput()
		if err != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, v := range strings.Fields(string(out)) {
			volumes[v] = true
		}
	}
	// Cleanups run last-registered first: containers, then their volumes.
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
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

	out, err := exec.Command("docker", "run", "-d", "--name", name,
		"-v", "/data",
		"--mount", "type=volume,target=/mnt2,volume-label=capstan-it-3mox=1",
		"-v", named+":/named",
		"-v", slash+":/slash/",
		updateTestImage, "sleep", "600").CombinedOutput()
	require.NoError(t, err, "docker run: %s", strings.TrimSpace(string(out)))
	id := strings.TrimSpace(string(out))
	collect()

	out, err = exec.Command("docker", "exec", name, "sh", "-c",
		"echo keep-data > /data/f && echo keep-mnt2 > /mnt2/f && echo keep-named > /named/f && echo keep-slash > /slash/f").CombinedOutput()
	require.NoError(t, err, "writing the files: %s", strings.TrimSpace(string(out)))

	svc := newDockerService(t, t.TempDir())

	for round := 1; round <= 2; round++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		_, ar := svc.UpdateContainer(ctx, id, nil)
		cancel()
		collect()
		require.NotEqual(t, "failed", string(ar.Outcome), "update %d: %s (%v)", round, ar.Reason, ar.Err)

		out, err := exec.Command("docker", "inspect", "-f", "{{.Id}}", name).CombinedOutput()
		require.NoError(t, err, "update %d: the container exists: %s", round, strings.TrimSpace(string(out)))
		newID := strings.TrimSpace(string(out))
		require.NotEqual(t, id, newID, "update %d recreated the container", round)
		id = newID

		for path, want := range map[string]string{"/data/f": "keep-data", "/mnt2/f": "keep-mnt2", "/named/f": "keep-named", "/slash/f": "keep-slash"} {
			out, err := exec.Command("docker", "exec", name, "cat", path).CombinedOutput()
			assert.NoError(t, err, "update %d: %s is readable: %s", round, path, strings.TrimSpace(string(out)))
			assert.Equal(t, want, strings.TrimSpace(string(out)), "update %d: %s", round, path)
		}

		out, err = exec.Command("docker", "inspect", "-f",
			`{{range .Mounts}}{{.Destination}} {{end}}`, name).CombinedOutput()
		require.NoError(t, err, strings.TrimSpace(string(out)))
		dests := strings.Fields(string(out))
		assert.ElementsMatch(t, []string{"/data", "/mnt2", "/named", "/slash"}, dests,
			"update %d: one mount per destination", round)

		out, err = exec.Command("docker", "ps", "-aq", "--filter", "name="+leftover).CombinedOutput()
		require.NoError(t, err, strings.TrimSpace(string(out)))
		assert.Empty(t, strings.TrimSpace(string(out)), "update %d: the old container was removed", round)
	}
}
