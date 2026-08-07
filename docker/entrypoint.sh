#!/bin/sh
# Capstan container entrypoint.
#
# Runs briefly as root to (1) align the runtime user to the host's file owner
# and (2) grant access to the mounted Docker socket — whatever its group GID
# happens to be on this host — then drops privileges to the non-root `appuser`.
# No changes to users/groups on the host are required.
set -e

PUID="${PUID:-1000}"
PGID="${PGID:-1000}"

# 1. Align appuser/appgroup to the host owner of the stacks/data files, so
#    Capstan reads/writes them cleanly and you can still edit them from the
#    host. -o permits non-unique IDs to avoid collisions on the target value.
groupmod -o -g "$PGID" appuser
usermod  -o -u "$PUID" -g "$PGID" appuser

# 2. Grant Docker access by joining the group that owns the socket. The GID is
#    discovered at runtime — never assume a fixed number (it differs per host).
SOCK=/var/run/docker.sock
if [ -S "$SOCK" ]; then
  SOCK_GID="$(stat -c '%g' "$SOCK")"
  if [ "$SOCK_GID" -ne 0 ]; then
    if ! getent group "$SOCK_GID" >/dev/null 2>&1; then
      groupadd -g "$SOCK_GID" dockerhost
    fi
    usermod -aG "$(getent group "$SOCK_GID" | cut -d: -f1)" appuser
  else
    # Socket owned by the root group (e.g. some rootless Docker setups).
    usermod -aG root appuser 2>/dev/null || true
  fi
else
  echo "WARN: $SOCK not found. Mount it with:" >&2
  echo "      -v /var/run/docker.sock:/var/run/docker.sock" >&2
fi

# 3. Capstan owns its own data dir (SQLite db, state) — align it to appuser.
#    The stacks dir is intentionally NOT chowned: PUID/PGID match its owner
#    instead, so we never rewrite ownership of your existing compose projects.
#    /app/data also holds the default restic-repo (RESTIC_REPOSITORY), so the
#    recursive chown grows with backup volume. Skip it when the top-level dir is
#    already owned by the target user, so an already-aligned (possibly large)
#    tree is not rewalked on every container start.
if [ "$(stat -c '%u:%g' /app/data 2>/dev/null)" != "$PUID:$PGID" ]; then
  chown -R appuser:appuser /app/data 2>/dev/null || true
fi

# 4. Drop privileges and run the server (CMD) as the non-root user.
#    gosu, like the su-exec it replaces, execs directly and does no environment
#    munging — HOME and the rest of the container env carry through unchanged.
exec gosu appuser "$@"
