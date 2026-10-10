#!/usr/bin/env bash
# scripts/pull-with-retry.sh <image-ref>...
#
# Pulls each image ref into the local Docker image store, ONE AT A TIME,
# with a bounded retry (agent-os-an6d). CI pulls its images from the AWS ECR
# Public mirror of the Docker Official Images (public.ecr.aws/docker/library/)
# because anonymous Docker Hub pulls from GitHub's shared runner IPs hit 429.
# ECR Public allows one anonymous pull per second per IP, so this script:
#
#   - never runs two pulls at once (a plain loop, no `&`);
#   - waits PULL_SPACING seconds between refs, so a run of refs cannot burst
#     past the limit;
#   - retries a failed pull MAX_ATTEMPTS times with a growing pause
#     (2, 4, 6, 8 s), so a throttled pull costs seconds, not a rerun.
#
# It only helps where the later step resolves images from the local store:
# `docker compose up` with the default pull_policy (missing), `docker build`
# with the docker driver, and `docker pull` of an image already present. A
# buildx docker-container builder does NOT read the local store; those jobs
# retry the build step itself instead (security.yml, docker-publish.yml).
#
# The job fails, naming the ref, when every attempt fails. A ref that does not
# exist costs MAX_ATTEMPTS attempts before failing; that is the price of not
# trying to classify registry errors.
#
# Guarded by scripts/check-dockerhub-pulls.sh: a CI job that starts stacks,
# runs the integration tests or builds with the docker driver must call this
# script before it does.

set -euo pipefail

MAX_ATTEMPTS="${MAX_ATTEMPTS:-5}"
PULL_SPACING="${PULL_SPACING:-1.1}"
# Upper bound on one `docker pull`, so a black-holed registry costs minutes,
# not the job's whole timeout.
ATTEMPT_TIMEOUT="${ATTEMPT_TIMEOUT:-300}"

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <image-ref>..." >&2
  exit 2
fi

first=1
for ref in "$@"; do
  [ "$first" = 1 ] || sleep "$PULL_SPACING"
  first=0
  attempt=1
  while :; do
    echo "pull-with-retry: $ref (attempt $attempt/$MAX_ATTEMPTS)"
    if timeout "$ATTEMPT_TIMEOUT" docker pull --quiet "$ref"; then
      break
    fi
    if [ "$attempt" -ge "$MAX_ATTEMPTS" ]; then
      echo "::error::pull-with-retry: could not pull $ref after $MAX_ATTEMPTS attempts" >&2
      exit 1
    fi
    sleep $((attempt * 2))
    attempt=$((attempt + 1))
  done
done
