# Configuration Reference

Capstan is configured entirely through environment variables, read once at
startup by `backend/internal/config/config.go` (`Load()`). Backup and cloud-sync
settings additionally have a database layer (Settings → Backup in the UI) that
takes precedence over the environment variable at runtime — the env var is a
fallback for scripted or headless deployments, not the only way to set them.

Precedence, where a database layer exists: **DB setting → environment variable
→ hard-coded default**.

> **Before a production deploy, at minimum change:** `JWT_SECRET` (required,
> generate with `openssl rand -hex 32`) and `STORAGE_KEY` (strongly
> recommended, same generation command). Review `PUID`/`PGID` against the
> host user that owns your stacks directory, and `HOST_STACKS_DIR` if
> `STACKS_DIR` differs inside and outside the container.

> **Secrets — never commit these, never log them:** `JWT_SECRET`,
> `STORAGE_KEY`, `RESTIC_PASSWORD`, `GIT_HTTPS_TOKEN`, and the contents of
> whatever file `GIT_SSH_KEY` points at.

## Server & logging

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `PORT` | `5001` | No | TCP port the server listens on. Must parse as a number between 1 and 65535, checked at startup (`config.go`'s `validate`). |
| `LOG_LEVEL` | `info` | No | Log verbosity. Validated against `logging.ParseLevel`; an unrecognised value fails startup rather than silently falling back, so a typo is caught immediately instead of during an incident. |
| `LOG_FORMAT` | `text` | No | `text` or `json`. Validated the same way as `LOG_LEVEL`. |

## Auth & secrets

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `JWT_SECRET` | none | **Yes**, unless `AUTH_DISABLED=true` | Signs session JWTs. Must be at least 32 characters and must not be the literal placeholder `change-this-secret-in-production` — both enforced as hard startup failures. Rotating it logs every session out. |
| `STORAGE_KEY` | falls back to `JWT_SECRET` | No | Derives the at-rest encryption key (via HKDF) for stored secrets — git tokens, restic password — independently of `JWT_SECRET`. If set but shorter than 32 characters, the server logs a startup warning and boots anyway (rotating it would make already-encrypted data unreadable, so this is a warning, not a hard failure, unlike `JWT_SECRET`). If unset, it inherits `JWT_SECRET`'s strength. |
| `AUTH_DISABLED` | `false` | No | Set to exactly `true` to skip authentication entirely (e.g. for local development). When `true`, `JWT_SECRET` is not required. |
| `AUTH_DISABLED_ALLOWED_NETWORKS` | loopback only | No | Comma-separated CIDRs, beyond loopback, permitted to use the `AUTH_DISABLED` bypass. Kept deliberately separate from `TRUSTED_NETWORKS`: that value is Gin's trusted-proxy list, and reusing it here would let trusting a reverse proxy for IP attribution silently widen who can skip authentication. |
| `TRUSTED_NETWORKS` | none | No | Comma-separated CIDRs Gin trusts for `X-Forwarded-For` client-IP attribution (affects rate limiting). The bundled `docker-compose.yaml` sets this to common private ranges by default. |
| `HEALTH_ALLOWED_NETWORKS` | loopback only | No | Comma-separated CIDRs, beyond loopback, permitted to reach `/health` and `/health/ready`. Deliberately separate from `TRUSTED_NETWORKS` so granting an uptime monitor health access doesn't also grant it `X-Forwarded-For` spoofing. |
| `CORS_ORIGINS` | none (no cross-origin access) | No | Comma-separated list of allowed CORS origins. Trimmed and split via `config.NormalizeOrigins`; empty entries are dropped. |

## Paths, volumes & container user

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `STACKS_DIR` | `/opt/stacks` | No | Where Capstan reads/writes managed stacks. Must be identical inside and outside the container (bind mount) for Docker Compose operations issued from inside Capstan to resolve correctly on the host. |
| `DOCKGE_STACKS_DIR` | — | No | Fallback for `STACKS_DIR` when `STACKS_DIR` is unset, for compatibility with a prior Dockge stacks directory. Ignored if `STACKS_DIR` is set. |
| `HOST_STACKS_DIR` | none (empty) | No | The host-side path matching `STACKS_DIR`, used only to verify volume path identity at startup. Left unset, the server logs a warning on every boot; set but mismatched from `STACKS_DIR`, it also warns. Neither warning blocks startup. |
| `EXTRA_STACKS_DIRS` | none | No | Comma-separated list of additional stack directories beyond `STACKS_DIR`, whitespace-trimmed, empty entries dropped. |
| `DATA_DIR` | `/app/data` | No | Where Capstan stores its SQLite database, keys, and (by default) the restic backup repository. |
| `PUID` | `1000` | No | Host UID the container's `appuser` is remapped to at startup by `docker/entrypoint.sh`. Not read by the Go binary — set as a container environment variable and consumed by the entrypoint script before the server process starts. Match it to the host user that owns your `stacks`/`data` directories. |
| `PGID` | `1000` | No | Host GID counterpart to `PUID`, same remapping mechanism. |

## Git integration

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `GIT_SSH_KEY` | `$HOME/.ssh/id_rsa` | No | Path to the SSH private key used for git operations over SSH. `$HOME` here is the OS-provided environment variable inside the container, not a Capstan setting in its own right — it only supplies this default. |
| `GIT_HTTPS_TOKEN` | none | No | Token used for git operations over HTTPS. |
| `GIT_HTTPS_USER` | `git` | No | Username paired with `GIT_HTTPS_TOKEN` for HTTPS git operations. |

## Backups & restic

These are environment-variable fallbacks only. The Settings → Backup UI writes
to the database, which always takes precedence over the variable below at
runtime (`resolveBackupConfig` in `backend/internal/services/backup_config.go`).
Set them if you prefer environment-level configuration to the UI, or for
scripted/headless deployments.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `RESTIC_REPOSITORY` | `{DATA_DIR}/restic-repo` | No | Path to the local restic repository. |
| `RESTIC_PASSWORD` | none | No, but backups will fail without one (DB or env) | Restic repository encryption password. Never logged. |
| `BACKUP_KEEP_DAILY` | `7` | No | Daily snapshots to retain (`restic forget --keep-daily`). Non-numeric values are ignored and fall back to the default. |
| `BACKUP_KEEP_WEEKLY` | `4` | No | Weekly snapshots to retain. |
| `BACKUP_KEEP_MONTHLY` | `6` | No | Monthly snapshots to retain. |
| `BACKUP_KEEP_YEARLY` | `0` | No | Yearly snapshots to retain. |
| `BACKUP_AUTO_PRUNE` | `true` | No | Whether `--prune` is appended to the retention `forget` command. Only the literal string `true` (case-sensitive) is treated as true. |
| `BACKUP_SCHEDULE_INTERVAL` | `0` (disabled) | No | Backup scheduler tick, in minutes. `0` disables the scheduler. |
| `BACKUP_SYNC_AFTER` | `false` | No | Whether an rclone sync runs automatically after each local backup. Only the literal string `true` is treated as true. |
| `BACKUP_HOSTNAME` | system hostname | No | Value passed as `--hostname` to restic `backup`/`forget`. |

## Cloud sync & rclone

Same DB-over-env precedence as the backup settings above; used for Stage 2 /
disaster-recovery sync of the restic repository to a remote.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `RCLONE_REMOTE` | none | No | Name of the configured rclone remote to sync the restic repository to. Sync is skipped if unset. |
| `RCLONE_PATH` | none | No | Destination path within the rclone remote. |
| `RCLONE_TRANSFERS` | `4` | No | Number of parallel file transfers rclone uses during sync. |

## Database & migrations

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `CAPSTAN_ALLOW_SCHEMA_DOWNGRADE` | unset (refuse) | No | Escape hatch for the forward-version guard in `RunMigrations` (`backend/internal/database/migrations.go`): if the database schema version is newer than the running binary understands, startup fails fatally unless this is set to exactly `1`, which downgrades the refusal to a warning and continues. This only affects the forward-version check — it runs no down-migration and does not by itself make a rollback safe. Set it only after confirming the specific rollback is safe; see [Rolling back](../how-to/upgrade-and-roll-back.md#rolling-back) for the full procedure this supports. |

## Not covered here

`HOME` is read (via `os.Getenv("HOME")`) only to build `GIT_SSH_KEY`'s default
path; it is OS/container-provided, not a Capstan setting, and isn't listed as
one above.
