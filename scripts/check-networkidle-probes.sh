#!/usr/bin/env bash
# scripts/check-networkidle-probes.sh
#
# Two rules, both about the same 750ms:
#
#   1. Request counting in testing/ goes through the sanctioned helper, so that
#      the helper's runtime guard can enforce the measurement window.
#   2. The debounce constant that helper copies still matches the app's.
#
# WHY: `waitForLoadState('networkidle')` resolves 500ms after the last
# connection closes, but this app debounces its WebSocket-driven react-query
# invalidations by 750ms, in `scheduleInvalidations()` in
# frontend/src/hooks/useStackEvents.ts. 750 > 500, so a probe bounded by
# networkidle stops listening ~250ms before a WS-triggered refetch can be
# scheduled. OBSERVED 2026-09-01: a baseline probe so bounded reported
# "/api/v1/stacks fires 1x", while a run holding the page for 12s measured the
# same 1x AND saw a second fetch land 752-928ms after a container_event frame.
# The count was consistent with both "no bug" and "bug invisible to this
# instrument".
#
# WHY THIS SCRIPT DOES NOT MENTION networkidle (2026-09-02). It used to: it
# looked for a bare networkidle wait sitting near a "request-counting marker".
# That heuristic was removed after review measured it failing in both
# directions at once. False positives: one ordinary `await
# page.waitForResponse(...)` — a WAIT, not a counter — produced 6 violations,
# five on pre-existing untouched lines, and `attemptCount++` in a retry loop
# fired it too. False negative: a spec that called the counting helper, bounded
# it with a bare networkidle and asserted on the tally exited 0, because the
# listener lives in another file and a per-file line scanner cannot see it.
# A required gate that reddens correct code and passes the exact bug it was
# built for is worse than no gate. "Is this wait bounding that count" is not a
# question a line scanner can answer; "is there a raw request listener in a
# spec" is. So that is the question asked here, and the real enforcement lives
# in the helper at runtime, where it cannot be evaded by naming or formatting:
# reading a tally's `count` throws unless the page was settled first.
#
# Rule 2 exists because the helper restates the 750 rather than importing it
# (it is transpiled outside the frontend's tsconfig graph). A duplicated
# constant nothing checks is rot waiting to happen, so the source value is
# re-read on every run. The source is located by its function NAME and the
# timeout read from inside it -- deliberately NOT by grepping the file for a
# bare `750`, which would match any unrelated occurrence and could not fail for
# the right reason.
#
# Comments are excluded, so a commented-out listener does not redden the build.
#
# `.route()` came INTO scope 2026-09-02, matched on the METHOD and not the
# receiver. The only site in this repo is `sharedContext.route(...)`, so a
# `page.`-anchored pattern would have missed it, and a route handler is a
# measured evasion of layer 1: `page.route('**/api/v1/stacks', r => { hits++ })`
# counts requests without ever constructing a tally, so nothing at runtime can
# refuse to hand back the number. Unlike the deleted proximity rule this fires
# only on a line that genuinely installs interception -- never on a comment, a
# `waitForResponse` wait or a retry counter -- and its cost is one annotated
# line per deliberate stub, which is bounded and self-documenting.
#
# DELIBERATELY NOT MATCHED, decided 2026-09-02: `.routeFromHAR(` and
# `.routeWebSocket(`. Both are interception, and `/\.route\(/` does not match
# either -- it requires the `(` immediately after `route`. That is a judgement,
# not an oversight. Neither is a plausible way to reach for an HTTP request
# counter: routeFromHAR replays a recorded archive and routeWebSocket intercepts
# WS frames rather than requests, so adding them buys no coverage of the evasion
# path this rule closes while widening a REQUIRED gate onto rare-but-innocent
# APIs -- which is the exact failure the redesign removed. Revisit only if
# someone actually counts with one.
#
# KNOWN GAP (by design): counting by repeated `waitForResponse()` calls is not
# caught here. Keeping ordinary waits unflagged is the whole point of the
# rewrite; the same gap is restated in the helper's docblock, which is where
# that trade is actually made and where the next author will be reading.
#
# Dependency-free by design: git, grep, sort and awk only.
#
# Usage:
#   check-networkidle-probes.sh              scan every tracked testing/ .ts file
#   check-networkidle-probes.sh FILE...      scan exactly these paths
#   check-networkidle-probes.sh --self-test  prove rule 1 still fires, both ways
#
# The debounce-drift check is repo-global and runs in both modes.
#
# Exit: 0 clean, 1 violations found, 2 usage/environment error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SELF="$SCRIPT_DIR/$(basename "${BASH_SOURCE[0]}")"

HELPER_REL='testing/tests/playwright/helpers/network-settle.ts'
HELPER_CONST='WS_INVALIDATION_DEBOUNCE_MS'
SOURCE_REL='frontend/src/hooks/useStackEvents.ts'
SOURCE_FN='scheduleInvalidations'

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [FILE...|--self-test]

With no arguments, every tracked TypeScript file under testing/ is scanned,
except the helpers/ directory that implements the sanctioned counting API.
With arguments, exactly those paths are scanned.

--self-test runs this rule against fixtures in a temp directory and asserts the
exit code both ways, so that a rule which has silently stopped firing is not
mistaken for a clean tree. check-docs.sh runs it before every scan.

Fails when a spec intercepts requests itself -- a raw listener (.on('request'),
'response', 'requestfinished', 'requestfailed') or a .route() handler --
instead of using countMatchingRequests() from $HELPER_REL,
whose tally refuses to be read until the page has been settled. Annotate a
deliberate exception with "request-listener-ok: <reason>".

Also fails when $HELPER_CONST in that helper no longer
matches the setTimeout() inside $SOURCE_FN() in $SOURCE_REL.
USAGE
}

# ---------------------------------------------------------------------------
# --self-test: the gate's own regression suite
# ---------------------------------------------------------------------------

# WHY THIS EXISTS. This script is a REQUIRED status check with, until now, no
# protection against its own regression: someone editing the mask, the `.route(`
# walk or annotated()'s lookback had nothing to tell them they had broken it.
# Two earlier revisions of this rule passed re-execution and still failed an
# adversary, and nothing in the repo would have caught either. The controls that
# proved this revision correct lived only in a report, which is the same
# category of thing as a comment: true when written, unenforced afterwards.
#
# Every control is TWO-SIDED. Each fixture expecting exit 0 has a twin whose
# only difference is that the innocent construct has been made real -- a comment
# marker or the quoting removed, or a genuine listener appended. Without the
# twin, an "expected 0, got 0" proves nothing: a file the scanner never read
# scores exactly the same. The twin is what proves the file was inspected.
selftest_case() {
  local name=$1 want=$2 file out rc
  file="$ST_DIR/$name.spec.ts"
  command cat > "$file"
  out=$(command bash "$SELF" "$file" 2>&1)
  rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    return 0
  fi
  echo "FAIL: networkidle-probes self-test - control '$name' expected exit $want, got $rc"
  echo "  scanner said:"
  command printf '%s\n' "$out" | command sed 's/^/    /'
  echo "  fixture was:"
  command sed 's/^/    /' "$file"
  ST_FAILS=$((ST_FAILS + 1))
}

selftest() {
  ST_DIR="$(command mktemp -d)" || {
    echo "FAIL: networkidle-probes self-test - could not create a temp directory"
    exit 2
  }
  trap 'command rm -rf "$ST_DIR"' EXIT
  ST_RUN=0
  ST_FAILS=0

  # First, so that an unrelated breakage is not reported 20 times over. A spec
  # with nothing to find must pass; if this one fails and the message below is
  # about the debounce constant, that is rule 2 drift, not rule 1.
  selftest_case sanity 0 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('nothing to find here', async ({ page }) => {
  await page.goto('/dashboard')
  await expect(page.getByRole('heading')).toBeVisible()
})
FIXTURE

  # --- the four false-positive controls, each paired with a real listener so
  # --- that "exit 0" cannot be explained by the file going unread
  selftest_case fp-wait-for-response 0 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('an ordinary wait is not a counter', async ({ page }) => {
  const res = await page.waitForResponse((r) => r.url().includes('/api/v1/stacks'))
  expect(res.ok()).toBe(true)
})
FIXTURE
  selftest_case fp-wait-for-response-mut 1 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('an ordinary wait is not a counter', async ({ page }) => {
  page.on('request', (r) => { void r })
  const res = await page.waitForResponse((r) => r.url().includes('/api/v1/stacks'))
  expect(res.ok()).toBe(true)
})
FIXTURE

  selftest_case fp-attempt-count 0 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('a retry loop is not a counter', async ({ page }) => {
  let attemptCount = 0
  while (attemptCount < 3) {
    attemptCount++
    await page.reload()
  }
  expect(attemptCount).toBe(3)
})
FIXTURE
  selftest_case fp-attempt-count-mut 1 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('a retry loop is not a counter', async ({ page }) => {
  let attemptCount = 0
  page.on('request', () => { attemptCount++ })
  expect(attemptCount).toBe(3)
})
FIXTURE

  selftest_case fp-template-literal 0 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('prose quoting the call is prose', async ({ page }) => {
  const advice = `never write page.on('request', fn) by hand; use the helper`
  expect(advice.length).toBeGreaterThan(0)
})
FIXTURE
  selftest_case fp-template-literal-mut 1 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('prose quoting the call is prose', async ({ page }) => {
  page.on('request', fn)
  expect(advice.length).toBeGreaterThan(0)
})
FIXTURE

  selftest_case fp-line-comment 0 <<'FIXTURE'
import { test } from '@playwright/test'

// page.on('request', (r) => { void r })
test('noop', async () => {})
FIXTURE
  selftest_case fp-line-comment-mut 1 <<'FIXTURE'
import { test } from '@playwright/test'

page.on('request', (r) => { void r })
test('noop', async () => {})
FIXTURE

  selftest_case fp-block-comment 0 <<'FIXTURE'
import { test } from '@playwright/test'

/*
 * page.on('response', (r) => { void r })
 */
test('noop', async () => {})
FIXTURE
  selftest_case fp-block-comment-mut 1 <<'FIXTURE'
import { test } from '@playwright/test'

page.on('response', (r) => { void r })
test('noop', async () => {})
FIXTURE

  # --- the same three shapes again for the `.route(` walk, which is a SEPARATE
  # --- match() walk and therefore a separate chance to get the masking wrong
  selftest_case route-line-comment 0 <<'FIXTURE'
import { test } from '@playwright/test'

// await page.route('**/api/v1/stacks', (r) => { hits++; return r.continue() })
test('noop', async () => {})
FIXTURE
  selftest_case route-line-comment-mut 1 <<'FIXTURE'
import { test } from '@playwright/test'

await page.route('**/api/v1/stacks', (r) => { hits++; return r.continue() })
test('noop', async () => {})
FIXTURE

  selftest_case route-block-comment 0 <<'FIXTURE'
import { test } from '@playwright/test'

/*
 * await page.route('**\/api/v1/stacks', (r) => { hits++ })
 */
test('noop', async () => {})
FIXTURE
  selftest_case route-block-comment-mut 1 <<'FIXTURE'
import { test } from '@playwright/test'

await page.route('**\/api/v1/stacks', (r) => { hits++ })
test('noop', async () => {})
FIXTURE

  selftest_case route-string-literal 0 <<'FIXTURE'
import { test, expect } from '@playwright/test'

const tmpl = `count with the helper, never page.route('**/api/v1/stacks', fn)`
const str = "never page.route('**/x', fn) by hand"

test('prose', async () => { expect(tmpl && str).toBeTruthy() })
FIXTURE
  selftest_case route-string-literal-mut 1 <<'FIXTURE'
import { test, expect } from '@playwright/test'

await page.route('**/api/v1/stacks', fn)

test('prose', async () => { expect(tmpl && str).toBeTruthy() })
FIXTURE

  # --- the must-fail side, and the helper it points authors at
  selftest_case listener-counter 1 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('hand-rolled tally', async ({ page }) => {
  let n = 0
  page.on('request', (r) => { if (r.url().includes('/api/v1/stacks')) n++ })
  await page.goto('/dashboard')
  expect(n).toBe(1)
})
FIXTURE
  selftest_case helper-counter 0 <<'FIXTURE'
import { test, expect } from '@playwright/test'
import { countMatchingRequests, waitForInvalidationSettle } from './helpers/network-settle'

test('the sanctioned shape', async ({ page }) => {
  const tally = countMatchingRequests(page, /\/api\/v1\/stacks$/)
  await page.goto('/dashboard')
  await waitForInvalidationSettle(page)
  expect(tally.count).toBe(1)
})
FIXTURE

  selftest_case route-counter 1 <<'FIXTURE'
import { test, expect } from '@playwright/test'

test('route as a tally', async ({ page }) => {
  let hits = 0
  await page.route('**/api/v1/stacks', (r) => { hits++; return r.continue() })
  await page.goto('/dashboard')
  expect(hits).toBe(1)
})
FIXTURE

  # The receiver is deliberately NOT `page.`: a `page.`-anchored pattern would
  # miss the only real site in this repo, which is `sharedContext.route(...)`.
  selftest_case route-any-receiver 1 <<'FIXTURE'
import { test } from '@playwright/test'

test('receiver is not what is matched', async () => {
  await sharedContext.route('**/api/v1/**', (route) => route.continue())
})
FIXTURE

  # --- the annotation path, both ways, for each rule
  selftest_case annotated-route 0 <<'FIXTURE'
import { test } from '@playwright/test'

test('canned responses', async () => {
  // request-listener-ok: canned-response stubbing harness, no request tally
  await sharedContext.route('**/api/v1/**', (route) => route.continue())
})
FIXTURE
  selftest_case annotated-route-stripped 1 <<'FIXTURE'
import { test } from '@playwright/test'

test('canned responses', async () => {
  await sharedContext.route('**/api/v1/**', (route) => route.continue())
})
FIXTURE

  selftest_case annotated-listener 0 <<'FIXTURE'
import { test } from '@playwright/test'

test('logging, not counting', async ({ page }) => {
  // request-listener-ok: dumps failed requests into the trace, tallies nothing
  page.on('requestfailed', (r) => console.log(r.url()))
})
FIXTURE
  selftest_case annotated-listener-stripped 1 <<'FIXTURE'
import { test } from '@playwright/test'

test('logging, not counting', async ({ page }) => {
  page.on('requestfailed', (r) => console.log(r.url()))
})
FIXTURE

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "FAIL: networkidle-probes self-test - $ST_FAILS of $ST_RUN control(s) failed; rule 1 does not behave as documented"
    return 1
  fi
  echo "networkidle-probes self-test: $ST_RUN control(s) passed, each proven both ways"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
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
  echo "FAIL: networkidle-probes - $HELPER_REL is missing, but this gate tells authors to count requests with it"
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
# rule 1: no raw request listener outside the sanctioned helper
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

# The helpers/ directory implements the sanctioned API and is the one place a
# raw listener belongs.
readable=()
for f in "${files[@]:-}"; do
  [ -z "$f" ] && continue
  case "$f" in
    */helpers/*) continue ;;
  esac
  if [ -r "$f" ] && [ -f "$f" ]; then
    readable+=("$f")
  else
    # Not a warning. A tracked path that cannot be read means the gate did not
    # inspect what it claims to inspect, and a green "PASS" would be a lie.
    echo "FAIL: networkidle-probes - $f is tracked but not a readable regular file; the gate could not inspect it"
    exit 1
  fi
done

# A gate that cannot tell "clean" from "inspected nothing" carries no
# information. Renaming testing/ used to leave this check green forever.
if [ "${#readable[@]}" -eq 0 ]; then
  echo "FAIL: networkidle-probes - no files to scan; expected tracked TypeScript under testing/, so either the tree moved or the pathspec is wrong"
  exit 1
fi

# Whole-file buffering: the opt-out annotation may sit on the three lines above
# the violation, and the block-comment state has to carry across lines. C[]
# holds each line with comments removed and is what the detector reads; L[]
# holds the raw line and is what the annotation is read from.
command awk -v DRIFT="$drift_note" '
  BEGIN { SQ = sprintf("%c", 39); DQ = sprintf("%c", 34); BT = sprintf("%c", 96) }

  # Strip // and /* */ comments, preserving string literals so that neither a
  # URL containing "//" truncates a line nor a commented-out example counts as
  # code. BLOCKSTATE carries the open-block-comment state to the next line.
  function strip_comments(s, inblock,   out, mask, i, len, c, c2, q) {
    out = ""
    mask = ""
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
        mask = mask "."
        i++
        continue
      }
      if (c == SQ || c == DQ || c == BT) {
        q = c
        out = out c
        mask = mask "S"
        i++
        while (i <= len) {
          c = substr(s, i, 1)
          out = out c
          mask = mask "S"
          i++
          if (c == "\\") {
            if (i <= len) { out = out substr(s, i, 1); mask = mask "S"; i++ }
            continue
          }
          if (c == q) break
        }
        continue
      }
      out = out c
      mask = mask "."
      i++
    }
    BLOCKSTATE = inblock
    MASK = mask
    return out
  }

  # Only an ATTACH. `waitForResponse()` is an ordinary wait and `attemptCount++`
  # is an ordinary retry loop; neither is counting requests, and treating them as
  # such is what got the previous rule deleted.
  # The listener syntax needs the quoted event name, so string literals are kept
  # in C[] rather than blanked. M[] then says which characters are INSIDE a
  # literal, so that a template literal quoting the whole call -- prose, not code
  # -- is not reported. Found 2026-09-02 by a false-positive control.
  function is_listener(l, m,   low, pos, start) {
    low = tolower(l)
    pos = 1
    while (match(substr(low, pos), /\.on\([ \t]*["'"'"'`](request|response|requestfinished|requestfailed)["'"'"'`]/)) {
      start = pos + RSTART - 1
      if (substr(m, start, 1) != "S") return 1
      pos = start + 1
    }
    return 0
  }
  # Route interception, matched on the METHOD alone. Constraining to `page.`
  # would miss the one real site in this repo (`sharedContext.route(...)`) and
  # would be the same no-receiver looseness the review faulted. Same string mask
  # as is_listener(), so `.route(` quoted inside a literal is still prose.
  function is_route(l, m,   low, pos, start) {
    low = tolower(l)
    pos = 1
    while (match(substr(low, pos), /\.route\(/)) {
      start = pos + RSTART - 1
      if (substr(m, start, 1) != "S") return 1
      pos = start + 1
    }
    return 0
  }
  function annotated(i,   j) {
    for (j = i; j >= 1 && j >= i - 3; j--) {
      if (tolower(L[j]) ~ /request-listener-ok:[ \t]*[^ \t]/) return 1
    }
    return 0
  }
  function process(f,   i, what) {
    scanned++
    BLOCKSTATE = 0
    for (i = 1; i <= n; i++) { C[i] = strip_comments(L[i], BLOCKSTATE); M[i] = MASK }
    for (i = 1; i <= n; i++) {
      if (is_listener(C[i], M[i])) what = "raw request listener"
      else if (is_route(C[i], M[i])) what = "route interception"
      else continue
      if (annotated(i)) continue
      printf "%s:%d: %s in a spec; use countMatchingRequests() instead, or annotate it\n", f, i, what
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
      printf "FAIL: networkidle-probes - %d request interception(s) outside the sanctioned helper\n", bad
      printf "Count requests with countMatchingRequests() from testing/tests/playwright/helpers/network-settle.ts:\n"
      printf "its tally refuses to be read until waitForInvalidationSettle() has run, which a hand-rolled listener cannot enforce.\n"
      printf "If it genuinely counts nothing -- a canned-response stub, say -- annotate it with \"request-listener-ok: <reason>\".\n"
      exit 1
    }
    printf "networkidle-probes: %d file(s) scanned, no unannotated request interception outside the helper; %s\n", scanned, DRIFT
  }
' "${readable[@]}"
