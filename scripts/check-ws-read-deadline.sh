#!/usr/bin/env bash
# scripts/check-ws-read-deadline.sh
#
# ONE invariant, the ratchet for agent-os-euyg:
#
#   No *_test.go file under backend/internal/handlers/ sets a WebSocket
#   deadline as `Set{Read,Write}Deadline(time.Now().Add(...))`. Test waits use
#   hangGuardDeadline(t) (backup_ws_cap_test.go), an ABSOLUTE deadline a
#   passing run never consults.
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
# WHY IT MATCHES ANY ARGUMENT, NOT ONLY A LITERAL. The class's own acceptance
# sweep is textual -- `SetReadDeadline(time.Now().Add` -- and one of the eight
# original sites passed a VARIABLE (`time.Now().Add(within)`, a helper taking a
# time.Duration). A literal-only rule would disagree with the sweep it exists
# to ratchet, and a duration parameter is precisely what forces the offending
# expression into the callee. The absolute-deadline form has no such trap.
#
# WHAT THIS DOES **NOT** COVER, AND WHY A PASSING RUN IS NOT A CLOSED CLASS.
# The defect class is "a test whose PASS depends on a fixed wall-clock bound
# rather than on the condition it waits for". That class has THREE syntactic
# routes in this package and this check ratchets exactly ONE of them. MEASURED,
# by pointing this script at a fixture carrying all three (reproducible in four
# lines; agent-os-euyg, independently re-run by the orchestrator):
#
#   require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))  REPORTED
#   deadline := time.Now().Add(10 * time.Second)                             NOT reported
#   case <-time.After(5 * time.Second):                                      NOT reported
#
# So a green run here means "no site of the SetReadDeadline shape", never "no
# fixed wall-clock bound". The two uncovered routes are:
#
#   1. time.After(<literal>) / time.NewTimer(<literal>) as a select arm whose
#      expiry is a t.Fatal.
#   2. `deadline := time.Now().Add(<literal>)` assigned to a variable and then
#      used as the bound. THIS IS HOW THE EIGHTH SITE HID: the bead that
#      created this ratchet counted seven sites from a
#      `SetReadDeadline(time.Now().Add` sweep, and
#      ear5_metrics_empty_recheck_test.go:164 was an eighth in the same class,
#      in one of the same files, added by the same PR as a converted sibling 71
#      lines above it -- invisible to that sweep because the call and the
#      arithmetic sit on different lines. This check would NOT catch it
#      regrowing.
#
# They are deliberately not matched here rather than accidentally: adding them
# today would redden a REQUIRED gate against seven live sites nobody is fixing
# in this change (ws_test.go:350 and :465, mjrl_refused_frame_test.go:156,
# dashboard_ws_refusal_test.go:117, health_test.go:220, backup_test.go:1804,
# updates_stack_test.go:202). Those are tracked by agent-os-jar5, which is the
# watcher for them the way this file is the watcher for the shape above. When
# jar5 converts them, widen this pattern rather than adding a second script --
# and note that a sweep for route 2 must match `time.Now().Add(` without
# requiring the SetReadDeadline prefix, or it repeats the original blindness.
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

Fails when a test sets a WebSocket deadline to now+duration
(Set{Read,Write}Deadline(time.Now().Add(...))) instead of the absolute
hangGuardDeadline(t), which makes the test's pass depend on the clock.

--self-test runs the check against fixtures in a temp directory and asserts it
still reports the shapes it must and stays silent on the shapes it must not.
USAGE
}

# ---------------------------------------------------------------------------
# the check
# ---------------------------------------------------------------------------

# The rule is a PURE PER-LINE MATCH: one line in, one verdict out, no proximity
# window and no multi-line aggregation. There is no "within N lines" rule that
# could borrow the next call site's guard and read a defective line as clean.
#
# The one exemption is a WHOLE-LINE comment (first non-blank characters are
# `//`), because Go has no way to put code on such a line, and this file's own
# prose has to be able to name the pattern it forbids. A code line with a
# TRAILING comment is still code and still matches; the self-test pins both
# halves of that boundary.
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

  command awk '
    {
      line = $0
      sub(/\r$/, "", line)
      if (line ~ /^[ \t]*\/\//) next
      if (line ~ /Set(Read|Write)Deadline\(time\.Now\(\)\.Add\(/) {
        printf "%s:%d: %s\n", FILENAME, FNR, line
        bad++
      }
    }
    END {
      if (bad > 0) {
        printf "FAIL: ws-read-deadline - %d site(s) set a WebSocket deadline to now+duration. Use hangGuardDeadline(t) (backend/internal/handlers/backup_ws_cap_test.go): an absolute deadline a passing run never consults, so a loaded runner cannot turn correct code red.\n", bad
        exit 1
      }
      printf "ws-read-deadline: %d file(s) scanned, no Set{Read,Write}Deadline(time.Now().Add( outside a comment\n", ARGC - 1
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

selftest() {
  ST_DIR="$(command mktemp -d)" || {
    echo "FAIL: ws-read-deadline self-test - could not create a temp directory"
    exit 2
  }
  trap 'command rm -rf "$ST_DIR"' EXIT
  ST_RUN=0
  ST_FAILS=0

  local literal guarded vardur absvar barenow writedl commentonly trailing nested prodonly noTests

  # --- the offending shape, and its converted twin
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

  selftest_case literal-5s          1 "$literal"     'a_test\.go:[0-9]+:.*SetReadDeadline'
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
