#!/usr/bin/env bash
# scripts/check-networkidle-probes.sh
#
# Two rules, both about the same 750ms:
#
#   1. A request-counting assertion in testing/ must not bound its measurement
#      window with a bare `waitForLoadState('networkidle')`.
#   2. The debounce constant the E2E helper copies must still match the one the
#      app actually uses.
#
# WHY: networkidle resolves 500ms after the last connection closes, but this
# app debounces its WebSocket-driven react-query invalidations by 750ms
# (`scheduleInvalidations()` in frontend/src/hooks/useStackEvents.ts).
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
# Rule 2 exists because that helper restates the 750 rather than importing it
# (it is transpiled outside the frontend's tsconfig graph). A duplicated
# constant nothing checks is rot waiting to happen, so the source value is
# re-read from `scheduleInvalidations()` on every run. The source is located by
# its function name and the timeout read from inside it -- deliberately NOT by
# grepping the file for a bare `750`, which would match any unrelated
# occurrence and could not fail for the right reason.
#
# Rule 1 is scoped to counting sites on purpose. Waiting for RENDER with
# networkidle is fine and is what every current call site in this suite does,
# so a blanket ban would be noise the next author routes around. A violation is
# a networkidle wait sitting within WINDOW lines of a request-counting marker.
#
# Comments are excluded from BOTH the marker scan and the violation scan. A
# comment cannot be a request-counting assertion, and reporting one is worse
# than useless: there is no remedy an author could apply, since you cannot call
# waitForInvalidationSettle() from a comment. Found 2026-09-02 by review, after
# the first version flagged the very note that explains this rule.
#
# Opt out with the token `networkidle-ok:` plus a reason, on the offending line
# or in the three lines above it, when a render-wait genuinely sits next to a
# counter. The reason is required -- a bare token is not an opt-out.
#
# KNOWN LIMIT (not tested -- inferred from the matched forms): detection is
# line-based, so a `waitForLoadState(` split across lines is not seen. Every
# current call site is single-line (e.g. backup-flow.spec.ts:83).
#
# Dependency-free by design: git, grep, sort and awk only.
#
# Usage:
#   check-networkidle-probes.sh              scan every tracked testing/ .ts file
#   check-networkidle-probes.sh FILE...      scan exactly these paths
#
# The debounce-drift check is repo-global and runs in both modes.
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

HELPER_REL='testing/tests/playwright/helpers/network-settle.ts'
HELPER_CONST='WS_INVALIDATION_DEBOUNCE_MS'
SOURCE_REL='frontend/src/hooks/useStackEvents.ts'
SOURCE_FN='scheduleInvalidations'

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [FILE...]

With no arguments, every tracked TypeScript file under testing/ is scanned.
With arguments, exactly those paths are scanned.

Fails when a bare waitForLoadState('networkidle') sits within $WINDOW lines of a
request-counting marker (page.on('request'/'response'), waitForResponse(), or a
count/tally/requests identifier being incremented). Use
waitForInvalidationSettle() from $HELPER_REL,
or annotate the line with "networkidle-ok: <reason>".

Also fails when $HELPER_CONST in that helper no longer
matches the setTimeout() inside $SOURCE_FN() in $SOURCE_REL.
USAGE
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
esac

# ---------------------------------------------------------------------------
# rule 2: the helper's copy of the debounce still matches the app's
# ---------------------------------------------------------------------------

# Every failure path below is loud. A drift check that silently passes when it
# cannot find its target is exactly the decorative gate this script exists to
# avoid -- and the file it reads is under active edit by other work.
helper_path="$REPO_ROOT/$HELPER_REL"
source_path="$REPO_ROOT/$SOURCE_REL"

if [ ! -r "$helper_path" ]; then
  echo "FAIL: networkidle-probes - $HELPER_REL is missing, but this gate tells authors to use waitForInvalidationSettle() from it"
  exit 1
fi
if [ ! -r "$source_path" ]; then
  echo "FAIL: networkidle-probes - $SOURCE_REL is missing; the debounce the helper copies cannot be verified"
  exit 1
fi

helper_ms=$(command awk -v name="$HELPER_CONST" '
  $0 ~ ("(export[ \t]+)?const[ \t]+" name "[ \t]*=") {
    if (match($0, /=[ \t]*[0-9]+/)) {
      print substr($0, RSTART + 1) + 0
      exit
    }
  }
' "$helper_path")

# Locate the named function, then read the timeout from inside it. Both the
# multi-line form (`setTimeout(() => { ... }, 750)`) and the single-line form
# (`setTimeout(fn, 750)`) are handled; the 60-line bound keeps a renamed or
# deleted function from silently matching a later, unrelated setTimeout.
source_ms=$(command awk -v fn="$SOURCE_FN" '
  !seen && $0 ~ ("(const|let|var|function)[ \t]+" fn "[ \t]*[=(]") { seen = 1; next }
  seen { span++; if (span > 60) exit }
  seen && !intimeout && index($0, "setTimeout(") {
    if (match($0, /,[ \t]*[0-9]+[ \t]*\)/)) {
      v = substr($0, RSTART, RLENGTH)
      gsub(/[^0-9]/, "", v)
      print v + 0
      exit
    }
    intimeout = 1
    next
  }
  intimeout && match($0, /\}[ \t]*,[ \t]*[0-9]+[ \t]*\)/) {
    v = substr($0, RSTART, RLENGTH)
    gsub(/[^0-9]/, "", v)
    print v + 0
    exit
  }
' "$source_path")

if [ -z "$helper_ms" ]; then
  echo "FAIL: networkidle-probes - could not read $HELPER_CONST from $HELPER_REL; the drift check cannot run"
  exit 1
fi
if [ -z "$source_ms" ]; then
  echo "FAIL: networkidle-probes - could not read the setTimeout() inside $SOURCE_FN() in $SOURCE_REL; if that function was renamed or restructured, update SOURCE_FN in $(basename "$0") and $HELPER_REL together"
  exit 1
fi
if [ "$helper_ms" != "$source_ms" ]; then
  echo "FAIL: networkidle-probes - the E2E helper's debounce constant has drifted from the app"
  echo "    $HELPER_REL: $HELPER_CONST = $helper_ms"
  echo "    $SOURCE_REL: $SOURCE_FN() waits $source_ms"
  echo "The helper restates this number instead of importing it, so it must be updated by hand whenever the app's debounce changes."
  exit 1
fi

drift_note="debounce constant ${helper_ms}ms matches $SOURCE_FN() in $SOURCE_REL"

# ---------------------------------------------------------------------------
# rule 1: no request-counting assertion bounded by bare networkidle
# ---------------------------------------------------------------------------

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
  echo "networkidle-probes: no files to scan; $drift_note"
  exit 0
fi

# Whole-file buffering, because a violation is defined by lines on BOTH sides
# of the networkidle wait: the marker may precede it or follow it. C[] holds
# each line with comments removed, and is what the two detectors read; L[]
# holds the raw line, and is what the opt-out annotation is read from.
command awk -v WINDOW="$WINDOW" -v DRIFT="$drift_note" '
  BEGIN { SQ = sprintf("%c", 39); DQ = sprintf("%c", 34); BT = sprintf("%c", 96) }

  # Strip // and /* */ comments, preserving string literals so that neither a
  # URL containing "//" truncates a line nor a commented-out example counts as
  # code. BLOCKSTATE carries the open-block-comment state to the next line.
  function strip_comments(s, inblock,   out, i, len, c, c2, q) {
    out = ""
    i = 1
    len = length(s)
    while (i <= len) {
      c = substr(s, i, 1)
      if (inblock) {
        if (c == "*" && substr(s, i + 1, 1) == "/") { inblock = 0; i += 2; continue }
        i++
        continue
      }
      if (c == "/") {
        c2 = substr(s, i + 1, 1)
        if (c2 == "/") break
        if (c2 == "*") { inblock = 1; i += 2; continue }
        out = out c
        i++
        continue
      }
      if (c == SQ || c == DQ || c == BT) {
        q = c
        out = out c
        i++
        while (i <= len) {
          c = substr(s, i, 1)
          out = out c
          i++
          if (c == "\\") {
            if (i <= len) { out = out substr(s, i, 1); i++ }
            continue
          }
          if (c == q) break
        }
        continue
      }
      out = out c
      i++
    }
    BLOCKSTATE = inblock
    return out
  }

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
      if (tolower(C[j]) ~ /await[ \t]+[a-z0-9_$.]*waitforinvalidationsettle[ \t]*\(/) return 1
    }
    return 0
  }
  function nearest_marker(i,   j) {
    for (j = (i - WINDOW < 1 ? 1 : i - WINDOW); j <= i + WINDOW && j <= n; j++) {
      if (j != i && is_marker(C[j])) return j
    }
    return 0
  }
  function process(f,   i, m) {
    scanned++
    BLOCKSTATE = 0
    for (i = 1; i <= n; i++) C[i] = strip_comments(L[i], BLOCKSTATE)
    for (i = 1; i <= n; i++) {
      if (!is_idle(C[i])) continue
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
      printf "FAIL: networkidle-probes - %d site(s); networkidle resolves at 500ms but the WS-invalidation debounce is longer, so the refetch lands outside the window\n", bad
      printf "Use waitForInvalidationSettle() from testing/tests/playwright/helpers/network-settle.ts, or annotate with \"networkidle-ok: <reason>\".\n"
      exit 1
    }
    printf "networkidle-probes: %d file(s) scanned, no request-counting assertion relies on bare networkidle; %s\n", scanned, DRIFT
  }
' "${readable[@]}"
