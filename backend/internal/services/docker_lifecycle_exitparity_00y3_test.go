package services

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// agent-os-00y3: a compose run that exits non-zero is FAILED on both the REST
// verbs (StopVerified/StartVerified) and the operations WebSocket path
// (RunStreaming), even when the end state looks like what was asked for
// (Edwin, 2026-11-13). RunStreaming used to verify the end state with the exit
// error in hand and classifyContainers' success arms dropped it, so the same
// child read "failed" on REST and "success" over the socket.

// stubArgsChild00y3 runs script as the docker CLI, with the real compose argv
// as its arguments, so one script can answer `down`, `up` and `ps` differently.
func stubArgsChild00y3(t *testing.T, script string) {
	t.Helper()
	origCmd, origCtx := execCommand, execCommandContext
	execCommand = func(_ string, args ...string) *exec.Cmd {
		//nolint:gosec // G204: script is a literal from this file
		return exec.Command("sh", append([]string{"-c", script, "docker"}, args...)...)
	}
	execCommandContext = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		//nolint:gosec // G204: script is a literal from this file
		return exec.CommandContext(ctx, "sh", append([]string{"-c", script, "docker"}, args...)...)
	}
	t.Cleanup(func() { execCommand, execCommandContext = origCmd, origCtx })
}

func exitParity00y3Fixture(t *testing.T) (*DockerService, models.Stack) {
	t.Helper()
	dir := t.TempDir()
	svc := &DockerService{config: &config.Config{StacksDir: dir, ComposeTimeout: 5 * time.Second}}
	return svc, models.Stack{ID: "s1", Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}
}

func lastStreamLine00y3(t *testing.T, ch <-chan StreamLine) (StreamLine, []StreamLine) {
	t.Helper()
	var all []StreamLine
	for line := range ch {
		all = append(all, line)
	}
	require.NotEmpty(t, all)
	return all[len(all)-1], all
}

func TestRunStreaming_ExitParity_00y3(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		subcommand string
		extraArgs  []string
		rest       func(*DockerService, models.Stack) (truth.ActionResult, string)
		reason     string
	}{
		{
			// down removes the containers, then exits 1 (a network it could
			// not remove, say); ps then lists nothing.
			name:       "down",
			script:     `case "$*" in *" down"*) echo "Container p-web-1  Removed"; exit 1;; *) exit 0;; esac`,
			subcommand: "down",
			rest:       (*DockerService).StopVerified,
			reason:     "compose down failed",
		},
		{
			// up exits 1 while ps shows every container running.
			name:       "up",
			script:     `case "$*" in *" up "*) echo "Container p-web-1  Started"; echo "some hook failed" 1>&2; exit 1;; *" ps "*) echo '{"ID":"a","Name":"p-web-1","Service":"web","State":"running","Health":""}'; exit 0;; *) exit 0;; esac`,
			subcommand: "up",
			extraArgs:  []string{"-d"},
			rest:       (*DockerService).StartVerified,
			reason:     "compose up failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubArgsChild00y3(t, tc.script)
			svc, stack := exitParity00y3Fixture(t)

			restAR, _ := tc.rest(svc, stack)
			require.Equal(t, truth.OutcomeFailed, restAR.Outcome, "REST is the reference: %+v", restAR)

			done, all := lastStreamLine00y3(t, svc.RunStreaming(context.Background(), stack, tc.subcommand, tc.extraArgs))
			require.Equal(t, "done", done.Type, "frames: %+v", all)
			assert.Equal(t, restAR.Outcome, done.Outcome, "WS and REST disagree on one child: %+v", done)
			assert.False(t, done.Success, "frames: %+v", all)
			assert.Equal(t, tc.reason, done.Reason)
			assert.Contains(t, done.Error, "exit status 1")
		})
	}
}

// The other side on the same instrument: the same verified end state with a
// zero exit is still success on both paths, so the rule is "the exit code
// counts", not "streaming always fails".
func TestRunStreaming_ExitParity_00y3_ZeroExitStillSucceeds(t *testing.T) {
	stubArgsChild00y3(t, `case "$*" in *" down"*) echo "Container p-web-1  Removed"; exit 0;; *) exit 0;; esac`)
	svc, stack := exitParity00y3Fixture(t)

	restAR, _ := svc.StopVerified(stack)
	require.Equal(t, truth.OutcomeSuccess, restAR.Outcome, "%+v", restAR)
	done, all := lastStreamLine00y3(t, svc.RunStreaming(context.Background(), stack, "down", nil))
	require.Equal(t, "done", done.Type, "frames: %+v", all)
	assert.Equal(t, restAR.Outcome, done.Outcome)
	assert.True(t, done.Success)
}

// A compose process that cannot be started at all (here: its working
// directory does not exist) ends in a failed DONE frame, not a bare error
// frame. operations.go stops a restart only on a failed done frame, so an
// error frame alone let a restart whose down never ran go on to start.
func TestRunStreaming_ExitParity_00y3_StartFailureEndsInAFailedDoneFrame(t *testing.T) {
	stubArgsChild00y3(t, `exit 0`)
	svc, stack := exitParity00y3Fixture(t)
	stack.Directory = stack.Directory + "/does-not-exist"

	done, all := lastStreamLine00y3(t, svc.RunStreaming(context.Background(), stack, "down", nil))
	require.Equal(t, "done", done.Type, "frames: %+v", all)
	assert.Len(t, all, 1, "one terminal frame: %+v", all)
	assert.False(t, done.Success)
	assert.Equal(t, truth.OutcomeFailed, done.Outcome)
	assert.Equal(t, "compose down failed", done.Reason)
	assert.Contains(t, done.Error, "failed to start command")
}
