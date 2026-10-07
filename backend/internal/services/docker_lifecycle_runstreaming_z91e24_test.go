package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thinkbig1979/capstan/backend/internal/config"
	"github.com/thinkbig1979/capstan/backend/internal/models"
	"github.com/thinkbig1979/capstan/backend/internal/truth"
)

// TestRunStreaming_GrandchildHoldingThePipeStillEndsAtTheDeadline is
// agent-os-z91e.24's acceptance. TestRunStreaming_HungChildEndsWithATimeoutDoneFrame
// uses `exec sleep`, a direct child the kill reaches, so it cannot see this:
// plain `sleep` is a grandchild that keeps the output pipe open after the shell
// is killed, as the docker CLI's compose plugin does. RunStreaming used to wait
// for its scanners before cmd.Wait, and WaitDelay only acts inside Wait, so the
// call ran until the grandchild exited on its own. The sleep outlasts the bound
// so the call cannot pass by the grandchild finishing.
func TestRunStreaming_GrandchildHoldingThePipeStillEndsAtTheDeadline(t *testing.T) {
	const timeout = 300 * time.Millisecond
	// timeout + WaitDelay + slack
	const bound = commandWaitDelay + 3*time.Second

	stubHangingChild(t, "sleep 20")
	dir := t.TempDir()
	svc := &DockerService{config: &config.Config{StacksDir: dir, ComposeTimeout: timeout}}
	stack := models.Stack{Directory: dir, ComposeFile: "compose.yaml", ProjectName: "p"}

	var last StreamLine
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for line := range svc.RunStreaming(context.Background(), stack, "up", []string{"-d"}) {
			last = line
		}
	}()
	select {
	case <-finished:
	case <-time.After(bound):
		t.Fatalf("RunStreaming still running %s after a %s deadline: a grandchild holding the pipe keeps it open", bound, timeout)
	}
	require.Equal(t, "done", last.Type)
	assert.False(t, last.Success)
	assert.Equal(t, truth.OutcomeFailed, last.Outcome)
	assert.Contains(t, last.Error, "docker compose up timed out after 300ms")
}
