package services

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// execRunner takes its deadline from the caller's context, so these tests give
// it a short one and ask whether the call comes back. The child is a shell whose
// grandchild keeps the inherited stdout/stderr pipe open, which is how rclone and
// restic behave when they spawn a helper.
//
// Two grandchildren, because they fail differently (agent-os-z91e.25):
//   - in-group: `sleep &` stays in the child's process group, so a group kill
//     reaches it.
//   - setsid: `setsid sleep &` leaves the group, so no group kill reaches it. Only
//     WaitDelay can release the caller, and WaitDelay acts only inside cmd.Wait.
//
// The sleep outlasts every bound so a call cannot pass by the grandchild
// finishing on its own.
var execRunnerGrandchildCases = []struct {
	name   string
	script string
	bound  time.Duration
}{
	{"in-group grandchild", "sleep 20 & exec sleep 20", 3 * time.Second},
	{"setsid grandchild", "setsid sleep 20 & exec sleep 20", commandWaitDelay + 3*time.Second},
}

func TestExecRunner_Run_GrandchildHoldingThePipeEndsAtTheContext(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, tc := range execRunnerGrandchildCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			out := make(chan StreamLine, 8)

			done := make(chan error, 1)
			go func() { done <- (&execRunner{}).Run(ctx, "sh", []string{"-c", tc.script}, nil, out) }()

			select {
			case err := <-done:
				require.Error(t, err, "the child was killed at the deadline, so Run must not report success")
			case <-time.After(tc.bound):
				t.Fatalf("Run still running %s after a %s context: a grandchild holding the pipe keeps it open", tc.bound, timeout)
			}
		})
	}
}

func TestExecRunner_Output_GrandchildHoldingThePipeEndsAtTheContext(t *testing.T) {
	const timeout = 300 * time.Millisecond
	for _, tc := range execRunnerGrandchildCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			done := make(chan error, 1)
			go func() {
				_, err := (&execRunner{}).Output(ctx, "sh", []string{"-c", tc.script}, nil)
				done <- err
			}()

			select {
			case err := <-done:
				require.Error(t, err, "the child was killed at the deadline, so Output must not report success")
			case <-time.After(tc.bound):
				t.Fatalf("Output still running %s after a %s context: a grandchild holding the pipe keeps it open", tc.bound, timeout)
			}
		})
	}
}
