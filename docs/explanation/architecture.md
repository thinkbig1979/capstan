# Architecture

Capstan ships as a single multi-arch container (`linux/amd64`, `linux/arm64`)
that serves both the REST API and the built React UI on one port — there is no
separate frontend server or reverse proxy in the shipped image, and no
build-time API URL to configure: in production the UI is served from the same
origin as the API.

## How one process serves both API and UI

The Go backend (Gin) registers `/api/v1/*` routes and, separately, serves the
built frontend assets. Any request that doesn't match an API route or a static
asset falls through to a catch-all (`backend/cmd/server/main.go`'s
`registerIndexRoute`) that serves `index.html` — the standard single-page-app
fallback, so client-side routes work on a hard refresh. That handler also
splices a per-request CSP nonce into the page's `<script>`/`<link>` tags and
serves the response with `Cache-Control: no-store`, since the spliced body is
unique on every request and cannot be cached.

## Build: four Dockerfile stages

`docker/Dockerfile` is a multi-stage build:

1. **`frontend-build`** (`node:22-trixie-slim`) — installs pnpm, builds the
   React + Vite app, produces static `dist/` output. Runs on the build host's
   native architecture (`$BUILDPLATFORM`) since the output is
   architecture-neutral, avoiding QEMU emulation of the Node build.
2. **`backend-build`** (`golang:1.26.5-trixie`) — cross-compiles the Go
   server with `CGO_ENABLED=0` for the target architecture. Also runs on
   `$BUILDPLATFORM`; Go's own cross-compiler emits the target binary without
   emulation.
3. **`backup-tools`** — downloads pinned, checksum-verified `restic` and
   `rclone` static binaries for the target architecture directly from their
   upstream releases, rather than installing them via a package manager.
4. **`docker-cli`** — lifts the `docker`, `docker-buildx`, and
   `docker-compose` CLI binaries out of the official `docker:28-cli` image
   (pinned by digest), since Capstan talks to the *host's* Docker daemon and
   must never bundle a daemon of its own.

The runtime stage (`debian:trixie-slim`) copies the built server binary,
frontend assets, and backup tools into one image, adds a non-root `appuser`
(UID/GID 1000 by default), and runs `entrypoint.sh` to discover the mounted
Docker socket's group at container start before dropping privileges — see
[Docker Socket & Security](security-model.md#docker-socket--security) for why
that step exists. A `HEALTHCHECK` calls `GET /health` (liveness, not
readiness — see the API Endpoints section of the README for the distinction).

## Deployment shape

Two Compose files select how the image is obtained, not how it runs:

- `docker-compose.yaml` — local development; builds `docker/Dockerfile` from
  source.
- `docker-compose.prod.yaml` — runs the published `ghcr.io/thinkbig1979/capstan`
  image and is the one intended for production use.

Both define a single `app`/`capstan` service with the same shape: the Docker
socket, a stacks directory, and a data directory (which holds `capstan.db` and
the restic repository) bind-mounted in; `PUID`/`PGID` to match host file
ownership; and a `stop_grace_period` of 60s so an in-flight backup, restore, or
sync (bounded by the server's own drain timeout) can finish cleanly on
`docker stop`/`compose down` instead of being SIGKILLed mid-write.

## Local development

For frontend hot-reload, the backend and Vite dev server run as two separate
processes instead of the single built image: the backend serves the API on
`:5001`, and Vite serves the frontend on `:5173`, proxying `/api` requests to
`:5001` (`frontend/vite.config.ts`). This is the only mode where API and UI are
served from different origins/ports; the built image always serves both from
one.
