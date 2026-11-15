package services

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-rpbx. Status listed containers with `docker compose ps` and no -a,
// so a stopped container was not listed at all and a partly stopped stack read
// "running". Compose v5.7.1 (probed 2026-11-15, wk-dm-status): without -a, ps
// lists running containers only and no one-offs; with -a it lists stopped
// containers AND one-off containers from `compose run` (Labels carries
// com.docker.compose.oneoff=True).
//
// rpbxCompose stands in for the docker binary with that behaviour: for `ps` it
// prints the rows compose would print for the argv it got; every other
// subcommand exits 0 and prints nothing.

type rpbxRow struct {
	name, state string
	oneOff      bool
}

func rpbxCompose(t *testing.T, rows []rpbxRow) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		if !slices.Contains(arg, "ps") {
			return exec.CommandContext(ctx, "true")
		}
		all := slices.Contains(arg, "-a") || slices.Contains(arg, "--all")
		var out strings.Builder
		for _, r := range rows {
			// Without -a, like `docker ps`: running, paused and restarting only.
			listedByDefault := r.state == "running" || r.state == "paused" || r.state == "restarting"
			if !all && (r.oneOff || !listedByDefault) {
				continue
			}
			oneOff := "False"
			if r.oneOff {
				oneOff = "True"
			}
			out.WriteString(composePSLine(t, arg,
				map[string]any{"ID": r.name, "Name": r.name, "Service": r.name, "State": r.state, "Image": "busybox:latest"},
				map[string]string{"com.docker.compose.project": "proj", "com.docker.compose.oneoff": oneOff}))
			out.WriteByte('\n')
		}
		//nolint:gosec // G204: a test fixture's own JSON, passed as an argument, not a shell string
		return exec.CommandContext(ctx, "printf", "%s", out.String())
	}
	t.Cleanup(func() { execCommandContext = orig })
}

func TestStatus_ListsStoppedContainers_Rpbx(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	rpbxCompose(t, []rpbxRow{{name: "proj-a-1", state: "running"}, {name: "proj-b-1", state: "exited"}})

	status, containers, err := svc.Status(stack)
	require.NoError(t, err)
	assert.Equal(t, "partial", status, "one running and one stopped container is a partly stopped stack")
	assert.Equal(t, []string{"proj-a-1", "proj-b-1"}, containerNames(containers))
}

func TestStatus_StateMapping_Rpbx(t *testing.T) {
	cases := []struct {
		name string
		rows []rpbxRow
		want string
	}{
		{"all running", []rpbxRow{{name: "a", state: "running"}, {name: "b", state: "running"}}, "running"},
		{"all exited is stopped, not partial", []rpbxRow{{name: "a", state: "exited"}, {name: "b", state: "exited"}}, "stopped"},
		{"created and dead are stopped too", []rpbxRow{{name: "a", state: "created"}, {name: "b", state: "dead"}}, "stopped"},
		{"a restarting container is not stopped", []rpbxRow{{name: "a", state: "restarting"}, {name: "b", state: "exited"}}, "partial"},
		{"no containers", nil, "stopped"},
		{"a running one-off does not make a stopped stack partial", []rpbxRow{{name: "a", state: "exited"}, {name: "a-run", state: "running", oneOff: true}}, "stopped"},
		{"an exited one-off does not make a running stack partial", []rpbxRow{{name: "a", state: "running"}, {name: "a-run", state: "exited", oneOff: true}}, "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, stack := stubbedStatusService(t)
			rpbxCompose(t, tc.rows)
			status, containers, err := svc.Status(stack)
			require.NoError(t, err)
			assert.Equal(t, tc.want, status)
			for _, c := range containers {
				assert.NotContains(t, c.Name, "-run", "one-off containers are not listed by Status")
			}
		})
	}
}

// The restart wait loop (RestartVerified) waits up to 30 s for Status to say
// "stopped". With -a and the old mapping a stack left with exited containers
// read "partial", so every such restart waited the full 30 s.
func TestRestartVerified_WaitLoopSeesStopped_Rpbx(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	rpbxCompose(t, []rpbxRow{{name: "a", state: "exited"}, {name: "b", state: "exited"}})

	start := time.Now()
	svc.RestartVerified(stack)
	assert.Less(t, time.Since(start), 10*time.Second, "the wait loop must stop at once on a stack whose containers are all exited")
}

// observeRunState decides whether a backup restarts the stack afterwards. A
// stopped stack read as running would be STARTED by a backup.
func TestObserveRunState_AllExitedIsNotRunning_Rpbx(t *testing.T) {
	svc, stack := stubbedStatusService(t)
	rpbxCompose(t, []rpbxRow{{name: "a", state: "exited"}, {name: "b", state: "exited"}})
	backup := &BackupService{docker: svc, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	assert.Equal(t, runStateStopped, backup.observeRunState(stack, "s1"))

	rpbxCompose(t, []rpbxRow{{name: "a", state: "running"}, {name: "b", state: "exited"}})
	assert.Equal(t, runStateRunning, backup.observeRunState(stack, "s1"), "positive side: a partly running stack is running")
}
