# Security Model

## Volume Path Identity

**Important:** the `STACKS_DIR` path inside the container must match the path on
the host for Docker Compose operations to work correctly. Compose records the
project's directory, and the host's Docker daemon must be able to find that same
path when Capstan runs commands against it.

Set both variables to the same value:

```yaml
environment:
  - STACKS_DIR=/opt/stacks
  - HOST_STACKS_DIR=/opt/stacks
```

On startup, Capstan validates path identity and logs a warning if the paths
don't match:

```bash
docker compose logs | grep "Volume path identity"
```

## Docker Socket & Security

Capstan manages your stacks by talking to the host's Docker daemon through the
mounted socket (`/var/run/docker.sock`). A few things worth understanding:

### It runs as non-root, with zero host changes

You do **not** need to create users, edit groups, or change any permissions on
your host. Just mount the socket and a data directory:

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
  - ./data:/app/data
  - /opt/stacks:/opt/stacks   # your compose projects
```

On startup the container briefly runs as root only to:
1. discover the **actual group GID** of the mounted socket (it differs per
   host — Debian/Ubuntu often use `999`, others `998`/`130`/…) and join it, and
2. align its runtime user to your file owner,

then it drops privileges and runs the app as the non-root `appuser`. Because the
socket's group is detected at runtime, the same image works on any host without
rebuilding.

### Matching file ownership (PUID / PGID)

`appuser` defaults to UID/GID **1000** — the typical first Linux user, so on most
single-user hosts it "just works" and your stack files stay editable from the
host. If the user that owns your stacks/data is a different ID, set:

```yaml
environment:
  - PUID=1001
  - PGID=1001
```

Capstan owns and chowns its own `./data` dir; it does **not** rewrite ownership
of your existing compose projects — it matches their owner via PUID/PGID instead.

### The honest security note

Anyone who can reach `/var/run/docker.sock` has **root-equivalent control of the
host** (the Docker API can start a privileged container that mounts `/`). This is
true regardless of the in-container user, so the non-root `appuser` is
defense-in-depth for Capstan's own files — not a containment boundary for Docker
itself. Two consequences:

- **`:ro` on the socket mount is cosmetic.** It makes the socket *file*
  read-only but the Docker API still accepts write commands (create/start/delete)
  through it, so it is not a safeguard. Capstan does not use it.
- **For real least-privilege**, put a socket proxy in front of Capstan and expose
  only the API endpoints it needs (containers, exec, and the system/version
  endpoints), denying the rest:

  ```yaml
  services:
    docker-proxy:
      image: tecnativa/docker-socket-proxy
      environment:
        CONTAINERS: 1
        SERVICES: 1
        TASKS: 1
        POST: 1            # required for start/stop/create
        EXEC: 1            # required for the in-app terminal
      volumes:
        - /var/run/docker.sock:/var/run/docker.sock:ro
      networks: [capstan-network]

    capstan:
      image: ghcr.io/thinkbig1979/capstan:latest
      environment:
        - DOCKER_HOST=tcp://docker-proxy:2375
      # no socket mount on Capstan itself
      networks: [capstan-network]
  ```

## Application Security

Capstan holds credentials, runs commands against your host's Docker daemon, and
serves an in-browser terminal, so the application is hardened by default rather
than left to the operator to secure. The codebase has been through a security
audit; the measures below are in place and covered by tests.

**Authentication and sessions**
- Authentication is on by default. Passwords are hashed with bcrypt and must meet
  a strength policy (length, character classes, common-password blocklist).
- Login is rate-limited and runs a constant-time comparison whether or not the
  username exists, so it does not leak which accounts are valid.
- Sessions are tracked server-side and revoked on logout and on password change.
  The session cookie is `HttpOnly`, `SameSite=Lax`, and marked `Secure` when the
  request arrives over HTTPS. JWTs are bound to an issuer claim.
- Exactly one account exists and there is no network-reachable password reset. If
  the password is lost, recover it offline — see
  [Recovering Admin Access](../how-to/recover-admin-access.md).

**Secrets at rest**
- Stored secrets (git HTTPS tokens, the restic repository password) are encrypted
  with AES-256-GCM. The key is derived with HKDF from a dedicated `STORAGE_KEY`,
  independent of the JWT signing secret, so leaking one does not expose the other.
- The restic password is passed to restic via a private file, never on the
  command line or in logs. Secrets are never returned in API responses.

**Command execution and file access**
- Every call to `docker`, `docker compose`, and `git` is made with an explicit
  argument vector, not a shell string, so stack names, container names, and file
  paths cannot inject commands.
- Reads and writes to compose files, `.env` files, and backup targets are
  confined to the configured stacks and data directories. Containment is
  symlink-aware, so a symlink inside a stack directory cannot redirect a write
  onto a host file outside it.

**Web layer**
- State-changing requests require a CSRF token (double-submit cookie + header).
- CORS uses an exact-match allowlist; credentialed requests are never paired with
  a wildcard origin.
- WebSocket endpoints (including the terminal) validate the request `Origin`, so
  the shell cannot be driven from another site (cross-site WebSocket hijacking).
- Responses carry a Content-Security-Policy with `frame-ancestors 'none'`, plus
  `X-Frame-Options`, `X-Content-Type-Options`, and `Referrer-Policy`. Generic
  error responses avoid leaking stack traces or internals.

**Dependencies and build**
- CI scans dependencies on every pull request, on every push to `main`, and on a
  weekly schedule, so advisories published against unchanged code are still
  caught. Two checks block a merge: Go vulnerabilities that `govulncheck` traces
  to a call in Capstan's own code, and production npm advisories of high severity
  or above. Container image findings (`trivy`, covering the Alpine base and the
  vendored `restic`/`rclone` binaries) and dev-dependency npm advisories are
  reported but do not block, since neither reaches the running application.
- A small number of advisories are accepted rather than fixed, each recorded in
  the file that suppresses it with the reason and the issue that removes it.
  These are visible in the scan output rather than silently filtered.
- Dependabot opens grouped update pull requests for all four ecosystems (Go
  modules, npm, Docker base images, and GitHub Actions).
- The binary is built with a patched Go toolchain, and base images are pinned by
  tag with the Go image kept in lockstep with `go.mod`'s toolchain directive.

**The honest boundary.** None of this changes the fact that access to the Docker
socket is root-equivalent control of the host (see
[Docker Socket & Security](#docker-socket--security) above). Treat a Capstan
login as administrative access, run it on a trusted network behind TLS, and use a
socket proxy if you need least-privilege. The application hardening above reduces
the ways that trust can be abused; it is not a substitute for protecting access
to Capstan itself.

## Deployment Security

Four configuration-dependent risks to understand before exposing Capstan
beyond localhost:

**TLS is not optional off localhost.** The session cookie's `Secure` flag is
set based on whether the request arrived over TLS, directly or via
`X-Forwarded-Proto: https` from a reverse proxy (`backend/internal/middleware/csrf.go`,
`backend/internal/handlers/auth.go`). Serve Capstan over plain HTTP on any
network you don't fully control, and the session token and CSRF cookie travel
in cleartext. Terminate TLS at a reverse proxy and forward
`X-Forwarded-Proto: https` (see [Production Deployment](../how-to/deploy-production.md)).

`X-Forwarded-Proto` is only honoured from a peer listed in `TRUSTED_NETWORKS`
(default: loopback only) — otherwise any client could set it and choose the
`Secure` flag and HSTS for itself. The practical consequence is that a proxy on
a Docker bridge or LAN address must be added to `TRUSTED_NETWORKS`, or Capstan
falls back to the real connection and issues cookies without `Secure`. It logs
a warning naming the peer the first time it ignores the header.

**`AUTH_DISABLED` trusts whoever `AUTH_DISABLED_ALLOWED_NETWORKS` says is
trusted, based on the real socket peer — never a header a proxy could
forward.** With `AUTH_DISABLED=true`, any request whose *actual* TCP peer
address is loopback or falls in `AUTH_DISABLED_ALLOWED_NETWORKS` is admitted
without a login (`backend/internal/middleware/auth.go`). Unset, that variable
defaults to loopback only. It is deliberately **not** `TRUSTED_NETWORKS` and
the check deliberately ignores `X-Forwarded-For` even from a trusted proxy:
those two used to be the same value and the same resolved-client-IP check,
which meant adding a reverse proxy's subnet to `TRUSTED_NETWORKS` for correct
client-IP attribution silently widened who could skip authentication, and a
proxy that forwarded a client-supplied `X-Forwarded-For: 127.0.0.1` could
reach the bypass too (agent-os-0s4). Widening the `AUTH_DISABLED` bypass
beyond loopback is now a separate, explicit opt-in via
`AUTH_DISABLED_ALLOWED_NETWORKS`.

**A reverse proxy must overwrite `X-Forwarded-For`, and its address must be in
`TRUSTED_NETWORKS`.** The resolved client IP keys the login rate limiter (not
`AUTH_DISABLED`, which is peer-address-only as above), so a proxy
misconfiguration shows up there. If the proxy's address is missing from
`TRUSTED_NETWORKS`, Capstan ignores `X-Forwarded-For` and sees every request as
coming from the proxy: all users then share one per-IP login budget, and one
person mistyping a password can start returning `429` to everybody. If the
proxy forwards a client-supplied `X-Forwarded-For` instead of overwriting it, a
caller picks their own apparent address and rotates past the per-IP limit at
will. Capstan logs its effective trusted-proxy list at startup, and warns once
per peer when a forwarding header arrives from an address it does not trust —
check that log after any proxy change.

Login attempts are limited in three layers, so a shared proxy address degrades
the limiter rather than collapsing it
(`backend/internal/middleware/ratelimit.go`): 5 per minute per account per
client address, 20 per minute per client address across all accounts, and 60
per minute per account across all addresses. The second layer is the one a
shared proxy address concentrates, which is why it is looser than the
per-account budget; the third is the only layer an attacker rotating source
addresses still meets. None of them is an account lockout — each is a rolling
one-minute window that clears itself. The configuration above is still what
makes the limiter behave correctly.

**There is one role: authenticated.** Any account that can log in has full
control of the Docker socket, which is root-equivalent control of the host
(see [Docker Socket & Security](#docker-socket--security)). Capstan has no
read-only or scoped-permission user; treat every login as administrative
access, and don't expose an instance to anyone you wouldn't hand host root.
