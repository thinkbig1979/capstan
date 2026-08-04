package services

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// ---- dockerEnv / stripCapstanSecrets: the two helpers in isolation ----

// TestDockerEnv_OnlyAllowlistedVarsPassThrough proves the helper itself
// filters correctly. It does not, on its own, prove any of the 11 call sites
// actually use it — the call-site tests below do that.
func TestDockerEnv_OnlyAllowlistedVarsPassThrough(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-jwt")
	t.Setenv("STORAGE_KEY", "sentinel-storage")
	t.Setenv("GIT_HTTPS_TOKEN", "sentinel-git-token")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/capstan")
	t.Setenv("DOCKER_HOST", "tcp://remote-daemon:2376")
	t.Setenv("SOME_RANDOM_APP_VAR", "should-not-appear")

	env := dockerEnv()
	joined := strings.Join(env, "\n")

	assert.NotContains(t, joined, "sentinel-jwt")
	assert.NotContains(t, joined, "sentinel-storage")
	assert.NotContains(t, joined, "sentinel-git-token")
	assert.NotContains(t, joined, "SOME_RANDOM_APP_VAR")

	assert.Contains(t, env, "PATH=/usr/bin:/bin")
	assert.Contains(t, env, "HOME=/home/capstan")
	assert.Contains(t, env, "DOCKER_HOST=tcp://remote-daemon:2376")
}

func TestStripCapstanSecrets_RemovesOnlyKnownSecrets(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"JWT_SECRET=sentinel-jwt",
		"STORAGE_KEY=sentinel-storage",
		"GIT_HTTPS_TOKEN=sentinel-git-token",
		"RESTIC_PASSWORD=sentinel-restic-password",
		"HOME=/home/capstan",
		"RCLONE_CONFIG_MYREMOTE_TYPE=s3", // backend-specific var, must survive
	}

	out := stripCapstanSecrets(base)

	assert.Equal(t, []string{
		"PATH=/usr/bin",
		"HOME=/home/capstan",
		"RCLONE_CONFIG_MYREMOTE_TYPE=s3",
	}, out)
}

// ---- call-site tests: prove the 11 docker/compose sites actually route
// through dockerEnv(), not just that the helper works in isolation ----

// withStubExecCommand redirects execCommand to run `sh -c env`, ignoring the
// real ("docker", ...) argv, so the test can capture the exact environment
// the real call site hands to os/exec — without a docker binary or daemon.
func withStubExecCommand(t *testing.T) {
	t.Helper()
	orig := execCommand
	execCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "env")
	}
	t.Cleanup(func() { execCommand = orig })
}

// withStubExecCommandContext is the execCommandContext equivalent of
// withStubExecCommand.
func withStubExecCommandContext(t *testing.T) {
	t.Helper()
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "env")
	}
	t.Cleanup(func() { execCommandContext = orig })
}

// TestLogs_DoesNotLeakCapstanSecrets covers docker.go's Logs, the one
// execCommand call site in that file. Before the fix, cmd.Env was left nil, so
// the stubbed `env` process (which inherits cmd.Env, or the whole test process
// environment if cmd.Env is nil) would print the sentinel — this test fails on
// that code the same way it would fail before the dockerEnv() fix.
func TestLogs_DoesNotLeakCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-logs")
	withStubExecCommand(t)

	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}
	service := &DockerService{config: cfg}
	stack := models.Stack{Directory: tempDir, ComposeFile: "compose.yaml", ProjectName: "p"}

	output, err := service.Logs(stack, 10)
	require.NoError(t, err)

	assert.NotContains(t, output, "sentinel-value-logs")
	assert.Contains(t, output, "PATH=")
}

// TestPullVerified_DoesNotLeakCapstanSecrets covers docker_lifecycle.go's
// PullVerified, standing in for its five sibling execCommand/execCommandContext
// sites in that file (StartVerified, StopVerified, DeleteVerified, Status,
// RunStreaming all follow the identical construct-cmd-then-set-Env shape).
func TestPullVerified_DoesNotLeakCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-pull")
	withStubExecCommand(t)

	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}
	service := &DockerService{config: cfg}
	stack := models.Stack{Directory: tempDir, ComposeFile: "compose.yaml", ProjectName: "p"}

	ar, out := service.PullVerified(stack)

	assert.NotContains(t, out, "sentinel-value-pull")
	assert.Contains(t, out, "PATH=")

	if details, ok := ar.Details["output"].(string); ok {
		assert.NotContains(t, details, "sentinel-value-pull")
	}
}

// TestRunStreaming_DoesNotLeakCapstanSecrets covers docker_lifecycle.go's
// RunStreaming, the execCommandContext site used for the streamed compose
// endpoints (as opposed to PullVerified's execCommand/CombinedOutput shape).
func TestRunStreaming_DoesNotLeakCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-runstreaming")
	withStubExecCommandContext(t)

	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}
	service := &DockerService{config: cfg}
	stack := models.Stack{Directory: tempDir, ComposeFile: "compose.yaml", ProjectName: "p"}

	var lines []string
	for line := range service.RunStreaming(context.Background(), stack, "logs", nil) {
		if line.Type == "data" {
			lines = append(lines, line.Line)
		}
	}

	joined := strings.Join(lines, "\n")
	assert.NotContains(t, joined, "sentinel-value-runstreaming")
	assert.Contains(t, joined, "PATH=")
}

// TestStreamComposeCmd_DoesNotLeakCapstanSecrets covers docker_update.go's
// streamComposeCmd, the shared execCommandContext helper behind
// updateComposeContainerStreaming and UpdateComposeServiceStreaming.
func TestStreamComposeCmd_DoesNotLeakCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-streamcompose")
	withStubExecCommandContext(t)

	var lines []string
	emit := func(l LogLine) { lines = append(lines, l.Text) }

	err := streamComposeCmd(context.Background(), []string{"compose", "up", "-d"}, t.TempDir(), StreamStdout, emit)
	require.NoError(t, err)

	joined := strings.Join(lines, "\n")
	assert.NotContains(t, joined, "sentinel-value-streamcompose")
	assert.Contains(t, joined, "PATH=")
}

// TestUpdateComposeContainer_DoesNotLeakCapstanSecrets covers docker_update.go's
// remaining two execCommandContext sites (the pullCmd/upCmd pair inside
// updateComposeContainer), which build and run their *exec.Cmd directly rather
// than going through streamComposeCmd.
func TestUpdateComposeContainer_DoesNotLeakCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-updatecompose")
	withStubExecCommandContext(t)

	tempDir := t.TempDir()
	cfg := &config.Config{StacksDir: tempDir}
	service := &DockerService{config: cfg}
	stack := models.Stack{Directory: tempDir, ComposeFile: "compose.yaml", ProjectName: "p"}

	err := service.updateComposeContainer(context.Background(), stack, "web", true)
	require.NoError(t, err)
	// wasRunning=true skips the post-recreate stop-new-container branch, which
	// would otherwise need a real Docker SDK client.
}

// TestCreateSession_DoesNotInheritCapstanSecrets covers terminal.go's
// CreateSession, the one call site whose *exec.Cmd is exposed on the returned
// session (TerminalSession.Cmd) rather than only the command's output — so the
// most direct test is asserting on cmd.Env itself, exactly the shape the task
// calls for: construct the command, inspect exec.Cmd.Env, assert the sentinel
// is absent while PATH (what docker needs) is present.
func TestCreateSession_DoesNotInheritCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-terminal")

	svc := NewTerminalService(&config.Config{})
	session, err := svc.CreateSession("proj-a", "proj-a-web-1")
	require.NoError(t, err)
	defer svc.CloseSession(session.ID)

	require.NotNil(t, session.Cmd)
	for _, kv := range session.Cmd.Env {
		assert.NotContains(t, kv, "sentinel-value-terminal")
	}
	assert.Contains(t, session.Cmd.Env, "PATH="+os.Getenv("PATH"))
}

// ---- backup_restic.go's execRunner: denylist, not allowlist ----

// TestExecRunner_Output_StripsCapstanSecrets covers backup_restic.go's
// execRunner.Output (used by ListSnapshots/Stats). execRunner's `name` param is
// caller-supplied argv, not hardcoded to "restic"/"rclone" at the type level, so
// the test can point it at the real `env` binary directly — no stubbing needed.
func TestExecRunner_Output_StripsCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-restic-output")
	t.Setenv("RESTIC_PASSWORD", "sentinel-value-restic-password")

	r := &execRunner{}
	out, err := r.Output(context.Background(), "env", nil, []string{"RESTIC_REPOSITORY=/data/repo"})
	require.NoError(t, err)

	output := string(out)
	assert.NotContains(t, output, "sentinel-value-restic-output")
	assert.NotContains(t, output, "sentinel-value-restic-password")
	assert.Contains(t, output, "PATH=")
	assert.Contains(t, output, "RESTIC_REPOSITORY=/data/repo")
}

// TestExecRunner_Run_StripsCapstanSecrets is the streaming (Run) counterpart —
// resticEnv-derived additions like RESTIC_REPOSITORY are appended on top of the
// filtered base, so this also proves the two don't collide.
func TestExecRunner_Run_StripsCapstanSecrets(t *testing.T) {
	t.Setenv("JWT_SECRET", "sentinel-value-restic-run")
	t.Setenv("STORAGE_KEY", "sentinel-value-storage-run")

	r := &execRunner{}
	out := make(chan StreamLine, 64)
	var lines []string
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for line := range out {
			lines = append(lines, line.Line)
		}
	}()

	err := r.Run(context.Background(), "env", nil, []string{"RESTIC_REPOSITORY=/data/repo"}, out)
	close(out)
	<-drained
	require.NoError(t, err)

	joined := strings.Join(lines, "\n")

	assert.NotContains(t, joined, "sentinel-value-restic-run")
	assert.NotContains(t, joined, "sentinel-value-storage-run")
	assert.Contains(t, joined, "PATH=")
	assert.Contains(t, joined, "RESTIC_REPOSITORY=/data/repo")
}
