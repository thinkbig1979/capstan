// Package dockerenv builds the scrubbed environment Capstan hands to every
// docker/docker-compose child process it spawns.
//
// It is its own package, separate from internal/services (where this
// allowlist originated — agent-os-iey), because internal/truth also runs
// docker/compose commands directly (RemoteRegistryDigest's `docker buildx
// imagetools inspect`) and needs the identical scrubbed environment.
// internal/services already imports internal/truth (docker_lifecycle.go,
// docker_update.go, backup.go, git.go, scheduler.go), so internal/truth
// importing internal/services back would be an import cycle. Both packages
// import dockerenv instead of each other (agent-os-3ux). This also keeps the
// allowlist itself in exactly one place — copying it into a second package
// would recreate the "second code path never updated to match the first"
// drift bug this bead exists to close (see exec_env.go's doc comment in
// internal/services).
package dockerenv

import "os"

// AllowedEnvVars lists exactly the environment variables passed through to a
// docker/docker-compose child process.
//
// Why this exists: Capstan reads several of its own secrets via os.Getenv at
// startup (config.Config.JWTSecret, StorageKey, GitHTTPSToken — see
// config/config.go) and never unsets them, so they remain in Capstan's own
// os.Environ() for the life of the process. exec.Cmd with a nil Env inherits
// os.Environ() in full. Every docker/compose command Capstan runs may be
// built from a stack's compose.yaml, which any authenticated user can write
// verbatim (handlers/stack_crud.go) — and `docker compose` interpolates
// ${VAR} from the process environment. Without this allowlist, a
// user-authored compose file referencing e.g. ${JWT_SECRET} would bake
// Capstan's own secret into an image tag pulled from an attacker-controlled
// registry, or into a container's environment readable via `docker inspect`.
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
var AllowedEnvVars = []string{
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

// Env builds the environment for a docker/docker-compose child process: only
// AllowedEnvVars, taken from Capstan's own process environment, so that
// Capstan's secrets never reach a docker/compose subprocess. See
// AllowedEnvVars for the full rationale.
func Env() []string {
	env := make([]string, 0, len(AllowedEnvVars))
	for _, key := range AllowedEnvVars {
		if val, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+val)
		}
	}
	return env
}
