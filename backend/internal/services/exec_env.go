package services

import (
	"os/exec"
	"strings"

	"github.com/thinkbig1979/capstan/backend/internal/dockerenv"
)

// execCommand and execCommandContext are indirections over exec.Command and
// exec.CommandContext used at every docker/docker-compose call site in this
// package (docker.go, docker_lifecycle.go, docker_update.go, terminal.go).
// Production never overrides them. Tests substitute them to redirect the
// constructed command at a harmless stand-in binary (e.g. `sh -c env`) so they
// can inspect the *exec.Cmd the real call site actually builds — including its
// Env — rather than asserting a helper's return value in isolation, which would
// prove nothing about whether the call site actually uses it.
var (
	execCommand        = exec.Command
	execCommandContext = exec.CommandContext
)

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
