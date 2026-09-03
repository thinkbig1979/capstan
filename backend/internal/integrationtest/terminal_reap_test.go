//go:build integration

// Package integrationtest — terminal session reaping (agent-os-pnbj).
//
// TerminalService.CreateSession spawns the interactive shell as a LOCAL
// `docker exec -it -- <container> <shell>` CLI process. CloseSession only ever
// touches that local CLI process and the local pty pair between this process
// and the CLI — it never reaches the shell that the CLI's `-it` connection
// caused the daemon to run *inside* the container. So a closed/revoked
// terminal session leaves a live, fully-functional shell running in the
// container, with the caller's access intact.
//
// The instrument for all three arms is `docker top <container>`, which reports
// what is actually running inside the container as tracked by the daemon —
// never the local `docker exec` CLI's own pid, which trivially dies on
// Process.Kill() regardless of whether the remote shell was reaped. Asserting
// on the local CLI pid is exactly the kind of check that cannot fail; it would
// pass green against the live bug.
//
// Each arm gets its own freshly-started container (a stopped-then-restarted or
// reused container previously showed cross-arm contamination in manual
// testing: arm A's orphaned shell was still present when arm B's control ran,
// so the control could not discriminate zero from non-zero).
package integrationtest

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/services"
)

// terminalReapStackYAML is a single long-running service so each arm has a
// stable container to attach a terminal session to. sleep is PID 1 and is
// never a candidate shell process.
const terminalReapStackYAML = `services:
  target:
    image: alpine:3.21
    command: ["sleep", "3600"]
    restart: "no"
`

// startTerminalReapContainer brings up a fresh single-container stack and
// returns the resulting container name (matching compose's
// "<project>-<service>-1" naming).
func startTerminalReapContainer(t *testing.T) (containerName, project string) {
	t.Helper()

	dir, project, _ := NewTempStack(t, terminalReapStackYAML)
	RunCompose(t, dir, project, "up", "-d")
	AssertContainerState(t, dir, project, "target", true)

	containerName = project + "-target-1"
	return containerName, project
}

// dockerTopShellCount counts processes inside containerName whose short
// command name is exactly "sh" or "bash" — i.e. live terminal shells, as
// opposed to the container's own PID 1 (sleep) or a transient `ps`/`docker
// top` helper process. It uses `docker top <container> -eo comm` (the same
// daemon-side accounting `docker top` always uses, not an exec into the
// container), so it works whether or not the container image ships a `ps`
// binary, and it cannot be fooled by killing the local `docker exec` CLI.
func dockerTopShellCount(t *testing.T, containerName string) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// docker top needs the PID field present internally to match processes to
	// the container's cgroup ("Couldn't find PID field in ps output" without
	// it) even though this helper only cares about the comm column.
	cmd := exec.CommandContext(ctx, "docker", "top", containerName, "-eo", "pid,comm")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "docker top %s: %v\n%s", containerName, err, out)

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	count := 0
	for _, line := range lines[1:] { // skip the "PID COMMAND" header line
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[len(fields)-1]
		if name == "sh" || name == "bash" {
			count++
		}
	}
	return count
}

// waitForShellCount polls dockerTopShellCount until it equals want or the
// 10-second budget expires, then asserts. Reaping happens over a real docker
// exec round trip, so a single immediate sample would be timing-sensitive.
func waitForShellCount(t *testing.T, containerName string, want int, msgAndArgs ...interface{}) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var last int
	for {
		last = dockerTopShellCount(t, containerName)
		if last == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dockerTopShellCount(%s) = %d, want %d (timed out after 10s); %v",
				containerName, last, want, msgAndArgs)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestCloseSession_ReapsShellInsideContainer is the REAP ARM. It must FAIL
// before the fix: CloseSession kills only the local `docker exec` CLI, so the
// shell survives inside the container.
func TestCloseSession_ReapsShellInsideContainer(t *testing.T) {
	RequireDocker(t)
	containerName, project := startTerminalReapContainer(t)

	require.Equal(t, 0, dockerTopShellCount(t, containerName), "sanity: no shell before a session exists")

	svc := services.NewTerminalService(&config.Config{})
	session, err := svc.CreateSession(project, containerName)
	require.NoError(t, err)

	waitForShellCount(t, containerName, 1, "shell should appear after CreateSession")

	svc.CloseSession(session.ID)

	waitForShellCount(t, containerName, 0, "shell must be reaped inside the container after CloseSession")
}

// TestCloseSession_PositiveControl_ShellExitingOnItsOwnReachesZero proves the
// instrument itself can observe a return to zero shells — i.e. a passing REAP
// ARM assertion is possible in principle, not a check that can never fail.
func TestCloseSession_PositiveControl_ShellExitingOnItsOwnReachesZero(t *testing.T) {
	RequireDocker(t)
	containerName, project := startTerminalReapContainer(t)

	svc := services.NewTerminalService(&config.Config{})
	session, err := svc.CreateSession(project, containerName)
	require.NoError(t, err)
	defer svc.CloseSession(session.ID)

	waitForShellCount(t, containerName, 1, "shell should appear after CreateSession")

	_, writeErr := session.Pty.Write([]byte("exit\n"))
	require.NoError(t, writeErr)

	waitForShellCount(t, containerName, 0, "shell exiting on its own must reach zero")
}

// TestCloseSession_NegativeControl_OpenSessionShowsShell proves the instrument
// can also observe a non-zero count, so "zero" in the other two arms is
// informative rather than a value the instrument always reports.
func TestCloseSession_NegativeControl_OpenSessionShowsShell(t *testing.T) {
	RequireDocker(t)
	containerName, project := startTerminalReapContainer(t)

	svc := services.NewTerminalService(&config.Config{})
	session, err := svc.CreateSession(project, containerName)
	require.NoError(t, err)
	defer svc.CloseSession(session.ID)

	waitForShellCount(t, containerName, 1, "an open session must show its shell")
}
