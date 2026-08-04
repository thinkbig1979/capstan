package services

import (
	"os"
	"os/exec"
	"strings"
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

// dockerAllowedEnvVars lists exactly the environment variables passed through
// to docker/docker-compose child processes (agent-os-iey).
//
// Why this exists: Capstan reads several of its own secrets via os.Getenv at
// startup (config.Config.JWTSecret, StorageKey, GitHTTPSToken — see
// config/config.go) and never unsets them, so they remain in Capstan's own
// os.Environ() for the life of the process. exec.Cmd with a nil Env inherits
// os.Environ() in full. Every docker/compose command Capstan runs is built from
// a stack's compose.yaml, which any authenticated user can write verbatim
// (handlers/stack_crud.go) — and `docker compose` interpolates ${VAR} from the
// process environment. Without this allowlist, a user-authored compose file
// referencing e.g. ${JWT_SECRET} would bake Capstan's own secret into an image
// tag pulled from an attacker-controlled registry, or into a container's
// environment readable via `docker inspect`.
//
// Deliberately an ALLOWLIST, not a denylist. A denylist fails open: the next
// secret config.go grows leaks by default until someone remembers to add it
// here — which is exactly how this bug happened (a second code path,
// exec.Cmd with Env left nil, was simply never updated to match the first). An
// allowlist fails closed: it can only ever break docker by omission of a
// legitimate var, never leak a secret by omission. The list below was checked
// against how Capstan actually reaches the daemon (client.FromEnv in
// NewDockerService, docker.go — DOCKER_HOST and friends already govern the SDK
// client, so the CLI subprocess needs the same variables to reach the same
// daemon) and how docker resolves registry auth (~/.docker/config.json via
// HOME). Stack-level interpolation does not depend on this list: buildComposeArgs
// already passes global.env and the stack's own .env file via --env-file, so a
// stack's intended ${VAR} interpolation keeps working through those files
// regardless of what is or isn't in this allowlist.
var dockerAllowedEnvVars = []string{
	"PATH", // required to find the docker binary at all

	// Registry auth / CLI config: docker reads ~/.docker/config.json (or
	// $DOCKER_CONFIG/config.json) for credentials. Stripping HOME breaks every
	// private-registry pull.
	"HOME",
	"XDG_CONFIG_HOME",
	"XDG_DATA_HOME",
	"XDG_CACHE_HOME",
	"DOCKER_CONFIG",

	// How the docker CLI reaches the daemon. These already govern the SDK
	// client (client.FromEnv in NewDockerService); the compose CLI subprocess
	// needs the same variables to reach the same daemon. Stripping DOCKER_HOST
	// would break every operation against a remote-daemon deployment.
	"DOCKER_HOST",
	"DOCKER_TLS_VERIFY",
	"DOCKER_CERT_PATH",
	"DOCKER_API_VERSION",
	"DOCKER_CONTEXT",

	// docker compose behaviour flags. Capstan itself passes project name,
	// compose file, and env files as explicit CLI flags (buildComposeArgs), not
	// via these, but an operator's shell/systemd unit may still set them.
	"COMPOSE_PROJECT_NAME",
	"COMPOSE_FILE",
	"COMPOSE_PROFILES",
	"COMPOSE_PARALLEL_LIMIT",
	"COMPOSE_HTTP_TIMEOUT",
	"COMPOSE_TLS_VERSION",
	"COMPOSE_CONVERT_WINDOWS_PATHS",
	"COMPOSE_IGNORE_ORPHANS",

	// Needed for image pulls / daemon access from behind a proxy.
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"http_proxy", "https_proxy", "no_proxy",

	// TLS trust store overrides some hardened base images/hosts require to
	// reach a registry or daemon over TLS with a custom CA bundle.
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

// dockerEnv builds the environment for a docker/docker-compose child process:
// only dockerAllowedEnvVars, taken from Capstan's own process environment, so
// that Capstan's secrets never reach a docker/compose subprocess. See
// dockerAllowedEnvVars for the full rationale.
func dockerEnv() []string {
	env := make([]string, 0, len(dockerAllowedEnvVars))
	for _, key := range dockerAllowedEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
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
// This is a DENYLIST, unlike dockerAllowedEnvVars' allowlist, and that is a
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
