package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/models"
)

// A nil *DockerService is the production shape when the daemon was unreachable
// at startup: main.go leaves dockerService nil and hands it to every handler
// (agent-os-xay).
//
// The whole-surface guarantee — no exported method panics, and none degrades
// silently — is enforced by reflection in docker_nil_receiver_test.go. The tests
// here cover the named regressions and the collaborators that hold a Docker
// dependency of their own.

// TestNilDockerService_RunStreamingDoesNotPanic is the direct regression test
// for the crash this task exists to remove: buildComposeArgs dereferenced the
// nil receiver inside RunStreaming's goroutine, so the panic escaped gin's
// RecoveryMiddleware and killed the server.
func TestNilDockerService_RunStreamingDoesNotPanic(t *testing.T) {
	t.Parallel()

	var s *DockerService

	var lines []StreamLine
	require.NotPanics(t, func() {
		for line := range s.RunStreaming(context.Background(), models.Stack{}, "up", []string{"-d"}) {
			lines = append(lines, line)
		}
	})

	require.Len(t, lines, 1, "the outage must be reported as a single terminal error frame")
	assert.Equal(t, "error", lines[0].Type)
	assert.Contains(t, lines[0].Error, "Docker daemon unreachable")
}

func TestNilMonitorService_RefusesInsteadOfPanicking(t *testing.T) {
	t.Parallel()

	// main.go leaves the Docker client nil when it cannot be constructed, and
	// StreamStats spawns a goroutine that would dereference it.
	svc := NewMonitorService(nil)
	ctx := context.Background()

	require.NotPanics(t, func() {
		_, err := svc.StreamStats(ctx, []string{"abc"})
		assert.ErrorIs(t, err, ErrDockerUnavailable)

		_, err = svc.ListenEvents(ctx)
		assert.ErrorIs(t, err, ErrDockerUnavailable)

		_, err = svc.GetContainersForStack(ctx, "proj")
		assert.ErrorIs(t, err, ErrDockerUnavailable)
	})
}
