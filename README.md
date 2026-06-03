<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="Supporting-Docs/Branding/capstan-mark-dark.svg">
    <img alt="Capstan" src="Supporting-Docs/Branding/capstan-mark-light.svg" width="96" height="96">
  </picture>
</p>

<h1 align="center">Capstan</h1>

<p align="center">
  <a href="https://github.com/thinkbig1979/capstan/actions/workflows/docker-publish.yml"><img alt="Build and publish image" src="https://github.com/thinkbig1979/capstan/actions/workflows/docker-publish.yml/badge.svg"></a>
  <a href="https://github.com/thinkbig1979/capstan/pkgs/container/capstan"><img alt="GHCR image" src="https://img.shields.io/badge/ghcr.io-capstan-2496ED?logo=docker&logoColor=white"></a>
  <img alt="Architectures" src="https://img.shields.io/badge/arch-amd64%20%7C%20arm64-blue">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-green"></a>
  <a href="https://claude.com/claude-code"><img alt="Built with AI assistance" src="https://img.shields.io/badge/built%20with-AI%20assistance-8A2BE2"></a>
</p>

A web-based Docker Compose stack manager with Git integration.

## Quick Start

### Run the published image (any machine)

Pre-built multi-arch images (`linux/amd64`, `linux/arm64`) are published to the
GitHub Container Registry on each release:

```bash
docker pull ghcr.io/thinkbig1979/capstan:latest
```

The fastest way to run it is with `docker-compose.prod.yaml`, which already
points at the published image:

```bash
docker compose -f docker-compose.prod.yaml up -d
```

Pin a version tag (e.g. `ghcr.io/thinkbig1979/capstan:0.7.0`) for reproducible
deployments; `:latest` tracks the most recent release.

### Run from source (local development)

```bash
# Start everything (backend + frontend)
./start-local.sh
```

Then open http://localhost:3001

## Features

- **Docker Compose Management**: Create, start, stop, restart, and delete stacks
- **Compose Editor**: Edit docker-compose.yaml files with live linting
- **Environment Files**: Manage .env files with comment preservation
- **Git Integration**: Status, pull, log, and diff for git-managed stacks
- **Real-time Updates**: File watching for automatic stack detection
- **Action Logging**: Audit trail of all operations

## Backups

Capstan includes a built-in backup engine powered by [restic](https://restic.net) and
[rclone](https://rclone.org), both shipped inside the container image at pinned versions:

| Tool   | Version | Purpose                          |
| ------ | ------- | -------------------------------- |
| restic | 0.18.0  | Local encrypted snapshot backups |
| rclone | 1.68.2  | Cloud sync / offsite DR copies   |

### What gets backed up

Each stack's compose directory is backed up as a restic snapshot tagged with the stack ID.
Backups can be triggered manually (Settings, Backup tab) or run on a schedule.

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
curl -X POST http://localhost:5001/api/v1/backup/run
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
curl http://localhost:5001/api/v1/backup/snapshots?stackId=<id>

# Restore a snapshot via UI: Settings → Backup → Snapshots → Restore
# Or via API:
curl -X POST http://localhost:5001/api/v1/backup/restore \
  -H 'Content-Type: application/json' \
  -d '{"stackId":"<id>","snapshotId":"<short-id>"}'
```

### Disaster recovery

If the host is lost, recover from an rclone remote:

1. Deploy a fresh Capstan instance with the same `./data` bind mount path.
2. Trigger a DR restore (Settings, Backup, DR Restore) pointing at your rclone remote.
   This syncs the full restic repository back to `/app/data/restic-repo`.
3. Restore individual stacks via the Snapshots panel.

The restic repository password is required for DR recovery. Store it separately
from the server (e.g. a password manager).

## Volume Path Identity

**Important:** Capstan requires that the `STACKS_DIR` path inside the container must match the path on the host system for Docker Compose operations to work correctly.

### Quick Setup

Add both environment variables to your `docker-compose.yaml`:

```yaml
environment:
  - STACKS_DIR=/opt/stacks
  - HOST_STACKS_DIR=/opt/stacks
```

### Verification

On startup, Capstan validates path identity and logs warnings if paths don't match:

```bash
docker-compose logs backend | grep "Volume path identity"
```

### Detailed Documentation

See [Volume Path Identity](Supporting-Docs/Security/Volume-Path-Identity.md) for:
- Why this requirement exists
- Correct vs incorrect examples
- Troubleshooting steps
- Migration guide from Dockge

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
      image: capstan:latest
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
- Go and npm dependencies are scanned (`govulncheck`, `pnpm audit`) and kept
  current; the binary is built with a patched Go toolchain and base images are
  pinned.

**The honest boundary.** None of this changes the fact that access to the Docker
socket is root-equivalent control of the host (see
[Docker Socket & Security](#docker-socket--security) above). Treat a Capstan
login as administrative access, run it on a trusted network behind TLS, and use a
socket proxy if you need least-privilege. The application hardening above reduces
the ways that trust can be abused; it is not a substitute for protecting access
to Capstan itself.

## Project Structure

```
capstan/
├── backend/          # Go backend API
│   ├── cmd/         # Main application
│   ├── internal/    # Internal packages
│   └── services/    # Service layer
├── frontend/        # React frontend
│   └── src/        # Source code
└── .agent-os/       # Agent OS configuration
```

## Documentation

### Core Documentation
- **[TESTING.md](TESTING.md)** - Local testing and development guide
- **[CLAUDE.md](CLAUDE.md)** - Agent OS framework instructions

### Deployment & Operations
- **[Deployment Guide](Supporting-Docs/Deployment.md)** - Production deployment, SSL/TLS configuration, environment variables, reverse proxy setup
- **[Migration from Dockge](Supporting-Docs/Migration-From-Dockge.md)** - Step-by-step migration guide from Dockge
- **[Troubleshooting Guide](Supporting-Docs/Troubleshooting.md)** - Common issues and solutions

### Security & Configuration
- **[Volume Path Identity](Supporting-Docs/Security/Volume-Path-Identity.md)** - Critical configuration requirement for Docker Compose operations

## Quick Commands

### Docker Compose (Full Stack)
```bash
./start-local.sh          # Start all services
docker-compose logs -f     # View logs
docker-compose down        # Stop all services
```

### Backend Only
```bash
cd backend
./run-local.sh           # Quick start
make run                # Make target
make test               # Run tests
```

### Frontend Only
```bash
cd frontend
./run-dev.sh            # Quick start (dev server)
npm run build           # Build for production
```

## API Endpoints

### Health
- `GET /health` - Health check

### Authentication (if enabled)
- `POST /api/v1/auth/login` - Login
- `POST /api/v1/auth/logout` - Logout

### Stacks
- `GET /api/v1/stacks` - List all stacks
- `GET /api/v1/stacks/:id` - Get stack details
- `POST /api/v1/stacks` - Create new stack
- `POST /api/v1/stacks/:id/start` - Start stack
- `POST /api/v1/stacks/:id/stop` - Stop stack
- `POST /api/v1/stacks/:id/restart` - Restart stack
- `DELETE /api/v1/stacks/:id` - Delete stack

### Compose Files
- `GET /api/v1/stacks/:id/compose` - Get compose file
- `PUT /api/v1/stacks/:id/compose` - Save compose file
- `POST /api/v1/stacks/:id/compose/lint` - Lint compose file

### Environment Files
- `GET /api/v1/stacks/:id/env` - Get env file
- `PUT /api/v1/stacks/:id/env` - Save env file

### Git
- `GET /api/v1/directories/:path/git` - Get git status
- `POST /api/v1/directories/:path/git/pull` - Pull changes
- `GET /api/v1/directories/:path/git/log` - Get commit log
- `GET /api/v1/directories/:path/git/diff/:hash` - Get commit diff

## Migration from Dockge

Migrating from Dockge? See the comprehensive [Migration from Dockge guide](Supporting-Docs/Migration-From-Dockge.md) for:
- Prerequisites and backup procedures
- Side-by-side setup (both apps running)
- Port differences (Dockge 5001 → Capstan 5001)
- Account migration (manual: create new admin)
- Complete feature comparison table
- Troubleshooting common migration issues

### Quick Migration

For a quick overview:

1. **Backup existing stacks:**
   ```bash
   cp -r /opt/stacks /opt/stacks.backup
   ```

2. **Update environment variables** (Dockge uses `DOCKGE_STACKS_DIR`, Capstan uses `STACKS_DIR`):
   ```yaml
   environment:
     - STACKS_DIR=/opt/stacks
     - HOST_STACKS_DIR=/opt/stacks
   ```

3. **Restart** service:
   ```bash
   docker-compose down
   docker-compose up -d
   ```

4. **Verify path validation:**
   ```bash
   docker-compose logs backend | grep "Volume path identity"
   ```

5. **Test with a simple stack** before migrating production data

For detailed migration steps, see the [Migration from Dockge guide](Supporting-Docs/Migration-From-Dockge.md).

## Production Deployment

For production deployment, see the comprehensive [Deployment Guide](Supporting-Docs/Deployment.md) which covers:

- **Quick Start**: Basic deployment steps
- **Production Checklist**: Security, SSL, monitoring, backups
- **Environment Variables**: Complete list with descriptions
- **Reverse Proxy**: nginx, Traefik, Caddy examples
- **SSL/TLS**: Certbot examples
- **Docker Socket Security**: Permissions and best practices

### Production Configuration

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
```

`STORAGE_KEY` encrypts stored secrets (git tokens, restic password) at rest with
a key independent of `JWT_SECRET`; if unset it falls back to `JWT_SECRET`. Using a
separate value means rotating `JWT_SECRET` doesn't require re-encryption and a
leaked `JWT_SECRET` alone can't decrypt stored secrets.

### Security Considerations

- **Always set a strong JWT secret** (min 32 characters)
- **Set a separate `STORAGE_KEY`** so stored secrets are encrypted independently of `JWT_SECRET`
- **Enable authentication** in production (`AUTH_DISABLED=false`)
- **Use SSL/TLS** for all connections
- **Forward `X-Forwarded-Proto: https`** from your reverse proxy — Capstan uses it to set `Secure` cookies and HSTS (all proxy examples in the Deployment Guide do this)
- **Mount Docker socket as read-only** (`/var/run/docker.sock:/var/run/docker.sock:ro`)
- **Configure trusted networks** for access control
- **Set up regular backups** of stack configurations
- **Monitor resource usage** and set appropriate limits
- **Use a reverse proxy** (nginx, Traefik, Caddy) with SSL termination

> **Upgrading:** this release binds JWTs to an issuer claim, so existing sessions
> are invalidated on upgrade — log in again once. Previously stored secrets stay
> readable and are re-encrypted under the new key scheme on next save.

For detailed production deployment instructions, see the [Deployment Guide](Supporting-Docs/Deployment.md).

## Development

### Backend
- Language: Go 1.25
- Database: SQLite
- Framework: Gin
- Docker SDK: go-docker
- Git Library: go-git

### Frontend
- Language: TypeScript
- Framework: React + Vite
- UI: Tailwind CSS
- State: TanStack Query
- Editor: CodeMirror 6

## Versioning

Capstan follows [Semantic Versioning](https://semver.org/) (`MAJOR.MINOR.PATCH`),
published as git tags prefixed with `v` (e.g. `v0.1.0`).

While in the **`0.x` range, Capstan is pre-stable**: it offers no
backward-compatibility guarantees yet. During this phase, treat a `MINOR` bump
(`0.1.x` → `0.2.0`) as potentially breaking and a `PATCH` bump (`0.1.0` →
`0.1.1`) as fixes only. The first stable release will be `v1.0.0`, after which
standard SemVer rules apply (`MAJOR` for breaking changes).

Each release tag publishes the matching container image tags:

| Git tag      | Image tags                                |
| ------------ | ----------------------------------------- |
| `v0.7.0`     | `:0.7.0`, `:0.7`, `:latest`               |
| `v0.8.0-rc.1`| `:0.8.0-rc.1` (pre-release; **not** `:latest`) |

Pin a specific version (e.g. `ghcr.io/thinkbig1979/capstan:0.7.0`) for
reproducible deployments; `:latest` always tracks the most recent stable
release.

## License

MIT
