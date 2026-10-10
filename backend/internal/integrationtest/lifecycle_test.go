//go:build integration

// Package integrationtest — stack lifecycle integration tests (B2).
//
// These tests exercise the full lifecycle action + verification path against a
// real Docker daemon using the integrationtest harness.
//
// Tests covered:
//
//  1. Start a healthy pinned-image stack → action reports outcome=success and
//     the container is confirmed running.
//
//     2a. Start a stack whose single service exits immediately (exit 1, restart:"no")
//     → action reports outcome=failed or partial, NEVER success.
//
//     2b. Start a stack whose single service runs briefly then exits (sleep 0.7; exit 1)
//     → must also report failed/partial, NOT success. This is the slow-crash guard
//     that the old single-interval pollUntilStable got wrong.
//
//  3. Stop a running stack → action reports outcome=success and the container is
//     confirmed gone.
//
//     4a/4b. Streaming path for the same crash-loop and slow-crash cases: done frame
//     must have success=false and outcome=failed|partial.
package integrationtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/services"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// healthyStackYAML is a small single-service stack that runs indefinitely.
const healthyStackYAML = `services:
  sleeper:
    image: public.ecr.aws/docker/library/alpine:3.21
    command: ["sleep", "3600"]
    restart: "no"
`

// crashLoopStackYAML exits immediately (exit 1) with no restart.
// docker compose up exits 0, but the container lands in "exited" state.
const crashLoopStackYAML = `services:
  crasher:
    image: public.ecr.aws/docker/library/alpine:3.21
    command: ["sh", "-c", "exit 1"]
    restart: "no"
`

// slowCrashStackYAML runs for ~0.7 s then exits. This is the harder case:
// a single-sample poller sees the container as "running" on the first check
// and incorrectly reports success. The dwell window must catch this.
const slowCrashStackYAML = `services:
  slowcrasher:
    image: public.ecr.aws/docker/library/alpine:3.21
    command: ["sh", "-c", "sleep 0.7; exit 1"]
    restart: "no"
`

// newDockerService creates a DockerService for integration tests.
func newDockerService(t *testing.T, dir string) *services.DockerService {
	t.Helper()

	cfg := &config.Config{StacksDir: dir}
	svc, err := services.NewDockerService(cfg)
	require.NoError(t, err, "DockerService must connect to the local daemon")
	return svc
}

// tempStack creates a temp directory with compose.yaml and a minimal Stack model.
// EnvFile is left empty so buildComposeArgs does not reference a missing .env.
//
// The stack's ProjectName is the one NewTempStack returns, because that is the
// name its cleanup runs `compose down` under. Callers read stack.ProjectName
// and never pick a second name: a stack started under a different name than
// the cleanup's is never taken down (agent-os-6e64).
func tempStack(t *testing.T, yaml string) (models.Stack, func()) {
	t.Helper()

	dir, project, cleanup := NewTempStack(t, yaml)

	// NewTempStack already writes compose.yaml; rewrite with the exact content
	// passed (belt-and-suspenders — same bytes, but guarantees our YAML wins).
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("tempStack: write compose.yaml: %v", err)
	}

	stack := models.Stack{
		ID:          project,
		Directory:   dir,
		ComposeFile: "compose.yaml",
		// EnvFile deliberately empty: temp dir has no .env file.
		ProjectName: project,
	}
	return stack, cleanup
}

// assertCrashOutcome is the shared assertion for all crash-loop variants: the
// outcome must be failed or partial, never success.
func assertCrashOutcome(t *testing.T, ar truth.ActionResult, label string) {
	t.Helper()
	t.Logf("%s outcome=%s reason=%q", label, ar.Outcome, ar.Reason)

	assert.NotEqual(t, truth.OutcomeSuccess, ar.Outcome,
		"%s must NOT report outcome=success (false-success guard)", label)
	assert.Contains(t,
		[]truth.Outcome{truth.OutcomeFailed, truth.OutcomePartial},
		ar.Outcome,
		"%s outcome must be failed or partial", label)
}

// ---- Test_Lifecycle_Start_Success ----

func Test_Lifecycle_Start_Success(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, healthyStackYAML)
	defer cleanup()

	svc := newDockerService(t, stack.Directory)

	ar, output := svc.StartVerified(stack)
	t.Logf("StartVerified outcome=%s reason=%q output_len=%d", ar.Outcome, ar.Reason, len(output))

	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome,
		"starting a healthy stack must report outcome=success")
	assert.Nil(t, ar.Err, "Err must be nil on success")

	AssertContainerState(t, stack.Directory, stack.ProjectName, "sleeper", true)
}

// ---- Test_Lifecycle_Start_CrashLoop (immediate exit) ----

func Test_Lifecycle_Start_CrashLoop(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, crashLoopStackYAML)
	defer cleanup()

	svc := newDockerService(t, stack.Directory)

	ar, output := svc.StartVerified(stack)
	assertCrashOutcome(t, ar, "StartVerified(immediate-exit)")
	_ = output

	AssertContainerState(t, stack.Directory, stack.ProjectName, "crasher", false)
}

// ---- Test_Lifecycle_Start_SlowCrash (sleep 0.7; exit 1) ----
// This is the blocker case: the old single-sample poller reported success here.

func Test_Lifecycle_Start_SlowCrash(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, slowCrashStackYAML)
	defer cleanup()

	svc := newDockerService(t, stack.Directory)

	ar, output := svc.StartVerified(stack)
	assertCrashOutcome(t, ar, "StartVerified(slow-crash)")
	_ = output

	AssertContainerState(t, stack.Directory, stack.ProjectName, "slowcrasher", false)
}

// ---- Test_Lifecycle_Stop_Success ----

func Test_Lifecycle_Stop_Success(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, healthyStackYAML)
	defer cleanup()

	svc := newDockerService(t, stack.Directory)

	startAR, _ := svc.StartVerified(stack)
	require.Equal(t, truth.OutcomeSuccess, startAR.Outcome,
		"prerequisite: stack must start successfully before stop test")

	AssertContainerState(t, stack.Directory, stack.ProjectName, "sleeper", true)

	ar, output := svc.StopVerified(stack)
	t.Logf("StopVerified outcome=%s reason=%q output_len=%d", ar.Outcome, ar.Reason, len(output))

	assert.Equal(t, truth.OutcomeSuccess, ar.Outcome,
		"stopping a running stack must report outcome=success")
	assert.Nil(t, ar.Err, "Err must be nil on success")

	AssertContainerState(t, stack.Directory, stack.ProjectName, "sleeper", false)
}

// ---- Test_Lifecycle_Streaming_CrashLoop (immediate exit) ----

func Test_Lifecycle_Streaming_CrashLoop(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, crashLoopStackYAML)
	defer cleanup()

	svc := newDockerService(t, stack.Directory)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	lastFrame := drainStreamingDone(t, svc, ctx, stack)
	assertStreamingCrashDone(t, lastFrame, "streaming(immediate-exit)")
}

// ---- Test_Lifecycle_Streaming_SlowCrash (sleep 0.7; exit 1) ----
// The blocker case on the streaming path: done frame must not carry success=true.

func Test_Lifecycle_Streaming_SlowCrash(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, slowCrashStackYAML)
	defer cleanup()

	svc := newDockerService(t, stack.Directory)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	lastFrame := drainStreamingDone(t, svc, ctx, stack)
	assertStreamingCrashDone(t, lastFrame, "streaming(slow-crash)")
}

// drainStreamingDone runs RunStreaming("up", ["-d"]) and returns the terminal
// done frame. Logs all frames for debugging.
func drainStreamingDone(t *testing.T, svc *services.DockerService, ctx context.Context, stack models.Stack) services.StreamLine {
	t.Helper()

	ch := svc.RunStreaming(ctx, stack, "up", []string{"-d"})

	var lastFrame services.StreamLine
	for line := range ch {
		t.Logf("stream frame: type=%s line=%q success=%v outcome=%s reason=%q",
			line.Type, line.Line, line.Success, line.Outcome, line.Reason)
		if line.Type == "done" {
			lastFrame = line
		}
	}

	require.Equal(t, "done", lastFrame.Type, "last frame must be the done frame")
	return lastFrame
}

// assertStreamingCrashDone asserts the done frame from a crash-loop stack is
// correctly classified as non-success.
func assertStreamingCrashDone(t *testing.T, frame services.StreamLine, label string) {
	t.Helper()

	assert.False(t, frame.Success,
		"%s: done frame success must be false for a crashing service (finding #5 fix)", label)
	assert.NotEmpty(t, frame.Outcome,
		"%s: done frame must carry a non-empty outcome field (finding #18 fix)", label)
	assert.NotEqual(t, truth.OutcomeSuccess, frame.Outcome,
		"%s: done frame outcome must not be success for a crashing service", label)
	assert.NotEmpty(t, frame.Reason,
		"%s: done frame must carry a reason string", label)
}

// ---- Test_Lifecycle_TempStackCleanupRemovesStartedStack ----

// composeProjectResources returns the container IDs labelled with the compose
// project and whether its <project>_default network exists.
func composeProjectResources(t *testing.T, project string) (containers []string, hasNetwork bool) {
	t.Helper()

	out, err := exec.Command("docker", "ps", "-a", "-q",
		"--filter", "label=com.docker.compose.project="+project).CombinedOutput()
	require.NoError(t, err, "docker ps: %s", out)
	containers = strings.Fields(string(out))

	out, err = exec.Command("docker", "network", "ls",
		"--filter", "name="+project+"_default", "--format", "{{.Name}}").CombinedOutput()
	require.NoError(t, err, "docker network ls: %s", out)
	for _, name := range strings.Fields(string(out)) {
		if name == project+"_default" {
			hasNetwork = true
		}
	}
	return containers, hasNetwork
}

// A temp stack started under stack.ProjectName must be gone once the cleanup
// tempStack returned has run. Before agent-os-6e64 tempStack discarded
// NewTempStack's project name, so the cleanup downed a project that never
// started and every Test_Lifecycle_* run left containers and a
// <project>_default network behind.
func Test_Lifecycle_TempStackCleanupRemovesStartedStack(t *testing.T) {
	RequireDocker(t)
	PullPinnedImage(t, "public.ecr.aws/docker/library/alpine:3.21")

	stack, cleanup := tempStack(t, healthyStackYAML)
	// Safety net for the failing case only: a leak found below must not outlive
	// this test. Registered after tempStack's own cleanup, so it runs first.
	t.Cleanup(func() {
		down := exec.Command("docker", "compose", "-p", stack.ProjectName,
			"-f", filepath.Join(stack.Directory, "compose.yaml"),
			"down", "-v", "--remove-orphans")
		down.Dir = stack.Directory
		_ = down.Run()
	})

	RunCompose(t, stack.Directory, stack.ProjectName, "up", "-d")

	// Control: the instrument must see the started stack before cleanup, or a
	// "gone" result after cleanup proves nothing.
	containers, hasNetwork := composeProjectResources(t, stack.ProjectName)
	require.NotEmpty(t, containers, "started stack must have a container under project %q", stack.ProjectName)
	require.True(t, hasNetwork, "started stack must have network %s_default", stack.ProjectName)

	cleanup()

	containers, hasNetwork = composeProjectResources(t, stack.ProjectName)
	assert.Empty(t, containers,
		"cleanup must remove every container of project %q it was asked to tear down", stack.ProjectName)
	assert.False(t, hasNetwork,
		"cleanup must remove network %s_default", stack.ProjectName)
}
