# Contributing to Capstan

This covers the project layout, local development workflow, testing, branch
protection, and release/versioning process. For running a Capstan instance as
an operator, see [README.md](README.md).

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

## Development

See [Testing](#testing) below for local testing and development workflow. Example
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

### Git hooks

Hooks are tracked in `.githooks/` — see
[`.githooks/pre-commit`](.githooks/pre-commit) — so every clone gets the same
ones. `core.hooksPath` points git at that directory, and the root `prepare`
script sets it during `pnpm install`.

That covers fresh clones. A clone that already has `node_modules` will not
re-run `prepare`, because pnpm short-circuits with "Already up to date", so wire
it up once by hand:

```bash
git config core.hooksPath .githooks
```

`core.hooksPath` is per-clone local config. It is not carried by a fetch or a
pull, so an existing checkout stays on its old hooks until someone runs the line
above.

`pre-commit` runs two things, each of which no-ops when its tool is missing:

- **react-doctor** scans staged files via `./node_modules/.bin/react-doctor` or a
  `react-doctor` on `PATH`. There is deliberately no `pnpm dlx` or `npx`
  fallback: those fetch and execute whatever npm currently tags `latest` on every
  commit, and `pnpm dlx` ignores the repo's `minimumReleaseAge` policy entirely.
  CI runs the `millionco/react-doctor` action on every pull request, so the local
  scan is a convenience rather than the gate. With no local binary the hook exits
  silently.
- **beads** (`bd`) owns the block between its own markers. Leave the markers in
  place; `bd hooks install` rewrites that section, and it reads `core.hooksPath`,
  so it updates the tracked file rather than a stale copy under `.git/hooks`.

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

Rolling back a deployed instance to an older image tag is an operator task —
see [Upgrading and rolling back](docs/how-to/upgrade-and-roll-back.md).

### Documentation versioning

Docs on `main` describe `main`, not the latest tagged release. Because images
publish only on version tags (see the table above), `main` routinely carries
commits with no published image yet — a doc that only ever described the
latest tag would then be silently behind the code sitting right next to it.
Tracking `main` avoids that: what a page says matches what you get if you
build from the commit you're reading it at.

The exception is a page that can only be verified by running an instance —
currently just [Getting Started](docs/getting-started.md). Those pages carry
a "Verified against Capstan version `X`" line naming the exact build the
walkthrough was actually run against. Until a tagged release ships from a
commit at or after the page's last edit, that stamp legitimately reads `dev`
(Settings → About and `GET /api/v1/version` both report `dev` for a
from-source build) — this is expected, not a sign the stamp is stale. Update
the stamp when you re-verify the page, not on every unrelated release.

## Testing

### Quick Start

#### Option 1: Docker Compose (Recommended)

Start everything with a single command:

```bash
./start-local.sh
```

This will:
- Build and start the single all-in-one container (API + web UI) on `http://localhost:5001`
- Create necessary directories
- Set up environment variables

**Access the application:** http://localhost:5001

#### Option 2: Backend Only (Native Go)

Start just the backend (for development):

```bash
cd backend
./run-local.sh
```

**Access the API:** http://localhost:5001

#### Option 3: Frontend Only (Native Node)

Start just the frontend dev server (requires backend running); Vite proxies
`/api` requests to `http://localhost:5001`:

```bash
cd frontend
./run-dev.sh
```

**Access the UI:** http://localhost:5173

### Directory Structure

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

### Configuration

#### Backend Environment Variables

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

#### Frontend Environment Variables

The frontend has no build-time API URL to configure: in production it's
served from the same origin as the API (single container), and in dev the
Vite server proxies `/api` to `http://localhost:5001` (see
`frontend/vite.config.ts`).

### Docker Compose Commands

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

### Manual Docker Build

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

### Testing the Backend

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

### Automated Tests

#### Backend (Go)

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

##### `Test_Resource_ImagePrune_CountsUntaggedEntries` is destructive and opt-in

This test exercises `DockerService.PruneImages` with `All: false`, which maps
to the Docker daemon's default image-prune filter (an empty filter set). The
Docker Engine API's image-prune filters are `dangling`/`until`/`label` — none
of them can scope a prune to a specific set of image IDs — so this call
removes **every dangling image already on the daemon**, not just the two
throwaway images the test builds. On an ephemeral CI runner that's harmless;
on a developer workstation it silently deletes dangling images belonging to
other work, with no prompt and no undo.

Because of that, the test **skips itself by default**. To run it, opt in
explicitly:

```bash
CAPSTAN_ALLOW_DESTRUCTIVE_IMAGE_PRUNE=1 \
  go test -tags=integration -count=1 -run Test_Resource_ImagePrune_CountsUntaggedEntries \
  ./internal/integrationtest/
```

Only do this on a machine where you don't mind losing dangling images (check
`docker images --filter dangling=true` first). To run the rest of the
integration suite without it (the default — no env var needed), or to
exclude it explicitly from a wider run:

```bash
go test -tags=integration -count=1 -skip Test_Resource_ImagePrune_CountsUntaggedEntries \
  ./internal/integrationtest/...
```

CI opts in: `.github/workflows/integration.yml`'s "Run integration tests" step
sets `CAPSTAN_ALLOW_DESTRUCTIVE_IMAGE_PRUNE: "1"` in its `env:` block, since
the runner is ephemeral and single-use — the hazard this guards against
doesn't apply there, so CI keeps full coverage of `PruneImages`.

#### Frontend (Vitest)

```bash
cd frontend
pnpm test -- --run    # 600 tests across 60 files
pnpm lint
pnpm build
```

Runs in CI via `.github/workflows/frontend.yml` (lint, unit tests, build).

#### End-to-end (Playwright)

A single backup/restore flow spec lives at
`testing/tests/playwright/backup-flow.spec.ts`, driven by the root
`playwright.config.ts`:

```bash
npx playwright test
```

See the header comment in `playwright.config.ts` for the environment
variables it reads (base URL, test credentials, backup repo path, etc).

#### Bash-harness E2E suite

A broader smoke/core/regression suite driven by browser automation lives
under `testing/` with its own orchestrator; see `testing/README.md` for how
to run it (`./testing/test-orchestrator.sh`).

### Stacks Directory

When running locally, Docker Compose stacks are stored at:
- **Docker Compose**: a bind mount of `${STACKS_DIR}` (host) to the same path
  inside the container — see `docker-compose.yaml` and
  [Volume Path Identity](docs/explanation/security-model.md#volume-path-identity)
  for why the host and container paths must match.
- **Local Go**: `/tmp/stacks`

You can add your own Docker Compose files here and they'll be detected automatically.

### Troubleshooting

#### Backend won't start

1. Check Docker is running: `docker info`
2. Check logs: `docker compose logs app`
3. Verify port 5001 isn't in use: `lsof -i :5001`

#### Frontend won't connect to backend

1. Verify backend is running: `curl http://localhost:5001/health`
2. If running the Vite dev server standalone, confirm the proxy target in
   `frontend/vite.config.ts` matches where the backend is listening
3. Check `CORS_ORIGINS` in `backend/.env`

#### Docker socket permission denied

The backend needs access to the Docker socket. Ensure:
- The socket is mounted: `-v /var/run/docker.sock:/var/run/docker.sock`
- The user has permission to access the Docker socket

#### Database errors

The SQLite database is created automatically. If you see errors:
1. Check `DATA_DIR` is writable
2. Remove the database (back it up first) and restart the backend

### Development workflow

#### Backend Development

```bash
cd backend
make run          # Run the server
make test         # Run unit tests
make build        # Build binary
make lint         # go vet + staticcheck (if installed)
```

#### Frontend Development

```bash
cd frontend
pnpm dev          # Start dev server
pnpm build        # Build for production
pnpm test         # Run tests (watch mode)
pnpm lint         # Lint
```

### Production Deployment

See [Production Deployment](docs/how-to/deploy-production.md) for the full
checklist (secrets, `AUTH_DISABLED`, TLS, trusted networks, backups).

### Security Notes

- For local testing, authentication is disabled (`AUTH_DISABLED=true`). Never
  expose an instance with authentication disabled beyond localhost/trusted
  networks.
- In production, always enable authentication and use a strong `JWT_SECRET`
  (and a separate `STORAGE_KEY`).
- The Docker socket is mounted read-write, not read-only — that's deliberate.
  Docker's API accepts control commands over the socket regardless of the
  mount's `:ro`/`:rw` flag, so `:ro` only blocks writes to the socket *file*,
  it doesn't restrict what the API will do through it. See
  [Docker Socket & Security](docs/explanation/security-model.md#docker-socket--security)
  for the full rationale and how to front it with a socket proxy for real
  least-privilege.
- See [Application Security](docs/explanation/security-model.md#application-security)
  for the full list of hardening measures (auth, CSRF, CORS, secrets
  encryption, etc).
