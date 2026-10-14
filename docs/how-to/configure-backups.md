# Configuring Backups

Capstan includes a built-in backup engine powered by [restic](https://restic.net) and
[rclone](https://rclone.org), both shipped inside the container image at pinned versions:

| Tool   | Version | Purpose                          |
| ------ | ------- | -------------------------------- |
| restic | 0.19.1  | Local encrypted snapshot backups |
| rclone | 1.75.1  | Cloud sync / offsite DR copies   |

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

Named Docker volumes are not part of a backup. The snapshot of a stack is its
directory only: the compose file, `.env`, and files inside the directory.
Backups skip anything mounted from another filesystem inside the stack
directory (`--one-file-system`) and directories marked with a `CACHEDIR.TAG`
file. Back up volume data separately.

Backups can be triggered manually (Settings, Backup, **Back up now**) or run
on a schedule.

## Checking that backups can be restored

A backup that completes is not proof the repository can restore it: pack files
can go missing or rot on the storage underneath. **Check backups** on the
Backup Status card (Settings, Backup) reads 5% of the backup data and confirms it is intact.
It cannot run while a backup, sync, restore or prune is running, because the
check locks the repository.

The same check also runs once a week, after a scheduled backup, unless you turn
off **Check the repository weekly** in Settings, Backup. It only runs when
scheduled backups are on. A failed check shows a red "Repository integrity check failed" banner on the
Backup Status card in Settings, Backup; backups keep running either way. A scheduled backup that falls due while the
check is still running is skipped and shows as **Skipped** in the backup
history (Dashboard, Backups).

> **`STORAGE_KEY` is not in the backup, and that is deliberate.** The secrets
> inside `capstan.db` are encrypted with a key derived from it, so a stolen
> backup does not yield your git tokens. The corollary is that restoring onto a
> host with a *different* `STORAGE_KEY` gives you a database whose secrets
> cannot be decrypted. Store `STORAGE_KEY` and `JWT_SECRET` in the same password
> manager as the restic repository password. All three are needed for a complete
> recovery.

## Stale repository locks

restic locks the repository while it works. A restic process that is killed
part-way (a container restart in the middle of a backup or a check, for
example) leaves its lock behind, and restic does not remove it on its own.
Such a stale lock blocks the cleanup of old snapshots, the weekly check, and
prune, and a stale lock from an interrupted check or prune blocks backups too.

Capstan handles this itself. When a backup, cleanup, check or prune finds the
repository locked, Capstan runs `restic unlock` once and retries. `restic
unlock` only removes locks restic considers stale: older than 30 minutes, or
left by a process on this machine that no longer exists. A lock held by a
restic process that is still running is left alone, and the operation reports
that the repository is locked by another restic process.

If the cleanup of old snapshots still fails, the new snapshot is kept: each
stack keeps **success** and shows the cleanup error, and the run shows
**partial**:
"old snapshots were not cleaned up for stack(s) ...". Check whether another
restic process is using the repository before running `restic unlock
--remove-all` by hand.

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
# Via the UI: Settings → Backup → Back up now
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

---

[← Documentation index](../../README.md#documentation)
