#!/usr/bin/env bash
# scripts/docker-registry-mirror.sh
#
# Points a GitHub-hosted runner's Docker daemon at mirror.gcr.io (Google's
# Docker Hub cache) for Docker Hub pulls, then restarts it (agent-os-an6d,
# Edwin's decision D58). Run it as the FIRST step of a job, before anything
# that uses the daemon: a restart would kill containers or builders an earlier
# step started.
#
# WHY. setup-buildx's docker-container driver pulls docker.io/moby/buildkit,
# and anonymous Docker Hub pulls from GitHub's shared runner IPs hit 429. The
# ref is kept as docker.io/moby/buildkit on purpose: buildx only checks the
# BuildKit image's signature policy for that exact repository name
# (bkimage.TrustedRepo in buildx v0.37.x), so renaming it to a mirror path
# would switch the check off. A daemon registry-mirror changes where the bytes
# come from, not the name. If the mirror fails, the daemon falls back to
# Docker Hub itself.
#
# The existing /etc/docker/daemon.json is merged into, not replaced: the
# runner image may already configure the daemon there.

set -euo pipefail

MIRROR="${MIRROR:-https://mirror.gcr.io}"
CONF=/etc/docker/daemon.json

current='{}'
if sudo test -s "$CONF"; then
  current=$(sudo cat "$CONF")
fi
echo "daemon.json before: $current"
merged=$(jq --arg m "$MIRROR" '."registry-mirrors" = ((."registry-mirrors" // []) - [$m] + [$m])' <<<"$current")
echo "daemon.json after:  $merged"
sudo mkdir -p "$(dirname "$CONF")"
printf '%s\n' "$merged" | sudo tee "$CONF.tmp" >/dev/null
sudo mv "$CONF.tmp" "$CONF"

sudo systemctl restart docker

for _ in $(seq 1 30); do
  docker info >/dev/null 2>&1 && break
  sleep 1
done
mirrors=$(docker info --format '{{json .RegistryConfig.Mirrors}}')
echo "daemon registry mirrors: $mirrors"
case "$mirrors" in
  *"${MIRROR#https://}"*) ;;
  *)
    echo "::error::docker-registry-mirror: the daemon did not pick up $MIRROR" >&2
    exit 1
    ;;
esac
