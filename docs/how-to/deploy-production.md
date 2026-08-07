# Production Deployment

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

## Security checklist

- **Set a strong `JWT_SECRET`** (min 32 characters) and a separate `STORAGE_KEY`.
- **Keep `AUTH_DISABLED=false`** for anything reachable off localhost.
- **Terminate TLS** at a reverse proxy (nginx, Traefik, Caddy) and forward
  `X-Forwarded-Proto: https` — Capstan uses it to set `Secure` cookies and HSTS.
  **The proxy's own address must be in `TRUSTED_NETWORKS` for that header to be
  honoured**, otherwise it is ignored and cookies are not marked `Secure` even
  on an HTTPS site. A warning naming the peer is logged the first time this
  happens.
- **Configure `TRUSTED_NETWORKS`** for correct client-IP attribution (rate
  limiting), reverse-proxy trust, and — since it gates `X-Forwarded-Proto` —
  whether `Secure` cookies and HSTS are issued at all. If you must run `AUTH_DISABLED=true`
  beyond loopback, set `AUTH_DISABLED_ALLOWED_NETWORKS` explicitly — it is a
  separate list, not implied by `TRUSTED_NETWORKS`.
- **Set up and test backups** (Settings → Backup).
- For least-privilege Docker access, front the socket with a proxy (see
  [Docker Socket & Security](../explanation/security-model.md#docker-socket--security)).

> **Upgrading an existing instance:** see
> [Upgrading and rolling back](upgrade-and-roll-back.md).

## Resource limits

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
