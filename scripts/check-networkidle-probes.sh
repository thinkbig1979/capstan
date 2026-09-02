#!/usr/bin/env bash
# scripts/check-networkidle-probes.sh
#
# ONE rule: the E2E settle helper's copy of the app's WebSocket-invalidation
# debounce still matches the app's.
#
# WHY THAT NUMBER MATTERS. `waitForLoadState('networkidle')` resolves 500ms
# after the last connection closes, but this app debounces its WS-driven
# react-query invalidations by 750ms, in `scheduleInvalidations()` in
# frontend/src/hooks/useStackEvents.ts. 750 > 500, so a probe bounded by
# networkidle stops listening ~250ms before a WS-triggered refetch can even be
# scheduled. OBSERVED 2026-09-01: a baseline probe so bounded reported
# "/api/v1/stacks fires 1x", while a run holding the page 12s measured the same
# 1x AND saw a second fetch land 752-928ms after a container_event frame. That
# count was consistent with both "no bug" and "bug invisible to this
# instrument".
#
# The helper restates the 750 rather than importing it, because it is
# transpiled outside the frontend's tsconfig graph and importing would drag that
# project's path aliases in for one number. A duplicated constant nothing checks
# is rot waiting to happen, so the source value is re-read on every run. The
# source is located by its function NAME and the timeout read from inside it --
# deliberately NOT by grepping the file for a bare `750`, which would match any
# unrelated occurrence and could not fail for the right reason.
#
# WHY THIS SCRIPT NO LONGER SCANS SPECS (2026-09-02). It used to, three
# different ways, and all three were deleted. First a line-proximity heuristic
# ("a bare networkidle wait near a request-counting marker"). Then a "raw
# request listener in a spec" scanner. Then that plus `.route(`. Every one of
# them failed in BOTH directions on a REQUIRED check. The last reddened a
# multi-line template literal of prose, because the string mask is per-line and
# only the block-comment state carried across; reddened a correctly annotated
# stub whose JSDoc pushed the marker past a three-line lookback, telling the
# author to do the thing they had already done; and reddened
# `router.route('/stacks').get(handler)`. Meanwhile it waved through
# `page.on(\n  'request',\n  fn\n)` -- which is exactly what prettier emits once
# the handler grows -- along with `page['on']`, a computed event name, and
# `page.on.bind(page)`.
#
# TypeScript is not line-oriented and the formatter decides where the lines
# fall, so "is there a raw request listener in this file" is not a question a
# line scanner can answer. A leaky backstop on a required check is worse than
# none: it blocks correct code and misses the realistic shapes. The enforcement
# that survived is a RUNTIME guard in the helper itself -- reading a request
# tally throws unless a settle completed after the last request it matched --
# which lives inside the code that is actually counting and therefore has no
# false positives by construction. See
# testing/tests/playwright/helpers/network-settle.ts.
#
# Dependency-free by design: awk only.
#
# Usage:
#   check-networkidle-probes.sh              check the helper against the app
#   check-networkidle-probes.sh --self-test  prove the check still fires, both ways
#
# Exit: 0 clean, 1 drift or an unreadable input, 2 usage error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

HELPER_REL='testing/tests/playwright/helpers/network-settle.ts'
HELPER_CONST='WS_INVALIDATION_DEBOUNCE_MS'
SOURCE_REL='frontend/src/hooks/useStackEvents.ts'
SOURCE_FN='scheduleInvalidations'

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [--self-test]

Fails when $HELPER_CONST in
$HELPER_REL
no longer matches the setTimeout() inside $SOURCE_FN() in
$SOURCE_REL.

The helper restates that number instead of importing it, so it must be updated
by hand whenever the app's debounce changes, and this check is what notices.

--self-test runs the check against fixtures in a temp directory and asserts the
result both ways, so that a check which has silently stopped working is not
mistaken for a clean tree. check-docs.sh runs it before every real check.
USAGE
}

# Read the delay from the LAST `, <digits>)` on a line rather than the first.
#
# WHY LAST: the first is wrong, and reddened the build. A perfectly ordinary
# refactor -- `setTimeout(() => flush(pending, qc, 0), 750)` -- has an earlier
# `, 0)` belonging to the inner call, so a leftmost match read the debounce as
# 0 and accused the author of a drift that did not exist. The delay is always
# the final argument of the setTimeout call, so the last match on the line is
# the one that means anything.
#
# KNOWN LIMIT, stated rather than chased: in the multi-line form the closing
# `}, 750)` is found on the first line that has one, so a NESTED setTimeout
# closing inside the function would be read instead. That shape does not exist
# in the source today, and if it appeared the reading would almost certainly
# differ from the helper's constant and fail LOUDLY, naming both values and
# both files -- actionable, not silent.
AWK_LAST_DELAY='
  function last_delay(s, re,   pos, v, found) {
    found = ""
    pos = 1
    while (match(substr(s, pos), re)) {
      v = substr(s, pos + RSTART - 1, RLENGTH)
      gsub(/[^0-9]/, "", v)
      found = v
      pos = pos + RSTART
    }
    return found
  }
'

read_helper_ms() {
  command awk -v name="$HELPER_CONST" '
    $0 ~ ("(export[ \t]+)?const[ \t]+" name "[ \t]*=") {
      if (match($0, /=[ \t]*[0-9]+/)) {
        print substr($0, RSTART + 1) + 0
        exit
      }
    }
  ' "$1"
}

# Locate the named function, then read the timeout from inside it. Both the
# multi-line form (`setTimeout(() => { ... }, 750)`) and the single-line form
# (`setTimeout(fn, 750)`) are handled; the 60-line bound keeps a renamed or
# deleted function from silently matching a later, unrelated setTimeout.
read_source_ms() {
  command awk -v fn="$SOURCE_FN" "$AWK_LAST_DELAY"'
    !seen && $0 ~ ("(const|let|var|function)[ \t]+" fn "[ \t]*[=(]") { seen = 1; next }
    seen { span++; if (span > 60) exit }
    seen && !intimeout && index($0, "setTimeout(") {
      d = last_delay($0, ",[ \t]*[0-9]+[ \t]*\\)")
      if (d != "") { print d + 0; exit }
      intimeout = 1
      next
    }
    intimeout {
      d = last_delay($0, "\\}[ \t]*,[ \t]*[0-9]+[ \t]*\\)")
      if (d != "") { print d + 0; exit }
    }
  ' "$1"
}

# Every failure path is loud. A drift check that silently passes when it cannot
# find its target is exactly the decorative gate this script was cut down to
# avoid -- and the file it reads is under active edit by other work.
check_drift() {
  local helper_path=$1 source_path=$2 helper_ms source_ms

  if [ ! -r "$helper_path" ]; then
    echo "FAIL: networkidle-probes - $HELPER_REL is missing, but it is what bounds every request count in the E2E suite"
    return 1
  fi
  if [ ! -r "$source_path" ]; then
    echo "FAIL: networkidle-probes - $SOURCE_REL is missing; the debounce the helper copies cannot be verified"
    return 1
  fi

  helper_ms=$(read_helper_ms "$helper_path")
  source_ms=$(read_source_ms "$source_path")

  if [ -z "$helper_ms" ]; then
    echo "FAIL: networkidle-probes - could not read $HELPER_CONST from $HELPER_REL; the drift check cannot run"
    return 1
  fi
  if [ -z "$source_ms" ]; then
    echo "FAIL: networkidle-probes - could not read the setTimeout() inside $SOURCE_FN() in $SOURCE_REL; if that function was renamed or restructured, update SOURCE_FN in $(basename "$0") and $HELPER_REL together"
    return 1
  fi
  if [ "$helper_ms" != "$source_ms" ]; then
    echo "FAIL: networkidle-probes - the E2E helper's debounce constant has drifted from the app"
    echo "    $HELPER_REL: $HELPER_CONST = $helper_ms"
    echo "    $SOURCE_REL: $SOURCE_FN() waits $source_ms"
    echo "The helper restates this number instead of importing it, so it must be updated by hand whenever the app's debounce changes."
    return 1
  fi

  echo "networkidle-probes: debounce constant ${helper_ms}ms matches $SOURCE_FN() in $SOURCE_REL"
  return 0
}

# ---------------------------------------------------------------------------
# --self-test: the check's own regression suite
# ---------------------------------------------------------------------------

# WHY THIS EXISTS. This runs as a REQUIRED status check, and a check that has
# silently stopped working produces output identical to a clean tree. Three
# earlier revisions of this file shipped rules that passed re-execution and
# still failed an adversary; nothing in the repo would have caught any of them.
#
# Every control is TWO-SIDED. Each fixture expecting success has a twin whose
# only difference is the thing being checked, so "expected 0, got 0" cannot be
# explained by the check having read nothing at all.
#
# A control may also demand a MESSAGE, and several must. Exit status alone is
# too coarse to pin a specific failure path: OBSERVED 2026-09-02, deleting the
# readability guard entirely still passed all ten controls, because a missing
# file then falls through to the "could not read the setTimeout()" branch and
# exits 1 for the wrong reason with a message that misdirects the reader. A
# control that its own mutation cannot fail is not a control.
selftest_case() {
  local name=$1 want=$2 helper=$3 source=$4 want_msg=${5:-} out rc
  out=$(check_drift "$helper" "$source" 2>&1)
  rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || command printf '%s\n' "$out" | command grep -qE "$want_msg"; then
      return 0
    fi
    echo "FAIL: networkidle-probes self-test - control '$name' exited $rc as expected but did not explain itself; wanted a message matching /$want_msg/"
  else
    echo "FAIL: networkidle-probes self-test - control '$name' expected exit $want, got $rc"
  fi
  echo "  check said:"
  command printf '%s\n' "$out" | command sed 's/^/    /'
  ST_FAILS=$((ST_FAILS + 1))
}

# Write a fake useStackEvents.ts whose scheduleInvalidations() body is $2.
write_source() {
  command cat > "$ST_DIR/$1.ts" <<SOURCE
import { useCallback, useRef } from 'react'

export function useStackEvents() {
  const timerRef = useRef(null)

  const $SOURCE_FN = useCallback((keys) => {
    clearTimeout(timerRef.current)
$2
  }, [])

  return { $SOURCE_FN }
}
SOURCE
  echo "$ST_DIR/$1.ts"
}

write_helper() {
  command cat > "$ST_DIR/$1.ts" <<HELPER
export const $HELPER_CONST = $2
HELPER
  echo "$ST_DIR/$1.ts"
}

selftest() {
  ST_DIR="$(command mktemp -d)" || {
    echo "FAIL: networkidle-probes self-test - could not create a temp directory"
    exit 2
  }
  trap 'command rm -rf "$ST_DIR"' EXIT
  ST_RUN=0
  ST_FAILS=0

  local h750 h500 hempty
  h750=$(write_helper helper-750 750)
  h500=$(write_helper helper-500 500)
  hempty="$ST_DIR/helper-empty.ts"
  echo 'export const SOMETHING_ELSE = 1' > "$hempty"

  local multiline single innerzero innerzero_mut renamed
  multiline=$(write_source src-multiline '    timerRef.current = setTimeout(() => {
      keys.forEach((k) => k)
    }, 750)')
  single=$(write_source src-single '    timerRef.current = setTimeout(flush, 750)')

  # Finding 10, the regression this anchor fix exists for: an inner call whose
  # own last argument is a number, ahead of the real delay on the same line.
  innerzero=$(write_source src-innerzero '    timerRef.current = setTimeout(() => flush(pending, qc, 0), 750)')
  innerzero_mut=$(write_source src-innerzero-mut '    timerRef.current = setTimeout(() => flush(pending, qc, 0), 1200)')

  renamed=$(write_source src-renamed '    timerRef.current = setTimeout(() => {
      keys.forEach((k) => k)
    }, 750)')
  command sed -i "s/$SOURCE_FN/queueInvalidations/g" "$renamed"

  # --- the check agrees, three call shapes, each with a drifted twin
  selftest_case multiline-match      0 "$h750" "$multiline"     'debounce constant 750ms matches'
  selftest_case multiline-drifted    1 "$h500" "$multiline"     'has drifted from the app'
  selftest_case single-line-match    0 "$h750" "$single"        'debounce constant 750ms matches'
  selftest_case single-line-drifted  1 "$h500" "$single"        'has drifted from the app'
  selftest_case inner-numeric-arg    0 "$h750" "$innerzero"     'debounce constant 750ms matches'
  selftest_case inner-numeric-arg-mut 1 "$h750" "$innerzero_mut" 'waits 1200'

  # --- every way the check can lose its footing must be loud, never a pass,
  # --- and must say WHICH footing it lost: see the note on selftest_case
  selftest_case fn-renamed           1 "$h750" "$renamed"       'could not read the setTimeout'
  selftest_case helper-const-missing 1 "$hempty" "$multiline"   'could not read WS_INVALIDATION_DEBOUNCE_MS'
  selftest_case source-missing       1 "$h750" "$ST_DIR/does-not-exist.ts"      'is missing; the debounce the helper copies cannot be verified'
  selftest_case helper-missing       1 "$ST_DIR/does-not-exist.ts" "$multiline" 'is missing, but it is what bounds every request count'

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "FAIL: networkidle-probes self-test - $ST_FAILS of $ST_RUN control(s) failed; the drift check does not behave as documented"
    return 1
  fi
  echo "networkidle-probes self-test: $ST_RUN control(s) passed, each proven both ways"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') ;;
  *)
    echo "ERROR: unknown argument '$1'; this check takes no file paths since the spec scanner was removed" >&2
    usage
    exit 2
    ;;
esac

check_drift "$REPO_ROOT/$HELPER_REL" "$REPO_ROOT/$SOURCE_REL"
