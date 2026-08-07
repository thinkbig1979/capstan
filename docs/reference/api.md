# API Reference

Every route below is registered in `backend/cmd/server/main.go`, either
directly or through a handler's `RegisterRoutes` method (handlers live under
`backend/internal/handlers/`). All routes are under `/api/v1` and require
authentication (unless `AUTH_DISABLED=true`), except `/health`, `/health/ready`
and `/api/v1/version`. The web UI is the primary interface; this page exists
so a change to a route and a change to this list can be checked against each
other mechanically — see `scripts/check-api-docs.sh`.

`:param` segments are Gin route parameters (a stack ID, a container ID, and so
on), not literal path text. `/ws/...` routes are WebSocket upgrades, not plain
HTTP.

## Health

Liveness and readiness are separate endpoints, because Capstan is a separate
process from Docker. Point restart-on-failure checks at liveness and
dependency monitoring at readiness.

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

## Version

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

## Authentication

- `GET /api/v1/auth/status` — whether an admin exists yet (drives the
  first-run setup screen). Public, and deliberately not rate-limited: the
  frontend polls it on every navigation, and it shares no bucket with `/login`.
- `POST /api/v1/auth/setup` — create the first admin (only when no user exists)
- `POST /api/v1/auth/login` — log in
- `POST /api/v1/auth/logout` — log out
- `GET /api/v1/auth/me` — current user
- `POST /api/v1/auth/verify-password` — re-confirm the current user's password
  for a step-up check (e.g. before revealing a secret)
- `PUT /api/v1/auth/password` — change the current user's password

## Settings

- `GET /api/v1/settings/config` — effective server configuration (non-secret
  fields)
- `GET /api/v1/settings/global-env` / `PUT /api/v1/settings/global-env` —
  read/write the global `.env` overlay applied across stacks
- `GET /api/v1/settings/log-retention` / `PUT /api/v1/settings/log-retention`
  — log retention policy
- `GET /api/v1/settings/updates` / `PUT /api/v1/settings/updates` —
  update-scan scheduler settings
- `GET /api/v1/settings/git` / `PUT /api/v1/settings/git` — Git integration
  settings (SSH key, etc.)
- `GET /api/v1/settings/directories` / `PUT /api/v1/settings/directories` —
  configured stack directories
- `GET /api/v1/settings/scan-depth` / `PUT /api/v1/settings/scan-depth` —
  directory scan depth
- `GET /api/v1/settings/audit-log` — action audit log
- `GET /api/v1/settings/backup` / `PUT /api/v1/settings/backup` — backup
  engine settings (repository, schedule, retention)

## Directories

- `GET /api/v1/directories` — list configured directories and their scan state
- `POST /api/v1/directories/scan` — trigger a rescan
- `PUT /api/v1/directories/credentials` — update stored directory credentials
- `GET /api/v1/directories/credential-status` — whether credentials are set,
  without exposing them

## Stacks

- `GET /api/v1/stacks` — list stacks
- `POST /api/v1/stacks` — create a stack
- `GET /api/v1/stacks/:id` — stack details
- `POST /api/v1/stacks/:id/start` — start a stack
- `POST /api/v1/stacks/:id/stop` — stop a stack
- `POST /api/v1/stacks/:id/restart` — restart a stack
- `POST /api/v1/stacks/:id/pull` — pull images for a stack
- `DELETE /api/v1/stacks/:id` — delete a stack
- `GET /api/v1/stacks/:id/logs` — fetch recent logs for a stack
- `GET /api/v1/ws/logs/:id` — WebSocket stream of a stack's logs, tailing
- `GET /api/v1/stacks/:id/containers` — list a stack's containers with live
  status

## Compose & Environment Files

- `GET /api/v1/stacks/:id/compose` / `PUT /api/v1/stacks/:id/compose` —
  read/write a stack's compose file
- `POST /api/v1/stacks/:id/compose/lint` — lint a stack's compose file
- `PUT /api/v1/stacks/:id/compose-env` — write a stack's compose file and
  `.env` file together, atomically
- `POST /api/v1/compose/lint` — lint an arbitrary compose document, not tied
  to a saved stack (used by the create-stack form)
- `GET /api/v1/stacks/:id/env` / `PUT /api/v1/stacks/:id/env` — read/write a
  stack's `.env` file
- `POST /api/v1/stacks/:id/env` — create a stack's `.env` file

## Git

- `GET /api/v1/git` — status; the web UI passes `?stackId=<id>`, but the
  route itself is a plain query-string GET
- `POST /api/v1/git/pull` — pull changes
- `GET /api/v1/git/log` — commit log
- `GET /api/v1/git/diff/:hash` — commit diff

## Monitoring & Dashboard

- `GET /api/v1/dashboard/stats` — aggregate counts (stacks, containers, images,
  volumes, networks) for the dashboard landing page
- `GET /api/v1/ws/dashboard/metrics` — WebSocket stream of dashboard metrics
- `GET /api/v1/ws/metrics/:id` — WebSocket stream of a single container's
  resource metrics
- `GET /api/v1/ws/events` — WebSocket stream of Docker events

## Resources

Direct Docker resource management, independent of any stack.

- `GET /api/v1/resources/images` — list images
- `DELETE /api/v1/resources/images/:id` — delete an image
- `POST /api/v1/resources/images/prune` — prune unused images
- `GET /api/v1/resources/containers` — list containers
- `GET /api/v1/resources/containers/:id/inspect` — full inspect output for a
  container
- `POST /api/v1/resources/containers/:id/start` — start a container
- `POST /api/v1/resources/containers/:id/stop` — stop a container
- `POST /api/v1/resources/containers/:id/restart` — restart a container
- `DELETE /api/v1/resources/containers/:id` — delete a container
- `POST /api/v1/resources/containers/prune` — prune stopped containers
- `GET /api/v1/resources/updates` — check for available image updates
- `POST /api/v1/resources/containers/:id/update` — update a single container
  to its latest image
- `POST /api/v1/resources/stacks/:id/update` — update every container in a
  stack to its latest image
- `GET /api/v1/resources/updates/jobs` — list running/recent update jobs
- `GET /api/v1/resources/updates/jobs/:jobId` — a single update job's status
- `GET /api/v1/resources/updates/history` — update history
- `DELETE /api/v1/resources/updates/history` — clear update history
- `GET /api/v1/resources/auto-update/policies` — list auto-update policies
- `PUT /api/v1/resources/auto-update/policies/:targetType/:targetId` —
  create/update an auto-update policy for a container or stack
- `DELETE /api/v1/resources/auto-update/policies/:targetType/:targetId` —
  remove an auto-update policy
- `GET /api/v1/resources/volumes` — list volumes
- `DELETE /api/v1/resources/volumes/:name` — delete a volume
- `POST /api/v1/resources/volumes/prune` — prune unused volumes
- `GET /api/v1/resources/networks` — list networks
- `POST /api/v1/resources/networks` — create a network
- `DELETE /api/v1/resources/networks/:id` — delete a network
- `POST /api/v1/resources/networks/prune` — prune unused networks
- `GET /api/v1/resources/build-cache` — inspect Docker build cache usage
- `POST /api/v1/resources/build-cache/prune` — prune the build cache

## Terminal

- `GET /api/v1/ws/terminal/:id/:container` — WebSocket PTY session `docker
  exec`'d into a running container. Capped at 5 concurrent sessions
  (`backend/internal/handlers/terminal.go`) — materially more expensive than a
  log or metrics stream.

## Operations

- `GET /api/v1/ws/operations/:id/:action` — WebSocket stream of a long-running
  stack operation's progress (start/stop/restart/pull/delete)

## Update Jobs (WebSocket)

- `GET /api/v1/ws/updates/jobs/:jobId` — WebSocket stream of a single update
  job's progress, companion to the polling `GET
  /api/v1/resources/updates/jobs/:jobId` above

## Backups

- `GET /api/v1/backups/policies` — list backup policies
- `PUT /api/v1/backups/policies/stack/:stackId` — create/update a stack's
  backup policy
- `DELETE /api/v1/backups/policies/stack/:stackId` — remove a stack's backup
  policy
- `GET /api/v1/backups/status` — current backup engine availability and state
- `GET /api/v1/backups/history` — run history
- `GET /api/v1/backups/runs/:runId` — a single run's detail
- `GET /api/v1/backups/snapshots` — list restic snapshots
- `GET /api/v1/backups/snapshots/:snapshotId/preview` — preview a snapshot's
  contents before restoring
- `POST /api/v1/backups/run` — start a backup run
- `POST /api/v1/backups/sync` — sync to the configured cloud remote
- `POST /api/v1/backups/restore` — restore a stack from a snapshot
- `POST /api/v1/backups/dr-restore` — disaster-recovery restore (whole
  instance, onto a fresh host)
- `POST /api/v1/backups/prune` — apply retention and prune old snapshots
- `POST /api/v1/backups/repo/init` — initialize the restic repository
- `POST /api/v1/backups/cloud/test` — test the configured cloud remote's
  connectivity/credentials
- `GET /api/v1/ws/backups/run/:runId` — WebSocket stream of a backup run's
  progress
- `GET /api/v1/ws/backups/sync/:runId` — WebSocket stream of a sync run's
  progress
- `GET /api/v1/ws/backups/restore/:runId` — WebSocket stream of a restore
  run's progress
- `GET /api/v1/ws/backups/dr-restore/:runId` — WebSocket stream of a
  DR-restore run's progress
- `GET /api/v1/ws/backups/prune/:runId` — WebSocket stream of a prune run's
  progress

## Keeping this page honest

`scripts/check-api-docs.sh` extracts every `group.METHOD("path", ...)` call
from `backend/internal/handlers/*.go` and fails CI if a registered route is
missing from this page (or from that script's own allowlist). If you add,
rename, or remove a route, either update this page or explain the omission in
the script's `ROUTE_ALLOW` table.

**Not covered here on purpose:** `/assets/*`, `/fonts/*` and `/vite.svg`
(static file serving, not API endpoints) and the SPA fallback that serves
`index.html` for any unmatched non-`/api/` path. None of these are
`.GET`/`.POST`/etc. calls, so the coverage script does not extract them
either — there is nothing to allowlist.

**Out of scope for this page:** a full OpenAPI/Swagger specification (e.g.
via `swaggo` annotations or `huma`). This route list plus the coverage script
catches drift at a fraction of the cost; a generated spec with request/response
schemas would be a reasonable follow-up if the API grows a public/external
consumer beyond the bundled web UI.

---

[← Documentation index](../../README.md#documentation)
