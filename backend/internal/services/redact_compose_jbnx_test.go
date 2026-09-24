package services

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// agent-os-jbnx: Logs' zero-exit debug line wrote compose stderr unredacted.

func TestLogs_ZeroExitStderrDebugIsRedacted(t *testing.T) {
	// Everything to stderr with exit 0: the branch that writes the debug line.
	svc, stack := fvk3Service(t, "{ "+fvk3Script("0")+"; } 1>&2")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	out, err := svc.Logs(stack, 10)
	require.NoError(t, err)
	assert.Empty(t, out)

	logged := buf.String()
	require.Contains(t, logged, "docker compose logs wrote to stderr but exited 0")
	assertFvk3Redacted(t, "debug log", logged)
}
