# Upgrading and Rolling Back

## Upgrading

To upgrade, pull a newer image tag and recreate the container:

```bash
docker compose -f docker-compose.prod.yaml pull
docker compose -f docker-compose.prod.yaml up -d
```

`docker-compose.prod.yaml` defaults to the `:latest` tag and carries a
`com.centurylinklabs.watchtower.enable=true` label, so an instance with
[watchtower](https://github.com/containrrr/watchtower) attached to the same
network can pick up new releases automatically. Either way, confirm what's
actually running afterwards with `GET /api/v1/version` or Settings → About.

> **Upgrading:** this release binds JWTs to an issuer claim, so existing sessions
> are invalidated on upgrade — log in again once. Previously stored secrets stay
> readable and are re-encrypted under the new key scheme on next save.

> **Upgrading from a `docker-compose.prod.yaml` without `init: true`:** add
> `init: true` to the `app` service (next to `restart:`), then run
> `docker compose -f docker-compose.prod.yaml up -d`. Compose sees the changed
> config and recreates the container. Without it, killed child processes
> accumulate as zombies for the container's lifetime.

> **Upgrading from a `docker-compose.prod.yaml` that mounts `./stacks:/opt/stacks`:**
> that older template broke Volume Path Identity. Your stack files live in
> `./stacks` next to the compose file, but a managed stack's relative binds
> (`./data:/data`) were resolved by the host against `/opt/stacks/<stack>/`, so
> that data is under `/opt/stacks` on the host, outside what Capstan backs up.
> The current template mounts `${STACKS_DIR:-/opt/stacks}` at the same path on
> both sides. Before switching, stop Capstan and either:
>
> - move `./stacks` to `/opt/stacks` on the host and keep the default, or
> - set `STACKS_DIR` (and `HOST_STACKS_DIR`) in `.env` to the **absolute** path
>   of your existing `stacks` directory, e.g. `STACKS_DIR=/home/capstan/stacks`.
>
> Then check `/opt/stacks/<stack>/` on the host for data your stacks wrote
> through relative binds and merge it into the stack directory. After
> `up -d`, `docker compose -f docker-compose.prod.yaml logs | grep "Volume path identity"`
> must not show an `ERROR` line.

> **Upgrading an `AUTH_DISABLED=true` instance that you reach by anything other
> than `localhost`:** set `ALLOWED_HOSTS` first. With authentication disabled,
> Capstan now answers `403` (message: `Host "<name>" is not allowed while
> authentication is disabled; add it to ALLOWED_HOSTS`) to a request whose
> `Host` is not `localhost`, `127.0.0.1`, `[::1]` or in `ALLOWED_HOSTS`. That
> covers browsing by LAN IP or LAN name, a reverse proxy forwarding its public
> name, and an uptime monitor probing `/health` by address. Add each name or IP
> you use, e.g. `ALLOWED_HOSTS=capstan.lan,192.168.1.10`, then
> `docker compose -f docker-compose.prod.yaml up -d`. The container's own
> healthcheck uses `localhost` and needs nothing. Instances with authentication
> on are unaffected.

> **Upgrading to the release that binds stored secrets to their setting:** at
> the first start, Capstan re-encrypts every stored secret in a new format tied
> to the setting or directory it belongs to, and starts encrypting the restic
> repository and rclone remote, which were stored in clear before. Nothing to do
> on upgrade. Two consequences:
>
> - **Rolling back below this release needs the pre-upgrade database.** Older
>   releases cannot read the new format, and would read the encrypted repository
>   as a local folder path. The schema version stops an older image from
>   starting. Do not get past that with `CAPSTAN_ALLOW_SCHEMA_DOWNGRADE=1`: for
>   this release it is not safe. Restore the `capstan.db` snapshot taken before
>   the upgrade, or roll back and then re-enter the git tokens and all backup
>   settings (repository, password, rclone remote).
> - **Changing `STORAGE_KEY` now also requires re-entering the restic repository
>   and rclone remote**, not only the password and git tokens.

## Rolling back

Recovering from a bad release usually means re-pinning an older image tag (or
letting watchtower revert one). Capstan's database schema is versioned and
guards against this: on startup it logs the database's schema version
alongside the version this binary understands, and if the database was
already migrated by a **newer** binary than the one now starting, startup
refuses with a fatal error naming both versions rather than running against a
schema it doesn't fully understand — rolling back across a migration can
corrupt data.

If you've checked the specific rollback is safe (e.g. the migrations added
between the two versions are additive and don't change data the older binary
writes to), set `CAPSTAN_ALLOW_SCHEMA_DOWNGRADE=1` to downgrade the refusal to
a warning and continue startup anyway. This variable only affects the
forward-version check; it does not run any down-migration, and it does not by
itself make an unsafe rollback safe.

---

[← Documentation index](../../README.md#documentation)
