// Package execx builds non-interactive child processes whose wait is bounded
// once their context ends. It is a leaf (standard library only) so that every
// package can use it: internal/truth cannot import internal/services, which
// imports truth (agent-os-qags.29).
package execx

import (
	"context"
	"os/exec"
	"time"
)

// WaitDelay is how long Wait keeps waiting for a killed child's output pipes
// to close. The docker CLI runs compose and buildx as plugin subprocesses, and
// git spawns helpers, so killing the direct child can leave a grandchild
// holding stdout open; without a WaitDelay, CombinedOutput/Output then block
// until the grandchild exits on its own, which is the unbounded wait the
// context exists to prevent. OBSERVED in services'
// lifecycle_deadline_a1ye3_test.go "grandchild holds the pipe" case (8s+ past
// a 300ms deadline) and truth's imagedigest_deadline_z91e26_test.go (20s past
// a 300ms context) before it was set.
const WaitDelay = 5 * time.Second

// Bound sets WaitDelay on cmd and returns it. It is the one place the policy
// is applied, for constructors that build cmd through a test seam of their
// own (services.boundCommand).
func Bound(cmd *exec.Cmd) *exec.Cmd {
	cmd.WaitDelay = WaitDelay
	return cmd
}

// Command builds a child process that is killed when ctx ends and whose wait
// is then bounded by WaitDelay. The caller owns ctx's lifetime: a deadline
// for a one-shot call, a connection's context for a stream.
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	//nolint:gosec // G204: the caller passes an explicit argv, never a shell string; see README.md "Command execution and file access".
	return Bound(exec.CommandContext(ctx, name, args...))
}
