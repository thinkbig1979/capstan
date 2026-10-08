package services

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/thinkbig1979/capstan/backend/internal/dockerenv"
	"github.com/thinkbig1979/capstan/backend/internal/execx"
)

// execCommand and execCommandContext are indirections over exec.Command and
// exec.CommandContext used at every docker/docker-compose and git call site in
// this package. execCommand (no context, no deadline) is left with exactly one
// caller, terminal.go's interactive `docker exec -it`, whose lifetime is the
// user's session; everything else goes through commandWithDeadline.
// Production never overrides them. Tests substitute them to redirect the
// constructed command at a harmless stand-in binary (e.g. `sh -c env`) so they
// can inspect the *exec.Cmd the real call site actually builds — including its
// Env — rather than asserting a helper's return value in isolation, which would
// prove nothing about whether the call site actually uses it.
var (
	execCommand        = exec.Command
	execCommandContext = exec.CommandContext
)

// commandWaitDelay is execx.WaitDelay under this package's existing name: how
// long Wait keeps waiting for a killed child's output pipes to close. See
// execx.WaitDelay for why (agent-os-qags.29 moved it there so internal/truth
// and internal/handlers share it).
const commandWaitDelay = execx.WaitDelay

// commandTimeoutError is the cause attached to a commandWithDeadline context.
// Using it as the context's cause (rather than checking DeadlineExceeded) means
// a parent cancellation, such as UpdateJobManager.Stop, is never misreported
// as a timeout.
type commandTimeoutError struct{ timeout time.Duration }

func (e *commandTimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s", e.timeout)
}

// withCommandDeadline derives the context a child process runs under: parent,
// bounded by timeout. Callers defer the returned cancel.
func withCommandDeadline(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, timeout, &commandTimeoutError{timeout: timeout})
}

// boundCommand builds the child under ctx, with execx's WaitDelay so a killed
// child's pipes are reaped. It is execx.Command built through this package's
// execCommandContext seam, which tests redirect at a stand-in binary.
func boundCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	return execx.Bound(execCommandContext(ctx, name, args...))
}

// commandWithDeadline is the one constructor for a non-interactive child
// process in this package (agent-os-a1ye.3): the command is killed once
// timeout passes or parent ends, whichever is first. Lifecycle commands run
// while the stack's operation lock is held, so before this a single hung
// `docker compose` held that lock until the server restarted. The caller
// defers cancel and passes its error through timeoutError, so a timeout reads
// as one rather than as "signal: killed".
func commandWithDeadline(parent context.Context, timeout time.Duration, name string, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	ctx, cancel := withCommandDeadline(parent, timeout)
	return boundCommand(ctx, name, args...), ctx, cancel
}

// timeoutError names the timeout when err came from a command whose
// commandWithDeadline context ran out, e.g. "docker compose up timed out
// after 10m0s: signal: killed". Any other err, including nil, is returned
// unchanged.
func timeoutError(ctx context.Context, err error, what string) error {
	var te *commandTimeoutError
	if err == nil || !errors.As(context.Cause(ctx), &te) {
		return err
	}
	return fmt.Errorf("%s %w: %w", what, te, err)
}

// commandTimedOut reports whether ctx, from commandWithDeadline, ended because
// its timeout passed (and not because its parent was cancelled).
func commandTimedOut(ctx context.Context) bool {
	var te *commandTimeoutError
	return errors.As(context.Cause(ctx), &te)
}

// dockerEnv builds the environment for a docker/docker-compose child process:
// only dockerenv.AllowedEnvVars, taken from Capstan's own process
// environment, so that Capstan's secrets never reach a docker/compose
// subprocess. See package dockerenv for the full rationale.
//
// The allowlist itself lives in package dockerenv, not here, because
// internal/truth also needs it (RemoteRegistryDigest's `docker buildx
// imagetools inspect` calls) and internal/truth cannot import internal/services
// without an import cycle (internal/services already imports internal/truth).
// This function is kept as a thin unexported wrapper so the ~10 existing call
// sites in this package (docker.go, docker_lifecycle.go, docker_update.go,
// terminal.go) didn't need to change (agent-os-3ux).
func dockerEnv() []string {
	return dockerenv.Env()
}

// DockerEnv is the exported form of dockerEnv, for callers outside package
// services (e.g. handlers.LogsHandler) that build their own docker/compose
// *exec.Cmd and need the same scrubbed environment. See dockerEnv and package
// dockerenv for rationale (agent-os-3ux).
func DockerEnv() []string {
	return dockerenv.Env()
}

// capstanSecretEnvVars are the environment variables config.Config reads
// (config/config.go) that must never reach a restic/rclone child process.
var capstanSecretEnvVars = map[string]struct{}{
	"JWT_SECRET":      {},
	"STORAGE_KEY":     {},
	"GIT_HTTPS_TOKEN": {},
	// RESTIC_PASSWORD is read into cfg.ResticPassword as a fallback (see
	// config.go), but ResticManager never relies on it being inherited — it
	// writes the password to a 0600 temp file and passes
	// RESTIC_PASSWORD_FILE explicitly (withPasswordFile). Forwarding the raw
	// value serves no purpose and is exactly the class of leak this closes.
	"RESTIC_PASSWORD": {},
}

// stripCapstanSecrets returns a copy of base (an environment slice such as
// os.Environ()) with capstanSecretEnvVars removed.
//
// This is a DENYLIST, unlike dockerenv.AllowedEnvVars' allowlist, and that is a
// deliberate difference in technique rather than a lesser fix. restic and
// rclone each support dozens of backend-specific credential variables
// (AWS_*, GOOGLE_*, AZURE_*, B2_*, RCLONE_CONFIG_*, ...) that vary by which
// storage backend an operator configured, and there is no local daemon
// available in this environment to test against — an allowlist here risks
// silently breaking a real deployment's backup destination in a way nothing
// in this repo's test suite would catch. The attack vector this bead is
// about — an attacker-controlled compose file interpolating ${VAR} from the
// process environment — also does not apply to restic/rclone: their argv is
// built entirely by Capstan itself from static flags and cfg fields (see
// Backup, ApplyRetention, etc.), never from user-supplied content. A denylist
// removing exactly Capstan's own known secrets closes that leak without
// gambling on rclone/restic's credential surface.
func stripCapstanSecrets(base []string) []string {
	out := make([]string, 0, len(base))
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if _, denied := capstanSecretEnvVars[key]; denied {
			continue
		}
		out = append(out, kv)
	}
	return out
}
