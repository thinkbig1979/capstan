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
[Production Deployment](docs/how-to/deploy-production.md)), then:

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

## API Endpoints

See [API reference](docs/reference/api.md) for the full route list, grouped
by resource, plus health-check and version examples.

## Documentation

- [Getting started](docs/getting-started.md)

### How-to

- [Deploy to production](docs/how-to/deploy-production.md)
- [Configure backups](docs/how-to/configure-backups.md)
- [Restore a backup](docs/how-to/restore-a-backup.md)
- [Migrate from Dockge](docs/how-to/migrate-from-dockge.md)
- [Recover admin access](docs/how-to/recover-admin-access.md)
- [Upgrade and roll back](docs/how-to/upgrade-and-roll-back.md)

### Reference

- [Configuration](docs/reference/configuration.md)
- [API](docs/reference/api.md)

### Explanation

- [Security model](docs/explanation/security-model.md)
- [Architecture](docs/explanation/architecture.md)

## Contributing

Project structure, local development, testing, branch protection, and the
release process live in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
