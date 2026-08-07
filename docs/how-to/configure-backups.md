# Configuring Backups

Capstan includes a built-in backup engine powered by [restic](https://restic.net) and
[rclone](https://rclone.org), both shipped inside the container image at pinned versions:

| Tool   | Version | Purpose                          |
| ------ | ------- | -------------------------------- |
| restic | 0.19.1  | Local encrypted snapshot backups |
| rclone | 1.74.4  | Cloud sync / offsite DR copies   |

## What gets backed up

Every backup run captures two kinds of thing:

| Artifact | restic tag | Notes |
| --- | --- | --- |
| Each stack's compose directory | the stack ID | One snapshot per enabled stack |
| `capstan.db` | `capstan-database` | Captured on **every** run, before the stacks |

The database is what makes an instance *this* instance: user accounts, the
encrypted git tokens and restic password, every setting, backup and auto-update
policies, the stacks registry, and the audit log. Without it a restore gives you
your compose files back and nothing else.

It is snapshotted with SQLite's `VACUUM INTO` rather than copied. `capstan.db`
runs in WAL mode, so a plain file copy taken while writes are in flight can be
torn or missing recent commits.

A run that saves every stack but fails to capture the database is reported as
**partial**, never success.

Backups can be triggered manually (Settings, Backup tab) or run on a schedule.

> **`STORAGE_KEY` is not in the backup, and that is deliberate.** The secrets
> inside `capstan.db` are encrypted with a key derived from it, so a stolen
> backup does not yield your git tokens. The corollary is that restoring onto a
> host with a *different* `STORAGE_KEY` gives you a database whose secrets
> cannot be decrypted. Store `STORAGE_KEY` and `JWT_SECRET` in the same password
> manager as the restic repository password. All three are needed for a complete
> recovery.

## Configuration

The quickest path is the **Settings UI** (Settings, Backup). All fields save to the
encrypted database and take effect without a restart.

Alternatively, set env vars in your `.env` file (see `.env.example` for the full list).
The UI always wins over env vars.

Key variables:

| Variable             | Default                    | Notes                                     |
| -------------------- | --------------------------- | ----------------------------------------- |
| `RESTIC_REPOSITORY`  | `/app/data/restic-repo`    | Path inside the container                 |
| `RESTIC_PASSWORD`    | _(none)_                   | Required; stored encrypted when set in UI |
| `RCLONE_REMOTE`      | _(none)_                   | rclone remote name (optional)             |
| `RCLONE_PATH`        | `capstan-backups`          | Destination path on the remote            |

## Bind-mount requirement

The restic repository lives inside `/app/data`. Your compose file MUST mount this
as a host bind mount so that snapshots survive container recreation:

```yaml
volumes:
  - ./data:/app/data   # host bind mount — required for backup persistence
```

Never replace this with a Docker named volume. Both `docker-compose.prod.yaml`
and `docker-compose.yaml` use a bind mount by default.

## Running a backup

```bash
# Via the UI: Settings → Backup → Run Backup Now
# Via the API:
curl -X POST http://localhost:5001/api/v1/backups/run
```

## Cloud sync (optional)

Configure an rclone remote and set `RCLONE_REMOTE` (or use the UI). Enable
"Sync after backup" to push every snapshot to the remote automatically.

Rclone config file: mount it read-only into the container if you manage it externally:

```yaml
volumes:
  - ~/.config/rclone/rclone.conf:/home/appuser/.config/rclone/rclone.conf:ro
```
