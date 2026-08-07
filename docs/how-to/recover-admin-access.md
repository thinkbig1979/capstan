# Recovering Admin Access

Capstan allows exactly one account. First-run setup refuses to run a second time,
and there is no password-reset endpoint, no email recovery, and no fallback
account — all deliberate for a single-operator tool that should not grow a
credential-reset surface reachable from the network.

If you lose the password, reset it offline from a shell on the host:

```bash
read -rs NEWPW && printf '%s' "$NEWPW" | \
  docker compose exec -T capstan /app/server admin reset-password
```

The server does not need restarting, and the reset **revokes every session** for
the account, so anyone holding a login cookie is signed out.

```
server admin reset-password [--username <name>] [--data-dir <path>]
```

- `--username` is only needed if the database somehow holds more than one
  account; with one account it is resolved automatically.
- `--data-dir` defaults to `$DATA_DIR`, then `/app/data`. It must contain
  `capstan.db`; the command refuses a directory that has none rather than
  creating an empty database there.
- The new password is read from **stdin**, never a flag, so it stays out of the
  process list and your shell history — hence `read -rs` above. It must satisfy
  the same strength policy as any other password.
- `JWT_SECRET` is not required: the command is dispatched before configuration
  loads, so a lost or unset secret does not block recovery.

**Why this is not a backdoor.** Running it requires shell access to the
container, and anyone with that can already read `JWT_SECRET` and mint a token,
or edit `capstan.db` directly. It adds convenience to what host access already
permits, which is exactly the claim a network-facing reset flow could not make.
Protecting shell access to the host remains the control that matters.
