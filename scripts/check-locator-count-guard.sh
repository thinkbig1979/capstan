#!/usr/bin/env bash
# scripts/check-locator-count-guard.sh
#
# Ratchet for agent-os-o1jp.5: no `if (await <locator>.count() <op> <N>)`
# guard in testing/tests/playwright/ may wrap an expect(...) call in its
# outcome.
#
# WHY. `await locator.count()` is a point-in-time read with NO auto-retry;
# `await locator.click()` / `expect(...).toBeVisible()` auto-wait up to
# actionTimeout/expect timeout. A guard shaped `if (await x.count() > 0) {
# ...expect... } else { ...expect... }` therefore has a race: under ordinary
# render slowness the count() read lands before the element paints, reads 0,
# and the branch containing the real assertion is silently skipped in favour
# of whichever branch runs instead. The test still reports green.
#
# OBSERVED regression this ratchets against, commit 205b1707 (agent-os-59r0):
#   if (await toggleEl.count() > 0) {
#     await expect(toggleEl).toBeVisible()
#     ...
#   } else {
#     // API-only fallback with its own expect(...) -- always reachable
#     expect(enabledPolicy, 'Policy not enabled after toggle').toBeTruthy()
#   }
#   MEASURED both before and after an unrelated fix landed: toggleCount=0 in
#   both trees, so the `await expect(toggleEl).toBeVisible()` branch had
#   never executed, for an unknown period, in either tree.
#
# SCOPE, DELIBERATELY NARROW (agent-os-kvxs precedent: three static scanners
# for a DIFFERENT class were built and deleted here for failing in both
# directions on a required check -- see check-networkidle-probes.sh's header
# for the full story). This script:
#   - only recognises `count()` compared to a numeric literal (`> 0`, `>= 1`,
#     `=== 0`, `!= 2`, ...) with count() written FIRST. A reversed comparison
#     (`0 < x.count()`) or a bare negation (`if (!x.count())`) is NOT
#     matched. Neither shape exists anywhere in this repo today (verified:
#     `git grep -n "count()"` under testing/tests/playwright/ turns up
#     exactly the one already-justified site plus three comments naming this
#     exact hazard), and matching them would widen the pattern well past the
#     one historical shape this bead exists to ratchet, which is the kind of
#     over-matching that has independently sunk checks on this epic. If a
#     reversed or negated form shows up, extend GUARD_RE deliberately, with
#     its own --self-test fixture, rather than guessing here.
#   - only understands BRACED if/else chains (`if (...) { ... } else { ... }`,
#     with `else`/`else if` attached to the previous `}` on the SAME line --
#     this repo's formatting has never done otherwise; verified across all
#     four spec files). A brace-less single-statement if cannot be scoped by
#     a line/brace scanner without guessing where the statement ends, which
#     is exactly the guesswork that reddened correct code three times on the
#     networkidle check. Rather than guess, a bare (no-brace) count()-if is
#     reported as WARN and does not fail the gate -- see check_unbraced().
#   - does NOT flag a count() comparison used as an assertion's own value
#     (`expect(await x.count()).toBe(0)`), only one used as an `if` guard:
#     GUARD_RE anchors on the `if (` prefix.
#   - does NOT flag a count()-guard whose outcome has no expect() at all
#     (e.g. backup-flow.spec.ts:81's navLink guard, which only clicks and
#     falls back to a direct page.goto -- no assertion is ever skipped by
#     taking either branch, so nothing here is unsound).
#
# Comments and string/template-literal contents are masked out per line
# before matching, so a comment or string that happens to contain this text
# (including the three comments in backup-flow.spec.ts documenting this very
# hazard) is not mistaken for the code shape itself. The masker is line-based
# and does not track an unterminated multi-line template literal past the
# line it starts on -- a known limitation, same class as documented in
# check-networkidle-probes.sh's AWK_LAST_DELAY notes; no such literal exists
# in the current corpus.
#
# Dependency-free by design: awk only.
#
# Usage:
#   check-locator-count-guard.sh              scan every tracked *.ts file
#                                              under testing/tests/playwright/
#   check-locator-count-guard.sh FILE...       scan exactly these paths
#   check-locator-count-guard.sh --self-test   prove the check still fires,
#                                              both ways
#
# Exit: 0 clean (or braceless-only warnings), 1 a guard wraps expect(...),
#       2 usage/environment error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Quote characters the masker treats as string delimiters, passed via -v so
# no literal quote character has to appear inside the single-quoted AWK
# program text below.
QUOTE_CHARS=$'\047\042\140'

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [FILE...] | --self-test

With no arguments, every tracked *.ts file under testing/tests/playwright/
is scanned. With FILE arguments, exactly those paths are scanned instead.

Fails when a braced 'if (await <locator>.count() <op> <N>)' guard's outcome
(the if-block, or a same-line-attached else/else-if chain) contains an
expect(...) call. count() has no auto-retry, so a slow or flaky render can
silently skip that assertion while the test still reports green.

--self-test runs the check against fixtures in a temp directory and asserts
the result both ways, so a check that has silently stopped working is not
mistaken for a clean tree. check-docs.sh runs it before every real check.
USAGE
}

# ---------------------------------------------------------------------------
# The AWK program: masking, guard detection, brace-chain walk.
# ---------------------------------------------------------------------------

AWK_PROG='
function mask(line,    out, n, pos, c, two, q, i, closepos, cend) {
  out = ""
  n = length(line)
  pos = 1
  while (pos <= n) {
    two = substr(line, pos, 2)
    c = substr(line, pos, 1)
    if (two == "//") { return out }
    if (two == "/*") {
      cend = index(substr(line, pos + 2), "*/")
      if (cend > 0) { pos = pos + 2 + cend + 1; continue }
      IN_BLOCK_COMMENT = 1
      return out
    }
    if (index(qc, c) > 0) {
      q = c
      closepos = 0
      i = pos + 1
      while (i <= n) {
        if (substr(line, i, 1) == "\\") { i += 2; continue }
        if (substr(line, i, 1) == q) { closepos = i; break }
        i++
      }
      out = out "\"\""
      if (closepos > 0) { pos = closepos + 1; continue }
      return out
    }
    out = out c
    pos++
  }
  return out
}

# finalize_chain: the guarded chain (if-block plus any same-line-attached
# else/else-if blocks) has closed. Score it once, loudly either way.
function finalize_chain() {
  TOTAL_GUARDS++
  if (HASEXPECT) {
    BAD_COUNT++
    printf "FAIL: locator-count-guard - %s:%d: this count()-guard'"'"'s outcome includes expect(...); count() has no auto-retry, so a slow/flaky render can silently take the branch without that assertion ever running (see agent-os-59r0, commit 205b1707)\n", GFILE, GLINE
  } else {
    CLEAN_COUNT++
  }
  STATE = 0
  DEPTH = 0
}

# process_chain_line: walk one masked line'"'"'s braces one character at a time
# (not a net per-line count) so that "} else {" on one line is read as
# close-then-reopen in order, not cancelled out to a no-op.
function process_chain_line(masked,    i, n, c, rest) {
  if (index(masked, "expect(") > 0) { HASEXPECT = 1 }
  n = length(masked)
  i = 1
  while (i <= n && STATE == 1) {
    c = substr(masked, i, 1)
    if (c == "{") { DEPTH++; i++; continue }
    if (c == "}") {
      DEPTH--
      if (DEPTH <= 0) {
        rest = substr(masked, i + 1)
        sub(/^[ \t]+/, "", rest)
        if (rest == "else" || rest ~ /^else[^A-Za-z0-9_$]/) {
          # same-line else/else-if attached to this close brace: the chain
          # continues, and its own "{" (encountered later in this same
          # walk, or on a later line) will bring DEPTH back above 0.
          i++
          continue
        }
        finalize_chain()
        return
      }
      i++
      continue
    }
    i++
  }
}

BEGIN {
  OPS = "===|!==|==|!=|>=|<=|>|<"
  GUARD_RE = "if[ \t]*\\(.*\\.count\\(\\)[ \t]*(" OPS ")[ \t]*[0-9]+"
  STATE = 0
  DEPTH = 0
  TOTAL_GUARDS = 0
  BAD_COUNT = 0
  CLEAN_COUNT = 0
  UNBRACED_COUNT = 0
}

FNR == 1 {
  if (STATE == 1) { finalize_chain() }
  IN_BLOCK_COMMENT = 0
  STATE = 0
  DEPTH = 0
  CURFILE = FILENAME
}

{
  raw = $0
  sub(/\r$/, "", raw)
  if (IN_BLOCK_COMMENT) {
    endp = index(raw, "*/")
    if (endp > 0) {
      IN_BLOCK_COMMENT = 0
      raw = substr(raw, endp + 2)
    } else {
      next
    }
  }
  masked = mask(raw)

  if (STATE == 0) {
    if (masked ~ GUARD_RE) {
      STATE = 1
      GLINE = FNR
      GFILE = CURFILE
      HASEXPECT = 0
      DEPTH = 0
      process_chain_line(masked)
      if (STATE == 1 && DEPTH == 0) {
        # process_chain_line ran to completion without ever seeing "{" (so
        # finalize_chain, which only fires on a "}", never ran): a
        # brace-less single-statement if. See the SCOPE note in this
        # script'"'"'s header for why this is reported, not guessed at.
        printf "WARN: locator-count-guard - %s:%d: count()-guard has no braces; this check only analyses braced if/else blocks (documented limitation) -- add braces so it can verify whether an assertion is wrapped\n", GFILE, GLINE
        UNBRACED_COUNT++
        STATE = 0
        DEPTH = 0
      }
    }
    next
  }

  SPAN++
  if (SPAN > 300) {
    # Safety valve against an unbalanced file running the scan off the end;
    # matches the 60-line bound used for the same reason in
    # check-networkidle-probes.sh, scaled up because a guarded UI flow'"'"'s
    # block can legitimately run for tens of lines in this suite.
    finalize_chain()
    next
  }
  process_chain_line(masked)
}

END {
  if (STATE == 1) { finalize_chain() }
  printf "locator-count-guard: %d guard(s) scanned, %d flagged, %d clean, %d unbraced (unscored)\n", TOTAL_GUARDS, BAD_COUNT, CLEAN_COUNT, UNBRACED_COUNT
  exit (BAD_COUNT > 0) ? 1 : 0
}
'

# scan_files: run the AWK program over the given file list. Prints AWK'\''s
# output verbatim and returns its exit status.
scan_files() {
  command awk -v qc="$QUOTE_CHARS" "$AWK_PROG" "$@"
}

tracked_files() {
  command git -C "$REPO_ROOT" ls-files -- ':(glob)testing/tests/playwright/**/*.ts' | command sort -u
}

# ---------------------------------------------------------------------------
# --self-test: the check's own regression suite
# ---------------------------------------------------------------------------

ST_DIR=""

write_fixture() {
  local name=$1 body=$2
  command cat > "$ST_DIR/$name.ts" <<FIXTURE
$body
FIXTURE
  echo "$ST_DIR/$name.ts"
}

selftest_case() {
  local name=$1 want=$2 file=$3 want_msg=${4:-} out rc
  out=$(scan_files "$file" 2>&1)
  rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || command printf '%s\n' "$out" | command grep -qE "$want_msg"; then
      return 0
    fi
    echo "FAIL: locator-count-guard self-test - control '$name' exited $rc as expected but did not explain itself; wanted a message matching /$want_msg/"
  else
    echo "FAIL: locator-count-guard self-test - control '$name' expected exit $want, got $rc"
  fi
  echo "  check said:"
  command printf '%s\n' "$out" | command sed 's/^/    /'
  ST_FAILS=$((ST_FAILS + 1))
}

selftest() {
  ST_DIR="$(command mktemp -d)" || {
    echo "FAIL: locator-count-guard self-test - could not create a temp directory"
    exit 2
  }
  trap 'command rm -rf "$ST_DIR"' EXIT
  ST_RUN=0
  ST_FAILS=0

  # --- the real historical regression (agent-os-59r0, commit 205b1707):
  # count()-guard, expect() in BOTH branches. Must go red.
  local buggy_if_else
  buggy_if_else=$(write_fixture buggy-if-else '
async function f(page, toggleEl, enabledPolicy) {
  if (await toggleEl.count() > 0) {
    await expect(toggleEl).toBeVisible()
    const stopPolicyTrigger = page.locator("x")
    await expect(stopPolicyTrigger).toBeVisible()
  } else {
    expect(enabledPolicy, "Policy not enabled after toggle").toBeTruthy()
  }
}')
  selftest_case buggy-if-else 1 "$buggy_if_else" "count\\(\\)-guard's outcome includes expect"

  # --- same shape, expect() only in the if-branch, no else at all.
  local buggy_if_only
  buggy_if_only=$(write_fixture buggy-if-only '
async function f(backupNowBtn) {
  if (await backupNowBtn.count() > 0) {
    await expect(backupNowBtn).toBeVisible()
  }
}')
  selftest_case buggy-if-only 1 "$buggy_if_only" "count\\(\\)-guard's outcome includes expect"

  # --- expect() only reachable via a chained else-if. Proves the walk
  # follows the whole same-line-attached chain, not just the first block.
  local buggy_else_if
  buggy_else_if=$(write_fixture buggy-else-if '
async function f(a, b) {
  if (await a.count() > 1) {
    await a.first().click()
  } else if (await b.count() > 0) {
    await expect(b).toBeVisible()
  } else {
    await a.first().click()
  }
}')
  selftest_case buggy-else-if 1 "$buggy_else_if" "count\\(\\)-guard's outcome includes expect"

  # --- the FIX for the regression above: no guard at all, unconditional
  # retrying expect(). Must stay green.
  local clean_unconditional
  clean_unconditional=$(write_fixture clean-unconditional '
async function f(toggleEl) {
  await expect(toggleEl).toBeVisible()
}')
  selftest_case clean-unconditional 0 "$clean_unconditional"

  # --- the one real site left in this repo (backup-flow.spec.ts:81): the
  # guard'\''s outcome is a click and a return, no expect() anywhere in it.
  # This MUST NOT be flagged -- see the SCOPE note in the header for why.
  local clean_navlink
  clean_navlink=$(write_fixture clean-navlink '
async function expandBackupSection(page, navLink, BASE_URL) {
  if (await navLink.count() > 0) {
    await navLink.first().click()
    await page.waitForLoadState("networkidle")
    return
  }
  await page.goto(BASE_URL + "/settings/backup")
  await page.waitForLoadState("networkidle")
}')
  selftest_case clean-navlink 0 "$clean_navlink"

  # --- count() used as an assertion'\''s own value, not as a branch guard.
  # GUARD_RE anchors on "if (", so this must not match at all.
  local clean_assertion_value
  clean_assertion_value=$(write_fixture clean-assertion-value '
async function f(x) {
  expect(await x.count()).toBe(0)
}')
  selftest_case clean-assertion-value 0 "$clean_assertion_value"

  # --- the guarded pattern appearing only in a // line comment, with the
  # real code beneath it unconditional. The masker must strip the comment.
  local clean_line_comment
  clean_line_comment=$(write_fixture clean-line-comment '
async function f(toggleEl) {
  // old code used to say: if (await toggleEl.count() > 0) { await expect(toggleEl).toBeVisible() }
  await expect(toggleEl).toBeVisible()
}')
  selftest_case clean-line-comment 0 "$clean_line_comment"

  # --- the guarded pattern inside a /* block comment */.
  local clean_block_comment
  clean_block_comment=$(write_fixture clean-block-comment '
/*
 * if (await toggleEl.count() > 0) { await expect(toggleEl).toBeVisible() }
 */
async function f(toggleEl) {
  await expect(toggleEl).toBeVisible()
}')
  selftest_case clean-block-comment 0 "$clean_block_comment"

  # --- the guarded pattern inside a template-literal string.
  local clean_string_literal
  clean_string_literal=$(write_fixture clean-string-literal '
async function f(toggleEl) {
  const msg = `if (await toggleEl.count() > 0) { expect(toggleEl).toBeVisible() }`
  console.log(msg)
  await expect(toggleEl).toBeVisible()
}')
  selftest_case clean-string-literal 0 "$clean_string_literal"

  # --- braceless single-statement guard: unscored WARN, not a failure --
  # this check does not guess at unbraced statement extent (see header).
  local unbraced
  unbraced=$(write_fixture unbraced '
async function f(x) {
  if (await x.count() > 0) return
}')
  selftest_case unbraced 0 "$unbraced" "WARN.*count\\(\\)-guard has no braces"

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "FAIL: locator-count-guard self-test - $ST_FAILS of $ST_RUN control(s) failed; the check does not behave as documented"
    return 1
  fi
  echo "locator-count-guard self-test: $ST_RUN control(s) passed, each proven both ways"
  return 0
}

# ---------------------------------------------------------------------------
# dispatch
# ---------------------------------------------------------------------------

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
esac

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
for f in "${files[@]}"; do
  if [ -r "$f" ] && [ -f "$f" ]; then
    readable+=("$f")
  else
    echo "SKIP: $f (not a readable regular file)" >&2
  fi
done

if [ "${#readable[@]}" -eq 0 ]; then
  echo "locator-count-guard: no files to scan"
  exit 0
fi

scan_files "${readable[@]}"
