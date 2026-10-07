package truth

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// RemoteRegistryDigest's two `docker buildx imagetools inspect` children are
// read with Output, which waits for every holder of the stdout pipe. The child
// here is a shell whose grandchild keeps that pipe open, which is how the
// docker CLI behaves when it runs buildx as a plugin subprocess. Killing the
// direct child at the deadline does not reach the grandchild, so only
// WaitDelay can release the caller (agent-os-z91e.26). The sleep outlasts every
// bound so a call cannot pass by the grandchild finishing on its own.
func TestImagetoolsCmds_GrandchildHoldingThePipeEndsAtTheContext(t *testing.T) {
	const timeout = 300 * time.Millisecond
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)

	builders := map[string]func(context.Context, string) *exec.Cmd{
		"raw":     buildImagetoolsRawCmd,
		"verbose": buildImagetoolsVerboseCmd,
	}
	scripts := map[string]string{
		"in-group grandchild": "sleep 20 & exec sleep 20",
		"setsid grandchild":   "setsid sleep 20 & exec sleep 20",
	}
	for bName, build := range builders {
		for sName, script := range scripts {
			t.Run(bName+"/"+sName, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				cmd := build(ctx, "example.com/img:tag")
				cmd.Path = sh
				cmd.Args = []string{"sh", "-c", script}

				done := make(chan error, 1)
				go func() {
					_, err := cmd.Output()
					done <- err
				}()

				// imagetoolsWaitDelay (5s) plus slack, as a literal so this
				// test builds against the pre-fix code and fails on the assertion.
				const bound = 8 * time.Second
				select {
				case err := <-done:
					require.Error(t, err, "the child was killed at the deadline, so Output must not report success")
				case <-time.After(bound): // wall-clock ok: the fixed call returns within WaitDelay; the broken one waits 20s for the grandchild
					t.Fatalf("imagetools %s still running %s after a %s context: a grandchild holding the pipe keeps it open", bName, bound, timeout)
				}
			})
		}
	}
}
