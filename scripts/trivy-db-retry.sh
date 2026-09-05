#!/usr/bin/env bash
# scripts/trivy-db-retry.sh
#
# Pre-downloads Trivy's vulnerability database into TRIVY_CACHE_DIR, retrying
# ONLY when the OCI registry serving it stalls (agent-os-pvta). It runs
# BEFORE the scan step, and the scan step runs with TRIVY_SKIP_DB_UPDATE=true,
# so the scan itself never touches the network: either this script put a DB
# in the cache dir, or the job failed here with a message that says so.
# Sibling of scripts/pnpm-audit-retry.sh (agent-os-r1wl): same shape.
#
# WHY A SEPARATE STEP AND NOT A WRAPPER: the scan is an `aquasecurity/trivy-action`
# `uses:` step, which cannot be wrapped in `timeout` or a loop. The action's
# own internal `actions/cache` restore of the cache dir is disabled on the scan
# step (`cache: 'false'`) because it runs AFTER this script and its
# `restore-keys: cache-trivy-` fallback would overwrite the fresh DB with an
# older day's copy. The cost is one DB download per run: MEASURED 2026-09-05,
# 110.94 MiB in 14.87s from mirror.gcr.io on the dev box.
#
# trivy v0.70.0's default `--db-repository` is already a two-entry fallback,
# `mirror.gcr.io/aquasec/trivy-db:2,ghcr.io/aquasecurity/trivy-db:2` (OBSERVED
# in `trivy image --help`), so a single registry outage is survived by trivy
# itself; this script covers both being unreachable and the black-hole case
# trivy's fallback does not time out of.
#
# STALL SIGNATURES, every one OBSERVED 2026-09-05 by running
# `docker run aquasec/trivy:0.70.0 image --download-db-only --db-repository <x>`
# against an induced failure (verbatim tails of the FATAL line):
#
#   refused (127.0.0.1:9):
#     Get "https://127.0.0.1:9/v2/": dial tcp 127.0.0.1:9: connect: connection refused
#   DNS failure (nonexistent.invalid):
#     Get "https://nonexistent.invalid/v2/": dial tcp: lookup nonexistent.invalid on 9.9.9.9:53: no such host
#   registry answering 502 / 503 / 504 (local stub server):
#     GET http://127.0.0.1:18503/v2/: unexpected status code 503 Service Unavailable
#     (identically shaped for "502 Bad Gateway" and "504 Gateway Timeout")
#   black-holed registry (10.255.255.1): the process hangs; trivy's own dial
#     gave up after ~6 minutes with
#     dial tcp 10.255.255.1:9: connect: connection timed out
#     which is far past this script's budget, so `timeout` kills it: 124 if
#     SIGTERM was enough, 137 after the SIGKILL escalation (see the notes at
#     the call site; both were OBSERVED).
#
# Anything not listed above — a permission error on the cache dir, a bad
# flag, a message this list has never seen — fails IMMEDIATELY with no retry.
#
# FAILING CLOSED. Findings never pass through this script (it downloads, it
# does not scan), so there is nothing here a retry could mask; it still
# cannot exit 0 unless a real `trivy image --download-db-only` exited 0.
# If you change this file, keep that property.

set -euo pipefail

# Sizing. ATTEMPT_TIMEOUT is MEASURED: 110.94 MiB in 14.87s here, and
# registry reachability at 0.164-0.180s per request (8 curls to ghcr.io/v2/).
# 120s is ~8x the measured download, so a slow but live registry finishes; a
# black-holed one costs at most 120s per attempt.
#
# MAX_ATTEMPTS is GUESSED, not measured: this registry has never been observed
# failing in this repo, so there is no stall rate to size against. 4 attempts
# keeps the worst case (4 x 120s + delays ≈ 8.3 min, only on a run that was
# going to fail anyway) well inside the job's 30-minute timeout-minutes.
MAX_ATTEMPTS="${TRIVY_DB_MAX_ATTEMPTS:-4}"
ATTEMPT_TIMEOUT="${TRIVY_DB_ATTEMPT_TIMEOUT:-120}"
RETRY_DELAY="${TRIVY_DB_RETRY_DELAY:-5}"

# Signatures of the registry stalling. Kept narrow on purpose: see the header.
is_stall() {
  grep -qE \
    'dial tcp [^ ]*: connect: connection refused|dial tcp: lookup [^ ]* on [^ ]*: no such host|unexpected status code (502 Bad Gateway|503 Service Unavailable|504 Gateway Timeout)|connect: connection timed out' \
    "$1"
}

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
  set +e
  # `-k 10`: OBSERVED 2026-09-05 on the black-hole arm, trivy answers SIGTERM
  # with "Received signal, attempting graceful shutdown..." and then keeps
  # waiting on the dial for ~6 more minutes; without the escalation to
  # SIGKILL an attempt never ends and the retry never runs.
  started=$SECONDS
  timeout -k 10 "$ATTEMPT_TIMEOUT" trivy image --download-db-only >"$out" 2>&1
  status=$?
  elapsed=$((SECONDS - started))
  set -e

  cat "$out"

  # `timeout` reports 124 when SIGTERM ended the attempt, but 137 (128+KILL)
  # when it had to escalate — OBSERVED on the same black-hole arm, where the
  # first version of this script read 137 as "not a stall" and failed without
  # retrying. 137 is also what an out-of-memory kill looks like, so it only
  # counts as a stall when the attempt actually ran out its budget.
  timed_out=0
  if [ "$status" -eq 124 ] || { [ "$status" -eq 137 ] && [ "$elapsed" -ge "$ATTEMPT_TIMEOUT" ]; }; then
    timed_out=1
  fi

  if [ "$status" -eq 0 ]; then
    [ "$attempt" -gt 1 ] && echo "Trivy DB download succeeded on attempt ${attempt}/${MAX_ATTEMPTS}."
    exit 0
  fi

  if [ "$timed_out" -eq 1 ] || is_stall "$out"; then
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then
      echo "::notice::Trivy DB download attempt ${attempt}/${MAX_ATTEMPTS} stalled reaching the registry (exit ${status}); retrying in ${RETRY_DELAY}s. See agent-os-pvta."
      sleep "$RETRY_DELAY"
      continue
    fi
    echo "::error::Trivy could not download its vulnerability DB after ${MAX_ATTEMPTS} attempts. This is a network failure, not a scan result — the job fails closed rather than scanning against a stale or missing DB. See agent-os-pvta."
    exit "$status"
  fi

  echo "::error::Trivy DB download failed with exit ${status} and no network-stall signature — failing without retry."
  exit "$status"
done
