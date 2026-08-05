<p align="center">
  <img alt="Capstan" src="frontend/public/capstan.svg" width="96" height="96">
</p>

<h1 align="center">Capstan</h1>

<p align="center">
  <a href="https://github.com/thinkbig1979/capstan/actions/workflows/docker-publish.yml"><img alt="Build and publish image" src="https://github.com/thinkbig1979/capstan/actions/workflows/docker-publish.yml/badge.svg"></a>
  <a href="https://github.com/thinkbig1979/capstan/pkgs/container/capstan"><img alt="GHCR image" src="https://img.shields.io/badge/ghcr.io-capstan-2496ED?logo=docker&logoColor=white"></a>
  <img alt="Architectures" src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-blue">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-green"></a>
  <a href="https://claude.com/claude-code"><img alt="Built with AI assistance" src="https://img.shields.io/badge/built%20with-AI%20assistance-8A2BE2"></a>
</p>

A web-based Docker Compose stack manager with Git integration, backups, and a
built-in terminal. Capstan ships as a single multi-arch container that serves both
the API and the web UI on one port.

## Quick Start

### Run the published image (any machine)

Pre-built multi-arch images (`linux/amd64`, `linux/arm64`) are published to the
GitHub Container Registry on each release:

```bash
docker pull ghcr.io/thinkbig1979/capstan:latest
```

The fastest way to run it is with `docker-compose.prod.yaml`, which already
points at the published image. Create a `.env` first (see
[Production Deployment](#production-deployment)), then:

```bash
docker compose -f docker-compose.prod.yaml up -d
```

Pin a version tag (e.g. `ghcr.io/thinkbig1979/capstan:0.11.0`) for reproducible
deployments; `:latest` tracks the most recent release.

### Run from source (local development)

`start-local.sh` builds the same all-in-one image and runs it via
`docker-compose.yaml`, serving the whole app on port 5001:

```bash
./start-local.sh
```

Then open http://localhost:5001. Authentication is disabled in this local mode
(`backend/.env` is created from `backend/.env.example`). `docker-compose.yaml`
reads `STACKS_DIR` (default `/opt/stacks`) from a root `.env` file or your
shell environment — set it to the directory that holds your compose projects,
e.g. `STACKS_DIR=/opt/stacks docker compose up -d --build`.

For frontend hot-reload, run the backend and the Vite dev server separately:

```bash
cd backend  && ./run-local.sh    # API on :5001
cd frontend && ./run-dev.sh      # Vite dev server on :5173 (proxies /api to :5001)
```

## Features

- **Docker Compose Management**: create, start, stop, restart, and delete stacks
- **Bulk Actions**: select multiple stacks and start, stop, restart, or pull them together
- **Pinned Stacks**: keep frequently-used stacks at the top of the sidebar
- **Command Palette**: press `⌘K` / `Ctrl-K` to jump to any stack, action, or settings page
- **Log Viewer**: ANSI-colored output, per-container colors, container and level filters, search, and preferences that persist across sessions
- **Compose Editor**: edit `docker-compose.yaml` files with live linting
- **Environment Files**: manage `.env` files with comment preservation
- **Git Integration**: status, pull, log, and diff for git-managed stacks
- **Image Updates**: detect and apply image updates per service, with an at-a-glance count in the sidebar
- **Dashboard Metrics**: sortable CPU and memory view with per-container sparklines
- **Backups**: built-in restic snapshots with optional rclone cloud sync, with last-run and next-run shown in the sidebar
- **Web Terminal**: an in-browser shell into running containers
- **Real-time Updates**: file watching for automatic stack detection
- **Action Logging**: an audit trail of all operations

## Backups

Capstan includes a built-in backup engine powered by [restic](https://restic.net) and
[rclone](https://rclone.org), both shipped inside the container image at pinned versions:

| Tool   | Version | Purpose                          |
| ------ | ------- | -------------------------------- |
| restic | 0.19.1  | Local encrypted snapshot backups |
| rclone | 1.74.4  | Cloud sync / offsite DR copies   |

### What gets backed up

Every backup run captures two kinds of thing:

| Artifact | restic tag | Notes |
| --- | --- | --- |
| Each stack's compose directory | the stack ID | One snapshot per enabled stack |
| `capstan.db` | `capstan-database` | Captured on **every** run, before the stacks |

The database is what makes an instance *this* instance: user accounts, the
encrypted git tokens and restic password, every setting, backup and auto-update
policies, the stacks registry, and the audit log. Without it a restore gives you
your compose files back and nothing else.

It is snapshotted with SQLite's `VACUUM INTO` rather than copied. `capstan.db`
runs in WAL mode, so a plain file copy taken while writes are in flight can be
torn or missing recent commits.

A run that saves every stack but fails to capture the database is reported as
**partial**, never success.

Backups can be triggered manually (Settings, Backup tab) or run on a schedule.

> **`STORAGE_KEY` is not in the backup, and that is deliberate.** The secrets
> inside `capstan.db` are encrypted with a key derived from it, so a stolen
> backup does not yield your git tokens. The corollary is that restoring onto a
> host with a *different* `STORAGE_KEY` gives you a database whose secrets
> cannot be decrypted. Store `STORAGE_KEY` and `JWT_SECRET` in the same password
> manager as the restic repository password. All three are needed for a complete
> recovery.

### Configuration

The quickest path is the **Settings UI** (Settings, Backup). All fields save to the
encrypted database and take effect without a restart.

Alternatively, set env vars in your `.env` file (see `.env.example` for the full list).
The UI always wins over env vars.

Key variables:

| Variable             | Default                    | Notes                                     |
| -------------------- | -------------------------- | ----------------------------------------- |
| `RESTIC_REPOSITORY`  | `/app/data/restic-repo`    | Path inside the container                 |
| `RESTIC_PASSWORD`    | _(none)_                   | Required; stored encrypted when set in UI |
| `RCLONE_REMOTE`      | _(none)_                   | rclone remote name (optional)             |
| `RCLONE_PATH`        | `capstan-backups`          | Destination path on the remote            |

### Bind-mount requirement

The restic repository lives inside `/app/data`. Your compose file MUST mount this
as a host bind mount so that snapshots survive container recreation:

```yaml
volumes:
  - ./data:/app/data   # host bind mount — required for backup persistence
```

Never replace this with a Docker named volume. The `docker-compose.prod.yaml` and
`docker/compose.yaml` both use a bind mount by default.

### Running a backup

```bash
# Via the UI: Settings → Backup → Run Backup Now
# Via the API:
curl -X POST http://localhost:5001/api/v1/backups/run
```

### Cloud sync (optional)

Configure an rclone remote and set `RCLONE_REMOTE` (or use the UI). Enable
"Sync after backup" to push every snapshot to the remote automatically.

Rclone config file: mount it read-only into the container if you manage it externally:

```yaml
volumes:
  - ~/.config/rclone/rclone.conf:/home/appuser/.config/rclone/rclone.conf:ro
```

### Restore

```bash
# List snapshots for a stack:
curl http://localhost:5001/api/v1/backups/snapshots?stackId=<id>

# Restore a snapshot via UI: Settings → Backup → Snapshots → Restore
# Or via API:
curl -X POST http://localhost:5001/api/v1/backups/restore \
  -H 'Content-Type: application/json' \
  -d '{"stackId":"<id>","snapshotId":"<short-id>"}'
```

### Disaster recovery

Follow this in order. **The database is restored before the stacks** — restoring
stacks first gives you a Capstan that does not know they exist.

#### Before you start, you need

| Item | Why |
| --- | --- |
| The restic repository password | Without it the repository cannot be opened at all |
| `STORAGE_KEY` | The old value, or every stored secret is unreadable |
| `JWT_SECRET` | A different value invalidates existing sessions (survivable) |
| Access to the rclone remote | The only copy that survives losing the data volume |

If you have the repository but not `STORAGE_KEY`, you can still recover
everything except the encrypted secrets — see step 6.

#### 1. Deploy a fresh instance, stopped

Bring up a new Capstan with the same `./data` bind mount path and the **same**
`STORAGE_KEY` and `JWT_SECRET` as the lost instance, then stop the container:

```bash
docker compose -f docker-compose.prod.yaml up -d
docker compose -f docker-compose.prod.yaml stop capstan
```

Stopping matters. Overwriting `capstan.db` underneath a running server corrupts
it — the server holds the WAL open and will write over what you just restored.

#### Run steps 2–4 inside the image, not on the host

Every command below is run in a throwaway container built from the Capstan
image, with the same volumes. Three reasons, all learned the hard way in a real
drill:

- `restic` and `rclone` ship **inside** the image at pinned versions. A freshly
  provisioned recovery host usually has neither, and installing them mid-outage
  is a poor use of the outage.
- The rclone remote path stored in Capstan's settings is a path **as seen from
  inside the container**. Running the same `rclone sync` on the host resolves it
  against the host filesystem and fails with `directory not found`.
- The paths inside the restic snapshot are container paths, so restoring in the
  same namespace keeps them meaningful.

Define this once, adjusting the paths to your deployment:

```bash
alias capstan-tools='docker run --rm \
  -v ./data:/app/data \
  -v ~/.config/rclone/rclone.conf:/home/appuser/.config/rclone/rclone.conf:ro \
  -e RCLONE_CONFIG=/home/appuser/.config/rclone/rclone.conf \
  --entrypoint sh ghcr.io/thinkbig1979/capstan:latest -c'
```

`RCLONE_CONFIG` is not optional. `--entrypoint sh` skips the entrypoint that
would drop to `appuser`, so these commands run as **root** with `HOME=/root`,
and rclone looks for its config under `$HOME`. Without the variable it never
reads the file you just mounted and step 2 fails with:

```
NOTICE: Config file "/root/.config/rclone/rclone.conf" not found - using defaults
CRITICAL: Failed to create file system for "<remote>:<path>": didn't find section in config file
```

which reads like a broken remote rather than a config it never opened.

#### 2. Get the repository back from the remote

```bash
capstan-tools 'rclone sync <remote>:<path> /app/data/restic-repo --stats-one-line'
```

`<remote>:<path>` are exactly the values shown in Settings → Backup. This is
what the UI's DR Restore does; doing it by hand keeps step 1's "container
stopped" rule intact.

If the remote is a local-filesystem one, add its bind mount to the alias too —
the path is resolved inside the container.

#### 3. Find the database snapshot

```bash
capstan-tools '
  printf "%s" "$RESTIC_PASSWORD" > /tmp/pw
  export RESTIC_REPOSITORY=/app/data/restic-repo RESTIC_PASSWORD_FILE=/tmp/pw
  restic snapshots --tag capstan-database'
```

The newest one is normally what you want. Note its short ID.

#### 4. Restore the database

The snapshot contains the file at `/app/data/backup-staging/capstan.db`, so a
restore reproduces that path underneath `--target`:

```bash
capstan-tools '
  set -e
  printf "%s" "$RESTIC_PASSWORD" > /tmp/pw
  export RESTIC_REPOSITORY=/app/data/restic-repo RESTIC_PASSWORD_FILE=/tmp/pw
  restic restore <snapshot-id> --target /tmp/capstan-restore
  cp /tmp/capstan-restore/app/data/backup-staging/capstan.db /app/data/capstan.db
  rm -rf /tmp/capstan-restore
  rm -f /app/data/capstan.db-wal /app/data/capstan.db-shm
  chown appuser:appuser /app/data/capstan.db'
```

Two details that will bite if skipped:

- **Delete `capstan.db-wal` and `capstan.db-shm`.** They belong to the database
  you just replaced. Left in place, SQLite tries to apply them to the restored
  file.
- **`chown appuser:appuser`.** The restore runs as root; Capstan runs as
  `appuser` (UID 1000 by default, remappable via `PUID`/`PGID`). A root-owned
  `capstan.db` leaves the server unable to write to its own database.

#### 5. Start Capstan and verify before going further

```bash
docker compose -f docker-compose.prod.yaml start capstan
```

Check all four, in this order. Each one fails differently:

1. **Log in** with a pre-existing account. Failure here means the wrong
   `capstan.db`, not a `STORAGE_KEY` problem.
2. **Settings → Backup** shows your repository path, retention and schedule.
   Empty defaults mean the database did not actually land.
3. **A git-backed stack can pull.** This is the `STORAGE_KEY` test — the token
   is decrypted to run it. An authentication failure here with the right
   password means the key differs from the one that encrypted it.
4. **Settings → Backup → History** lists runs from before the incident, and your
   backup policies are still enabled.

#### 6. If `STORAGE_KEY` was lost

Everything except the encrypted secrets is recoverable. Accounts, settings,
policies, history and the stacks registry all come back. What must be re-entered
by hand: the restic repository password, git HTTPS tokens, and any other stored
credential. Re-enter them in Settings; the new values are encrypted with the new
key.

#### 7. Restore the stacks

Only now, with a working Capstan that knows about them, restore the compose
directories from Settings → Backup → Snapshots, or via the API:

```bash
curl -X POST http://localhost:5001/api/v1/backups/restore \
  -H 'Content-Type: application/json' \
  -d '{"stackId":"<id>","snapshotId":"<short-id>","confirm":true}'
```

#### Practise this

A runbook that has never been executed is a hypothesis. Run it end to end
against a scratch instance at least once, before you need it.

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
`X-Forwarded-Proto: https` (see [Production Deployment](#production-deployment)).

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

## Project Structure

```
capstan/
├── backend/                  # Go backend (Gin API, SQLite, Docker/Git services)
│   ├── cmd/server/           # main entrypoint
│   └── internal/             # handlers, services, middleware, models
├── frontend/                 # React + Vite + Tailwind SPA
│   └── src/                  # components, hooks, stores, lib
├── docker/                   # production Dockerfile + compose
├── docker-compose.yaml       # local dev (builds the all-in-one image)
└── docker-compose.prod.yaml  # runs the published image
```

## Quick Commands

```bash
# Full stack (Docker)
./start-local.sh           # build + start
docker compose logs -f     # view logs
docker compose down        # stop

# Backend only
cd backend
./run-local.sh             # quick start
make run                   # run the server
make test                  # run tests

# Frontend only
cd frontend
./run-dev.sh               # Vite dev server (:5173)
npm run build              # production build
```

## API Endpoints

All routes are under `/api/v1` and require authentication (unless
`AUTH_DISABLED=true`), except `/health`, `/health/ready` and `/api/v1/version`.
The web UI is the primary interface; these are the main REST routes.

### Health

Liveness and readiness are separate endpoints, because Capstan is a separate
process from Docker. Point restart-on-failure checks at liveness and dependency
monitoring at readiness.

- `GET /health` — **liveness**. 200 whenever the process is up and serving. Makes
  no Docker call, so a Docker daemon restart cannot mark the container unhealthy
  and get Capstan bounced for an outage restarting it would not fix. This is what
  the container `HEALTHCHECK`, and any orchestrator liveness probe, should use.
- `GET /health/ready` — **readiness**. Reports dependencies, 503 when any is
  degraded, naming which. The Docker probe is bounded by a 2-second timeout so a
  hung daemon cannot pile up goroutines across repeated probes. Point an uptime
  monitor, a load balancer, or a Kubernetes readiness probe here.

```console
$ curl -s localhost:5001/health
{"status":"healthy"}

$ curl -s localhost:5001/health/ready          # Docker up
{"checks":{"docker":{"status":"ok"}},"status":"ready"}

$ curl -s localhost:5001/health/ready          # Docker daemon stopped -> HTTP 503
{"checks":{"docker":{"error":"Cannot connect to the Docker daemon at unix:///var/run/docker.sock.",
"status":"unavailable"}},"degraded":["docker"],"status":"degraded"}
```

**Reachability.** Loopback reaches both with no configuration, so the container's
own healthcheck needs no setup. Every other address is denied by default —
upgrading does not silently widen exposure. To let an external monitor in, list
its network in `HEALTH_ALLOWED_NETWORKS`:

```bash
HEALTH_ALLOWED_NETWORKS=10.1.0.0/16,192.168.50.7
```

This is deliberately **not** `TRUSTED_NETWORKS` (Gin's trusted-proxy list) or
`AUTH_DISABLED_ALLOWED_NETWORKS` (the `AUTH_DISABLED` bypass list) — reusing
either would mean granting an uptime monitor `X-Forwarded-For` spoofing or
authentication bypass just to let it read a health endpoint.

### Version
- `GET /api/v1/version` — build identity of the running binary. Public, so an
  uptime check or a support conversation can answer "what is running here?"
  without a session.

```console
$ curl -s localhost:5001/api/v1/version
{"version":"0.9.0","commit":"6fb9879...","buildDate":"2026-07-31T09:12:44Z"}
```

The same values appear on the first startup log line and in the UI under
**Settings → About**. A published image reports the tag it was built under; a
local `docker build` with no `--build-arg` reports `version: dev`, which is the
honest answer rather than a blank.

### Authentication
- `POST /api/v1/auth/setup` — create the first admin (only when no user exists)
- `POST /api/v1/auth/login` — log in
- `POST /api/v1/auth/logout` — log out
- `GET  /api/v1/auth/me` — current user

### Stacks
- `GET    /api/v1/stacks` — list stacks
- `POST   /api/v1/stacks` — create a stack
- `GET    /api/v1/stacks/:id` — stack details
- `POST   /api/v1/stacks/:id/start` · `/stop` · `/restart` · `/pull` — lifecycle actions
- `DELETE /api/v1/stacks/:id` — delete a stack

### Compose & environment files
- `GET` / `PUT /api/v1/stacks/:id/compose` — read/write compose file
- `POST /api/v1/stacks/:id/compose/lint` — lint compose file
- `GET` / `PUT /api/v1/stacks/:id/env` — read/write `.env` file

### Git
- `GET  /api/v1/git?stackId=<id>` — status
- `POST /api/v1/git/pull` — pull changes
- `GET  /api/v1/git/log` — commit log
- `GET  /api/v1/git/diff/:hash` — commit diff

### Backups
- `POST /api/v1/backups/run` · `/sync` · `/restore` · `/dr-restore` · `/prune`
- `GET  /api/v1/backups/status` · `/history` · `/snapshots`

## Migration from Dockge

Capstan reads the same on-disk stack layout as Dockge, so migration is mostly a
matter of pointing it at your existing stacks directory.

1. **Back up existing stacks:**
   ```bash
   cp -r /opt/stacks /opt/stacks.backup
   ```

2. **Map the stacks directory** (Dockge uses `DOCKGE_STACKS_DIR`; Capstan uses
   `STACKS_DIR`, and requires `HOST_STACKS_DIR` to match — see
   [Volume Path Identity](#volume-path-identity)):
   ```yaml
   environment:
     - STACKS_DIR=/opt/stacks
     - HOST_STACKS_DIR=/opt/stacks
   ```

3. **Start Capstan** and create an admin account on first run (`/auth/setup`).
   Accounts are not migrated from Dockge.

4. **Verify path validation:**
   ```bash
   docker compose logs | grep "Volume path identity"
   ```

5. **Test with a single stack** before relying on it for production.

You can run Dockge and Capstan side by side during migration as long as only one
manages a given stack at a time.

## Production Deployment

```bash
# Generate secrets (use two distinct values)
JWT_SECRET=$(openssl rand -hex 32)
STORAGE_KEY=$(openssl rand -hex 32)

# Create production .env file
cat > .env << EOF
PORT=5001
LOG_LEVEL=info
JWT_SECRET=$JWT_SECRET
STORAGE_KEY=$STORAGE_KEY
AUTH_DISABLED=false
STACKS_DIR=/opt/stacks
HOST_STACKS_DIR=/opt/stacks
DATA_DIR=/app/data
TRUSTED_NETWORKS=172.16.0.0/12,10.0.0.0/8,192.168.0.0/16,127.0.0.1
EOF

docker compose -f docker-compose.prod.yaml up -d
```

`STORAGE_KEY` encrypts stored secrets (git tokens, restic password) at rest with
a key independent of `JWT_SECRET`; if unset it falls back to `JWT_SECRET`. Using a
separate value means rotating `JWT_SECRET` doesn't require re-encryption and a
leaked `JWT_SECRET` alone can't decrypt stored secrets.

### Security checklist

- **Set a strong `JWT_SECRET`** (min 32 characters) and a separate `STORAGE_KEY`.
- **Keep `AUTH_DISABLED=false`** for anything reachable off localhost.
- **Terminate TLS** at a reverse proxy (nginx, Traefik, Caddy) and forward
  `X-Forwarded-Proto: https` — Capstan uses it to set `Secure` cookies and HSTS.
- **Configure `TRUSTED_NETWORKS`** for correct client-IP attribution (rate
  limiting) and reverse-proxy trust. If you must run `AUTH_DISABLED=true`
  beyond loopback, set `AUTH_DISABLED_ALLOWED_NETWORKS` explicitly — it is a
  separate list, not implied by `TRUSTED_NETWORKS`.
- **Set up and test backups** (Settings → Backup).
- For least-privilege Docker access, front the socket with a proxy (see
  [Docker Socket & Security](#docker-socket--security)).

> **Upgrading:** this release binds JWTs to an issuer claim, so existing sessions
> are invalidated on upgrade — log in again once. Previously stored secrets stay
> readable and are re-encrypted under the new key scheme on next save.

### Resource limits

`docker-compose.prod.yaml` sets a 2 GiB memory ceiling on the `app` service
(both `mem_limit` and the matching `deploy.resources.limits.memory`, kept in
sync — `mem_limit` is what `docker compose up` actually enforces on a single
Engine; see the comment in the compose file for why both are set). Capstan
forks `restic`/`rclone` for backups, and restic's memory use tracks
repository *index* size rather than raw data size, so a large backup or
restore is the realistic way to exceed 2 GiB and get OOM-killed.

The 2 GiB default is a documented heuristic (restic users have reported
~6-7 GB RAM for a ~1.2 TB repository, i.e. roughly 0.5% of repository size —
see the compose file comment for the source), not a measurement against a
production-scale repository. Size it for your own deployment:

- **Estimate first, before you need it**: budget roughly 0.5-1% of your
  restic repository's total size (`du -sh` on the repo path, or check
  Settings → Backup) as a floor, plus ~256 MB for the Capstan server
  process itself. Round up.
- **Confirm under load**: while a backup or restore is running, watch
  `docker stats capstan` (or `docker exec capstan cat
  /sys/fs/cgroup/memory.peak` afterwards) and compare the peak to the
  configured limit. If a run gets OOM-killed (`docker inspect capstan
  --format '{{.State.OOMKilled}}'` shows `true`), raise `mem_limit` and
  `deploy.resources.limits.memory` together in `docker-compose.prod.yaml`
  and re-test.
- **Re-check after repository growth**: the ceiling that was safe at 100 GB
  may not be safe at 1 TB — revisit sizing periodically or after adding
  large new backup sources.

## Development

See [TESTING.md](TESTING.md) for local testing and development workflow. Example
environment files: [`.env.example`](.env.example) (production) and
[`backend/.env.example`](backend/.env.example) (local dev).

### Backend
- Language: Go 1.25
- Database: SQLite
- Framework: Gin
- Docker SDK: docker/docker (Moby) client
- Git library: go-git

### Frontend
- Language: TypeScript
- Framework: React + Vite
- UI: Tailwind CSS
- State: TanStack Query
- Editor: CodeMirror 6

### Branch protection

`main` requires these six checks to pass before a pull request can merge:

| Check | Workflow |
| --- | --- |
| Go vulnerabilities (govulncheck) | `security.yml` |
| npm advisories (pnpm audit) | `security.yml` |
| Image vulnerabilities (Trivy) | `security.yml` |
| Build, vet, and unit tests | `backend.yml` |
| Race detector | `backend.yml` |
| Lint, test, and build | `frontend.yml` |

Force pushes and branch deletion are blocked. Reviews are not required, and
administrators can bypass a stuck check — the rule is there to stop an
accidental merge over a red check, not to lock the repository against its
maintainer.

Real-Docker integration tests run on every backend change but are deliberately
**not** required: the job depends on Docker Hub being reachable, so requiring it
would make `main` mergeable only while an external service is up. The reasoning
is recorded in the header of `.github/workflows/integration.yml`.

This is also why `backend.yml` and `frontend.yml` carry no `paths:` filters. A
path-filtered workflow does not report a required check at all on an unrelated
PR, which leaves it pending forever rather than passing.

## Versioning

Capstan follows [Semantic Versioning](https://semver.org/) (`MAJOR.MINOR.PATCH`),
published as git tags prefixed with `v` (e.g. `v0.1.0`).

While in the **`0.x` range, Capstan is pre-stable**: it offers no
backward-compatibility guarantees yet. During this phase, treat a `MINOR` bump
(`0.1.x` → `0.2.0`) as potentially breaking and a `PATCH` bump (`0.1.0` →
`0.1.1`) as fixes only. The first stable release will be `v1.0.0`, after which
standard SemVer rules apply (`MAJOR` for breaking changes).

Each release tag publishes the matching container image tags:

| Git tag       | Image tags                                     |
| ------------- | ---------------------------------------------- |
| `v0.11.0`     | `:0.11.0`, `:0.11`, `:latest`                  |
| `v0.12.0-rc.1`| `:0.12.0-rc.1` (pre-release; **not** `:latest`) |

Pin a specific version (e.g. `ghcr.io/thinkbig1979/capstan:0.11.0`) for
reproducible deployments; `:latest` always tracks the most recent stable
release.

### Rolling back

Recovering from a bad release usually means re-pinning an older image tag (or
letting watchtower revert one). Capstan's database schema is versioned and
guards against this: on startup it logs the database's schema version
alongside the version this binary understands, and if the database was
already migrated by a **newer** binary than the one now starting, startup
refuses with a fatal error naming both versions rather than running against a
schema it doesn't fully understand — rolling back across a migration can
corrupt data.

If you've checked the specific rollback is safe (e.g. the migrations added
between the two versions are additive and don't change data the older binary
writes to), set `CAPSTAN_ALLOW_SCHEMA_DOWNGRADE=1` to downgrade the refusal to
a warning and continue startup anyway. This variable only affects the
forward-version check; it does not run any down-migration, and it does not by
itself make an unsafe rollback safe.

## License

MIT — see [LICENSE](LICENSE).
