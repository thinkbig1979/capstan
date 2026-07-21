# Capstan - Local Testing Guide

## Quick Start

### Option 1: Docker Compose (Recommended)

Start everything with a single command:

```bash
./start-local.sh
```

This will:
- Build and start the single all-in-one container (API + web UI) on `http://localhost:5001`
- Create necessary directories
- Set up environment variables

**Access the application:** http://localhost:5001

### Option 2: Backend Only (Native Go)

Start just the backend (for development):

```bash
cd backend
./run-local.sh
```

**Access the API:** http://localhost:5001

### Option 3: Frontend Only (Native Node)

Start just the frontend dev server (requires backend running); Vite proxies
`/api` requests to `http://localhost:5001`:

```bash
cd frontend
./run-dev.sh
```

**Access the UI:** http://localhost:5173

## Directory Structure

```
capstan/
├── backend/              # Go backend
│   ├── cmd/server/      # Main application
│   ├── internal/        # Internal packages
│   ├── .env            # Environment variables (created by start script)
│   └── run-local.sh    # Quick start script
├── frontend/            # React frontend
│   ├── src/           # Source code
│   ├── dist/          # Built assets
│   ├── .env           # Environment variables
│   └── run-dev.sh     # Dev server script
├── docker/              # Dockerfile + prod compose helper
│   └── Dockerfile     # The single all-in-one image (API + built frontend)
├── docker-compose.yaml  # Local dev (builds docker/Dockerfile)
└── start-local.sh      # Quick start script
```

## Configuration

### Backend Environment Variables

See `backend/.env.example` for all available options:

| Variable | Description | Default |
|----------|-------------|---------|
| `JWT_SECRET` | JWT signing key (min 32 chars) | Required unless `AUTH_DISABLED=true` |
| `STORAGE_KEY` | Encrypts stored secrets at rest, independent of `JWT_SECRET` | Falls back to `JWT_SECRET` if unset |
| `PORT` | Server port | `5001` |
| `STACKS_DIR` | Directory for Docker Compose files | `/opt/stacks` |
| `DATA_DIR` | Directory for database/logs | `/app/data` |
| `AUTH_DISABLED` | Disable authentication | `true` (local dev only, never on an untrusted network) |
| `GIT_SSH_KEY` | Path to SSH key for git | `/root/.ssh/id_rsa` |
| `CORS_ORIGINS` | Comma-separated allowlist | empty = all origins |

### Frontend

The frontend has no build-time API URL to configure: in production it's
served from the same origin as the API (single container), and in dev the
Vite server proxies `/api` to `http://localhost:5001` (see
`frontend/vite.config.ts`).

## Docker Compose Commands

```bash
# Start all services
docker compose up -d

# Start with rebuild
docker compose up -d --build

# View logs
docker compose logs -f

# View the app service logs specifically
docker compose logs -f app

# Stop services
docker compose down

# Stop and remove volumes (WARNING: deletes data)
docker compose down -v
```

## Manual Docker Build

```bash
docker build -f docker/Dockerfile -t capstan .
docker run -d \
  -p 5001:5001 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/stacks:/opt/stacks \
  -v $(pwd)/data:/app/data \
  --env-file backend/.env \
  --name capstan \
  capstan
```

## Testing the Backend

```bash
# Health check
curl http://localhost:5001/health

# Create a test stack
curl -X POST http://localhost:5001/api/v1/stacks \
  -H "Content-Type: application/json" \
  -d '{
    "name": "nginx-test",
    "composeContent": "services:\n  web:\n    image: nginx:1.25\n    restart: always\n    ports:\n      - \"8080:80\"",
    "deploy": true
  }'

# List stacks
curl http://localhost:5001/api/v1/stacks

# Get stack details
curl http://localhost:5001/api/v1/stacks/nginx-test:default
```

## Automated Tests

### Backend (Go)

```bash
cd backend
make test    # go test ./... — 647 unit tests across 9 packages
```

Integration tests that exercise a real Docker daemon are gated behind a build
tag and excluded from the default `go test ./...` run:

```bash
cd backend
go test -tags=integration ./internal/integrationtest/... ./internal/truth/...
```

These run in CI via `.github/workflows/integration.yml`. Six
`TestBackup_*`/`TestRestore_*` durability tests currently fail there — it's a
test-helper bug (nil encryptor wired into the test DB), not an environment
problem; tracked as a known issue.

### Frontend (Vitest)

```bash
cd frontend
pnpm test -- --run    # 600 tests across 60 files
pnpm lint
pnpm build
```

Runs in CI via `.github/workflows/frontend.yml` (lint, unit tests, build).

### End-to-end (Playwright)

A single backup/restore flow spec lives at
`testing/tests/playwright/backup-flow.spec.ts`, driven by the root
`playwright.config.ts`:

```bash
npx playwright test
```

See the header comment in `playwright.config.ts` for the environment
variables it reads (base URL, test credentials, backup repo path, etc).

### Bash-harness E2E suite

A broader smoke/core/regression suite driven by browser automation lives
under `testing/` with its own orchestrator; see `testing/README.md` for how
to run it (`./testing/test-orchestrator.sh`).

## Stacks Directory

When running locally, Docker Compose stacks are stored at:
- **Docker Compose**: a bind mount of `${STACKS_DIR}` (host) to the same path
  inside the container — see `docker-compose.yaml` and the README's
  [Volume Path Identity](README.md#volume-path-identity) section for why the
  host and container paths must match.
- **Local Go**: `/tmp/stacks`

You can add your own Docker Compose files here and they'll be detected automatically.

## Troubleshooting

### Backend won't start

1. Check Docker is running: `docker info`
2. Check logs: `docker compose logs app`
3. Verify port 5001 isn't in use: `lsof -i :5001`

### Frontend won't connect to backend

1. Verify backend is running: `curl http://localhost:5001/health`
2. If running the Vite dev server standalone, confirm the proxy target in
   `frontend/vite.config.ts` matches where the backend is listening
3. Check `CORS_ORIGINS` in `backend/.env`

### Docker socket permission denied

The backend needs access to the Docker socket. Ensure:
- The socket is mounted: `-v /var/run/docker.sock:/var/run/docker.sock`
- The user has permission to access the Docker socket

### Database errors

The SQLite database is created automatically. If you see errors:
1. Check `DATA_DIR` is writable
2. Remove the database (back it up first) and restart the backend

## Development

### Backend Development

```bash
cd backend
make run          # Run the server
make test         # Run unit tests
make build        # Build binary
make lint         # go vet + staticcheck (if installed)
```

### Frontend Development

```bash
cd frontend
pnpm dev          # Start dev server
pnpm build        # Build for production
pnpm test         # Run tests (watch mode)
pnpm lint         # Lint
```

## Production Deployment

See the README's [Production Deployment](README.md#production-deployment)
section for the full checklist (secrets, `AUTH_DISABLED`, TLS, trusted
networks, backups).

## Security Notes

- For local testing, authentication is disabled (`AUTH_DISABLED=true`). Never
  expose an instance with authentication disabled beyond localhost/trusted
  networks.
- In production, always enable authentication and use a strong `JWT_SECRET`
  (and a separate `STORAGE_KEY`).
- The Docker socket is mounted read-write, not read-only — that's deliberate.
  Docker's API accepts control commands over the socket regardless of the
  mount's `:ro`/`:rw` flag, so `:ro` only blocks writes to the socket *file*,
  it doesn't restrict what the API will do through it. See
  [Docker Socket & Security](README.md#docker-socket--security) for the full
  rationale and how to front it with a socket proxy for real least-privilege.
- See the README's [Application Security](README.md#application-security)
  section for the full list of hardening measures (auth, CSRF, CORS, secrets
  encryption, etc).
