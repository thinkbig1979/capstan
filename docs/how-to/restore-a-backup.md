# Restoring a Backup

See [Configuring backups](configure-backups.md) for what gets backed up and
where the restic repository lives.

## Restore

```bash
# List snapshots for a stack:
curl http://localhost:5001/api/v1/backups/snapshots?stackId=<id>

# Restore a snapshot via UI: Settings → Backup → Snapshots → Restore
# Or via API:
curl -X POST http://localhost:5001/api/v1/backups/restore \
  -H 'Content-Type: application/json' \
  -d '{"stackId":"<id>","snapshotId":"<short-id>"}'
```

## Disaster recovery

Follow this in order. **The database is restored before the stacks** — restoring
stacks first gives you a Capstan that does not know they exist.

### Before you start, you need

| Item | Why |
| --- | --- |
| The restic repository password | Without it the repository cannot be opened at all |
| `STORAGE_KEY` | The old value, or every stored secret is unreadable |
| `JWT_SECRET` | A different value invalidates existing sessions (survivable) |
| Access to the rclone remote | The only copy that survives losing the data volume |

If you have the repository but not `STORAGE_KEY`, you can still recover
everything except the encrypted secrets — see step 6.

### 1. Deploy a fresh instance, stopped

Bring up a new Capstan with the same `./data` bind mount path and the **same**
`STORAGE_KEY` and `JWT_SECRET` as the lost instance, then stop the container:

```bash
docker compose -f docker-compose.prod.yaml up -d
docker compose -f docker-compose.prod.yaml stop capstan
```

Stopping matters. Overwriting `capstan.db` underneath a running server corrupts
it — the server holds the WAL open and will write over what you just restored.

### Run steps 2–4 inside the image, not on the host

Every command below is run in a throwaway container built from the Capstan
image, with the same volumes. Three reasons, all learned the hard way in a real
drill:

- `restic` and `rclone` ship **inside** the image at pinned versions. A freshly
  provisioned recovery host usually has neither, and installing them mid-outage
  is a poor use of the outage.
- The rclone remote path stored in Capstan's settings is a path **as seen from
  inside the container**. Running the same `rclone sync` on the host resolves it
  against the host filesystem and fails with `directory not found`.
- The paths inside the restic snapshot are container paths, so restoring in the
  same namespace keeps them meaningful.

Define this once, adjusting the paths to your deployment:

```bash
alias capstan-tools='docker run --rm \
  -v ./data:/app/data \
  -v ~/.config/rclone/rclone.conf:/home/appuser/.config/rclone/rclone.conf:ro \
  -e RCLONE_CONFIG=/home/appuser/.config/rclone/rclone.conf \
  --entrypoint sh ghcr.io/thinkbig1979/capstan:latest -c'
```

`RCLONE_CONFIG` is not optional. `--entrypoint sh` skips the entrypoint that
would drop to `appuser`, so these commands run as **root** with `HOME=/root`,
and rclone looks for its config under `$HOME`. Without the variable it never
reads the file you just mounted and step 2 fails with:

```
NOTICE: Config file "/root/.config/rclone/rclone.conf" not found - using defaults
CRITICAL: Failed to create file system for "<remote>:<path>": didn't find section in config file
```

which reads like a broken remote rather than a config it never opened.

### 2. Get the repository back from the remote

```bash
capstan-tools 'rclone sync <remote>:<path> /app/data/restic-repo --stats-one-line'
```

`<remote>:<path>` are exactly the values shown in Settings → Backup. This is
what the UI's DR Restore does; doing it by hand keeps step 1's "container
stopped" rule intact.

If the remote is a local-filesystem one, add its bind mount to the alias too —
the path is resolved inside the container.

### 3. Find the database snapshot

```bash
capstan-tools '
  printf "%s" "$RESTIC_PASSWORD" > /tmp/pw
  export RESTIC_REPOSITORY=/app/data/restic-repo RESTIC_PASSWORD_FILE=/tmp/pw
  restic snapshots --tag capstan-database'
```

The newest one is normally what you want. Note its short ID.

### 4. Restore the database

The snapshot contains the file at `/app/data/backup-staging/capstan.db`, so a
restore reproduces that path underneath `--target`:

```bash
capstan-tools '
  set -e
  printf "%s" "$RESTIC_PASSWORD" > /tmp/pw
  export RESTIC_REPOSITORY=/app/data/restic-repo RESTIC_PASSWORD_FILE=/tmp/pw
  restic restore <snapshot-id> --target /tmp/capstan-restore
  cp /tmp/capstan-restore/app/data/backup-staging/capstan.db /app/data/capstan.db
  rm -rf /tmp/capstan-restore
  rm -f /app/data/capstan.db-wal /app/data/capstan.db-shm
  chown appuser:appuser /app/data/capstan.db'
```

Two details that will bite if skipped:

- **Delete `capstan.db-wal` and `capstan.db-shm`.** They belong to the database
  you just replaced. Left in place, SQLite tries to apply them to the restored
  file.
- **`chown appuser:appuser`.** The restore runs as root; Capstan runs as
  `appuser` (UID 1000 by default, remappable via `PUID`/`PGID`). A root-owned
  `capstan.db` leaves the server unable to write to its own database.

### 5. Start Capstan and verify before going further

```bash
docker compose -f docker-compose.prod.yaml start capstan
```

Check all four, in this order. Each one fails differently:

1. **Log in** with a pre-existing account. Failure here means the wrong
   `capstan.db`, not a `STORAGE_KEY` problem.
2. **Settings → Backup** shows your repository path, retention and schedule.
   Empty defaults mean the database did not actually land.
3. **A git-backed stack can pull.** This is the `STORAGE_KEY` test — the token
   is decrypted to run it. An authentication failure here with the right
   password means the key differs from the one that encrypted it.
4. **Settings → Backup → History** lists runs from before the incident, and your
   backup policies are still enabled.

### 6. If `STORAGE_KEY` was lost

Everything except the encrypted secrets is recoverable. Accounts, settings,
policies, history and the stacks registry all come back. What must be re-entered
by hand: the restic repository password, git HTTPS tokens, and any other stored
credential. Re-enter them in Settings; the new values are encrypted with the new
key.

### 7. Restore the stacks

Only now, with a working Capstan that knows about them, restore the compose
directories from Settings → Backup → Snapshots, or via the API:

```bash
curl -X POST http://localhost:5001/api/v1/backups/restore \
  -H 'Content-Type: application/json' \
  -d '{"stackId":"<id>","snapshotId":"<short-id>","confirm":true}'
```

### Practise this

A runbook that has never been executed is a hypothesis. Run it end to end
against a scratch instance at least once, before you need it.

---

[← Documentation index](../../README.md#documentation)
