# Getting Started

This walks through a single Capstan instance end to end: bring it up, create
the admin account, and use the dashboard to create and start your first
stack. It assumes you've already read the README's
[Quick Start](../README.md#quick-start) — this is the tutorial that starts
where that leaves off.

Verified against Capstan version `dev` (commit `unknown`, no build date) —
this instance was built from source rather than a tagged release, so
Settings → About and `GET /api/v1/version` both report `dev` instead of a
version number. If you're running a published image, About will show a real
version tag instead.

## 1. Choose a stacks directory you own

`STACKS_DIR` controls where stack directories are written, both inside and
outside the container — the two paths must match. The default is
`/opt/stacks`, and the container **deliberately never takes ownership of
it**: `docker/entrypoint.sh` remaps its runtime user to `PUID`/`PGID`
(default `1000:1000`) instead of chowning the directory, specifically so
Capstan never rewrites ownership of compose projects that already live
there. On a host where `/opt/stacks` doesn't already belong to that user —
including a plain default install where nothing has claimed it yet — the
first stack you create will fail with a directory error, and the error
doesn't say why.

Avoid that by pointing `STACKS_DIR` at a directory you own before you start:

```bash
mkdir -p ~/capstan-stacks
```

If you'd rather use `/opt/stacks` (or another shared path), align
`PUID`/`PGID` to its owner instead — see
[Paths, volumes & container user](reference/configuration.md#paths-volumes--container-user).

## 2. Start the instance

From the repository root:

```bash
STACKS_DIR=~/capstan-stacks docker compose up -d --build
```

This builds the all-in-one image and starts it. Confirm it's actually ready
before opening it in a browser:

```bash
docker compose ps
```

Wait for `STATUS` to show `(healthy)` — that's Docker's own healthcheck,
which runs *inside* the container against genuine loopback. Don't use
`curl http://localhost:5001/health` from your host as a substitute: by
design that endpoint only answers loopback callers, and a request from the
host arrives via the Docker bridge instead, so you'll get a `403` even
though the instance is fine (`HEALTH_ALLOWED_NETWORKS` in
[Configuration Reference](reference/configuration.md) controls this). If you
want a host-reachable check, `curl http://localhost:5001/api/v1/version`
returns `200` and isn't subject to that restriction.

Once `docker compose ps` shows healthy, open <http://localhost:5001>.

## 3. Create the admin account

A fresh instance with `AUTH_DISABLED=false` opens straight to a setup form —
there's no separate login step yet, because there's no account to log in
with. Enter a username and a password (8+ characters, mixing case, a number,
and a symbol) and submit. Capstan creates the account and logs you straight
into the dashboard — you won't see the login page until you sign out or your
session expires.

## 4. Tour the dashboard

The dashboard's tab bar is your map of everything Capstan manages on this
host: **Metrics** (the landing view — CPU/memory/disk at a glance),
**Stacks**, **Containers**, **Dirs** (directories Capstan scans for compose
projects), **Updates** (available image updates), **Images**, **Volumes**,
**Networks**, and **Build Cache**. With no stacks yet, most of these are
empty — that's expected.

## 5. Create your first stack

Click **New Stack**. The dialog opens with a starter compose template
already filled in — edit it in place rather than starting from a blank
file:

1. **Stack Name**: give it a short, disposable name — this becomes both the
   directory name under `STACKS_DIR` and the Docker Compose project name.
2. **Docker Compose**: the default template runs `nginx:latest` on host port
   8080. Change the tag to something pinned (the linter warns on `:latest`
   for exactly this reason) and adjust the port if 8080 is already taken on
   your host.
3. Leave **Deploy after creation** unchecked for now — you'll start it
   explicitly in the next step, which makes the create/start distinction
   clearer, and don't need it for a first stack.
4. Click **Create Stack**. On a tall dialog, scroll down if the button isn't
   visible — the footer sits below the compose editor.

Capstan writes `compose.yaml` into the new stack directory and takes you to
the stack's **Overview** page, stopped.

## 6. Start it and confirm it's running

Click **Start**. Capstan runs `docker compose up -d` against the stack's
directory and streams the output live — you'll see the network and container
get created, then `stack running`. The **Containers** table below updates to
show the container's **State** (Running), **Status** (`Up N seconds`), and
its **Ports** mapping.

Confirm it from outside Capstan too:

```bash
curl -I http://localhost:8088   # or whatever host port you set
```

A `200 OK` from nginx means the container Capstan just created is genuinely
serving traffic, not just reported as running.

## 7. Explore the stack page

Beyond Overview, the stack page has tabs for **Compose** (edit and redeploy
the compose file), **Environment** (the stack's `.env`), **Logs** (tailing
container output), **Terminal** (a shell inside the container), **Metrics**,
**Updates**, and **Backups**. These cover the day-to-day operations once a
stack is running; this tutorial stops at "created and running."

## Next steps

- Deploying somewhere other than your workstation:
  [Production Deployment](how-to/deploy-production.md)
- Protecting what you just built:
  [Configuring Backups](how-to/configure-backups.md)
- Every environment variable Capstan reads:
  [Configuration Reference](reference/configuration.md)
- The full HTTP API surface behind the UI:
  [API Reference](reference/api.md)
- How the pieces fit together:
  [Architecture](explanation/architecture.md)
