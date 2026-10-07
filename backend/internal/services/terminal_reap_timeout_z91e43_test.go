package services

import (
	"bytes"
	"context"
	"log/slog"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

// reapContainerShell's in-container kill runs under a 5s budget. A reap that
// outlives it must be logged as a timeout, not as a bare "signal: killed",
// which reads like the docker exec itself failing (agent-os-z91e.43).
func TestReapContainerShell_TimeoutReadsAsTimeout(t *testing.T) {
	orig := execCommandContext
	execCommandContext = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "20")
	}
	t.Cleanup(func() { execCommandContext = orig })

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	NewTerminalService(nil).reapContainerShell(context.Background(), "c1", "session-1")

	require.Contains(t, buf.String(), "reapContainerShell")
	require.Contains(t, buf.String(), "timed out after 5s", "a reap that ran out its budget must say so")
}
