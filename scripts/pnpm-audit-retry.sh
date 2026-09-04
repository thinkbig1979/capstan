#!/usr/bin/env bash
# scripts/pnpm-audit-retry.sh
#
# Runs `pnpm audit` and retries ONLY when the npm advisories endpoint stalls
# (agent-os-r1wl). It exists because that endpoint fails roughly half of all
# requests, and the failure is a total stall rather than a slow answer.
#
# MEASURED 2026-09-04, 8 attempts from each of two machines, curl --max-time 120,
# against POST https://registry.npmjs.org/-/npm/v1/security/advisories/bulk:
#
#   dev workstation  4/8 succeeded   failures: 0 bytes received, ever
#   CI host pt       4/8 succeeded   failures: 0 bytes received, ever
#   successes returned in 0.45-1.6s (two stragglers at 9.6s and 59.8s)
#
# So it is not machine-specific, not payload-size-specific, and not "slow" —
# a request either gets a fast answer or is black-holed. pnpm makes 3 attempts
# at ~60s each, so a job fails when all three land on stalled paths: 0.5^3 =
# 12.5%, which matches the observed job failure rate and the ~4.5 minute red.
#
# Why the retry cannot live in pnpm config: pnpm 11.5.1 IGNORES fetch-retries
# and fetch-timeout from both env vars and .npmrc. OBSERVED — with
# npm_config_fetch_retries=5 it still prints "2 retries left" (the default),
# and with those keys in .npmrc `pnpm config get fetch-retries` returns
# `undefined`. A config-based fix here would be completely inert while every
# gate stayed green, so the retry has to be in the step we control.
#
# FAILING CLOSED IS THE WHOLE DESIGN. `pnpm audit` exits non-zero for BOTH a
# network stall and a genuine advisory, so retrying on a bare exit code would
# be indistinguishable from retrying a real finding. This script therefore
# decides on the OUTPUT:
#
#   * pnpm exits 0                  -> pass immediately.
#   * output matches a stall        -> retry, up to MAX_ATTEMPTS.
#   * anything else (a real finding
#     or any unrecognised error)    -> fail IMMEDIATELY, no retry.
#
# It can never exit 0 unless a real `pnpm audit` run exited 0. Retrying only
# changes how many chances a stalled network gets; it cannot turn a red audit
# green. If you change this file, keep that property.

set -euo pipefail

MAX_ATTEMPTS="${AUDIT_MAX_ATTEMPTS:-10}"
# Sizing, from MEASURED response times rather than a guess. Successful calls
# have returned in 0.45s, 0.57s, 1.09s, 1.55s, 8.5s, 18.9s and 59.8s — mostly
# sub-2s with a long tail. 35s keeps the 18.9s-class stragglers (a shorter cap
# discards real answers) while not spending a full minute on a stall, which
# never answers however long you wait. With ~50% of calls stalling, 10 attempts
# puts total failure at 0.5^10 ≈ 0.1%, against 12.5% for pnpm's own 3.
# Worst case is ~6.3 minutes, and only on a run that was going to fail anyway.
ATTEMPT_TIMEOUT="${AUDIT_ATTEMPT_TIMEOUT:-35}"
RETRY_DELAY="${AUDIT_RETRY_DELAY:-3}"

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <pnpm audit args...>" >&2
  exit 2
fi

# Signatures of the endpoint stalling, not of an audit finding. Kept narrow on
# purpose: anything not listed here is treated as a real failure and is NOT
# retried, so an unfamiliar error fails closed rather than being swallowed.
is_stall() {
  grep -qE \
    'The operation was aborted due to timeout|ERR_PNPM_FETCH|error \(23\)|ETIMEDOUT|ECONNRESET|socket hang up|request to .* failed' \
    "$1"
}

out="$(mktemp)"
trap 'rm -f "$out"' EXIT

for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
  set +e
  timeout "$ATTEMPT_TIMEOUT" pnpm audit "$@" >"$out" 2>&1
  status=$?
  set -e

  cat "$out"

  if [ "$status" -eq 0 ]; then
    [ "$attempt" -gt 1 ] && echo "pnpm audit succeeded on attempt ${attempt}/${MAX_ATTEMPTS}."
    exit 0
  fi

  # `timeout` reports 124 when it kills the attempt: the endpoint stalled past
  # our own budget, which is the case this script exists for.
  if [ "$status" -eq 124 ] || is_stall "$out"; then
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then
      echo "::notice::pnpm audit attempt ${attempt}/${MAX_ATTEMPTS} stalled reaching the npm advisories endpoint (exit ${status}); retrying in ${RETRY_DELAY}s. See agent-os-r1wl."
      sleep "$RETRY_DELAY"
      continue
    fi
    echo "::error::pnpm audit could not reach the npm advisories endpoint after ${MAX_ATTEMPTS} attempts. This is a network failure, not an audit finding — but the gate fails closed rather than assuming the dependencies are clean. See agent-os-r1wl."
    exit "$status"
  fi

  # Not a recognised stall: a real advisory, a malformed lockfile, a bad flag.
  # Fail now. Retrying a genuine finding would only delay the same red.
  echo "::error::pnpm audit failed with exit ${status} and no network-stall signature — treating as a real finding and failing without retry."
  exit "$status"
done
