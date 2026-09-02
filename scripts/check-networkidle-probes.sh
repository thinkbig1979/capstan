#!/usr/bin/env bash
# scripts/check-networkidle-probes.sh
#
# Fails when a request-counting assertion in testing/ bounds its measurement
# window with a bare `waitForLoadState('networkidle')`.
#
# WHY: networkidle resolves 500ms after the last connection closes, but this
# app debounces its WebSocket-driven react-query invalidations by 750ms
# (`scheduleInvalidations()` in frontend/src/hooks/useStackEvents.ts:130).
# 750 > 500, so a probe bounded by networkidle stops listening ~250ms before a
# WS-triggered refetch can be scheduled. OBSERVED 2026-09-01: a baseline probe
# so bounded reported "/api/v1/stacks fires 1x", while a run holding the page
# for 12s measured the same 1x AND saw a second fetch land 752-928ms after a
# container_event frame. The count was consistent with both "no bug" and "bug
# invisible to this instrument". Nothing else in CI can see that difference:
# the test passes either way, it just measures the wrong window.
#
# The correct wait is testing/tests/playwright/helpers/network-settle.ts,
# `waitForInvalidationSettle()`, which holds past the debounce.
#
# Scoped to counting sites on purpose. Waiting for RENDER with networkidle is
# fine and is what every current call site in this suite does, so a blanket ban
# would be noise the next author routes around. A violation is a networkidle
# wait sitting within WINDOW lines of a request-counting marker.
#
# Opt out with the token `networkidle-ok:` plus a reason, on the offending line
# or in the three lines above it, when a render-wait genuinely sits next to a
# counter. The reason is required — a bare token is not an opt-out.
#
# KNOWN LIMIT (not tested — inferred from the matched forms): detection is
# line-based, so a `waitForLoadState(` split across lines is not seen. Every
# current call site is single-line (e.g. backup-flow.spec.ts:75).
#
# Dependency-free by design: git, grep, sort and awk only.
#
# Usage:
#   check-networkidle-probes.sh              scan every tracked testing/ .ts file
#   check-networkidle-probes.sh FILE...      scan exactly these paths
#
# Exit: 0 clean, 1 violations found, 2 usage/environment error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# How far a networkidle wait may sit from a counting marker and still be
# considered part of the same measurement. Wide enough to span a helper
# function and its callers' setup; narrow enough that unrelated tests in the
# same file don't cross-contaminate.
WINDOW="${NETWORKIDLE_PROBE_WINDOW:-40}"

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [FILE...]

With no arguments, every tracked TypeScript file under testing/ is scanned.
With arguments, exactly those paths are scanned.

Fails when a bare waitForLoadState('networkidle') sits within $WINDOW lines of a
request-counting marker (page.on('request'/'response'), waitForResponse(), or a
count/tally/requests identifier being incremented). Use
waitForInvalidationSettle() from testing/tests/playwright/helpers/network-settle.ts,
or annotate the line with "networkidle-ok: <reason>".
USAGE
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
esac

# `git ls-files` rather than `find`: find sweeps node_modules and other
# untracked vendored trees, and an untracked file is not something this gate
# should redden the build over. A brand-new spec must be `git add`ed to be
# seen, same as scripts/check-line-continuation.sh.
tracked_files() {
  command git -C "$REPO_ROOT" ls-files -- testing | command grep -E '\.(ts|tsx)$' | command sort -u
}

files=()
if [ "$#" -gt 0 ]; then
  files=("$@")
else
  if ! command git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
    echo "ERROR: $REPO_ROOT is not a git repository; pass file paths explicitly" >&2
    exit 2
  fi
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    files+=("$REPO_ROOT/$f")
  done < <(tracked_files)
fi

readable=()
for f in "${files[@]:-}"; do
  [ -z "$f" ] && continue
  if [ -r "$f" ] && [ -f "$f" ]; then
    readable+=("$f")
  else
    echo "SKIP: $f (not a readable regular file)" >&2
  fi
done

if [ "${#readable[@]}" -eq 0 ]; then
  echo "networkidle-probes: no files to scan"
  exit 0
fi

# Whole-file buffering, because a violation is defined by lines on BOTH sides
# of the networkidle wait: the marker may precede it or follow it.
command awk -v WINDOW="$WINDOW" '
  function is_marker(l,   low) {
    low = tolower(l)
    if (low ~ /\.on\([ \t]*["'"'"'](request|response)/) return 1
    if (low ~ /waitforresponse[ \t]*\(/) return 1
    if (low ~ /[a-z0-9_$]*(count|tally|requests)[a-z0-9_$]*[ \t]*(\+\+|\+=)/) return 1
    return 0
  }
  function is_idle(l,   low) {
    low = tolower(l)
    return low ~ /waitforloadstate[ \t]*\([ \t]*["'"'"'`]networkidle/
  }
  function annotated(i,   j) {
    for (j = i; j >= 1 && j >= i - 3; j--) {
      if (tolower(L[j]) ~ /networkidle-ok:[ \t]*[^ \t]/) return 1
    }
    return 0
  }
  function settled(i,   j) {
    for (j = (i - 3 < 1 ? 1 : i - 3); j <= i + 3 && j <= n; j++) {
      if (tolower(L[j]) ~ /await[ \t]+[a-z0-9_$.]*waitforinvalidationsettle[ \t]*\(/) return 1
    }
    return 0
  }
  function nearest_marker(i,   j) {
    for (j = (i - WINDOW < 1 ? 1 : i - WINDOW); j <= i + WINDOW && j <= n; j++) {
      if (j != i && is_marker(L[j])) return j
    }
    return 0
  }
  function process(f,   i, m) {
    scanned++
    for (i = 1; i <= n; i++) {
      if (!is_idle(L[i])) continue
      if (annotated(i) || settled(i)) continue
      m = nearest_marker(i)
      if (m == 0) continue
      printf "%s:%d: request-counting assertion bounded by bare networkidle (counting marker on line %d)\n", f, i, m
      printf "    %d: %s\n", m, L[m]
      printf "    %d: %s\n", i, L[i]
      bad++
    }
  }
  FNR == 1 {
    if (n > 0) process(prevfile)
    n = 0
    prevfile = FILENAME
  }
  { n++; L[n] = $0 }
  END {
    if (n > 0) process(prevfile)
    if (bad > 0) {
      printf "FAIL: networkidle-probes - %d site(s); networkidle resolves at 500ms but the WS-invalidation debounce is 750ms, so the refetch lands outside the window\n", bad
      printf "Use waitForInvalidationSettle() from testing/tests/playwright/helpers/network-settle.ts, or annotate with \"networkidle-ok: <reason>\".\n"
      exit 1
    }
    printf "networkidle-probes: %d file(s) scanned, no request-counting assertion relies on bare networkidle\n", scanned
  }
' "${readable[@]}"
