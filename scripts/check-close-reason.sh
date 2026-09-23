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
#       PreToolUse hook. Reads Claude Code's hook JSON on stdin
#       (tool_input.command, cwd); Claude Code sets no per-parameter env vars.
#       Gates every `bd close|done` in command position: after ; && || |
#       newline or a paren, behind VAR=, env, rtk, command, time, eval, a
#       reserved word (! { if then elif else while until do), or inside
#       bash|sh -c "...". Unquoted redirections, backslash-newlines and #
#       comments are dropped first; none of them reach bd as arguments. The
#       type lookup and --reason-file follow the JSON cwd, any cd/pushd in the
#       chain, and bd -C. Anything that is not a close passes untouched. A
#       missing field, an unreadable input or an unparseable line exits 2
#       (Claude Code's "block, feed stderr back" status). Refused rather than
#       waved through, because the hook cannot read the reason: `--reason-file -`,
#       `xargs bd close`, and any
#       `bd ... close` the walk cannot follow (behind a wrapper it does not
#       know, e.g. timeout/nice/sudo -u, or inside $(...), backticks or <(...)).
#       -m/--message/--resolution/--comment count as --reason (bd aliases).
#       Known gaps: a quoted "<<EOF" is read as a heredoc, a `cd` inside a
#       subshell or behind a failing && is still applied, and `-C` after
#       --reason-file is applied too late (agent-os-jox1); bd update --status
#       closed and bd batch are not gated (agent-os-51ua).
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

usage() { command sed -n '2,/^set -euo pipefail$/p' "$0" | command sed '$d; s/^# \{0,1\}//'; }

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

# bead_type <id> [dir] ; prints the type, or returns 2 with a message.
bead_type() {
  local id=$1 dir=${2:-$PWD} out
  out=$(bd -C "$dir" show "$id" --json 2>/dev/null) || { echo "close-reason: could not read bead ${id} with 'bd -C ${dir} show --json' (wrong directory? bd resolves ids per cwd); pass --type to override" >&2; return 2; }
  command printf '%s' "$out" | node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const r=JSON.parse(s);const t=(Array.isArray(r)?r[0]:r)||{};if(!t.issue_type){process.exit(2)}process.stdout.write(String(t.issue_type))})' \
    || { echo "close-reason: 'bd -C ${dir} show ${id} --json' carried no issue_type; pass --type to override" >&2; return 2; }
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
#
# Claude Code hands a PreToolUse hook its input as JSON on stdin
# (tool_name, tool_input.command, cwd) and sets no per-parameter env vars.
# This hook used to read $CLAUDE_HOOK_TOOL_PARAMETERS_command, so it never saw
# a command and passed every close (agent-os-ffy4). The parser below is
# ported from SpecTacular's check-close-reason.sh (agent-os-fbwg, 655d2041),
# close path only.
#
# It writes NUL-separated records:
#   REFUSE <message>
#   CHECK <dir> <id> <reason>
HOOK_PARSER=$(command cat <<'PY_'
import json, os, re, shlex, sys

def out(*f):
    sys.stdout.write("\0".join(f) + "\0")

def refuse(msg):
    out("REFUSE", msg)

try:
    ev = json.loads(sys.stdin.read())
    if not isinstance(ev, dict):
        raise ValueError
except Exception:
    refuse("close-reason hook: stdin is not the hook's JSON object; refusing rather than guessing")
    sys.exit(0)

if ev.get("tool_name") != "Bash":
    sys.exit(0)
cmd = (ev.get("tool_input") or {}).get("command")
if not isinstance(cmd, str):
    sys.exit(0)
base = ev.get("cwd") or os.getcwd()

def resolve(cur, target):
    if cur is None:
        return None
    t = os.path.expanduser(os.path.expandvars(target))
    return os.path.normpath(os.path.join(cur, t))

OPS = set(";&|()\n")
# Words that leave the next word in command position.
WRAP = {"env", "rtk", "command", "time", "nohup", "exec", "builtin", "sudo", "eval",
        "!", "{", "if", "then", "elif", "else", "while", "until", "do"}
GLOBAL_VAL = {"--db", "--actor", "--dolt-auto-commit"}
CLOSE_VAL = GLOBAL_VAL | {"--session"}
REASON_FLAGS = {"-r", "--reason", "-m", "--message", "--resolution", "--comment"}
SHELLS = ("bash", "sh", "zsh", "dash")
# A bd close inside a command or process substitution: the walk cannot see
# into it, so the backstop below refuses it.
SUBST_CLOSE = re.compile(r"(\$\(|`|<\(|>\()\s*(\S*/)?bd\s(.*\s)?(close|done)(\s|\)|`|$)", re.S)
HIDDEN = ("close-reason hook: this command runs 'bd close' in a form the hook cannot follow "
          "(behind a wrapper such as timeout/nice/sudo/xargs, or inside $(...), backticks or <(...)), "
          "so it cannot check the reason. Run it as a plain 'bd close <id> --reason-file <path>'.")

def hidden_close(seg):
    """Backstop: an unquoted `bd` word followed later in the same simple
    command by `close`/`done`. The walk found no bd in command position, so
    something in front of it (a wrapper the WRAP set does not know, or a
    wrapper flag that takes a value) hid it. Quoted mentions ("bd close ...")
    are one token and never match."""
    for k, w in enumerate(seg):
        if os.path.basename(w) == "bd" and any(t in ("close", "done") for t in seg[k + 1:]):
            return True
    return False

def tokenize(s):
    lex = shlex.shlex(s, posix=True, punctuation_chars=";&|()\n")
    lex.whitespace = " \t\r"
    lex.whitespace_split = True
    # strip_redirs drops comments; shlex's own would swallow the newline and
    # hide the next line's command inside this one.
    lex.commenters = ""
    return list(lex)

REDIR_OP = re.compile(r"&>>|&>|>>|>\||>&|<&|<>|<<<|>|<")
WORD_END = " \t\r\n;&|()<>"

def strip_redirs(s):
    """Drop what the shell never passes to a command as an argument: unquoted
    redirections with their target (2>&1, 2>/dev/null, > out.txt, &>>log,
    <<< word), backslash-newline, and word-start # comments. Quoted text is
    copied as is. Heredocs and process substitution are left alone.
    Unterminated quotes are copied, so shlex still fails on them."""
    out, i, n = [], 0, len(s)
    wstart, wplain = 0, True

    def skip_quoted(j):
        q = s[j]
        if q == "'":
            k = s.find("'", j + 1)
            return n if k < 0 else k + 1
        j += 1
        while j < n and s[j] != '"':
            j += 2 if s[j] == "\\" else 1
        return min(j + 1, n)

    def skip_word(j):
        while j < n and s[j] in " \t":
            j += 1
        while j < n and s[j] not in WORD_END:
            if s[j] in "'\"":
                j = skip_quoted(j)
            else:
                j += 2 if s[j] == "\\" else 1
        return j

    while i < n:
        c = s[i]
        if c == "\\":
            if s.startswith("\\\n", i):
                i += 2; continue
            out.append(s[i:i + 2]); wplain = False; i += 2; continue
        if c in "'\"":
            k = skip_quoted(i)
            out.append(s[i:k]); wplain = False; i = k; continue
        if c == "#" and len(out) == wstart:
            k = s.find("\n", i)
            i = n if k < 0 else k; continue
        if c in "<>" and s[i + 1:i + 2] == "(" or s.startswith("<<", i) and not s.startswith("<<<", i):
            out.append(s[i:i + 2]); wplain = False; i += 2; continue
        if c in "<>" or c == "&" and s[i + 1:i + 2] == ">" and not (out and out[-1] == "&"):
            op = REDIR_OP.match(s, i).group(0)
            w = "".join(out[wstart:])
            if wplain and w.isdigit():
                del out[wstart:]
            i += len(op)
            m = re.compile(r"\d+-?|-").match(s, i) if op in (">&", "<&") else None
            i = m.end() if m else skip_word(i)
            out.append(" "); wstart, wplain = len(out), True; continue
        out.append(c); i += 1
        if c in WORD_END:
            wstart, wplain = len(out), True
    return "".join(out)

def strip_heredocs(s):
    """Drop here-document bodies: they are data, not shell words, and an
    apostrophe in one would make shlex fail. Unterminated -> unchanged."""
    lines, kept, pending = s.split("\n"), [], []
    for line in lines:
        if pending:
            if line.lstrip("\t") == pending[0]:
                pending.pop(0)
            continue
        kept.append(line)
        pending += [m.group(3) for m in re.finditer(r"(?<!<)<<(?!<)(-?)\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\2", line)]
    return s if pending else "\n".join(kept)

def segments(toks):
    seg = []
    for t in toks:
        if t and all(c in OPS for c in t):
            if seg:
                yield seg
            seg = []
        else:
            seg.append(t)
    if seg:
        yield seg

def take_dir(args, j, d):
    """-C/--directory handling; returns (new_j, new_d) or None."""
    a = args[j]
    if a in ("-C", "--directory"):
        return j + 2, resolve(d, args[j + 1] if j + 1 < len(args) else "")
    if a.startswith("--directory="):
        return j + 1, resolve(d, a.split("=", 1)[1])
    if a.startswith("-C") and len(a) > 2:
        return j + 1, resolve(d, a[2:].lstrip("="))
    return None

def bd_close(d, args):
    ids, reasons = [], []
    j = 0
    while j < len(args):
        a = args[j]
        r = take_dir(args, j, d)
        if r:
            j, d = r
            continue
        if a in ("-h", "--help"):
            return
        # bd 1.1.2 also takes -m/--message/--resolution/--comment as hidden
        # aliases of --reason (reviewer finding, agent-os-ffy4); an unknown
        # one would make bd itself fail, so treating them as reasons is safe.
        if a in REASON_FLAGS:
            reasons.append(args[j + 1] if j + 1 < len(args) else ""); j += 2; continue
        if "=" in a and a.split("=", 1)[0] in REASON_FLAGS:
            reasons.append(a.split("=", 1)[1]); j += 1; continue
        if a.startswith("-r") and not a.startswith("--") and len(a) > 2:
            reasons.append(a[2:][1:] if a[2] == "=" else a[2:]); j += 1; continue
        if a == "--reason-file" or a.startswith("--reason-file="):
            if "=" in a:
                p = a.split("=", 1)[1]; j += 1
            else:
                p = args[j + 1] if j + 1 < len(args) else ""; j += 2
            if p == "-":
                refuse("close-reason hook: --reason-file - (stdin) cannot be read by a hook; write the reason to a file or pass --reason")
                return
            fp = resolve(d, p) if d else None
            try:
                with open(fp) as fh:
                    reasons.append(fh.read())
            except Exception:
                refuse("close-reason hook: --reason-file '%s' is not readable (resolved to %s)" % (p, fp))
                return
            continue
        if a in CLOSE_VAL:
            j += 2; continue
        if a.startswith("-"):
            j += 1; continue
        ids.append(a); j += 1
    if not ids:
        refuse("close-reason hook: 'bd close' with no id closes the last-touched bead; name the id so its type and reason can be checked")
        return
    # Reasons map positionally, one-for-all when a single reason is given.
    for n, i in enumerate(ids):
        reason = reasons[n] if len(reasons) > 1 and n < len(reasons) else (reasons[0] if len(reasons) == 1 else "")
        out("CHECK", d or "", i, reason)

def walk(s, cur, depth=0):
    flat = strip_redirs(strip_heredocs(s))
    # On the text, not the tokens: shlex splits `bd into its own word. A
    # single-quoted mention of $(bd close ...) is refused too; that costs a
    # rephrase, where missing a real one would pass a bad close.
    if SUBST_CLOSE.search(flat):
        refuse(HIDDEN)
        return cur
    try:
        toks = tokenize(flat)
    except ValueError:
        refuse("close-reason hook: could not parse the command line (unbalanced quotes?); refusing rather than guessing")
        return cur
    for seg in segments(toks):
        i, xargs = 0, False
        while i < len(seg):
            w = seg[i]
            b = os.path.basename(w)
            if re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", w) or w.startswith("-") and i > 0:
                i += 1; continue
            if b in WRAP or (b == "proxy" and i > 0 and os.path.basename(seg[i - 1]) == "rtk"):
                i += 1; continue
            if b == "xargs":
                xargs = True; i += 1; continue
            break
        if i >= len(seg):
            continue
        b, args = os.path.basename(seg[i]), seg[i + 1:]
        if b in ("cd", "pushd"):
            tgt = args[0] if args else "~"
            cur = None if tgt == "-" else resolve(cur, tgt)
            continue
        if b in SHELLS:
            # -c, and combined short flags carrying it (-lc, -ec, -xc).
            k = next((n for n, a in enumerate(args) if re.match(r"^-[A-Za-z]*c[A-Za-z]*$", a)), None)
            if k is not None:
                rest = [a for a in args[k + 1:] if a != "--"]
                if rest and depth < 3:
                    walk(rest[0], cur, depth + 1)
                elif rest:
                    refuse("close-reason hook: shell -c nested too deep to follow; refusing rather than guessing")
                continue
        if b != "bd":
            if hidden_close(seg):
                refuse(HIDDEN)
            continue
        d, j, sub = cur, 0, None
        while j < len(args):
            r = take_dir(args, j, d)
            if r:
                j, d = r
                continue
            a = args[j]
            if a in GLOBAL_VAL:
                j += 2; continue
            if a.startswith("-"):
                j += 1; continue
            sub = a; j += 1
            break
        if sub not in ("close", "done"):
            continue
        if xargs:
            refuse("close-reason hook: 'xargs bd %s' hides the ids from the hook; name each id" % sub)
        else:
            bd_close(d, args[j:])
    return cur

if re.search(r"(^|[^A-Za-z0-9_.-])bd($|[^A-Za-z0-9_-])", cmd):
    walk(cmd, base)
PY_
)

hook_mode() {
  local rc=0 dir id reason type
  local -a recs=()
  # Globals on purpose: an EXIT trap runs after this function's locals are gone.
  local input; input=$(command cat)
  # Most Bash calls never mention bd; skip the parser for them, so a slow or
  # missing python3 can only ever affect commands that do.
  case "$input" in *bd*) ;; *) return 0 ;; esac
  REC_TMP=$(mktemp); REASON_TMP=$(mktemp); trap 'rm -f "$REC_TMP" "$REASON_TMP"' EXIT
  if ! command printf '%s' "$input" | python3 -c "$HOOK_PARSER" > "$REC_TMP"; then
    echo "close-reason hook: the command parser failed; refusing rather than guessing" >&2; return 2
  fi
  mapfile -d '' -t recs < "$REC_TMP"
  local i=0 n=${#recs[@]}
  while [ $i -lt $n ]; do
    case "${recs[$i]}" in
      REFUSE) echo "${recs[$((i+1))]}" >&2; rc=2; i=$((i+2)) ;;
      CHECK)
        dir=${recs[$((i+1))]}; id=${recs[$((i+2))]}; reason=${recs[$((i+3))]:-}
        i=$((i+4))
        if [ -z "$dir" ]; then
          echo "close-reason hook: can't tell which directory 'bd close ${id}' runs in (cd -?); refusing" >&2; rc=2; continue
        fi
        if ! type=$(bead_type "$id" "$dir"); then rc=2; continue; fi
        command printf '%s' "$reason" > "$REASON_TMP"
        if ! check_reason "$type" "$REASON_TMP"; then
          echo "close-reason hook: refusing 'bd close ${id}'" >&2; rc=2
          # The hook sees the reason as typed, so "$(cat f)" is checked as
          # that literal text. Say so, rather than only "fields missing".
          case "$reason" in *'$('*|*'`'*) echo "  If the reason is built by \$(...) or backticks: the hook reads it unexpanded. Write it to a file and pass --reason-file <path>." >&2 ;; esac
        fi ;;
      *) echo "close-reason hook: internal error, unknown record '${recs[$i]}'; refusing" >&2; return 2 ;;
    esac
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
# hook_case <name> <want> <command> [want_msg] : pipes the hook JSON Claude
# Code sends (tool_name, tool_input.command, cwd) into `--hook` as a process.
# Types come from the stub `bd` selftest() puts on PATH: t-bug-* is a bug,
# t-task-* a task, anything else an unknown id.
hook_case() {
  local name=$1 want=$2 cmd=$3 want_msg=${4:-}
  hook_raw "$name" "$want" "$(python3 -c 'import json,sys; print(json.dumps({"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":sys.argv[1]},"cwd":sys.argv[2]}))' "$cmd" "$ST_DIR")" "$want_msg"
}
# hook_raw <name> <want> <stdin> [want_msg] : for input that is not a Bash call.
hook_raw() {
  local name=$1 want=$2 input=$3 want_msg=${4:-} out rc
  out=$(command printf '%s' "$input" | PATH="$ST_DIR/bin:$PATH" bash "$0" --hook 2>&1) && rc=0 || rc=$?
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

  # --- the hook, driven by hook JSON on stdin, the way Claude Code calls it.
  # A stub `bd` answers the type lookup (`bd -C <dir> show <id> --json`).
  command mkdir -p "$ST_DIR/bin"
  command cat > "$ST_DIR/bin/bd" <<'STUB'
#!/usr/bin/env bash
dir=$PWD
while [ $# -gt 0 ]; do case "$1" in -C) dir=$2; shift 2 ;; show) id=$2; shift 2 ;; *) shift ;; esac; done
echo "$dir ${id:-}" >> "$(dirname "$0")/lookups.log"
case "${id:-}" in
  t-bug-*) t=bug ;; t-task-*) t=task ;;
  *) echo '{"error":"no issues found"}'; exit 1 ;;
esac
printf '[{"id":"%s","issue_type":"%s"}]\n' "$id" "$t"
STUB
  command chmod +x "$ST_DIR/bin/bd"
  # The reason is passed the way an agent types it: double-quoted, newlines
  # inside. (bash's %q form, $'...', is not POSIX and the splitter rejects it;
  # that rejection is itself covered by close-unparseable below.)
  local ok; ok=$(command cat "$ST_DIR/complete.txt")
  local G="--reason-file complete.txt" ALL='missing: Class statement, Sweep command, Verbatim output, Verdict'
  hook_case not-a-close               0 "bd show t-bug-1 --json"
  hook_case close-complete-reason     0 "bd close t-bug-1 --reason \"$ok\""
  hook_case close-bare-reason         2 "bd close t-bug-1 --reason 'Fixed it, merged.'"   "$ALL"
  hook_case close-task-bare           0 "bd close t-task-1 -r 'Done.'"                   'not a bug bead'
  hook_case close-reason-file         0 "bd close t-bug-1 --reason-file $ST_DIR/complete.txt"
  hook_case close-reason-file-broken  2 "bd close t-bug-1 --reason-file $ST_DIR/no-verdict.txt" 'missing: Verdict'
  hook_case close-reason-file-rel     0 "bd close t-bug-1 $G"
  hook_case close-reason-stdin        2 "bd close t-bug-1 --reason-file -"               'cannot be read by a hook'
  hook_case close-no-id               2 "bd close --reason 'x'"                          'name the id'
  hook_case close-two-ids-positional  2 "bd close t-bug-1 t-bug-2 -r \"$ok\" -r 'nope'"  "refusing 'bd close t-bug-2'"
  hook_case close-unparseable         2 "bd close t-bug-1 --reason \"unterminated"      'could not parse'
  hook_case close-unknown-id          2 "bd close nope-1 -r 'x'"                         'could not read bead nope-1'
  # --- input handling (agent-os-ffy4): stdin JSON is the only input
  hook_raw  non-bash-tool             0 '{"tool_name":"Write","tool_input":{"file_path":"x","command":"bd close t-bug-1 -r fixed"}}'
  hook_raw  bash-tool-twin            2 '{"tool_name":"Bash","tool_input":{"file_path":"x","command":"bd close t-bug-1 -r fixed"}}' "$ALL"
  hook_raw  not-json-no-bd            0 'ls -la'
  hook_raw  not-json                  2 'bd close t-bug-1 -r fixed'                       'not the hook.s JSON'
  # --- redirections never reach bd as arguments (both sides)
  hook_case redir-2to1-good           0 "bd close t-bug-1 $G 2>&1 | grep -v Warning"
  hook_case redir-2to1-bad            2 "bd close t-bug-1 -r fixed 2>&1"                  "$ALL"
  hook_case redir-devnull-good        0 "bd close t-bug-1 $G 2>/dev/null"
  hook_case redir-file-good           0 "bd close t-bug-1 $G > out.txt"
  hook_case redir-before-id-good      0 "bd close 2>/dev/null t-bug-1 $G"
  hook_case redir-before-id-bad       2 "bd close 2>/dev/null t-bug-1 -r fixed"           "$ALL"
  hook_case redir-herestring-good     0 "bd close t-bug-1 $G <<< 'x y'"
  hook_case redir-hides-id-bad        2 "bd close t-task-1 -r ok \">\" t-bug-1"          "refusing 'bd close t-bug-1'"
  hook_case quoted-redir-reason-bad   2 "bd close t-bug-1 -r '2>&1'"                      "$ALL"
  # --- comments and continuations
  hook_case continuation-good         0 "bd close t-bug-1 \\
  $G"
  hook_case continuation-bad          2 "bd close t-bug-1 \\
  -r fixed"                                                                                   "$ALL"
  hook_case comment-good              0 "bd close t-bug-1 $G # closing it"
  hook_case comment-then-line-bad     2 "bd show x # note
bd close t-bug-1 -r fixed"                                                                    "$ALL"
  hook_case midword-hash-id-good      0 "bd close t-bug-1#3 $G"
  hook_case midword-hash-id-bad       2 "bd close t-bug-1#3 -r fixed"                     "$ALL"
  hook_case quoted-hash-reason-bad    2 "bd close t-bug-1 -r \"#1 fixed\""               "$ALL"
  # --- bd in command position behind a wrapper or reserved word
  hook_case brace-group-good          0 "{ bd close t-bug-1 $G; }"
  hook_case brace-group-bad           2 "{ bd close t-bug-1 -r fixed; }"                  "$ALL"
  hook_case if-then-bad               2 "if true; then bd close t-bug-1 -r fixed; fi"     "$ALL"
  hook_case for-do-bad                2 "for i in 1; do bd close t-bug-1 -r fixed; done"  "$ALL"
  hook_case bang-bad                  2 "! bd close t-bug-1 -r fixed"                     "$ALL"
  hook_case eval-bad                  2 "eval bd close t-bug-1 -r fixed"                  "$ALL"
  hook_case or-chain-bad              2 "true || bd close t-bug-1 -r fixed"               "$ALL"
  hook_case cmd-subst-bad             2 "x=\$(bd close t-bug-1 -r fixed)"                 'cannot follow'
  hook_case env-cd-chain-bad          2 "cd /tmp && FOO=1 command bd close t-bug-1 -r fixed" "$ALL"
  hook_case bash-c-bad                2 "bash -c 'bd close t-bug-1 -r fixed'"             "$ALL"
  # --- bd done, bd -C, --db, and the directory the close runs in
  hook_case done-good                 0 "bd done t-bug-1 $G"
  hook_case done-bad                  2 "bd done t-bug-1 -r fixed"                        "$ALL"
  hook_case db-value-good             0 "bd --db x close t-bug-1 $G"
  hook_case db-value-bad              2 "bd --db x close t-bug-1 -r fixed"                "$ALL"
  # ./r.txt is incomplete and sub/r.txt complete: the verdict shows which one
  # the hook read. The lookup log shows where the type lookup ran.
  command mkdir -p "$ST_DIR/sub"
  command cp "$ST_DIR/no-verdict.txt" "$ST_DIR/r.txt"; command cp "$ST_DIR/complete.txt" "$ST_DIR/sub/r.txt"
  hook_case cwd-reason-file-here      2 "bd close t-bug-1 --reason-file r.txt"            'missing: Verdict'
  hook_case cd-reason-file-sub        0 "cd sub && bd close t-bug-1 --reason-file r.txt"
  hook_case dash-C-reason-file-sub    0 "bd -C sub close t-bug-1 --reason-file r.txt"
  hook_case dash-C-bad                2 "bd -C sub close t-bug-1 -r fixed"                "$ALL"
  hook_case close-dash-C-sub          0 "bd close -C sub t-bug-1 --reason-file r.txt"
  : > "$ST_DIR/bin/lookups.log"
  hook_case cd-type-lookup            0 "cd sub && bd close t-task-7 -r 'Done.'"          'not a bug bead'
  ST_RUN=$((ST_RUN + 1))
  if ! command grep -qx "$ST_DIR/sub t-task-7" "$ST_DIR/bin/lookups.log"; then
    echo "FAIL: close-reason self-test - control 'cd-type-lookup-dir': the type lookup did not run in $ST_DIR/sub; lookups:"; command sed 's/^/    /' "$ST_DIR/bin/lookups.log"
    ST_FAILS=$((ST_FAILS + 1))
  fi
  hook_case cd-dash-refused           2 "cd - && bd close t-bug-1 -r \"$ok\""                     'which directory'
  # --- heredoc bodies are data: an apostrophe in one must not break the parse
  hook_case heredoc-then-close-good   0 "cat <<'EOF'
it's data
EOF
bd close t-bug-1 $G"
  hook_case heredoc-then-close-bad    2 "cat <<'EOF'
it's data
EOF
bd close t-bug-1 -r fixed"                                                                    "$ALL"
  # --- reason aliases, and a reason the hook cannot read
  command printf '%s\nSee `command grep` and $(this) in markdown.\n' "$ok" > "$ST_DIR/backticks.txt"
  hook_case reason-file-backticks-ok  0 "bd close t-bug-1 --reason-file backticks.txt"
  hook_case reason-alias-good         0 "bd close t-bug-1 -m \"$ok\""
  hook_case reason-alias-bad          2 "bd close t-bug-1 --resolution fixed"             "$ALL"
  hook_case reason-subst-refused      2 "bd close t-bug-1 -r \"\$(cat complete.txt)\""   'reads it unexpanded'
  # --- the backstop: a close the walk cannot follow is refused, not passed
  local H='cannot follow'
  hook_case xargs-refused             2 "echo t-bug-1 | xargs bd close -r fixed"          'xargs bd close'
  hook_case wrap-timeout              2 "timeout 5 bd close t-bug-1 -r fixed"             "$H"
  hook_case wrap-nice-flag            2 "nice -n 5 bd close t-bug-1 -r fixed"             "$H"
  hook_case wrap-sudo-u               2 "sudo -u x bd close t-bug-1 -r fixed"             "$H"
  hook_case wrap-xargs-n              2 "xargs -n 1 bd close t-bug-1 -r fixed"            "$H"
  hook_case subst-backticks           2 "echo \`bd close t-bug-1 -r fixed\`"            "$H"
  hook_case subst-dq-dollar           2 "echo \"\$(bd close t-bug-1 -r fixed)\""       "$H"
  hook_case shell-lc-bad              2 "bash -lc 'bd close t-bug-1 -r fixed'"            "$ALL"
  hook_case shell-lc-good             0 "bash -lc 'bd close t-bug-1 $G'"
  hook_case shell-c-dashdash-bad      2 "sh -c -- 'bd close t-bug-1 -r fixed'"            "$ALL"
  # --- a mention of bd close that is not a call stays ungated
  hook_case grep-mention              0 "command grep -rn 'bd close' scripts"
  hook_case wrapper-no-close          0 "timeout 5 bd show t-bug-1"
  hook_case echo-mention              0 "echo 'bd close t-bug-1 -r fixed'"
  hook_case commit-msg-mention        0 "git commit -m \"bd close t-bug-1 -r fixed\""

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
