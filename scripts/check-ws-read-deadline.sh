#!/usr/bin/env bash
# scripts/check-ws-read-deadline.sh
#
# ONE invariant, the ratchet for agent-os-euyg (shape A) and agent-os-jar5
# (shapes B, C and D):
#
#   No *_test.go file under backend/internal/handlers/ bounds a wait with a
#   FIXED WALL-CLOCK DURATION. Test waits use hangGuardDeadline(t)
#   (backup_ws_cap_test.go), an ABSOLUTE deadline a passing run never consults,
#   reached either directly or via time.Until(...).
#
# WHY. A fixed wall-clock bound makes the test's PASS depend on the clock
# rather than on the condition it is waiting for, so a loaded runner turns
# correct code red. OBSERVED in CI on PR #256 @ 8aed205 (agent-os-fzqb): the
# plain unit job red, a rerun of the IDENTICAL SHA green, and the SLOWER race
# job green both times -- load pointing at itself. hangGuardDeadline exists to
# make the bound un-consultable in a correct run; see its 55-line comment for
# why a BIGGER constant is not the fix.
#
# WHY A RATCHET AND NOT JUST A FIX. agent-os-o1jp.8 converted three of eight
# sites and DECLARED the other five open in its close reason. Nothing watched
# the declared remainder: five survived and two more grew beside them within a
# day (PR #297), and nothing noticed until an audit ran the grep again. A
# remainder nobody can regrow past is a check, not a note.
#
# THE FOUR SHAPES, and why the count grew from one to four. agent-os-euyg
# ratcheted shape A alone and said so in this header: it named the other
# routes, MEASURED that it did not report them, and listed the seven live
# sites it was deliberately not reddening. agent-os-jar5 converted those seven
# (plus an eighth this file's own author had not found, and two out-of-shape
# extras), so the pattern widens here rather than in a second script.
#
#   A. Set{Read,Write}Deadline(time.Now().Add(...))          -- agent-os-euyg
#   B. time.After / time.NewTimer / time.Tick / time.AfterFunc
#      given anything other than time.Until(...)             -- agent-os-jar5
#   C. time.Now().Add(...) used as a test deadline, with or
#      without a Set*Deadline call on the same line          -- agent-os-jar5
#   D. testify Eventually/Never given a literal (waitFor, tick) pair
#                                                            -- agent-os-jar5
#
# WHY B IS NOT ANCHORED ON A DIGIT. The orchestrator's own sweep for this bead
# was `time\.After\([0-9]+\s*\*\s*time\.` and it returned 3 of 5 sites,
# silently: ws_test.go:350 and :465 were `time.After(time.Second)`, a bare
# duration constant with NO DIGITS IN IT AT ALL. Rule B therefore keys on what
# a CORRECT bound looks like (the argument is time.Until(...)) and rejects
# everything else, rather than on what an incorrect one looks like. A rule
# written the other way round cannot enumerate the ways to spell a duration.
#
# WHY B ALSO REJECTS A DERIVED BOUND CARRYING A LITERAL. `time.Until(guard) +
# 500*time.Millisecond` (ws_auth_disabled_identity_test.go before jar5) is
# guard-derived and still carries fixed slop. Rule B's second clause reports a
# time.Until(...) argument on a line that also names a time.<Unit> constant.
#
# WHY C MATCHES time.Now().Add( WITHOUT REQUIRING Set*Deadline. This file's
# previous revision predicted the gap and asked for exactly this: rule A never
# saw `deadline := time.Now().Add(10 * time.Second)`, which is how
# ear5_metrics_empty_recheck_test.go:164 hid from the sweep that created rule
# A -- the call and the arithmetic sat on different lines. Rule C is the
# no-prefix version, which is why it needs the three exemptions below.
#
# WHY D KEYS ON TWO ADJACENT DURATION LITERALS RATHER THAN ON `Eventually(`.
# testify's Eventually(t, cond, waitFor, tick) is routinely written across
# four lines, with `Eventually(` on one and the bounds on another, so a
# per-line rule anchored on the call name cannot see the bound (the exact
# multi-line blindness that hid ear5's site from rule A). The (waitFor, tick)
# pair is itself unmistakable: a time.<Unit> constant immediately followed by
# another duration literal argument. MEASURED over
# backend/internal/handlers/*_test.go at agent-os-jar5: 0 lines match that
# outside an Eventually/Never bound.
#
# THE THREE EXEMPTIONS TO RULE C, each self-tested BOTH WAYS below.
#
#   1. TOKEN LIFETIMES. A JWT "exp"/"iat"/"nbf" claim or a session ExpiresAt
#      is a domain value, not a test deadline; 14 such lines are live in this
#      directory and a 24-hour token expiry must never be "converted".
#   2. THE FILE THAT DEFINES hangGuardDeadline. Detected BY CONTENT (it
#      contains `func hangGuardDeadline(`), never by filename: the fix's own
#      machinery is `guard := time.Now().Add(wsHangGuardCeiling)` plus a floor
#      of `time.Now().Add(time.Second)`, which is rule C's own shape. A
#      filename-keyed selector is the defect agent-os-1hig is about, so it is
#      not reintroduced here; if that helper moves file, the exemption moves
#      with it.
#   3. AN EXPLICIT `// wall-clock ok: <reason>` MARKER on the code line. One
#      live use: backup_test.go's bounded NEGATIVE probe, which asserts a run
#      has NOT finished -- there the bound IS the assertion, a slow box can
#      only push it toward the expected answer, and hangGuardDeadline(t) would
#      cost the full 60s ceiling to discriminate nothing extra.
#
#      THE REASON IS MANDATORY. A bare `// wall-clock ok:` does NOT exempt,
#      and the self-test pins that: an unaudited escape hatch is how this
#      class regrows, and a marker nobody has to justify is unaudited by
#      construction. Exempted lines are PRINTED in the clean output, with a
#      count, so the hatch cannot be widened silently -- growth in that number
#      is visible in the same place the verdict is.
#
# WHY BASH, AND WHY IT READS THE WORKING TREE. This runs inside the REQUIRED
# "Docs structure and coverage gates" job, advertised as dependency-free bash
# (.github/workflows/docs.yml:8-10); an earlier proposal to add actions/setup-go
# to it was refused. Reading the tree from disk is CORRECT for a bash ratchet
# and is not the `os.ReadFile` hazard that applies to a source-inspecting GO
# test (where the verifying instrument substitutes sources at build time via
# -overlay and a disk read would silently scan the unmutated file). Do not
# "improve" this into a Go test that reads the tree.
#
# SCOPE IS *_test.go ONLY, on purpose. Production WebSocket code sets a read
# deadline as now+pongWait by design; that is the protocol, not a test bound.
#
# Usage:
#   check-ws-read-deadline.sh              scan backend/internal/handlers
#   check-ws-read-deadline.sh DIR          scan exactly DIR (used by --self-test)
#   check-ws-read-deadline.sh --self-test  prove the check still fires, both ways
#
# Exit: 0 clean, 1 violations found OR the scan could not read anything,
#       2 usage error.
#
# THERE IS NO SKIP PATH, deliberately. Unlike check-getter-errors (a go/ast
# program that must stand down where Go is absent), this needs only bash, find,
# grep and awk, so it can always run wherever check-docs.sh runs. A scan root
# that is missing or holds no *_test.go file is therefore a FAILURE, not a
# skip: "I read nothing" and "the tree is clean" produce identical output, and
# that indistinguishability is the exact failure this whole check exists to
# prevent. check-docs.sh's `3 = skipped` arm is never reached from here.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_ROOT="$REPO_ROOT/backend/internal/handlers"

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [DIR|--self-test]

With no arguments, every *_test.go under backend/internal/handlers is scanned.
With a directory argument, exactly that tree is scanned.

Fails when a test bounds a wait with a fixed wall-clock duration instead of the
absolute hangGuardDeadline(t), which makes the test's pass depend on the clock.
Four shapes: [A] Set{Read,Write}Deadline(time.Now().Add(...)), [B] time.After /
NewTimer / Tick / AfterFunc given anything but time.Until(...), [C] a
time.Now().Add(...) test deadline, [D] a testify Eventually/Never bounded by a
literal (waitFor, tick) pair.

Exempt: JWT/session lifetime lines, the file that DEFINES hangGuardDeadline
(matched by content, not by name), and a line carrying an explicit
`// wall-clock ok: <reason>` marker. A bare marker with no reason does not
exempt. Exempt lines are printed and counted on a clean run.

--self-test runs the check against fixtures in a temp directory and asserts it
still reports the shapes it must and stays silent on the shapes it must not.
USAGE
}

# ---------------------------------------------------------------------------
# the check
# ---------------------------------------------------------------------------

# EVERY RULE IS A PURE PER-LINE MATCH: one line in, one verdict out, no
# proximity window and no multi-line aggregation. There is no "within N lines"
# rule that could borrow the next call site's guard and read a defective line
# as clean -- the failure that made handlers/directories.go:219 read GUARDED in
# three separate documents (agent-os-7lg1, agent-os-3h9x). The one thing that
# is NOT per-line is exemption 2, which is a property of the FILE and is
# computed from the file's CONTENT before awk runs.
#
# The one exemption to the line rules is a WHOLE-LINE comment (first non-blank
# characters are `//`), because Go has no way to put code on such a line, and
# this file's own prose has to be able to name the patterns it forbids. A code
# line with a TRAILING comment is still code and still matches; the self-test
# pins both halves of that boundary.
scan() {
  local root=$1

  if [ ! -d "$root" ]; then
    echo "FAIL: ws-read-deadline - scan root '$root' does not exist, so the ratchet did not run. This is not a clean tree."
    return 1
  fi

  local files=() f
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    files+=("$f")
  done < <(command find "$root" -type f -name '*_test.go' | command sort)

  if [ "${#files[@]}" -eq 0 ]; then
    echo "FAIL: ws-read-deadline - no *_test.go file under '$root', so the ratchet read nothing. A scan that read nothing is not a clean tree."
    return 1
  fi

  # Exemption 2, computed BY CONTENT, never by filename: the file that DEFINES
  # hangGuardDeadline is the fix's own machinery, and its `time.Now().Add(
  # wsHangGuardCeiling)` is rule C's shape by construction. Space-delimited on
  # both sides so membership cannot match on a path prefix.
  local hgfiles=" "
  for f in "${files[@]}"; do
    if command grep -q 'func hangGuardDeadline(' "$f"; then
      hgfiles+="$f "
    fi
  done

  command awk -v hgfiles="$hgfiles" '
    function report(rule, why,   _) {
      printf "%s:%d: [%s] %s\n      %s\n", FILENAME, FNR, rule, why, line
      bad++
    }
    {
      line = $0
      sub(/\r$/, "", line)
      if (line ~ /^[ \t]*\/\//) next

      # Exemption 3: an explicit marker WITH A NON-EMPTY REASON after the
      # colon. A bare "// wall-clock ok:" is not a reason and does not exempt.
      if (line ~ /\/\/[ \t]*wall-clock ok:[ \t]*[^ \t]/) {
        exempt++
        exempts[exempt] = sprintf("%s:%d: %s", FILENAME, FNR, line)
        next
      }

      # --- A: the agent-os-euyg shape, reported with its own message because
      # --- it names the one call the reader has to change.
      if (line ~ /Set(Read|Write)Deadline\(time\.Now\(\)\.Add\(/) {
        report("A", "WebSocket deadline set to now+duration")
        next
      }

      # --- B: a timer/ticker whose bound is not derived from an absolute
      # --- deadline. Keyed on what a CORRECT bound looks like, because the
      # --- ways to spell a duration cannot be enumerated (time.Second has no
      # --- digits in it).
      if (line ~ /time\.(After|NewTimer|Tick|AfterFunc)\(/) {
        if (line !~ /time\.(After|NewTimer|Tick|AfterFunc)\(time\.Until\(/) {
          report("B", "timer bound is a fixed duration, not time.Until(<absolute deadline>)")
          next
        }
        if (line ~ /time\.(Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)/) {
          report("B", "fixed slop added to a time.Until(...) bound")
          next
        }
      }

      # --- C: the no-prefix version of A. This is the rule that needs the
      # --- exemptions; see the header for why each exists.
      if (line ~ /time\.Now\(\)\.Add\(/) {
        if (line ~ /"exp"|"iat"|"nbf"|ExpiresAt|IssuedAt|NotBefore/) {
          exempt++
          exempts[exempt] = sprintf("%s:%d: %s", FILENAME, FNR, line)
        } else if (index(hgfiles, " " FILENAME " ") > 0) {
          exempt++
          exempts[exempt] = sprintf("%s:%d: %s", FILENAME, FNR, line)
        } else {
          report("C", "test deadline built as now+duration")
          next
        }
      }

      # --- D: testify Eventually/Never given a literal (waitFor, tick) pair.
      # --- Keyed on the BOUNDS line, not on the call name, because the call
      # --- is routinely four lines above its own bounds.
      if (line ~ /time\.(Nanosecond|Microsecond|Millisecond|Second|Minute|Hour),[ \t]*[0-9]+[ \t]*\*[ \t]*time\./) {
        report("D", "Eventually/Never bounded by a literal duration")
        next
      }
    }
    END {
      if (bad > 0) {
        printf "FAIL: ws-read-deadline - %d site(s) bound a test wait with a fixed wall-clock duration. Use hangGuardDeadline(t) (backend/internal/handlers/backup_ws_cap_test.go), directly or via time.Until(...): an absolute deadline a passing run never consults, so a loaded runner cannot turn correct code red. A site where the bound IS the assertion carries a `// wall-clock ok: <reason>` marker instead.\n", bad
        exit 1
      }
      printf "ws-read-deadline: %d file(s) scanned, no fixed wall-clock test bound (shapes A-D)\n", ARGC - 1
      printf "ws-read-deadline: %d exempt line(s) (token lifetimes, the hangGuardDeadline definition, and marked sites)\n", exempt
      for (i = 1; i <= exempt; i++) printf "  exempt %s\n", exempts[i]
    }
  ' "${files[@]}"
}

# ---------------------------------------------------------------------------
# --self-test: the check's own regression suite
# ---------------------------------------------------------------------------

# WHY THIS EXISTS, and why check-docs.sh runs it BEFORE believing any verdict:
# a scanner that has silently stopped firing produces output byte-identical to
# a clean tree. Every control below is TWO-SIDED -- each fixture expecting a
# clean result has a twin whose only difference is the thing being checked --
# so "expected 0, got 0" can never be explained by the check having read
# nothing at all. Controls that must fail also demand a MESSAGE, because exit
# status alone cannot tell "found a violation" apart from "lost its footing and
# exited 1 for an unrelated reason".
selftest_case() {
  local name=$1 want=$2 root=$3 want_msg=${4:-} out rc
  out=$(scan "$root" 2>&1)
  rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || command printf '%s\n' "$out" | command grep -qE "$want_msg"; then
      return 0
    fi
    echo "FAIL: ws-read-deadline self-test - control '$name' exited $rc as expected but did not explain itself; wanted a message matching /$want_msg/"
  else
    echo "FAIL: ws-read-deadline self-test - control '$name' expected exit $want, got $rc"
  fi
  echo "  check said:"
  command printf '%s\n' "$out" | command sed 's/^/    /'
  ST_FAILS=$((ST_FAILS + 1))
}

# write_fixture DIR FILENAME BODY -> creates $ST_DIR/DIR/FILENAME and echoes the dir
write_fixture() {
  local dir="$ST_DIR/$1"
  command mkdir -p "$dir"
  command cat > "$dir/$2" <<FIXTURE
package handlers

func TestFixture(t *testing.T) {
$3
}
FIXTURE
  echo "$dir"
}

# write_raw_fixture DIR FILENAME CONTENT -> whole-file content, no func wrapper
write_raw_fixture() {
  local dir="$ST_DIR/$1"
  command mkdir -p "$dir"
  command printf '%s\n' "$3" > "$dir/$2"
  echo "$dir"
}

selftest() {
  ST_DIR="$(command mktemp -d)" || {
    echo "FAIL: ws-read-deadline self-test - could not create a temp directory"
    exit 2
  }
  trap 'command rm -rf "$ST_DIR"' EXIT
  ST_RUN=0
  ST_FAILS=0

  local literal guarded vardur absvar barenow writedl commentonly trailing nested prodonly noTests
  local bLit bNoDigit bClean bSlop bTicker cVar cClean cJwt cHgDef cHgName cMark cBare dLit dNoDigit dClean cmtBCD

  # --- shape A: the offending shape, and its converted twin
  literal=$(write_fixture literal a_test.go '	require.NoError(t, clientConn.SetReadDeadline(time.Now().Add(5*time.Second)))')
  guarded=$(write_fixture guarded a_test.go '	require.NoError(t, clientConn.SetReadDeadline(hangGuardDeadline(t)))')

  # --- a VARIABLE duration is the same defect wearing a parameter (the site
  # --- that made the original acceptance sweep unreachable by fixing callers)
  vardur=$(write_fixture vardur a_test.go '	require.NoError(t, conn.SetReadDeadline(time.Now().Add(within)))')
  absvar=$(write_fixture absvar a_test.go '	require.NoError(t, conn.SetReadDeadline(deadline))')

  # --- SetReadDeadline(time.Now()) with no .Add is the deliberate
  # --- expire-immediately idiom (monitoring_metrics_close_test.go:225) and
  # --- must stay legal; the write side of the same shape must not.
  barenow=$(write_fixture barenow a_test.go '	err := nc.SetReadDeadline(time.Now())')
  writedl=$(write_fixture writedl a_test.go '	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(2*time.Second)))')

  # --- the comment exemption, and its boundary: a trailing comment does not
  # --- launder a real code line
  commentonly=$(write_fixture commentonly a_test.go '	// never write conn.SetReadDeadline(time.Now().Add(5*time.Second)) here
	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))')
  trailing=$(write_fixture trailing a_test.go '	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second))) // drain one frame')

  # --- the scan must recurse, or a violation one directory down is invisible
  nested=$(write_fixture nested/sub a_test.go '	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))')
  nested="$ST_DIR/nested"

  # --- production code is deliberately out of scope, but the scan must
  # --- provably have RUN: a clean _test.go beside a dirty non-test file.
  prodonly=$(write_fixture prodonly a_test.go '	require.NoError(t, conn.SetReadDeadline(hangGuardDeadline(t)))')
  command cat > "$prodonly/ws.go" <<'PROD'
package handlers

func readPump(c *websocket.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(pongWait))
}
PROD

  # --- "I read nothing" must never look like "the tree is clean"
  noTests="$ST_DIR/notests"
  command mkdir -p "$noTests"
  command cat > "$noTests/ws.go" <<'PROD'
package handlers
PROD

  # --- shape B (agent-os-jar5). bNoDigit is THE control for this bead: the
  # --- orchestrator's digit-anchored sweep returned 3 of 5 because
  # --- time.After(time.Second) has no digits in it. If only one B control
  # --- ever runs, it is this one.
  bLit=$(write_fixture bLit a_test.go '	case <-time.After(5 * time.Second):')
  bNoDigit=$(write_fixture bNoDigit a_test.go '	case <-time.After(time.Second):')
  bClean=$(write_fixture bClean a_test.go '	case <-time.After(time.Until(hangGuardDeadline(t))):')
  bSlop=$(write_fixture bSlop a_test.go '	case <-time.After(time.Until(guard) + 500*time.Millisecond):')
  # a NewTicker is a poll INTERVAL, not a bound whose expiry fails the test --
  # deliberately out of class, and the near-miss spelling of time.Tick(
  bTicker=$(write_fixture bTicker a_test.go '		ticker := time.NewTicker(100 * time.Millisecond)')

  # --- shape C and its three exemptions
  cVar=$(write_fixture cVar a_test.go '	deadline := time.Now().Add(5 * time.Second)')
  cClean=$(write_fixture cClean a_test.go '	deadline := hangGuardDeadline(t)')
  cJwt=$(write_fixture cJwt a_test.go '		"exp":      time.Now().Add(24 * time.Hour).Unix(),')
  # exemption 2 is keyed on CONTENT. cHgDef defines hangGuardDeadline and is
  # exempt; cHgName carries the identical line under the real machinery's
  # FILENAME but without the definition, and must still be reported -- that
  # pair is what proves the selector is not filename-keyed (agent-os-1hig).
  cHgDef=$(write_raw_fixture cHgDef backup_ws_cap_test.go 'package handlers

func hangGuardDeadline(t *testing.T) time.Time {
	guard := time.Now().Add(wsHangGuardCeiling)
	return guard
}')
  cHgName=$(write_raw_fixture cHgName backup_ws_cap_test.go 'package handlers

func somethingElse(t *testing.T) time.Time {
	guard := time.Now().Add(wsHangGuardCeiling)
	return guard
}')
  # exemption 3, and the arm that matters: a marker with NO REASON must NOT
  # exempt. An escape hatch nobody has to justify is unaudited by construction.
  cMark=$(write_fixture cMark a_test.go '	_, ok := awaitDurableRun(t, reg, id, time.Now().Add(500*time.Millisecond)) // wall-clock ok: bounded negative probe')
  cBare=$(write_fixture cBare a_test.go '	_, ok := awaitDurableRun(t, reg, id, time.Now().Add(500*time.Millisecond)) // wall-clock ok:')

  # --- shape D: the testify (waitFor, tick) pair, on its own line as testify
  # --- calls are normally written. dNoDigit is D's version of the no-digit
  # --- trap: a bare time.Second waitFor.
  dLit=$(write_fixture dLit a_test.go '	}, 2*time.Second, 10*time.Millisecond,')
  dNoDigit=$(write_fixture dNoDigit a_test.go '	}, time.Second, 10*time.Millisecond,')
  dClean=$(write_fixture dClean a_test.go '	}, time.Until(hangGuardDeadline(t)), 10*time.Millisecond,')

  # --- a whole-line comment naming B, C and D at once is still not a site
  cmtBCD=$(write_fixture cmtBCD a_test.go '	// never write time.After(5 * time.Second), deadline := time.Now().Add(5 * time.Second)
	// or }, 2*time.Second, 10*time.Millisecond, in this package
	deadline := hangGuardDeadline(t)')

  selftest_case literal-5s          1 "$literal"     'a_test\.go:[0-9]+: \[A\]'
  selftest_case literal-names-fix   1 "$literal"     'hangGuardDeadline\(t\)'
  selftest_case hang-guard-clean    0 "$guarded"     '1 file\(s\) scanned'
  selftest_case variable-duration   1 "$vardur"      '1 site\(s\)'
  selftest_case absolute-var-clean  0 "$absvar"      '1 file\(s\) scanned'
  selftest_case bare-now-clean      0 "$barenow"     '1 file\(s\) scanned'
  selftest_case set-write-deadline  1 "$writedl"     '1 site\(s\)'
  selftest_case comment-only-clean  0 "$commentonly" '1 file\(s\) scanned'
  selftest_case trailing-comment    1 "$trailing"    'a_test\.go:[0-9]+:'
  selftest_case recurses-subdirs    1 "$nested"      'sub/a_test\.go:[0-9]+:'
  selftest_case production-ignored  0 "$prodonly"    '1 file\(s\) scanned'
  selftest_case no-tests-is-a-fail  1 "$noTests"     'read nothing is not a clean tree'
  selftest_case missing-root        1 "$ST_DIR/does-not-exist" 'does not exist, so the ratchet did not run'

  selftest_case b-literal           1 "$bLit"        '\[B\] timer bound is a fixed duration'
  selftest_case b-no-digit          1 "$bNoDigit"    '\[B\] timer bound is a fixed duration'
  selftest_case b-until-clean       0 "$bClean"      '1 file\(s\) scanned'
  selftest_case b-additive-slop     1 "$bSlop"       '\[B\] fixed slop added'
  selftest_case b-ticker-out-of-class 0 "$bTicker"   '1 file\(s\) scanned'

  selftest_case c-assigned-deadline 1 "$cVar"        '\[C\] test deadline built as now\+duration'
  selftest_case c-absolute-clean    0 "$cClean"      '1 file\(s\) scanned'
  selftest_case c-jwt-exempt        0 "$cJwt"        '1 exempt line\(s\)'
  selftest_case c-hangguard-def     0 "$cHgDef"      '1 exempt line\(s\)'
  selftest_case c-hangguard-name    1 "$cHgName"     '\[C\] test deadline built as now\+duration'
  selftest_case c-marker-exempt     0 "$cMark"       '1 exempt line\(s\)'
  selftest_case c-marker-no-reason  1 "$cBare"       '\[C\] test deadline built as now\+duration'

  selftest_case d-literal-bound     1 "$dLit"        '\[D\] Eventually/Never bounded by a literal'
  selftest_case d-no-digit-bound    1 "$dNoDigit"    '\[D\] Eventually/Never bounded by a literal'
  selftest_case d-until-clean       0 "$dClean"      '1 file\(s\) scanned'

  selftest_case comment-names-bcd   0 "$cmtBCD"      '1 file\(s\) scanned'

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "FAIL: ws-read-deadline self-test - $ST_FAILS of $ST_RUN control(s) failed; the check does not behave as documented"
    return 1
  fi
  echo "ws-read-deadline self-test: $ST_RUN control(s) passed, each proven both ways"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') scan "$DEFAULT_ROOT"; exit $? ;;
  -*)
    echo "ERROR: unknown option '$1'" >&2
    usage
    exit 2
    ;;
  *) scan "$1"; exit $? ;;
esac
