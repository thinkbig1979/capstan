#!/usr/bin/env bash
# scripts/govulncheck-retry.sh <output.json>
#
# Runs the exact govulncheck command security.yml has always run, and retries
# it ONLY when one of its two upstreams stalls (agent-os-pvta): the Go module
# proxy that resolves `@latest`, and vuln.go.dev that serves the database.
# The JSON it writes is consumed unchanged by the "Evaluate findings" step;
# that gate, not this script, decides whether the run is red. Sibling of
# scripts/pnpm-audit-retry.sh (agent-os-r1wl): same shape, different tool.
#
# `@latest` is kept on purpose; the comment above the workflow step says why.
#
# STALL SIGNATURES, every one OBSERVED 2026-09-05 by inducing the failure
# (govulncheck v1.6.0, go1.25.0 / toolchain go1.26.6), verbatim:
#
#   module proxy refused (GOPROXY=http://127.0.0.1:9):
#     go: golang.org/x/vuln/cmd/govulncheck@latest: module golang.org/x/vuln/cmd/govulncheck:
#       Get "http://127.0.0.1:9/golang.org/x/vuln/cmd/govulncheck/@v/list":
#       dial tcp 127.0.0.1:9: connect: connection refused
#   module proxy DNS failure (GOPROXY=https://nonexistent.invalid):
#     ... dial tcp: lookup nonexistent.invalid on 9.9.9.9:53: no such host
#   module proxy answering 502 / 503 / 504 (local stub server):
#     ... reading http://127.0.0.1:18503/golang.org/x/vuln/cmd/govulncheck/@v/list: 503 Service Unavailable
#     (identically shaped for "502 Bad Gateway" and "504 Gateway Timeout")
#   vulnerability DB refused, DNS failure, 502, 503 and 504 (-db http://127.0.0.1:9 etc.)
#   all collapse into ONE line, govulncheck swallows the transport error:
#     creating client: unrecognized vulndb format; see https://go.dev/security/vuln/database#api for accepted schema
#   vulnerability DB unreachable through an HTTPS proxy (HTTPS_PROXY dead,
#   NO_PROXY exempting proxy.golang.org so only the DB side fails) — this is
#   the shape a real vuln.go.dev outage takes when `-db` is untouched:
#     govulncheck: fetching vulnerabilities: Get "https://vuln.go.dev/index/modules.json.gz": proxyconnect tcp: dial tcp 127.0.0.1:9: connect: connection refused
#     exit status 1
#   black-holed upstream (10.255.255.1, either side): no output at all, the
#     process hangs until `timeout` kills it -> exit 124.
#
# NOT a signature: GOVULNDB. govulncheck v1.6.0 ignores that env var
# (OBSERVED: with GOVULNDB=http://127.0.0.1:9 it still reported
# db=https://vuln.go.dev and exited 0); only `-db` switches the database, so
# the stall arm of the acceptance was induced with `-db`.
#
# Anything not listed above — a compile error in ./..., a bad go.mod, a
# message this list has never seen — fails IMMEDIATELY with no retry, so an
# unfamiliar error fails closed instead of being swallowed.
#
# FAILING CLOSED. `govulncheck -format json` exits 0 unconditionally, so a
# finding never reaches this script's retry branch: findings live in the JSON
# and the gate step reads them. A finding therefore costs exactly ONE attempt.
# The output file is only moved into place after an attempt exits 0 with a
# non-empty report, so a killed attempt's partial JSON never reaches the gate.
# This script cannot exit 0 unless a real govulncheck run exited 0 and wrote
# a report. If you change this file, keep that property.

set -euo pipefail

# Sizing. ATTEMPT_TIMEOUT is MEASURED: the CI-shaped command on this repo's
# backend/ took 13.2s and 22.8s with warm caches and 46.6s COLD (fresh
# GOMODCACHE + GOCACHE: toolchain, x/vuln and x/tools downloaded and compiled),
# on a loaded 8-core box, 2026-09-05. 150s is ~3x the cold run, so a slow but
# live upstream still finishes; a black-holed one costs at most 150s per
# attempt. Upstream reachability was measured at the same time: 8 curls each,
# vuln.go.dev/index/db.json 0.056-0.195s, proxy.golang.org x/vuln/@latest
# 0.044-0.096s, so a healthy upstream is nowhere near the cap.
#
# MAX_ATTEMPTS is GUESSED, not measured: neither upstream has ever been
# observed failing in this repo, so there is no stall rate to size against
# (r1wl had a measured ~50%; this has none). 4 attempts keeps the worst case
# (4 x 150s + delays ≈ 10.3 min, and only on a run that was going to fail
# anyway) inside the job's 15-minute timeout-minutes.
MAX_ATTEMPTS="${GOVULNCHECK_MAX_ATTEMPTS:-4}"
ATTEMPT_TIMEOUT="${GOVULNCHECK_ATTEMPT_TIMEOUT:-150}"
RETRY_DELAY="${GOVULNCHECK_RETRY_DELAY:-5}"

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <output.json>   (runs in the current directory, scans ./...)" >&2
  exit 2
fi
OUT="$1"

# Signatures of an upstream stalling, not of a finding or a broken build.
# Kept narrow on purpose: see the header for where each line came from.
is_stall() {
  grep -qE \
    'dial tcp [^ ]*: connect: connection refused|dial tcp: lookup [^ ]* on [^ ]*: no such host|reading https?://[^ ]*: (502 Bad Gateway|503 Service Unavailable|504 Gateway Timeout)|creating client: unrecognized vulndb format' \
    "$1"
}

out_dir="$(dirname "$OUT")"
tmp_out="$(mktemp "${out_dir}/govulncheck.XXXXXX")"
tmp_err="$(mktemp)"
trap 'rm -f "$tmp_out" "$tmp_err"' EXIT

for attempt in $(seq 1 "$MAX_ATTEMPTS"); do
  set +e
  # `-k 10` escalates to SIGKILL if the attempt ignores SIGTERM; govulncheck
  # died promptly on SIGTERM in every black-hole run here, the escalation is
  # for the case nobody has seen (its sibling trivy-db-retry.sh needed it).
  started=$SECONDS
  timeout -k 10 "$ATTEMPT_TIMEOUT" \
    go run golang.org/x/vuln/cmd/govulncheck@latest -format json ./... \
    >"$tmp_out" 2>"$tmp_err"
  status=$?
  elapsed=$((SECONDS - started))
  set -e

  cat "$tmp_err" >&2

  # `timeout` reports 124 when SIGTERM ended the attempt (OBSERVED here on
  # every black-hole run) but 137 (128+KILL) when it had to escalate
  # (OBSERVED on trivy-db-retry.sh's black-hole arm). 137 is also what an
  # out-of-memory kill looks like — govulncheck peaks at ~3.2 GB RSS on this
  # repo — so it only counts as a stall when the attempt ran out its budget.
  timed_out=0
  if [ "$status" -eq 124 ] || { [ "$status" -eq 137 ] && [ "$elapsed" -ge "$ATTEMPT_TIMEOUT" ]; }; then
    timed_out=1
  fi

  if [ "$status" -eq 0 ]; then
    if [ ! -s "$tmp_out" ]; then
      echo "::error::govulncheck exited 0 but wrote an empty report — treating as a broken scan, not a clean one, and failing without retry." >&2
      exit 1
    fi
    mv "$tmp_out" "$OUT"
    [ "$attempt" -gt 1 ] && echo "govulncheck succeeded on attempt ${attempt}/${MAX_ATTEMPTS}."
    exit 0
  fi

  # A killed attempt means an upstream black-holed the connection, which is
  # the case this script exists for.
  if [ "$timed_out" -eq 1 ] || is_stall "$tmp_err"; then
    if [ "$attempt" -lt "$MAX_ATTEMPTS" ]; then
      echo "::notice::govulncheck attempt ${attempt}/${MAX_ATTEMPTS} stalled reaching the module proxy or vuln.go.dev (exit ${status}); retrying in ${RETRY_DELAY}s. See agent-os-pvta."
      sleep "$RETRY_DELAY"
      continue
    fi
    echo "::error::govulncheck could not reach the module proxy or vuln.go.dev after ${MAX_ATTEMPTS} attempts. This is a network failure, not a finding — but the gate fails closed rather than assuming the dependencies are clean. See agent-os-pvta."
    exit "$status"
  fi

  # Not a recognised stall: a build error, a bad module, an unknown message.
  # Fail now; retrying would only delay the same red.
  echo "::error::govulncheck failed with exit ${status} and no network-stall signature — failing without retry."
  exit "$status"
done
