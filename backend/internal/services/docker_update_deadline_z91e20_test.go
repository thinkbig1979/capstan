package services

import (
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// stubComposeBySubcommand points execCommandContext at `sh -c <script>`, picking
// the script from the compose subcommand in argv ("pull" or "up"), so one test
// can let pull succeed and hang up. Every other subcommand runs `true`.
func stubComposeBySubcommand(t *testing.T, pull, up string) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		script := "true"
		switch {
		case slices.Contains(args, "pull"):
			script = pull
		case slices.Contains(args, "up"):
			script = up
		}
		//nolint:gosec // G204: script is a literal from this file
		return exec.CommandContext(ctx, "sh", "-c", script)
	}
	t.Cleanup(func() { execCommandContext = orig })
}

// hangShapes are the two ways a child outlives its deadline. `exec sleep` is
// killed directly; plain `sleep` is a grandchild that keeps the output pipe open
// after the shell is killed, as the docker CLI's compose plugin does. The sleep
// outlasts every bound below so a missing deadline cannot pass by the child
// exiting on its own.
var hangShapes = []struct {
	name   string
	script string
	bound  time.Duration // timeout + WaitDelay + slack
}{
	{"direct child", "exec sleep 20", 3 * time.Second},
	{"grandchild holds the pipe", "sleep 20", commandWaitDelay + 3*time.Second},
}

// TestUpdateComposeContainer_HungChildTimesOutAndNamesTheTimeout is
// agent-os-z91e.20's acceptance: the pull and up children of the auto-update
// path are bounded and a timeout reads as one. The caller's context here has no
// deadline of its own, so the bound has to come from the command itself.
func TestUpdateComposeContainer_HungChildTimesOutAndNamesTheTimeout(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, step := range []struct{ name, label string }{
		{"pull", "docker compose pull timed out after 300ms"},
		{"up", "docker compose up timed out after 300ms"},
	} {
		for _, shape := range hangShapes {
			t.Run(step.name+"/"+shape.name, func(t *testing.T) {
				pull, up := "true", "true"
				if step.name == "pull" {
					pull = shape.script
				} else {
					up = shape.script
				}
				stubComposeBySubcommand(t, pull, up)

				dir := t.TempDir()
				svc := &DockerService{config: &config.Config{StacksDir: dir, ComposeTimeout: timeout}}
				stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}

				done := make(chan error, 1)
				go func() {
					// wasRunning=true skips the post-recreate branch that needs a Docker client.
					done <- svc.updateComposeContainer(context.Background(), stack, "web", true)
				}()

				select {
				case err := <-done:
					require.Error(t, err)
					assert.Contains(t, err.Error(), step.label)
				case <-time.After(shape.bound):
					t.Fatalf("updateComposeContainer still running %s after a %s deadline: the %s child is unbounded", shape.bound, timeout, step.name)
				}
			})
		}
	}
}

// TestStreamComposeCmd_HungChildTimesOutAndNamesTheTimeout covers the streamed
// update path, which has its own pipe handling: the scanners must not wait on a
// pipe a grandchild still holds, or WaitDelay never gets the chance to fire.
func TestStreamComposeCmd_HungChildTimesOutAndNamesTheTimeout(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, shape := range hangShapes {
		t.Run(shape.name, func(t *testing.T) {
			stubComposeBySubcommand(t, shape.script, shape.script)

			done := make(chan error, 1)
			go func() {
				done <- streamComposeCmd(context.Background(), timeout, []string{"compose", "pull", "--", "web"}, t.TempDir(), nil, func(LogLine) {})
			}()

			select {
			case err := <-done:
				require.Error(t, err)
				assert.Contains(t, err.Error(), "docker compose timed out after 300ms")
			case <-time.After(shape.bound):
				t.Fatalf("streamComposeCmd still running %s after a %s deadline: the child is unbounded", shape.bound, timeout)
			}
		})
	}
}
