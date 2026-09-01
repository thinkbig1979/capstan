# Configuration Reference

Capstan is configured entirely through environment variables, read once at
startup by `backend/internal/config/config.go` (`Load()`). 16 of them —
Git and backup/cloud-sync settings — additionally have a database layer
(Settings → Git and Settings → Backup in the UI) that takes precedence over
the environment variable at runtime; the env var there is a fallback for
scripted or headless deployments, not the only way to set them. Every other
variable is env-only: read once at startup, with no UI or database override.

Precedence, for the 16 that have a database layer: **DB setting →
environment variable → hard-coded default**.

> **If a variable you set in `docker-compose.yaml`/`.env` doesn't seem to be
> taking effect:** check whether it's one of the 16 DB-backed variables below
> (marked **DB-overridable** in each affected section) and whether it's also
> been set through the corresponding Settings page. Once saved through the
> UI, the database value wins on every subsequent read, including after a
> container restart — the env var isn't ignored, it's just outranked. There
> is no log line or UI indicator that surfaces this; the only way to tell is
> to check the Settings page itself. Clear/reset the field there to fall
> back to the env var again.

> **Before a production deploy, at minimum change:** `JWT_SECRET` (required,
> generate with `openssl rand -hex 32`) and `STORAGE_KEY` (strongly
> recommended, same generation command). Review `PUID`/`PGID` against the
> host user that owns your stacks directory, and `HOST_STACKS_DIR` if
> `STACKS_DIR` differs inside and outside the container.

> **Secrets — never commit these, never log them:** `JWT_SECRET`,
> `STORAGE_KEY`, `RESTIC_PASSWORD`, `GIT_HTTPS_TOKEN`, and the contents of
> whatever file `GIT_SSH_KEY` points at.

## Server & logging

**Env-only** — read once at startup, no UI or database override.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `PORT` | `5001` | No | TCP port the server listens on. Must parse as a number between 1 and 65535, checked at startup (`config.go`'s `validate`). |
| `LOG_LEVEL` | `info` | No | Log verbosity. Validated against `logging.ParseLevel`; an unrecognised value fails startup rather than silently falling back, so a typo is caught immediately instead of during an incident. |
| `LOG_FORMAT` | `text` | No | `text` or `json`. Validated the same way as `LOG_LEVEL`. |

## Auth & secrets

**Env-only** — read once at startup, no UI or database override.

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

**Env-only** — read once at startup (`PUID`/`PGID` even earlier, by the
entrypoint script before the server process starts); no UI or database
override.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `STACKS_DIR` | `/opt/stacks` | No | Where Capstan reads/writes managed stacks. Must be identical inside and outside the container (bind mount) for Docker Compose operations issued from inside Capstan to resolve correctly on the host. |
| `DOCKGE_STACKS_DIR` | — | No | Fallback for `STACKS_DIR` when `STACKS_DIR` is unset, for compatibility with a prior Dockge stacks directory. Ignored if `STACKS_DIR` is set. |
| `HOST_STACKS_DIR` | none (empty) | No | The host-side path matching `STACKS_DIR`, used only to verify volume path identity at startup. Left unset, the server logs a warning on every boot; set but mismatched from `STACKS_DIR`, it also warns. Neither warning blocks startup. |
| `EXTRA_STACKS_DIRS` | none | No | Comma-separated list of additional stack directories beyond `STACKS_DIR`, whitespace-trimmed, empty entries dropped. |
| `DATA_DIR` | `/app/data` | No | Where Capstan stores its SQLite database, keys, and (by default) the restic backup repository. |
| `PUID` | `1000` | No | Host UID the container's `appuser` is remapped to at startup by `docker/entrypoint.sh`. Not read by the Go binary — set as a container environment variable and consumed by the entrypoint script before the server process starts. Match it to the host user that owns your `stacks`/`data` directories. |
| `PGID` | `1000` | No | Host GID counterpart to `PUID`, same remapping mechanism. |
| `TZ` | `UTC` | No | IANA zone name the container's clock runs in. Not read by the Go binary either — the runtime resolves it through `tzdata` (installed in the image at `docker/Dockerfile:165`) into Go's `time.Local`, which is what clock-time schedules are interpreted against. This is the one setting that decides *when* scheduled updates and backups actually fire: a schedule saved as `03:00` means 03:00 in this zone, so leaving it at `UTC` gives an operator in `Europe/Amsterdam` a 05:00 local run in summer and 04:00 in winter, with no error anywhere to say so. Both bundled compose files set it as `TZ=${TZ:-UTC}`, so exporting `TZ` or putting it in `.env` is enough. The resolved zone and offset are shown beside the schedule fields in Settings → Backup and Settings → Updates. |

## Git integration

**DB-overridable.** Settings → Git (`GetGitSettings`/`UpdateGitSettings` in
`backend/internal/handlers/settings.go`) reads/writes these in the database
ahead of the env var below, same DB → env → default precedence as the
backup settings further down this page. Set the env var for a scripted or
headless deployment; once a value is saved through Settings → Git, that
value wins until cleared there.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `GIT_SSH_KEY` | `$HOME/.ssh/id_rsa` | No | Path to the SSH private key used for git operations over SSH. `$HOME` here is the OS-provided environment variable inside the container, not a Capstan setting in its own right — it only supplies this default. |
| `GIT_HTTPS_TOKEN` | none | No | Token used for git operations over HTTPS. |
| `GIT_HTTPS_USER` | `git` | No | Username paired with `GIT_HTTPS_TOKEN` for HTTPS git operations. |

## Backups & restic

**DB-overridable.** These are environment-variable fallbacks, not the last
word: the Settings → Backup UI writes to the database, and
`resolveBackupConfig` (`backend/internal/services/backup_config.go`) always
checks the database first, then the env var, then the hard-coded default —
on every read, not just at startup, so a UI change takes effect without a
restart. Concretely: set `BACKUP_KEEP_DAILY=30` here, save a retention value
in Settings → Backup, restart the container, and you'll still see the
database's value, not 30 — the env var isn't broken, it's just outranked.
Set these if you prefer environment-level configuration to the UI, or for
scripted/headless deployments where nothing has touched Settings → Backup
yet.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `RESTIC_REPOSITORY` | `{DATA_DIR}/restic-repo` | No | Path to the local restic repository. |
| `RESTIC_PASSWORD` | none | No, but backups will fail without one (DB or env) | Restic repository encryption password. Never logged. |
| `BACKUP_KEEP_DAILY` | `7` | No | Daily snapshots to retain (`restic forget --keep-daily`). Non-numeric values are ignored and fall back to the default. |
| `BACKUP_KEEP_WEEKLY` | `4` | No | Weekly snapshots to retain. |
| `BACKUP_KEEP_MONTHLY` | `6` | No | Monthly snapshots to retain. |
| `BACKUP_KEEP_YEARLY` | `0` | No | Yearly snapshots to retain. |
| `BACKUP_AUTO_PRUNE` | `true` | No | Whether `--prune` is appended to the retention `forget` command. Only the literal string `true` (case-sensitive) is treated as true. |
| `BACKUP_SCHEDULE_INTERVAL` | `0` (disabled) | No | Backup scheduler tick, in minutes. `0` disables the scheduler. For a clock-time schedule set in Settings → Backup (e.g. `03:00`), the zone that time is read in comes from [`TZ`](#paths-volumes--container-user), which defaults to `UTC`. |
| `BACKUP_SYNC_AFTER` | `false` | No | Whether an rclone sync runs automatically after each local backup. Only the literal string `true` is treated as true. |
| `BACKUP_HOSTNAME` | system hostname | No | Value passed as `--hostname` to restic `backup`/`forget`. |

## Cloud sync & rclone

**DB-overridable**, same DB → env → default precedence as Backups & restic
above; used for Stage 2 / disaster-recovery sync of the restic repository to
a remote.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `RCLONE_REMOTE` | none | No | Name of the configured rclone remote to sync the restic repository to. Sync is skipped if unset. |
| `RCLONE_PATH` | none | No | Destination path within the rclone remote. |
| `RCLONE_TRANSFERS` | `4` | No | Number of parallel file transfers rclone uses during sync. |

## Database & migrations

**Env-only** — read once at startup, no UI or database override.

| Name | Default | Required | What it does |
| --- | --- | --- | --- |
| `CAPSTAN_ALLOW_SCHEMA_DOWNGRADE` | unset (refuse) | No | Escape hatch for the forward-version guard in `RunMigrations` (`backend/internal/database/migrations.go`): if the database schema version is newer than the running binary understands, startup fails fatally unless this is set to exactly `1`, which downgrades the refusal to a warning and continues. This only affects the forward-version check — it runs no down-migration and does not by itself make a rollback safe. Set it only after confirming the specific rollback is safe; see [Rolling back](../how-to/upgrade-and-roll-back.md#rolling-back) for the full procedure this supports. |

## Not covered here

`HOME` is read (via `os.Getenv("HOME")`) only to build `GIT_SSH_KEY`'s default
path; it is OS/container-provided, not a Capstan setting, and isn't listed as
one above.

---

[← Documentation index](../../README.md#documentation)
