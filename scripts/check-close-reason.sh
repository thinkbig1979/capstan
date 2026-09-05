#!/usr/bin/env bash
# scripts/check-close-reason.sh — a bug bead's close reason carries the four
# class-sweep fields, or the close is refused (agent-os-o1jp.6).
#
# CLAUDE.md, "Beads: closing a bug bead", requires every bug bead's close
# reason to state (1) a class statement, (2) the sweep command, (3) its
# verbatim output, (4) a verdict. agent-os-o1jp.4 put that rule in CLAUDE.md,
# which nothing reads. This script is the reader.
#
# WHY NOT CI: the tracker is not reachable from a runner. `.beads/` is
# gitignored (.gitignore:97), `git ls-files .beads` is empty, no workflow
# installs `bd` or holds a deploy key for the dolt remote — and beads close
# ~30 s AFTER their PR merges (agent-os-8uuw closed_at 07:12:46Z, its merge
# commit db4574f 07:12:14Z), so a PR-time check could never see a close
# reason even with a tracker. The gate that fires on every close that matters
# is therefore LOCAL: a PreToolUse hook on `bd close` (see --hook below and
# the PROPOSED settings entry in the o1jp.6 report). A human typing `bd close`
# in a bare shell is not gated by anything; that limit is stated, not hidden.
#
# Usage:
#   check-close-reason.sh <bead-id> [--type bug|task|...] [--reason-file <path>|-]
#       Reads the reason from --reason-file (or stdin when omitted or `-`),
#       looks the bead's type up with `bd show <id> --json` unless --type is
#       given, and exits 0 (all fields present, or not a bug bead) or 1 (a bug
#       bead missing one or more fields, each named on stderr). 2 = usage, or
#       the type could not be determined (fails closed, never passes).
#   check-close-reason.sh --hook
#       PreToolUse shape: reads the Bash command from
#       $CLAUDE_HOOK_TOOL_PARAMETERS_command (the same env-var convention this
#       repo's existing Write|Edit hook uses). Anything that is not a
#       `bd close` passes untouched. For `bd close <ids> --reason/-r <text>`
#       or `--reason-file <path>` every id is checked; a missing field exits 2
#       (Claude Code's "block, feed stderr back" status). `--reason-file -`
#       cannot be read by a hook and is refused rather than waved through.
#   check-close-reason.sh --self-test
#       Runs the fixtures below (files in a temp dir, no tracker needed) and
#       exits 0 with a tally, 1 naming the control that misbehaved.
#
# FIELD MARKERS, matched loosely and case-insensitively, one per field. They
# are what a close reason has to contain; the wording is free:
#   Class statement  a line beginning "class statement" (or "class:")
#   Sweep command    a line beginning "sweep", AND somewhere a command line:
#                    a ``` fence, or a line naming grep/rg/git grep/find
#   Verbatim output  a line beginning "verbatim"/"output", or an output
#                    line starting with "->", "=>" or "→" (the shape closes
#                    in this tracker already use under SWEEP)
#   Verdict          a line beginning "verdict", or the phrase "further sites"
# OBSERVED 2026-09-05: agent-os-hpe9's real close reason has exactly this
# shape (CLASS STATEMENT: / SWEEP (scope ...): / indented command grep /
# "->" lines / VERDICT:) and passes; the same text with its VERDICT line
# removed fails naming "Verdict".

set -euo pipefail

usage() { command sed -n '2,45p' "$0" | command sed 's/^# \{0,1\}//'; }

# --- the check -------------------------------------------------------------

# check_reason <type> <reason-file> ; prints its verdict, exits 0/1.
check_reason() {
  local type=$1 file=$2 missing=() text
  if [ "$type" != "bug" ]; then
    echo "close-reason: not a bug bead (type: ${type}); the class-sweep fields are not required"
    return 0
  fi
  text=$(command cat "$file")
  has() { command printf '%s\n' "$text" | command grep -qiE "$1"; }
  # A field header may sit behind a list marker ("1. ", "- ") and/or bold
  # markers, the way CLAUDE.md's own block writes them.
  local H='^[[:space:]]*([0-9]+[.)][[:space:]]*|[-*][[:space:]]+)?(\*\*)?'

  has "${H}class([[:space:]]+statement)?(\*\*)?[[:space:]]*[:—-]" || missing+=("Class statement")
  if has "${H}sweep" && has '^[[:space:]]*```|(^|[^a-z])(command[[:space:]]+)?(grep|rg|git[[:space:]]+grep|find)([[:space:]]|$)'; then :; else missing+=("Sweep command"); fi
  has "${H}(verbatim|output)|^[[:space:]]*(->|=>|→)" || missing+=("Verbatim output")
  has "${H}verdict|further sites" || missing+=("Verdict")

  if [ "${#missing[@]}" -eq 0 ]; then
    echo "close-reason: all four class-sweep fields present (Class statement, Sweep command, Verbatim output, Verdict)"
    return 0
  fi
  {
    echo "close-reason: REFUSED - a bug bead's close reason is missing: $(IFS=,; echo "${missing[*]}" | command sed 's/,/, /g')"
    echo "  Required by CLAUDE.md 'Beads: closing a bug bead': Class statement / Sweep command (command grep ..., receiver-agnostic, with a positive control) / Verbatim output / Verdict ('0 further sites' or the follow-up bead ids)."
  } >&2
  return 1
}

bead_type() {
  local id=$1 out
  out=$(bd show "$id" --json 2>/dev/null) || { echo "close-reason: could not read bead ${id} with 'bd show --json'; pass --type to override" >&2; return 2; }
  command printf '%s' "$out" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const r=JSON.parse(s);const t=(Array.isArray(r)?r[0]:r)||{};if(!t.issue_type){process.exit(2)}process.stdout.write(String(t.issue_type))})' \
    || { echo "close-reason: 'bd show ${id} --json' carried no issue_type; pass --type to override" >&2; return 2; }
}

run_check() {
  local id="" type="" reason_file="-"
  while [ $# -gt 0 ]; do
    case "$1" in
      --type) type=${2:-}; shift 2 ;;
      --type=*) type=${1#--type=}; shift ;;
      --reason-file) reason_file=${2:-}; shift 2 ;;
      --reason-file=*) reason_file=${1#--reason-file=}; shift ;;
      -h|--help) usage; exit 0 ;;
      -*) echo "close-reason: unknown option '$1'" >&2; usage >&2; exit 2 ;;
      *) if [ -n "$id" ]; then echo "close-reason: one bead id at a time" >&2; exit 2; fi; id=$1; shift ;;
    esac
  done
  [ -n "$id" ] || { echo "close-reason: a bead id is required" >&2; usage >&2; exit 2; }
  if [ -z "$type" ]; then type=$(bead_type "$id") || exit 2; fi
  # Global on purpose: an EXIT trap runs after this function's locals are gone.
  REASON_TMP=$(mktemp); trap 'rm -f "$REASON_TMP"' EXIT
  if [ "$reason_file" = "-" ]; then command cat > "$REASON_TMP"; else command cat "$reason_file" > "$REASON_TMP"; fi
  check_reason "$type" "$REASON_TMP"
}

# --- --hook: PreToolUse on Bash ---------------------------------------------

hook_mode() {
  local cmd=${CLAUDE_HOOK_TOOL_PARAMETERS_command:-} type_override=${CLOSE_REASON_TYPE:-}
  # Not a bd close: nothing to gate. `bd` may be prefixed by env/cd chains;
  # the loose match is deliberate, the strict parse below decides.
  command printf '%s' "$cmd" | command grep -qE '(^|[;&|[:space:]])bd[[:space:]]+(close|done)([[:space:]]|$)' || return 0
  # Split like a shell would, then walk the tokens after `bd close`.
  local -a ids=() reasons=() files=() toks=()
  local tok i n
  # Through a file, not `mapfile < <(...)` and not a variable: a process
  # substitution's failure is invisible to mapfile (OBSERVED on the first
  # self-test run: an unbalanced quote became "no id given"), and a command
  # substitution drops the NUL separators (OBSERVED on the second). Tokens are
  # NUL-separated so a reason containing newlines stays one token.
  TOK_TMP=$(mktemp)
  if ! command printf '%s' "$cmd" | python3 -c 'import shlex,sys; sys.stdout.write("\0".join(shlex.split(sys.stdin.read())))' > "$TOK_TMP" 2>/dev/null; then
    rm -f "$TOK_TMP"
    echo "close-reason hook: could not parse the command line (unbalanced quotes?); refusing rather than guessing" >&2; return 2
  fi
  mapfile -d '' -t toks < "$TOK_TMP"; rm -f "$TOK_TMP"
  n=${#toks[@]}; i=0
  while [ $i -lt $n ] && [ "${toks[$i]}" != "close" ] && [ "${toks[$i]}" != "done" ]; do i=$((i+1)); done
  i=$((i+1))
  while [ $i -lt $n ]; do
    tok=${toks[$i]}
    case "$tok" in
      -r|--reason) reasons+=("${toks[$((i+1))]:-}"); i=$((i+2)); continue ;;
      --reason=*) reasons+=("${tok#--reason=}") ;;
      --reason-file) files+=("${toks[$((i+1))]:-}"); i=$((i+2)); continue ;;
      --reason-file=*) files+=("${tok#--reason-file=}") ;;
      --actor|--db|--session) i=$((i+2)); continue ;;
      -*) ;;
      *) ids+=("$tok") ;;
    esac
    i=$((i+1))
  done
  if [ "${#ids[@]}" -eq 0 ]; then
    echo "close-reason hook: 'bd close' with no id closes the last-touched bead; name the id so its type and reason can be checked" >&2; return 2
  fi
  for f in "${files[@]:-}"; do
    [ -z "$f" ] && continue
    if [ "$f" = "-" ]; then echo "close-reason hook: --reason-file - (stdin) cannot be read by a hook; write the reason to a file or pass --reason" >&2; return 2; fi
    [ -r "$f" ] || { echo "close-reason hook: --reason-file '$f' is not readable" >&2; return 2; }
    reasons+=("$(command cat "$f")")
  done
  local rc=0 idx=0 type
  REASON_TMP=$(mktemp); trap 'rm -f "$REASON_TMP"' EXIT
  for id in "${ids[@]}"; do
    if [ -n "$type_override" ]; then type=$type_override; else type=$(bead_type "$id") || return 2; fi
    # Reasons map positionally, one-for-all when a single reason is given.
    if [ "${#reasons[@]}" -gt 1 ]; then command printf '%s' "${reasons[$idx]:-}" > "$REASON_TMP"; else command printf '%s' "${reasons[0]:-}" > "$REASON_TMP"; fi
    if ! check_reason "$type" "$REASON_TMP"; then rc=2; echo "close-reason hook: refusing 'bd close ${id}'" >&2; fi
    idx=$((idx+1))
  done
  return $rc
}

# --- --self-test -------------------------------------------------------------
#
# Every control is TWO-SIDED: each passing fixture has a twin whose only
# difference is the field under test, and each refusal must NAME the field
# (exit status alone cannot tell "missing Verdict" from "read nothing").

ST_RUN=0; ST_FAILS=0
selftest_case() {
  local name=$1 want=$2 type=$3 file=$4 want_msg=${5:-} out rc
  # `&& rc=0 || rc=$?`, not `; rc=$?`: under set -e a non-zero substitution
  # assignment exits the script, and the self-test would die silently on its
  # first expected refusal (OBSERVED on the first run of this file).
  out=$(check_reason "$type" "$file" 2>&1) && rc=0 || rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || command printf '%s\n' "$out" | command grep -qE "$want_msg"; then return 0; fi
    echo "FAIL: close-reason self-test - control '$name' exited $rc as expected but did not explain itself; wanted a message matching /$want_msg/"
  else
    echo "FAIL: close-reason self-test - control '$name' expected exit $want, got $rc"
  fi
  echo "  check said:"; command printf '%s\n' "$out" | command sed 's/^/    /'
  ST_FAILS=$((ST_FAILS + 1))
}
hook_case() {
  local name=$1 want=$2 type=$3 cmd=$4 want_msg=${5:-} out rc
  out=$(CLAUDE_HOOK_TOOL_PARAMETERS_command="$cmd" CLOSE_REASON_TYPE="$type" hook_mode 2>&1) && rc=0 || rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || command printf '%s\n' "$out" | command grep -qE "$want_msg"; then return 0; fi
    echo "FAIL: close-reason self-test - hook control '$name' exited $rc as expected but did not explain itself; wanted /$want_msg/"
  else
    echo "FAIL: close-reason self-test - hook control '$name' expected exit $want, got $rc"
  fi
  echo "  hook said:"; command printf '%s\n' "$out" | command sed 's/^/    /'
  ST_FAILS=$((ST_FAILS + 1))
}

selftest() {
  local ST_DIR
  ST_DIR=$(mktemp -d) || { echo "FAIL: close-reason self-test - could not create a temp directory"; return 1; }
  trap 'rm -rf "$ST_DIR"' RETURN

  # A complete reason in the shape agent-os-hpe9's real close reason uses.
  command cat > "$ST_DIR/complete.txt" <<'R'
MERGED as 80a393b (PR #300). The fix and its evidence go here.

CLASS STATEMENT: a reconnect counter zeroed on a signal the server emits before the handler can refuse.
SWEEP (scope frontend/src, non-test; positive control = the same command at d6b4789 returned the onopen site :73):
  command grep -rn 'reconnectAttempts = \|new WebSocket(' frontend/src --include=*.ts --include=*.tsx | command grep -v __tests__
  -> ws.ts:61 (field), :77 (connect(): fresh ladder), :98 (the only new WebSocket( in the app)
VERDICT: 0 further sites. Follow-up filed: agent-os-e06q (P4).
R
  # Twins: the same text minus exactly one field.
  command grep -viE '^verdict' "$ST_DIR/complete.txt" > "$ST_DIR/no-verdict.txt"
  command grep -viE '^class statement' "$ST_DIR/complete.txt" > "$ST_DIR/no-class.txt"
  command grep -vE '^  -> ' "$ST_DIR/complete.txt" > "$ST_DIR/no-output.txt"
  command grep -vE 'command grep' "$ST_DIR/complete.txt" > "$ST_DIR/no-command.txt"
  command grep -viE '^sweep' "$ST_DIR/complete.txt" > "$ST_DIR/no-sweep-header.txt"
  # The markdown-flavoured shape CLAUDE.md's block itself uses.
  command cat > "$ST_DIR/markdown.txt" <<'R'
1. **Class statement** — a WebSocket handler that upgrades but does not guarantee close on every exit path.
2. **Sweep command** —
```
command grep -rn 'Upgrade(' backend/internal/handlers/
```
3. **Verbatim output** —
```
dashboard.go:41
logs.go:88
```
4. **Verdict** — 0 further sites.
R
  # A reason with none of it, and an empty one.
  command printf 'Fixed the bug, tests green, merged as abc1234.\n' > "$ST_DIR/bare.txt"
  : > "$ST_DIR/empty.txt"

  # --- the three the bead asks for, each with its twin
  selftest_case complete-bug          0 bug  "$ST_DIR/complete.txt"   'all four class-sweep fields present'
  selftest_case bug-missing-verdict   1 bug  "$ST_DIR/no-verdict.txt" 'missing: Verdict$'
  selftest_case task-bare             0 task "$ST_DIR/bare.txt"       'not a bug bead \(type: task\)'
  selftest_case bug-bare              1 bug  "$ST_DIR/bare.txt"       'missing: Class statement, Sweep command, Verbatim output, Verdict'
  # --- every other field, one at a time, named
  selftest_case bug-missing-class     1 bug  "$ST_DIR/no-class.txt"   'missing: Class statement$'
  selftest_case bug-missing-output    1 bug  "$ST_DIR/no-output.txt"  'missing: Verbatim output$'
  selftest_case bug-missing-command   1 bug  "$ST_DIR/no-command.txt" 'missing: Sweep command$'
  selftest_case bug-missing-sweep-hdr 1 bug  "$ST_DIR/no-sweep-header.txt" 'missing: Sweep command$'
  # --- shape tolerance and the empty edge
  selftest_case markdown-shape        0 bug  "$ST_DIR/markdown.txt"   'all four class-sweep fields present'
  selftest_case bug-empty             1 bug  "$ST_DIR/empty.txt"      'missing: Class statement, Sweep command, Verbatim output, Verdict'
  selftest_case task-empty            0 task "$ST_DIR/empty.txt"      'not a bug bead'
  selftest_case epic-complete         0 epic "$ST_DIR/complete.txt"   'not a bug bead \(type: epic\)'

  # --- the hook: only `bd close` is gated, and it decides on the reason
  # The reason is passed the way an agent types it: double-quoted, newlines
  # inside. (bash's %q form, $'...', is not POSIX and the splitter rejects it;
  # that rejection is itself covered by the last control below.)
  local ok; ok=$(command cat "$ST_DIR/complete.txt")
  hook_case not-a-close               0 bug  "bd show agent-os-x --json"
  hook_case close-complete-reason     0 bug  "bd close agent-os-x --reason \"$ok\""
  hook_case close-bare-reason         2 bug  "bd close agent-os-x --reason 'Fixed it, merged.'"     'missing: Class statement, Sweep command, Verbatim output, Verdict'
  hook_case close-task-bare           0 task "bd close agent-os-x -r 'Done.'"                        'not a bug bead'
  hook_case close-reason-file         0 bug  "bd close agent-os-x --reason-file $ST_DIR/complete.txt"
  hook_case close-reason-file-broken  2 bug  "bd close agent-os-x --reason-file $ST_DIR/no-verdict.txt" 'missing: Verdict'
  hook_case close-reason-stdin        2 bug  "bd close agent-os-x --reason-file -"                   'cannot be read by a hook'
  hook_case close-no-id               2 bug  "bd close --reason 'x'"                                 'name the id'
  hook_case close-two-ids-positional  2 bug  "bd close agent-os-a agent-os-b -r \"$ok\" -r 'nope'" "refusing 'bd close agent-os-b'"
  hook_case close-unparseable         2 bug  "bd close agent-os-x --reason \"unterminated"          'could not parse'

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "FAIL: close-reason self-test - $ST_FAILS of $ST_RUN control(s) failed; the close-reason check does not behave as documented"
    return 1
  fi
  echo "close-reason self-test: $ST_RUN control(s) passed, each proven both ways"
  return 0
}

case "${1:-}" in
  --self-test) selftest; exit $? ;;
  --hook) hook_mode; exit $? ;;
  -h|--help|'') usage; [ -n "${1:-}" ] && exit 0; exit 2 ;;
  *) run_check "$@" ;;
esac
