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
