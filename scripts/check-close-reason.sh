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
#       newline or a paren, behind VAR=, env, rtk, command, time, a reserved
#       word (! { if then elif else while until do), and inside the script
#       text of eval or bash|sh -c "..." (also -lc, -ec, -c --). Unquoted
#       redirections, backslash-newlines and # comments are dropped first;
#       none of them reach bd as arguments. The type lookup and --reason-file
#       follow the JSON cwd, any cd/pushd in the chain, and bd -C. A missing
#       field, an unreadable input or an unparseable line exits 2 (Claude
#       Code's "block, feed stderr back" status).
#       Also refused, because bd takes no reason there: `bd update <bug>
#       --status closed`, and `bd batch` with a close line (stdin or -f).
#       Refused rather than waved through, because the hook cannot read the
#       reason: `--reason-file -`, `xargs bd close`, and a close the walk
#       cannot follow: behind a wrapper it does not know (timeout, nice,
#       sudo -u, find -exec), in a string another program runs (ssh, watch,
#       su -c, flock, ...), a shell fed on stdin (| sh, bash <<EOF), a
#       variable command word ($B close), or inside $(...), backticks, <(...)
#       or an unquoted heredoc body. Each substitution is walked on its own,
#       so a read-only `$(bd list)` passes and single-quoted text is literal.
#       -m/--message/--resolution/--comment count as --reason (bd aliases).
#       Anything else passes untouched.
#       --reason-file $SP/r.md resolves SP only from a plain `SP=...` (or
#       `export SP=...`) that the command makes exactly once, that no
#       builtin rewrites, and whose references are all unquoted; any other
#       variable stays literal, so the close is refused (a loop variable,
#       for one). The hook runs before the command, so a reason file the
#       same command writes does not exist yet.
#       Known gaps: a quoted "<<EOF" is read as a heredoc and can hide the
#       next line's close, a `cd` inside a subshell or behind a failing && is
#       still applied, and `-C` after --reason-file is applied too late
#       (agent-os-jox1).
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
# ported from SpecTacular's check-close-reason.sh (agent-os-fbwg 655d2041,
# agent-os-3kjf a5266249), without its beads-MCP arm: no beads MCP server is
# configured here.
#
# It writes NUL-separated records:
#   REFUSE <message>
#   CHECK <close|update> <dir> <id> <reason>
HOOK_PARSER=$(command cat <<'PY_'
import json, os, re, shlex, sys

# While a capture is open, records go to it instead of stdout: emits() uses
# this to ask "would this text close a bead?" without gating it.
CAPTURE = []

def out(*f):
    if CAPTURE:
        CAPTURE[-1].append(f)
        return
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

tool = str(ev.get("tool_name") or "")
ti = ev.get("tool_input") or {}
base = ev.get("cwd") or os.getcwd()

# Plain assignments seen earlier in the command (SP=/tmp/x; ... $SP/r.md).
# The hook's own environment never has them, so without this a reason file
# named through one is unreadable and a valid close is refused. OBSERVED
# 2026-09-23: about 30 of 48 refusals when 2,758 past commands from this
# repo's sessions were replayed through the hook (agent-os-9oo5).
#
# A value is used only where bash must read the same file, because a wrong
# guess can pass a bad reason: an adversary review found 16 fail-opens in a
# first version that ignored scope (a subshell or branch reassigning it,
# declare/read/for, a quoted '$SP', a nested bash -c or $(...)). So a name is
# TRUSTED only when the whole command text assigns it exactly once, no
# builtin writes it, every reference is unquoted and unescaped, and the
# assignment and the close are both at the top level of the walk. Anything
# else stays literal: unreadable, so the close is refused.
VARS = {}
DEPTH = [0]
VAR_REF = re.compile(r"\$(?:\{([A-Za-z_][A-Za-z0-9_]*)\}|([A-Za-z_][A-Za-z0-9_]*))")
# Builtins that write a variable without NAME=, each in the form that writes
# it, so `for f in $SP/*` or `printf '%s' "$SP"` (reads) keep SP trusted.
WRITERS = [
    r"(?<![A-Za-z0-9_])(?:for|select)\s+{n}(?![A-Za-z0-9_])",
    r"(?<![A-Za-z0-9_])printf\s+(?:-\S+\s+)*-v\s*{n}(?![A-Za-z0-9_])",
    r"(?<![A-Za-z0-9_])(?:declare|typeset|local|readonly|unset|let|read|mapfile|readarray|getopts)\b[^\n;&|]*?(?<![A-Za-z0-9_$]){n}(?![A-Za-z0-9_])",
]

def unquoted(t):
    """t with single-quoted spans and backslash escapes removed (quote-aware)."""
    out, i, n, dq = [], 0, len(t), False
    while i < n:
        c = t[i]
        if c == "\\":
            i += 2; continue
        if c == "'" and not dq:
            k = t.find("'", i + 1)
            i = n if k < 0 else k + 1; continue
        if c == '"':
            dq = not dq
        out.append(c); i += 1
    return "".join(out)

def trusted(name):
    # Heredoc bodies are data: an apostrophe in one is not a quote, and a
    # NAME= inside one assigns nothing.
    raw = strip_heredocs(cmd)[0]
    ref = r"\$(\{%s\}|%s(?![A-Za-z0-9_]))" % (name, name)
    if len(re.findall(r"(?<![A-Za-z0-9_$])%s=" % name, raw)) != 1:
        return False
    if any(re.search(w.replace("{n}", name), raw) for w in WRITERS):
        return False
    return len(re.findall(ref, raw)) == len(re.findall(ref, unquoted(raw)))

def expand(t, top=False):
    """$NAME / ${NAME} from VARS when trusted and at the top level, then from
    the environment. A reference neither knows stays literal, so the path it
    names is unreadable and the close is refused."""
    if top:
        t = VAR_REF.sub(lambda m: VARS.get(m.group(1) or m.group(2), m.group(0))
                        if (m.group(1) or m.group(2)) in VARS and trusted(m.group(1) or m.group(2))
                        else m.group(0), t)
    return os.path.expandvars(t)

def resolve(cur, target):
    # Expanding at any depth is safe: inside an eval or a double-quoted
    # bash -c the outer shell has already expanded an unquoted $SP to the
    # same value, and a single-quoted one is untrusted.
    if cur is None:
        return None
    return os.path.normpath(os.path.join(cur, os.path.expanduser(expand(target, True))))

def assigns(seg):
    """Record a simple command made only of NAME=value words (optionally
    behind export), at the top level of the walk only."""
    words = seg[1:] if seg and seg[0] == "export" else seg
    if not words or not all(re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", w) for w in words):
        return False
    if DEPTH[-1] == 0 and not CAPTURE:
        for w in words:
            k, v = w.split("=", 1)
            VARS[k] = expand(v, True)
    return True

if tool != "Bash":
    sys.exit(0)
cmd = ti.get("command")
if not isinstance(cmd, str):
    sys.exit(0)

OPS = set(";&|()\n")
WRAP = {"env", "rtk", "command", "time", "nohup", "exec", "builtin", "sudo",
        # reserved words that put the next word in command position
        "!", "{", "if", "then", "elif", "else", "while", "until", "do"}
GLOBAL_VAL = {"--db", "--actor", "--dolt-auto-commit"}
CLOSE_VAL = GLOBAL_VAL | {"--session"}
# bd 1.1.2 takes -m/--message/--resolution/--comment as hidden aliases of
# --reason (not in `bd close --help`; an unknown flag is rejected, these are not).
REASON_FLAGS = {"-r", "--reason", "-m", "--message", "--resolution", "--comment"}
SHELLS = {"bash", "sh", "zsh", "dash"}
SHELL_C = re.compile(r"^-[A-Za-z]*c[A-Za-z]*$")  # -c, -lc, -ec, -xc
# Commands that run a string argument as a command line (locally or not).
RUNNERS = {"ssh", "watch", "script", "su", "runuser", "flock", "parallel", "tmux",
           "screen", "chroot", "sg"}
BD_WORD = re.compile(r"(^|[^A-Za-z0-9_.-])bd($|[^A-Za-z0-9_-])")
CLOSE_TEXT = re.compile(r"(^|[^A-Za-z0-9_.-])bd\s(.*\s)?(close|done|batch|--status=closed|closed)($|[\s'\")`;&|])")
HIDDEN = ("close-reason hook: this command runs a bd close in a form the hook cannot follow "
          "(behind a wrapper such as timeout/nice/sudo -u/xargs/ssh/watch, inside $(...), "
          "backticks or <(...), or fed to a shell on stdin), so it cannot check the reason. "
          "Run it as a plain bd close <id> --reason-file <path>.")
UPDATE_VAL = GLOBAL_VAL | {
    "--acceptance", "--add-label", "--append-notes", "-a", "--assignee", "--await-id",
    "--body-file", "--defer", "-d", "--description", "--design", "--design-file", "--due",
    "-e", "--estimate", "--external-ref", "--metadata", "--notes", "--parent", "-p",
    "--priority", "--remove-label", "--session", "--set-labels", "--set-metadata",
    "--spec-id", "--title", "-t", "--type", "--unset-metadata"}

def tokenize(s):
    lex = shlex.shlex(s, posix=True, punctuation_chars=";&|()\n")
    lex.whitespace = " \t\r"
    lex.whitespace_split = True
    lex.commenters = ""  # strip_redirs drops comments; shlex's would eat the newline
    return list(lex)

REDIR_OP = re.compile(r"&>>|&>|>>|>\||>&|<&|<>|<<<|>|<")
WORD_END = " \t\r\n;&|()<>"

def strip_redirs(s):
    """Drop what the shell never passes to a command as an argument: unquoted
    redirections with their target (2>&1, 2>/dev/null, > out.txt, &>>log,
    <<< word), backslash-newline, and # comments (up to, not including, the
    newline). Quoted text is copied as is. Heredocs (<<, <<-) and process
    substitution <( >( are left alone. Unterminated quotes are copied, so
    shlex still fails on them."""
    out, i, n = [], 0, len(s)
    wstart, wplain = 0, True  # where the current word began in out; unquoted so far?

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
    apostrophe in one would make shlex fail. Unterminated -> unchanged.
    Returns (text, live): live holds the bodies of unquoted-delimiter heredocs,
    where the shell still expands $(...) and backticks."""
    lines, kept, pending, live, body = s.split("\n"), [], [], [], []
    for line in lines:
        if pending:
            delim, quoted = pending[0]
            if line.lstrip("\t") == delim:
                pending.pop(0)
                if not quoted:
                    live.append("\n".join(body))
                body = []
            else:
                body.append(line)
            continue
        kept.append(line)
        pending += [(m.group(3), bool(m.group(2))) for m in re.finditer(r"(?<!<)<<(?!<)(-?)\s*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\2", line)]
    return (s, []) if pending else ("\n".join(kept), live)

def close_paren(s, j):
    """Index of the ')' that closes a '(' opened just before s[j], or len(s).
    Quotes and backslashes inside are skipped. One forward pass: linear."""
    n, depth = len(s), 1
    while j < n:
        c = s[j]
        if c == "\\":
            j += 2; continue
        if c == "'":
            k = s.find("'", j + 1)
            j = n if k < 0 else k + 1; continue
        if c == '"':
            j += 1
            while j < n and s[j] != '"':
                j += 2 if s[j] == "\\" else 1
            j += 1; continue
        if c == "(":
            depth += 1
        elif c == ")":
            depth -= 1
            if depth == 0:
                return j
        j += 1
    return n

def substitutions(s, quotes=True):
    """The text inside each outermost $(...), `...`, <(...) and >(...) of s.
    Single-quoted text is literal to the shell and is skipped (quotes=False
    for a heredoc body, where quotes are literal and do not protect)."""
    res, i, n, dq = [], 0, len(s), False
    while i < n:
        c = s[i]
        if c == "\\":
            i += 2; continue
        if quotes and c == "'" and not dq:
            k = s.find("'", i + 1)
            i = n if k < 0 else k + 1; continue
        if quotes and c == '"':
            dq = not dq; i += 1; continue
        if c == "`":
            j = i + 1
            while j < n and s[j] != "`":
                j += 2 if s[j] == "\\" else 1
            res.append(s[i + 1:j]); i = j + 1; continue
        if s[i + 1:i + 2] == "(" and (c == "$" or c in "<>" and not dq):
            j = close_paren(s, i + 2)
            res.append(s[i + 2:j]); i = j + 1; continue
        i += 1
    return res

def emits(s, cur, depth):
    """Would walking s gate (or refuse) anything? Runs the walk into a capture,
    so nothing is emitted. Text with no bd word is not walked at all."""
    if not BD_WORD.search(s):
        return False
    if depth > 4:
        return True  # nested too deep to follow: fail closed
    CAPTURE.append([])
    try:
        walk(s, cur, depth)
    finally:
        recs = CAPTURE.pop()
    return bool(recs)

def bd_closes(rest):
    """The words after a bd word ask for a close: close/done/batch, or an
    update to status closed."""
    return any(t in ("close", "done", "batch") for t in rest) or (
        "update" in rest and any(t.endswith("closed") for t in rest))

def hidden(seg, cur, depth):
    """Backstop for a simple command whose command word is not bd: is a bd
    close hiding in it? A bare bd word followed by close/done (the walk found
    no bd in command position, so a wrapper it does not know, or a wrapper
    flag's value, hid it: timeout 5, nice -n 5, sudo -u x, xargs -n 1, find
    -exec, ssh host); a shell -c string behind such a wrapper; a string handed
    to a command runner (watch, ssh, script -c, su -c, ...); or a close run
    through a variable ($B close). Quoted mentions ("bd close ...") are one
    word, so echo/git/grep mentions do not match."""
    if seg[0].startswith("$") and bd_closes(seg[1:]):
        return True
    for k, w in enumerate(seg):
        wb = os.path.basename(w)
        if wb == "bd" and bd_closes(seg[k + 1:]):
            return True
        if wb in SHELLS:
            m = next((n for n, a in enumerate(seg[k + 1:], k + 1) if SHELL_C.match(a)), None)
            if m is not None:
                rest = [a for a in seg[m + 1:] if a != "--"]
                if rest and emits(rest[0], cur, depth + 1):
                    return True
    if os.path.basename(seg[0]) in RUNNERS:
        return any(emits(w, cur, depth + 1) for w in seg[1:])
    return False

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
                refuse("close-reason hook: --reason-file - (stdin) cannot be read by a hook; write the reason to a file and pass its path")
                return
            fp = resolve(d, p) if d else None
            try:
                with open(fp) as fh:
                    reasons.append(fh.read())
            except Exception:
                refuse("close-reason hook: --reason-file '%s' is not readable (resolved to %s). "
                       "The hook runs BEFORE the command, so a file this same command creates "
                       "(cat > f <<EOF ... bd close --reason-file f) does not exist yet: write the "
                       "file in an earlier call." % (p, fp))
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
    for n, i in enumerate(ids):
        reason = reasons[n] if len(reasons) > 1 and n < len(reasons) else (reasons[0] if len(reasons) == 1 else "")
        out("CHECK", "close", d or "", i, reason)

def bd_update(d, args):
    ids, status = [], ""
    j = 0
    while j < len(args):
        a = args[j]
        r = take_dir(args, j, d)
        if r:
            j, d = r
            continue
        if a in ("-s", "--status"):
            status = args[j + 1] if j + 1 < len(args) else ""; j += 2; continue
        if a.startswith("--status="):
            status = a.split("=", 1)[1]; j += 1; continue
        if a.startswith("-s") and not a.startswith("--") and len(a) > 2:
            status = a[2:].lstrip("="); j += 1; continue
        if "=" in a and a.startswith("-") and a.split("=", 1)[0] in UPDATE_VAL:
            j += 1; continue
        if a in UPDATE_VAL:
            j += 2; continue
        if a.startswith("-"):
            j += 1; continue
        ids.append(a); j += 1
    if status.lower() != "closed":
        return
    if not ids:
        refuse("close-reason hook: 'bd update --status closed' with no id; name the id and close it with bd close --reason-file")
    for i in ids:
        out("CHECK", "update", d or "", i, "")

def bd_batch(d, args, raw):
    text = raw
    for j, a in enumerate(args):
        if a in ("-f", "--file") and j + 1 < len(args):
            try:
                with open(resolve(d, args[j + 1])) as fh:
                    text = fh.read()
            except Exception:
                refuse("close-reason hook: bd batch file '%s' is not readable; refusing" % args[j + 1])
                return
    if re.search(r"(?m)^\s*close\s+\S", text) or re.search(r"status\s*=\s*closed", text):
        refuse("close-reason hook: bd batch closes beads without a reason the hook can check; close each with 'bd close <id> --reason-file <path>'")

def walk(s, cur, depth=0):
    DEPTH.append(depth)
    try:
        return walk_at(s, cur, depth)
    finally:
        DEPTH.pop()

def walk_at(s, cur, depth):
    text, live = strip_heredocs(s)
    flat = strip_redirs(text)
    # A close inside $(...), backticks or <(...) runs, but not where the walk
    # can gate it: refuse it. Each span is walked into a capture, so
    # `for i in $(bd list)` and `x=$(bd show X)` are not refused.
    spans = substitutions(flat) + [x for b in live for x in substitutions(b, quotes=False)]
    if any(emits(x, cur, depth + 1) for x in spans):
        refuse(HIDDEN)
        return cur
    try:
        toks = tokenize(flat)
    except ValueError:
        refuse("close-reason hook: could not parse the command line (unbalanced quotes?); refusing rather than guessing")
        return cur
    for seg in segments(toks):
        if assigns(seg):
            continue
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
        if b == "eval":
            # eval joins its words and parses the result as a command line.
            if depth < 3:
                walk(" ".join(args), cur, depth + 1)
            elif BD_WORD.search(" ".join(args)):
                refuse("close-reason hook: eval nested too deep to follow; refusing rather than guessing")
            continue
        if b in SHELLS:
            k = next((n for n, a in enumerate(args) if SHELL_C.match(a)), None)
            if k is not None:
                rest = [a for a in args[k + 1:] if a != "--"]
                if rest and depth < 3:
                    walk(rest[0], cur, depth + 1)
                elif rest and BD_WORD.search(rest[0]):
                    refuse("close-reason hook: shell -c nested too deep to follow; refusing rather than guessing")
            elif not [a for a in args if not a.startswith(("-", "<<"))] and CLOSE_TEXT.search(s):
                # No -c and no script: the shell runs its stdin (| sh, bash <<EOF).
                refuse(HIDDEN)
            continue
        if b != "bd":
            if hidden(seg[i:], cur, depth):
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
        rest = args[j:]
        if xargs and sub in ("close", "done", "update"):
            refuse("close-reason hook: 'xargs bd %s' hides the ids from the hook; name each id" % sub)
        elif sub in ("close", "done"):
            bd_close(d, rest)
        elif sub == "update":
            bd_update(d, rest)
        elif sub == "batch":
            bd_batch(d, rest, s)
    return cur

if re.search(r"(^|[^A-Za-z0-9_.-])bd($|[^A-Za-z0-9_-])", cmd):
    walk(cmd, base)
PY_
)

hook_mode() {
  local rc=0 kind dir id reason type
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
        kind=${recs[$((i+1))]}; dir=${recs[$((i+2))]}; id=${recs[$((i+3))]}; reason=${recs[$((i+4))]:-}
        i=$((i+5))
        if [ -z "$dir" ]; then
          echo "close-reason hook: can't tell which directory 'bd ${kind} ${id}' runs in (cd -?); refusing" >&2; rc=2; continue
        fi
        if ! type=$(bead_type "$id" "$dir"); then rc=2; continue; fi
        if [ "$kind" = update ]; then
          if [ "$type" = bug ]; then
            echo "close-reason hook: refusing 'bd update ${id} --status closed': a bug bead needs a close reason, and bd update has none. Use: bd close ${id} --reason-file <path>" >&2
            rc=2
          else
            echo "close-reason: not a bug bead (type: ${type}); status change allowed"
          fi
          continue
        fi
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
  # --- substitutions (agent-os-9oo5): a bd close INSIDE one is refused; a
  # read-only bd inside one, or a single-quoted mention, is not.
  hook_case subst-for-bd-list         0 "for i in \$(bd list --json); do echo \$i; done"
  hook_case subst-bd-show-done        0 "x=\$(bd show t-bug-1); echo done"
  hook_case subst-sq-mention          0 "echo '\$(bd close t-bug-1 -r fixed)'"
  hook_case subst-sq-commit-msg       0 "git commit -m 'teach \`bd close\` to refuse'"
  hook_case subst-dq-commit-msg       2 "git commit -m \"teach \`bd close\` to refuse\""    "$H"
  hook_case subst-nested              2 "echo \"\$(echo \$(bd close t-bug-1 -r fixed))\""   "$H"
  hook_case subst-procsub             2 "cat <(bd close t-bug-1 -r fixed)"                 "$H"
  hook_case subst-heredoc-live        2 "cat <<EOF
\$(bd close t-bug-1 -r fixed)
EOF"                                                                                         "$H"
  hook_case subst-heredoc-quoted      0 "cat <<'EOF'
\$(bd close t-bug-1 -r fixed)
EOF"
  # --- a close in script text another program runs (agent-os-vmhm)
  hook_case wrap-timeout-bash-c       2 "timeout 5 bash -c 'bd close t-bug-1 -r fixed'"   "$H"
  hook_case wrap-timeout-bash-show    0 "timeout 5 bash -c 'bd show t-bug-1'"
  hook_case eval-quoted-bad           2 "eval 'bd close t-bug-1 -r fixed'"                "$ALL"
  hook_case eval-quoted-good          0 "eval 'bd close t-bug-1 $G'"
  hook_case wrap-ssh-quoted           2 "ssh host 'bd close t-bug-1 -r fixed'"            "$H"
  hook_case wrap-watch-list           0 "watch -n 5 'bd list'"
  hook_case wrap-su-c                 2 "su -c 'bd close t-bug-1 -r fixed' someone"       "$H"
  hook_case wrap-find-exec            2 "find . -exec bd close t-bug-1 -r fixed \\;"      "$H"
  hook_case var-cmd-word-close        2 "B=bd; \$B close t-bug-1 -r fixed"                "$H"
  hook_case var-cmd-word-show         0 "B=bd; \$B show t-bug-1"
  hook_case pipe-to-sh                2 "echo 'bd close t-bug-1 -r fixed' | sh"           "$H"
  hook_case pipe-to-sh-no-close       0 "echo 'bd show t-bug-1' | sh"
  hook_case heredoc-to-bash           2 "bash <<'EOF'
bd close t-bug-1 -r fixed
EOF"                                                                                         "$H"
  hook_case heredoc-to-bash-show      0 "bash <<'EOF'
bd show t-bug-1
EOF"
  # --- bd update --status closed and bd batch (agent-os-51ua): bd has no
  # reason on either path, so a bug bead is refused there.
  hook_case update-closed-bug         2 "bd update t-bug-1 --status closed"               'bd update has none'
  hook_case update-eq-closed-bug      2 "bd update t-bug-1 --status=closed"               'bd update has none'
  hook_case update-s-closed-bug       2 "bd update t-bug-1 -s closed --notes x"           'bd update has none'
  hook_case update-closed-task        0 "bd update t-task-1 --status closed"              'status change allowed'
  hook_case update-inprogress-bug     0 "bd update t-bug-1 --status in_progress --title 'x'"
  hook_case update-closed-wrapped     2 "timeout 5 bd update t-bug-1 --status closed"     "$H"
  hook_case batch-close               2 "bd batch <<'EOF'
close t-bug-1
EOF"                                                                                         'bd batch closes'
  hook_case batch-create-only         0 "bd batch <<'EOF'
create --title x
EOF"
  command printf 'close t-bug-1\n' > "$ST_DIR/batch-close.txt"
  command printf 'create --title x\n' > "$ST_DIR/batch-create.txt"
  hook_case batch-file-close          2 "bd batch -f batch-close.txt"                     'bd batch closes'
  hook_case batch-file-create         0 "bd batch -f batch-create.txt"
  # --- a reason file named through a variable set in the same command
  # (agent-os-9oo5, found by replaying past session commands)
  hook_case var-reason-file-good      0 "SP=$ST_DIR; bd close t-bug-1 --reason-file \$SP/complete.txt"
  hook_case var-reason-file-bad       2 "SP=$ST_DIR; bd close t-bug-1 --reason-file \$SP/no-verdict.txt" 'missing: Verdict'
  hook_case var-braces-export-good    0 "export SP=$ST_DIR && bd close t-bug-1 --reason-file \${SP}/complete.txt"
  hook_case var-unset-refused         2 "bd close t-bug-1 --reason-file \$NOPE_UNSET_9OO5/complete.txt"     'not readable'
  hook_case var-subst-value-refused   2 "SP=\$(pwd); bd close t-bug-1 --reason-file \$SP/complete.txt"      'not readable'
  hook_case same-call-file-refused    2 "printf x > new-9oo5.txt && bd close t-bug-1 --reason-file new-9oo5.txt" 'runs BEFORE the command'
  # --- an unparseable line that mentions bd is refused: shlex cannot read
  # $'...' quoting, and a spelling like bd clos"e" hides the close from any
  # text pattern (review of agent-os-9oo5, OBSERVED fail-open when gated)
  hook_case unparseable-ansi-c-close  2 "bd clos\"e\" t-bug-1 -r \$'it\\'s fixed'"      'could not parse'
  # --- a variable is trusted only when bash must read the same file the hook
  # does: one plain top-level assignment, unquoted references, and not inside
  # a nested shell or substitution. ./r.txt is incomplete, sub/r.txt complete,
  # so a hook that believes the wrong value exits 0 (review of agent-os-9oo5).
  command cp "$ST_DIR/complete.txt" "$ST_DIR/rX.txt"
  hook_case var-plain-good            0 "SP=sub; bd close t-bug-1 --reason-file \$SP/r.txt"
  hook_case var-subshell-leak         2 "SP=.; (SP=sub); bd close t-bug-1 --reason-file \$SP/r.txt"        'not readable'
  hook_case var-branch-leak           2 "SP=.; false && SP=sub; bd close t-bug-1 --reason-file \$SP/r.txt" 'not readable'
  hook_case var-declare-reassign      2 "SP=sub; declare SP=.; bd close t-bug-1 --reason-file \$SP/r.txt"  'not readable'
  hook_case var-read-reassign         2 "SP=sub; read SP <<< .; bd close t-bug-1 --reason-file \$SP/r.txt" 'not readable'
  hook_case var-for-reassign          2 "SP=sub; for SP in .; do :; done; bd close t-bug-1 --reason-file \$SP/r.txt" 'not readable'
  hook_case var-single-quoted         2 "SP=sub; bd close t-bug-1 --reason-file '\$SP'/r.txt"             'not readable'
  hook_case var-escaped               2 "SP=sub; bd close t-bug-1 --reason-file \\\$SP/r.txt"             'not readable'
  hook_case var-into-bash-c           2 "SP=X; bash -c 'bd close t-bug-1 --reason-file r\$SP.txt'"        'not readable'
  hook_case var-backwards-from-subst  2 "bd close t-bug-1 --reason-file r\$SP.txt; x=\$(SP=X; bd list)"   'not readable'
  hook_case var-printf-v-reassign     2 "SP=sub; printf -v SP .; bd close t-bug-1 --reason-file \$SP/r.txt" 'not readable'
  hook_case var-read-only-uses-good   0 "SP=sub; for f in \$SP/*; do :; done; printf '%s' \"\$SP\"; bd close t-bug-1 --reason-file \$SP/r.txt"
  hook_case var-heredoc-apostrophe-good 0 "SP=sub; cat > /dev/null <<'EOF'
it's data, and SP=. here assigns nothing
EOF
bd close t-bug-1 --reason-file \$SP/r.txt"
  hook_case var-unknown-stays-literal 2 "bd close t-bug-1 --reason-file complete\$NOPE_9OO5.txt"          'not readable'
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
