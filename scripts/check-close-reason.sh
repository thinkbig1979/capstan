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
#       Gates every `bd close|done` (and `bd todo done`, whose --reason is
#       checked the same way) in command position: after ; && || |
#       newline or a paren, behind VAR=, env, rtk, command, time, a reserved
#       word (! { if then elif else while until do), and inside the script
#       text of eval or bash|sh -c "..." (also -lc, -ec, -c --). Unquoted
#       redirections, backslash-newlines and # comments are dropped first;
#       none of them reach bd as arguments. A quoted, commented, escaped or
#       arithmetic << starts no heredoc; a heredoc body line that runs a bd
#       close/done/batch/import is refused anyway (nested quoting can make
#       bash run it), and so is $'...' next to a close.
#       Files (--reason-file, batch -f, import) are read from the JSON cwd,
#       as moved by each `cd /absolute/dir` that starts a top-level list
#       (not in ( ), $( ), a body or an & list). A command that changes
#       directory any other way (cd elsewhere, pushd, popd, eval, source, .,
#       trap, a $VAR command word) needs absolute paths and an absolute
#       bd -C, or is refused: the hook does not track where bash keeps such
#       a cd. bd -C picks only the database, so it moves the type lookup,
#       never the files (bd 1.1.2).
#       A missing field, an unreadable input or an unparseable line exits 2
#       (Claude Code's "block, feed stderr back" status).
#       Also refused, because bd takes no reason there: `bd update <bug>
#       --status closed`, `bd batch` with a close line in its -f file or in
#       any heredoc of the command (other stdin is refused: a | before it,
#       a < or <<< anywhere, a heredoc body that expands; --dry-run passes),
#       `bd duplicate`/`supersede` on a bug, `bd delete --force` and
#       `bd mol burn` (alias protomolecule; flags may precede burn, and
#       todo's before done) on a bug (it vanishes with no reason; a
#       --dry-run=F/false/0 is no dry run), `bd duplicates
#       --auto-merge`, `bd orphans --fix` and delete --from-file/--cascade
#       (they pick the beads themselves), a `bd sql` write, and
#       `bd import` unless its file is readable with no closed row, keys
#       matched case-insensitively as Go does (no file reads the export, and
#       - reads stdin; --dry-run passes).
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
#       A --reason-file, batch -f or import path must be literal: one with
#       $VAR, ${VAR} or a leading ~ is refused, because the hook runs before
#       the shell expands it (so is a cd or -C to such a path). The hook also
#       runs before the command, so any other mention of such a file's name
#       in the same command (a write, cp or mv, but also a cat or rm; bd's
#       own reads of it do not count) is refused, as is a /dev or /proc path.
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
  # A here-string, never `printf | grep -q`: under pipefail grep -q exits on
  # its first match, the printf still writing gets SIGPIPE, and a field that
  # is present reads as missing (agent-os-7dkt, OBSERVED on CI at this line).
  has() { command grep -qiE "$1" <<<"$text"; }
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

# A path the shell would expand ($VAR, ${VAR}, ~) names a file the hook
# cannot know: it runs before the shell, and its own environment is not the
# command's. Tracking assignments was tried (agent-os-9oo5) and two review
# rounds found 31 ways for the hook to read a complete reason file while bash
# hands bd an incomplete one, so such a path is refused instead of guessed.
MOVED = ("close-reason hook: this command changes directory (cd, pushd, popd, eval, "
         "source, ., trap, or a $VAR command word; only a cd to an absolute path that "
         "starts a top-level list is followed), "
         "so the hook cannot tell where bd will read %s '%s'. Pass an absolute path, and "
         "bd -C <absolute dir>, or run the close in its own command.")
LITERAL = ("close-reason hook: %s '%s' uses a shell variable or ~, which the hook "
           "cannot expand (it runs before the shell does). Pass the literal path.")

def expands(t):
    return "$" in t or t.startswith("~")

def resolve(cur, target):
    """A literal path joined to the directory, or None when the directory is
    unknown or the path is one the shell would expand."""
    if expands(target):
        return None
    if os.path.isabs(target):
        return os.path.normpath(target)
    if cur is None:
        return None
    return os.path.normpath(os.path.join(cur, target))

if tool != "Bash":
    sys.exit(0)
cmd = CMD = ti.get("command")
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
CLOSE_TEXT = re.compile(r"(^|[^A-Za-z0-9_.-])bd\s(.*\s)?(close|done|batch|import|duplicates?|supersede|delete|burn|orphans|sql|--status=closed|closed)($|[\s'\")`;&|])")
HIDDEN = ("close-reason hook: this command runs a bd close (or a delete) in a form the hook cannot follow "
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
        if c in "<>" and s[i + 1:i + 2] == "(":
            out.append(s[i:i + 2]); wplain = False; i += 2; continue
        if s.startswith("<<", i) and not s.startswith("<<<", i):
            # its own word: bash splits bd batch<<'EOF' into batch and a heredoc
            out.append(" <<"); wplain = False; i += 2; continue
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

# The delimiter is one shell word; any quoting in it (<<'X', <<"X", <<E"OF",
# <<\X) makes it quoted, and the quotes are removed to get the word.
HEREDOC_OP = re.compile(r"<<(-?)[ \t]*((?:[^\s;&|<>()'\"\\]|\\.|'[^'\n]*'|\"[^\"\n]*\")+)")

# bd close/done/batch/import in command position on a heredoc body line: at
# its start, or after ; or & ("run bd close X", "(bd close X)" and a
# markdown "| bd close |" cell in prose do not match).
BODY_CLOSE = re.compile(r"(?m)(?:^|[;&])[ \t]*(?:[A-Za-z_]\w*=\S*[ \t]+)*"
                        r"bd[ \t]+(?:--?\S+[ \t]+)*(?:\S+[ \t]+)?(close|done|batch|import|duplicates?|supersede|delete|burn|orphans)\b")

# a bd close/done/batch/import invocation anywhere (not the loop word done,
# not --status closed)
BD_CLOSE_CMD = re.compile(r"(?:^|[^\w.-])bd\s+(?:--?\S+\s+)*(?:\S+\s+)?(close|done|batch|import|duplicates?|supersede|delete|burn|orphans|sql)\b")

def strip_heredocs(s):
    """Drop here-document bodies: they are data, not shell words, and an
    apostrophe in one would make shlex fail. Unterminated -> unchanged.
    Returns (text, live): live holds the bodies of unquoted-delimiter heredocs,
    where the shell still expands $(...) and backticks."""
    # Only an unquoted, uncommented << starts a heredoc (agent-os-jox1:
    # echo "<<EOF" does not, and swallowing the next line hid a close). The
    # quote state spans lines; $( inside double quotes is unquoted again, so
    # "$(cat <<'EOF' ...)" still strips.
    lines, kept, pending, live, body, bodies = s.split("\n"), [], [], [], [], []
    stack = []  # "'" / '"' quotes, "(" a $( or ( that is unquoted inside
    for line in lines:
        if pending:
            delim, quoted, dash = pending[0]
            if (line.lstrip("\t") if dash else line) == delim:
                pending.pop(0)
                bodies.append("\n".join(body))
                if not quoted:
                    live.append("\n".join(body))
                body = []
            else:
                body.append(line)
            continue
        kept.append(line)
        i, n, esc = 0, len(line), -1  # esc: where the last \x pair ended
        while i < n:
            c, top = line[i], stack[-1] if stack else ""
            if top in ("A", "a"):  # inside $(( )) or (( )): << is a shift
                if c == "(":
                    stack.append("a")
                elif c == ")" and top == "a":
                    stack.pop()
                elif line.startswith("))", i):
                    stack.pop(); i += 1
                i += 1; continue
            if top == "'":
                if c == "'":
                    stack.pop()
                i += 1; continue
            if c == "\\":
                i += 2; esc = i; continue
            if top == '"':
                if c == '"':
                    stack.pop()
                elif line.startswith("$((", i):
                    stack.append("A"); i += 2
                elif line.startswith("$(", i):
                    stack.append("("); i += 1
                i += 1; continue
            if c in "'\"":
                stack.append(c)
            elif line.startswith("((", i):
                stack.append("A"); i += 2; continue
            elif c == "(":
                stack.append("(")
            elif c == ")" and top == "(":
                stack.pop()
            elif c == "#" and (i == 0 or line[i - 1] in " \t;&|()" and esc != i):  # x\ # is one word
                break
            elif line.startswith("<<", i):
                m = HEREDOC_OP.match(line, i)
                if m:
                    word = m.group(2)
                    quoted = any(q in word for q in "'\"\\")
                    word = re.sub(r"\\(.)|'([^']*)'|\"([^\"]*)\"", lambda x: "".join(g or "" for g in x.groups()), word)
                    pending.append((word, quoted, bool(m.group(1))))
                    i = m.end(); continue
                i += 2; continue
            i += 1
    return (s, [], []) if pending else ("\n".join(kept), live, bodies)

def ansi_c(s):
    """Does s use $'...' quoting (a $' outside single and double quotes)?
    shlex reads it as $ plus a plain '...', so the words it sees are not
    bash's ('^[0-9]+$' in a grep pattern is not one)."""
    i, n, dq = 0, len(s), False
    while i < n:
        c = s[i]
        if c == "\\":
            i += 2; continue
        if c == "'" and not dq:
            k = s.find("'", i + 1)
            i = n if k < 0 else k + 1; continue
        if c == '"':
            dq = not dq
        elif c == "$" and not dq and s[i + 1:i + 2] == "'":
            return True
        i += 1
    return False

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
    """The words after a bd word ask for a close: close/done/batch/import, or an
    update to status closed."""
    j = 0  # the subcommand word, past flags and their values
    while j < len(rest) and rest[j].startswith("-"):
        j += 2 if rest[j] in GLOBAL_VAL | {"-C", "--directory"} else 1
    sub = rest[j] if j < len(rest) else ""
    if sub in NO_REASON or sub in ("mol", "protomolecule") and "burn" in rest:
        return True
    return any(t in ("close", "done", "batch", "import") for t in rest) or (
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

OP_SPLIT = re.compile(r"\(|\)|&&|\|\||\|&|\||;;|;|&|\n")

def items(toks):
    """Simple commands and the operators between them, in order. shlex glues
    adjacent punctuation (");", "&&("), so operator tokens are split here."""
    seg = []
    for t in toks:
        if t and all(c in OPS for c in t):
            if seg:
                yield "seg", seg
            seg = []
            for op in OP_SPLIT.findall(t):
                yield "op", op
        else:
            seg.append(t)
    if seg:
        yield "seg", seg

OPENS = {"if", "while", "until", "for", "case", "select", "{"}
CLOSES = {"fi", "done", "esac", "}"}

def shell_dirs(its, cur):
    """The directory each item of the command runs in, as a list parallel to
    its (None where unknown). Only one kind of directory change is followed:
    `cd /absolute/dir` (literal, existing) as a whole simple command that
    starts a list outside any ( ), $( ) or body (if/for/while/case/{ }),
    followed by &&, ; or a newline, in a list that is not run with &. Any
    other change anywhere (cd elsewhere or to a relative path, pushd, popd,
    eval that moves, source, ., trap, a $VAR command word) makes every item
    None, and so does a followed cd in a command whose nesting counters are
    `odd` (below: a case, `function f {`, a stray closer). Tracking
    where bash keeps a cd was tried (agent-os-jox1): a review found 27 ways
    for the hook to read one file while bd reads another."""
    unknown = [None] * len(its)
    # odd: the counters cannot be trusted (a case pattern's ")" closes no
    # subshell, `function f {` opens its body on the same line, a quoted
    # "fi" is not a keyword but shlex drops the quotes), so a followed cd
    # would be a guess
    depth, body, dirs, followed, odd = 0, 0, [], False, False
    for k, (kind, seg) in enumerate(its):
        if kind == "op":
            depth += {"(": 1, ")": -1}.get(seg, 0)
            odd = odd or depth < 0
            dirs.append(cur)
            continue
        i = 0
        while i < len(seg) and (seg[i] in WRAP or seg[i] in OPENS | CLOSES
                                or re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", seg[i])):
            body += (seg[i] in OPENS) - (seg[i] in CLOSES)
            odd = odd or body < 0 or seg[i] == "case"
            i += 1
        word = seg[i] if i < len(seg) else ""
        odd = odd or word == "function"
        before = its[k - 1][1] if k > 0 else None
        after = its[k + 1][1] if k + 1 < len(its) else None
        if seg == ["cd", seg[-1]] and depth == 0 and body == 0 and before in (None, ";", "\n") \
                and after in (None, "&&", ";", "\n") and os.path.isabs(seg[1]) \
                and not expands(seg[1]) and os.path.isdir(seg[1]):
            for kind2, x in its[k + 1:]:  # the rest of this list: run with &?
                if kind2 == "op" and x in (";", "\n"):
                    break
                if kind2 == "op" and x == "&":
                    return unknown
            cur, followed = os.path.normpath(seg[1]), True
            dirs.append(cur)
            continue
        # cd/pushd/popd anywhere else (env cd, nohup cd, timeout 5 cd);
        # source and . only as the command word (git add . is not a move).
        # A cd in a function body is never followed: f() { is a { } body,
        # and `function f {` makes the counters odd.
        # A command word from a variable ($C sub with C=cd) or a trap
        # (trap 'cd x' DEBUG) can change directory where no cd word shows.
        if any(t in ("cd", "pushd", "popd") for t in seg) or word in ("source", ".", "trap") \
                or word.startswith("$"):
            return unknown
        if word == "eval":
            try:
                inner = list(items(tokenize(strip_redirs(" ".join(seg[i + 1:])))))
            except ValueError:
                return unknown
            if any(d != cur for d in shell_dirs(inner, cur)):
                return unknown
        dirs.append(cur)
    return unknown if followed and odd else dirs

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

# (basename, what, path) of each file bd reads (file_arg's successes), for
# named_elsewhere().
READS = []

def file_arg(sh, p, what):
    """The file bd will read for `what` (a --reason-file, batch -f or import
    path), or None after refusing. It must be a literal path the hook can
    place, and not a device or process file (/dev/stdin reads the hook's own,
    already empty, stdin). named_elsewhere() checks the rest of the command."""
    if expands(p):
        refuse(LITERAL % (what, p))
        return None
    fp = resolve(sh, p)
    if fp is None:
        refuse(MOVED % (what, p))
        return None
    if re.match(r"/(dev|proc)(/|$)", os.path.realpath(fp)):
        refuse("close-reason hook: %s '%s' is a device or process file, which the hook "
               "cannot read the way bd will; write the content to a regular file." % (what, p))
        return None
    if not CAPTURE:  # a captured walk is refused as a whole anyway
        READS.append((os.path.basename(fp), what, p))
    return fp

def named_elsewhere():
    """The hook reads each file BEFORE the command runs, so a write, copy or
    move of it in the same call hands bd other text than the hook checked
    (agent-os-jox1). Any mention of the file's name, in any directory, that is
    not one of bd's own reads is refused: counting is by basename because the
    hook cannot place every mention (a cd inside sh -c, a symlink). bd's reads
    only read, so two closes sharing one reason file, or a/r.txt and b/r.txt,
    pass; a read-only cat or a later rm of it is refused too (accepted cost).
    A mention is the name at the END of a word, whatever precedes it, so a
    glued bd batch -fr.txt counts (and so does xr.txt: a refusal, never a
    pass), and so does a bead id that equals an extensionless file name.
    Quotes and backslashes are also dropped before counting ('r'.txt,
    r\\.txt). Not seen: a glob, brace or $ spelling of the name, and a move
    of a parent directory (mv sub old && mv sub2 sub)."""
    bare = re.sub(r"[\\'\"]", "", CMD)
    for name, what, p in READS:
        pat = r"%s(?![\w.-])" % re.escape(name)
        mentions = max(len(re.findall(pat, CMD)), len(re.findall(pat, bare)))
        if mentions > sum(r[0] == name for r in READS):
            refuse("close-reason hook: %s '%s' is also named elsewhere in this command. The hook "
                   "runs BEFORE the command and reads the file as it is now, so it refuses any "
                   "other mention of that name in the same call (a write, copy or move would "
                   "change what bd reads): write, read or remove the file in a separate call."
                   % (what, p))
            return

def bd_close(sh, d, args):
    """sh is the shell's directory, where bd reads --reason-file; d is the
    -C directory, which only picks the database (bd 1.1.2, agent-os-jox1)."""
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
            fp = file_arg(sh, p, "--reason-file")
            if fp is None:
                return
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
        if a.startswith(("-", "<<")):  # <<EOF is a heredoc, not an id
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
        if a.startswith(("-", "<<")):
            j += 1; continue
        ids.append(a); j += 1
    if status.lower() != "closed":
        return
    if not ids:
        refuse("close-reason hook: 'bd update --status closed' with no id; name the id and close it with bd close --reason-file")
    for i in ids:
        out("CHECK", "update", d or "", i, "")

BATCH_CLOSE = re.compile(r'(?im)^\s*"?close"?(\s|$)|status"?\s*=\s*"?closed')

def stdin_fed(s, pipes):
    """Does s hold an unquoted, uncommented < redirection (<, <&, <>, <<<;
    not <<, not <( ), or, with pipes, an unquoted | (not ||)? The text is
    scanned whole, so [[ a < b ]] counts too (a refusal, never a pass). A #
    starts a comment only at the start of a word (x\\ # is one word)."""
    i, n, start = 0, len(s), True
    while i < n:
        c = s[i]
        if c == "\\":
            i += 2; start = False; continue
        if c == "#" and start:
            k = s.find("\n", i)
            i = n if k < 0 else k; continue
        start = c in " \t\n;&|()"
        if c == "'":
            k = s.find("'", i + 1)
            i = n if k < 0 else k + 1; continue
        if c == '"':
            i += 1
            while i < n and s[i] != '"':
                i += 2 if s[i] == "\\" else 1
            i += 1; continue
        if c == "|":
            if s[i + 1:i + 2] == "|":
                i += 2; continue
            if pipes:
                return True
        if c == "<":
            if s.startswith("<<", i) and not s.startswith("<<<", i):
                i += 2; continue
            if s[i + 1:i + 2] != "(":
                return True
        i += 1
    return False

# Subcommands that close or delete a bead and take no reason (agent-os-yplt,
# OBSERVED on bd 1.1.2 against a throwaway embedded database): each gated
# bead of a bug type is refused; those that pick their own ids are refused
# outright. bd sql, doctor --fix and human respond cannot run on an embedded
# tracker (this repo's); sql writes are refused anyway.
NO_REASON = ("duplicate", "supersede", "delete", "mol burn", "duplicates", "orphans", "sql")
SQL_WRITE = re.compile(r"(?i)\b(update|insert|delete|replace|drop|alter|truncate|merge|call)\b")

def bd_no_reason(sub, d, args):
    """duplicate <id> --of X and supersede <id> --with X close <id>; delete
    <id> --force (no --force is a preview) and mol burn <id> delete it with
    no record. A bug bead there is refused after a type lookup, like
    bd update --status closed. duplicates --auto-merge and orphans --fix close
    beads they choose, and delete --from-file/--cascade deletes beads the
    command does not name: refused outright. --dry-run passes."""
    ids, flags, j = [], set(), 0
    while j < len(args):
        a = args[j]
        r = take_dir(args, j, d)
        if r:
            j, d = r
            continue
        if a in ("-h", "--help"):
            return
        if a in ("--of", "--with", "--from-file", "-l", "--label", "--label-any") or a in GLOBAL_VAL:
            flags.add(a); j += 2; continue
        if a.startswith("--"):
            # --x=false, =0, =F, =False switch x off, as pflag's strconv.ParseBool does
            name, _, val = a.partition("=")
            if val.lower() not in ("0", "f", "false"):
                flags.add(name)
            j += 1; continue
        if a.startswith("-") and len(a) > 1:
            # short cluster: -f is --force (delete) or --fix (orphans); -l takes
            # a value, glued (-lx) or as the next word when it ends the cluster
            cl = a[1:]
            k = cl.find("l")
            if "f" in (cl if k < 0 else cl[:k]):
                flags.add("-f")
            j += 2 if k == len(cl) - 1 else 1
            continue
        if not a.startswith("<<"):
            ids.append(a)
        j += 1
    if "--dry-run" in flags:
        return
    label = "bd " + sub
    if sub == "sql":
        q = " ".join(args)
        if SQL_WRITE.search(q) or re.search(r"[$`]", q):  # a query built by the shell is unread
            refuse("close-reason hook: 'bd sql' with a write statement (or a query the shell "
                   "builds, which the hook cannot read) can close or delete beads with no reason "
                   "the hook can check; refusing. Use bd close <id> --reason-file <path>.")
        return
    force = bool(flags & {"--force", "-f"})
    if sub == "duplicates" and "--auto-merge" not in flags or sub == "orphans" and not (flags & {"--fix", "-f"}) \
            or sub == "delete" and not force:
        return  # a listing or a preview
    if sub in ("duplicates", "orphans") or flags & {"--from-file", "--cascade"}:
        refuse("close-reason hook: '%s %s' closes or deletes beads it picks itself, so the hook "
               "cannot check any reason; refusing. Close each bead with bd close <id> --reason-file "
               "<path> (run it with --dry-run to see which)." % (label, " ".join(sorted(flags - {""}))))
        return
    if not ids:
        refuse("close-reason hook: '%s' with no id; name the id so its type can be checked" % label)
    for i in ids:
        out("CHECK", "noreason", d or "", i, label)

def bd_batch(sh, args, raw, piped, nested):
    """bd batch runs `close <id>` and `update <id> status=closed` lines with no
    reason the hook can check. It reads -f's file, else stdin; stdin can be
    read here only as a heredoc in this command text. Refused: a | anywhere
    before it (piped: it may feed a loop or group around it; nested: a
    bash -c string may sit inside any pipe of CMD), a < redirection or <<<
    anywhere (bash lets the last one win), and a heredoc body that expands
    ($, backticks, a backslash). Every heredoc body in the command is
    scanned, not only bd batch's own (a refusal, never a pass).
    The words here are dequoted, so a quoted '<<E' argument reads as a
    heredoc, and a | after this item (into a function defined before it)
    is not seen. Both need bd batch to take an argument, and bd 1.1.2
    rejects any (OBSERVED: `bd batch '<<E' --dry-run` exits 1, unknown
    command "<<E", before reading stdin)."""
    files, dry, j = [], False, 0
    while j < len(args):
        a = args[j]
        nxt = args[j + 1] if j + 1 < len(args) else ""
        if a == "--":
            break
        if a in ("-h", "--help"):
            return
        if a == "--dry-run":
            dry = True
        elif a.startswith("--dry-run="):
            dry = a.split("=", 1)[1].lower() in ("1", "t", "true")
        elif a == "--file":
            files.append(nxt); j += 1
        elif a.startswith("--file="):
            files.append(a.split("=", 1)[1])
        elif a in GLOBAL_VAL or a in ("--message", "--directory"):
            j += 1
        elif a.startswith("-") and not a.startswith("--"):
            # a pflag shorthand cluster: -qf x, -fx, -f=x; -m and -C take a value too
            for n, c in enumerate(a[1:], 1):
                if c in "fmC":
                    v = a[n + 1:]
                    if not v:
                        v = nxt; j += 1
                    if c == "f":
                        files.append(v[1:] if v.startswith("=") else v)
                    break
        j += 1
    if dry:
        return
    stop = ("close-reason hook: bd batch closes beads without a reason the hook can check; "
            "close each with 'bd close <id> --reason-file <path>'")
    texts = []
    for p in files:
        fp = file_arg(sh, p, "bd batch -f")
        if fp is None:
            return
        try:
            with open(fp) as fh:
                texts.append(fh.read())
        except Exception:
            refuse("close-reason hook: bd batch file '%s' is not readable; refusing" % p)
            return
    if not files:
        text, live, bodies = strip_heredocs(raw)
        if not any(a.startswith("<<") for a in args) or not bodies \
                or any(re.search(r"[$`\\]", b) for b in live) or piped \
                or stdin_fed(text, False) or stdin_fed(strip_heredocs(CMD)[0], nested):
            refuse("close-reason hook: bd batch reads stdin here, and the hook can only read it "
                   "as a heredoc in the command (bd batch <<'EOF'): not with a | before it, a < "
                   "or <<< anywhere in the command, or a body that expands $, ` or \\. "
                   "Use bd batch -f <file written in an earlier call>.")
            return
        texts = bodies
    if any(BATCH_CLOSE.search(t) for t in texts):
        refuse(stop)

def bd_import(sh, args):
    """bd import upserts rows, and a row with "status": "closed" closes that
    bead with no reason (agent-os-vi6d). Every row must be readable here and
    none closed. With no file bd reads the export (.beads/issues.jsonl, which
    holds closed rows), and - reads stdin, which the hook cannot see. Like
    --reason-file, the file is read from the shell's directory, not -C's."""
    paths, dry, flags, j = [], False, True, 0
    while j < len(args):
        a = args[j]
        if flags and a == "--":
            flags = False  # everything after is a file, even --dry-run
        elif flags and a in ("-h", "--help"):
            return
        elif flags and a == "--dry-run":
            dry = True
        elif flags and a.startswith("--dry-run="):  # the last one wins, as in pflag
            dry = a.split("=", 1)[1].lower() in ("1", "t", "true")
        elif flags and a in ("-i", "--input"):
            paths.append(args[j + 1] if j + 1 < len(args) else ""); j += 1
        elif flags and a.startswith("--input="):
            paths.append(a.split("=", 1)[1])
        elif flags and a in GLOBAL_VAL:
            j += 1
        elif a.startswith("<<"):
            pass  # a heredoc feeds stdin; it is not a file
        elif not flags or a == "-" or not a.startswith("-"):
            paths.append(a)
        j += 1
    if dry:
        return
    stop = ("close-reason hook: bd import can close beads without a reason the hook can check "
            "(a row with \"status\": \"closed\"); %s. Close each with 'bd close <id> --reason-file <path>'.")
    if not paths or "-" in paths:
        refuse(stop % ("it reads %s, which the hook cannot check"
                       % ("stdin" if paths else "the export (.beads/issues.jsonl) when given no file")))
        return
    for path in paths:
        if not import_file_ok(sh, path, stop):
            return

def import_file_ok(sh, path, stop):
    fp = file_arg(sh, path, "bd import")
    if fp is None:
        return False
    try:
        with open(fp) as fh:
            rows = [json.loads(l) for l in fh if l.strip()]
        if not all(isinstance(r, dict) for r in rows):
            raise ValueError
    except Exception:
        refuse("close-reason hook: bd import file '%s' is not readable as JSONL; refusing" % path)
        return False
    # bd (Go) matches JSON keys case-insensitively, so "Status" and "ſtatus"
    # set the status too (a later key wins, as a later duplicate does here).
    if any(k.casefold() == "status" and str(v).casefold().strip() == "closed"
           for r in rows for k, v in r.items()):
        refuse(stop % ("'%s' has a closed row" % path))
        return False
    return True

def walk(s, cur, depth=0):
    text, live, bodies = strip_heredocs(s)
    # Safety nets for quoting the hook reads differently from bash. A body
    # line that starts a bd close is more likely a line the stripper
    # swallowed than data (nested quotes such as "`echo "<<EOF"`" or
    # ${x:-<<EOF} put a real command there); a script is written with a file
    # tool instead. And shlex cannot read $'...' at all.
    if any(BODY_CLOSE.search(b) for b in bodies):
        refuse("close-reason hook: a heredoc body has a line that runs bd close/done/batch/import (or delete, duplicate, supersede, orphans, mol burn), "
               "and the hook cannot be sure it is data (nested quotes can make bash run it). "
               "If it is text (a bead description, markdown, a script), write it to a file "
               "with a file tool in an earlier call and pass the path (--body-file, "
               "--reason-file, bd batch -f); or run the close on its own.")
        return cur
    if ansi_c(text) and BD_CLOSE_CMD.search(s):
        refuse("close-reason hook: $'...' quoting next to a bd close (or delete); the hook cannot parse it. "
               "Run the close on its own with plain quotes.")
        return cur
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
    its = list(items(toks))
    dirs = shell_dirs(its, cur)
    for k, (kind, seg) in enumerate(its):
        if kind == "op":
            continue
        cur = dirs[k]
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
        if b in ("cd", "pushd", "popd", "source", "."):
            continue  # shell_dirs() has already accounted for it
        if b == "eval":
            # eval joins its words and parses the result as a command line.
            if depth < 3:
                walk(" ".join(args), cur, depth + 1)
            elif BD_WORD.search(" ".join(args)):
                refuse("close-reason hook: eval nested too deep to follow; refusing rather than guessing")
            continue
        if b in SHELLS:
            m = next((n for n, a in enumerate(args) if SHELL_C.match(a)), None)
            if m is not None:
                rest = [a for a in args[m + 1:] if a != "--"]
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
        # todo done / mol burn: a subcommand of a subcommand, with flags (and
        # their values) allowed before it; those flags still reach the gate
        sub = "mol" if sub == "protomolecule" else sub  # bd's alias for mol
        if sub in ("todo", "mol"):
            pre, j2 = [], 0
            while j2 < len(rest) and rest[j2].startswith("-"):
                takes = rest[j2] in GLOBAL_VAL | {"-C", "--directory", "--reason"}
                pre += rest[j2:j2 + 1 + takes]; j2 += 1 + takes
            if j2 < len(rest):
                sub, rest = sub + " " + rest[j2], pre + rest[j2 + 1:]
        if xargs and sub in ("close", "done", "update", "import", "todo done") + NO_REASON:
            refuse("close-reason hook: 'xargs bd %s' hides the ids from the hook; name each id" % sub)
        elif sub in ("close", "done", "todo done"):
            bd_close(cur, d, rest)  # todo done takes --reason, as close does
        elif sub in NO_REASON:
            bd_no_reason(sub, d, rest)
        elif sub == "update":
            bd_update(d, rest)
        elif sub == "batch":
            piped = any(kind2 == "op" and x in ("|", "|&") for kind2, x in its[:k])
            bd_batch(cur, rest, s, piped, depth > 0)
        elif sub == "import":
            bd_import(cur, rest)

if re.search(r"(^|[^A-Za-z0-9_.-])bd($|[^A-Za-z0-9_-])", cmd):
    walk(cmd, base)
    named_elsewhere()
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
          echo "close-reason hook: can't tell which directory 'bd ${kind} ${id}' runs in, so its type cannot be looked up: the command changes directory in a way the hook does not follow, or -C names a path the shell would expand. Use bd -C <absolute dir>, or run the close in its own command." >&2; rc=2; continue
        fi
        if ! type=$(bead_type "$id" "$dir"); then rc=2; continue; fi
        if [ "$kind" = noreason ]; then  # $reason carries the command, e.g. "bd duplicate"
          if [ "$type" = bug ]; then
            echo "close-reason hook: refusing '${reason} ${id}': it closes or deletes a bug bead with no reason. Use: bd close ${id} --reason-file <path>" >&2
            rc=2
          else
            echo "close-reason: not a bug bead (type: ${type}); '${reason}' allowed"
          fi
          continue
        fi
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
# st_says <ere> <text> : does a control's output say what it wanted? A
# here-string for the same reason as has() (agent-os-7dkt, OBSERVED on CI
# here too, with ~1 KB of output: bash writes it line by line).
st_says() { command grep -qE "$1" <<<"$2"; }
selftest_case() {
  local name=$1 want=$2 type=$3 file=$4 want_msg=${5:-} out rc
  # `&& rc=0 || rc=$?`, not `; rc=$?`: under set -e a non-zero substitution
  # assignment exits the script, and the self-test would die silently on its
  # first expected refusal (OBSERVED on the first run of this file).
  out=$(check_reason "$type" "$file" 2>&1) && rc=0 || rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || st_says "$want_msg" "$out"; then return 0; fi
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
  out=$(command printf '%s' "$input" | ST_ENV_9OO5="$ST_DIR" HOME="$ST_DIR" PATH="$ST_DIR/bin:$PATH" bash "$0" --hook 2>&1) && rc=0 || rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || st_says "$want_msg" "$out"; then return 0; fi
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
  # --- more text than a pipe buffer holds (64 KiB), fields on the first
  # lines: the reader stops at its first match while the writer still has
  # ~1 MiB to go, so a piped matcher loses these every time (agent-os-7dkt).
  local pad i
  command printf -v pad '%63s' ''; pad=${pad// /x}$'\n'
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14; do pad+=$pad; done
  { command cat "$ST_DIR/complete.txt"; command printf '%s' "$pad"; } > "$ST_DIR/large.txt"
  command grep -viE '^verdict' "$ST_DIR/large.txt" > "$ST_DIR/large-no-verdict.txt"
  selftest_case large-reason          0 bug  "$ST_DIR/large.txt"      'all four class-sweep fields present'
  selftest_case large-missing-verdict 1 bug  "$ST_DIR/large-no-verdict.txt" 'missing: Verdict$'
  # The self-test's own matcher, both ways, on the same large text.
  ST_RUN=$((ST_RUN + 1))
  if ! st_says '^line-1$' "line-1"$'\n'"$pad"; then
    echo "FAIL: close-reason self-test - control 'st-says-large-match': a match on line 1 of ~1 MiB read as no match"; ST_FAILS=$((ST_FAILS + 1))
  fi
  ST_RUN=$((ST_RUN + 1))
  if st_says '^absent$' "line-1"$'\n'"$pad"; then
    echo "FAIL: close-reason self-test - control 'st-says-large-absent': a pattern absent from ~1 MiB read as a match"; ST_FAILS=$((ST_FAILS + 1))
  fi
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
  # inside. (bash's %q form, $'...', is not something shlex can read: next to
  # a close the hook refuses it, see ansi_c() and the ansi-c-* controls.)
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
  hook_case cd-reason-file-sub        0 "cd $ST_DIR/sub && bd close t-bug-1 --reason-file r.txt"
  # -C does not move the reason file: bd reads it from the shell's directory.
  hook_case dash-C-reason-file-sub    2 "bd -C sub close t-bug-1 --reason-file r.txt"     'missing: Verdict'
  hook_case dash-C-bad                2 "bd -C sub close t-bug-1 -r fixed"                "$ALL"
  hook_case close-dash-C-sub          2 "bd close -C sub t-bug-1 --reason-file r.txt"     'missing: Verdict'
  : > "$ST_DIR/bin/lookups.log"
  hook_case cd-type-lookup            0 "cd $ST_DIR/sub && bd close t-task-7 -r 'Done.'"  'not a bug bead'
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
EOF"                                                                                         'heredoc body'
  hook_case heredoc-to-bash-mid-line  2 "bash <<'EOF'
echo go; bd close t-bug-1 -r fixed
EOF"                                                                                         'heredoc body'
  hook_case heredoc-prose-mention-ok  0 "cat <<'EOF'
Then run bd close on it, as usual (bd close X).
| bd close | the command |
EOF"
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
  # --- more bd subcommands that close or delete a bead with no reason
  # (agent-os-yplt, each OBSERVED closing/deleting an open bug on bd 1.1.2)
  local NR='closes or deletes a bug bead' PK='picks itself'
  hook_case duplicate-bug             2 "bd duplicate t-bug-1 --of t-task-1"              "$NR"
  hook_case duplicate-task            0 "bd duplicate t-task-1 --of t-bug-1"              'bd duplicate. allowed'
  hook_case supersede-bug             2 "bd supersede t-bug-1 --with t-task-2"            "$NR"
  hook_case supersede-task            0 "bd supersede t-task-1 --with t-bug-2"            'bd supersede. allowed'
  hook_case delete-bug-force          2 "bd delete t-bug-1 --force"                       "$NR"
  hook_case delete-bug-short-f        2 "bd delete -f t-bug-1"                            "$NR"
  hook_case delete-bug-preview        0 "bd delete t-bug-1"
  hook_case delete-task-force         0 "bd delete t-task-1 --force"                      'bd delete. allowed'
  hook_case delete-dry-run            0 "bd delete t-bug-1 --force --dry-run"
  hook_case delete-from-file          2 "bd delete --from-file ids.txt --force"           "$PK"
  hook_case delete-cascade            2 "bd delete t-task-1 --force --cascade"            "$PK"
  hook_case burn-bug                  2 "bd mol burn t-bug-1 --force"                     "$NR"
  hook_case burn-task                 0 "bd mol burn t-task-1 --force"                    'bd mol burn. allowed'
  hook_case burn-dry-run              0 "bd mol burn t-bug-1 --dry-run"
  hook_case squash-not-gated          0 "bd mol squash t-bug-1"
  hook_case auto-merge                2 "bd duplicates --auto-merge"                      "$PK"
  hook_case auto-merge-dry-run        0 "bd duplicates --auto-merge --dry-run"
  hook_case duplicates-list           0 "bd duplicates"
  hook_case orphans-fix               2 "bd orphans --fix"                                "$PK"
  hook_case orphans-f-cluster         2 "bd orphans --details -f"                         "$PK"
  hook_case orphans-list              0 "bd orphans --details"
  hook_case orphans-label-f           0 "bd orphans -l f"
  hook_case orphans-label-glued-f     0 "bd orphans -lf"
  hook_case sql-write                 2 "bd sql \"UPDATE issues SET status='closed' WHERE id='t-bug-1'\"" 'write statement'
  hook_case sql-read                  0 "bd sql 'SELECT id FROM issues'"
  hook_case todo-done-bare            2 "bd todo done t-bug-1"                            'missing: Class statement'
  hook_case todo-done-complete        0 "bd todo done t-bug-1 --reason \"$ok\""
  hook_case todo-done-task            0 "bd todo done t-task-1"                           'not a bug bead'
  hook_case todo-add-not-gated        0 "bd todo add 'write docs'"
  hook_case duplicate-wrapped         2 "timeout 5 bd duplicate t-bug-1 --of t-task-1"    "$H"
  hook_case delete-xargs              2 "echo t-bug-1 | xargs bd delete --force"          'xargs bd delete'
  # review of f3a5061, each OBSERVED closing/deleting a bug on real bd 1.1.2:
  # the protomolecule alias, flags before the sub-subcommand, bool spellings
  hook_case protomolecule-burn        2 "bd protomolecule burn t-bug-1 --force"           "$NR"
  hook_case burn-after-dir-flag       2 "bd mol -C $ST_DIR burn t-bug-1 --force"          "$NR"
  hook_case burn-after-actor-flag     2 "bd mol --actor x burn t-bug-1 --force"           "$NR"
  hook_case todo-reason-before-done   2 "bd todo --reason Completed done t-bug-1"         'missing: Class statement'
  hook_case todo-good-reason-before   0 "bd todo --reason \"$ok\" done t-bug-1"
  hook_case todo-dir-before-done      2 "bd todo -C $ST_DIR done t-bug-1"                 'missing: Class statement'
  hook_case delete-dry-run-F          2 "bd delete t-bug-1 --force --dry-run=F"           "$NR"
  hook_case delete-dry-run-undone     2 "bd delete t-bug-1 --force --dry-run=false"       "$NR"
  hook_case burn-dry-run-False        2 "bd mol burn t-bug-1 --force --dry-run=False"     "$NR"
  hook_case sql-built-query           2 "bd sql \"\$(cat q.sql)\""                        'write statement'
  hook_case todo-done-xargs           2 "echo t-bug-1 | xargs bd todo done"               'xargs bd todo done'
  hook_case delete-piped-to-sh        2 "echo 'bd delete t-bug-1 --force' | sh"           "$H"
  hook_case delete-heredoc-body       2 "cat > /dev/null <<'EOF'
bd delete t-bug-1 --force
EOF"                                                                                         'heredoc body'
  # ... and what must not be refused: a grep naming delete, a doc with bd sql
  hook_case grep-delete-word-ok       0 "grep -rn bd scripts/ --include=*.sh -e delete"
  hook_case heredoc-sql-doc-ok        0 "cat > /dev/null <<'EOF'
bd sql 'SELECT count(*) FROM issues'
EOF"
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
  # bd batch reads stdin with no -f: only a heredoc is text the hook can
  # read; a pipe, < or <<< is refused even when what it feeds is harmless
  # (qc-v3 finding 7: each of these passed a close line before)
  local BS='heredoc in the command'
  hook_case batch-pipe-close          2 "cat batch-close.txt | bd batch"                  "$BS"
  hook_case batch-pipe-create         2 "cat batch-create.txt | bd batch"                 "$BS"
  hook_case batch-printf-pipe         2 "printf 'close t-bug-1\\n' | bd batch"            "$BS"
  hook_case batch-redirect            2 "bd batch < batch-close.txt"                      "$BS"
  hook_case batch-herestring          2 "bd batch <<< 'close t-bug-1'"                    "$BS"
  hook_case batch-quoted-pipe-ok      0 "bd batch -m 'a | b < c' <<'EOF'
create --title x
EOF"
  hook_case batch-quoted-arg-only    2 "bd batch '<<EOF'"                                "$BS"
  hook_case batch-heredoc-not-its-own 2 "true <<'EOF'
create --title x
EOF
bd batch"                                                                                    "$BS"
  # a quoted '<<EOF' is an argument, not a heredoc; stdin is the pipe
  hook_case batch-fake-heredoc-arg    2 "cat batch-close.txt | bd batch '<<EOF'; true <<'EOF'
create --title x
EOF"                                                                                         "$BS"
  # bash applies redirections left to right: < after the heredoc wins
  hook_case batch-heredoc-then-redir  2 "bd batch <<'EOF' < batch-close.txt
create --title x
EOF"                                                                                         "$BS"
  hook_case batch-outer-pipe          2 "cat batch-close.txt | bash -c \"bd batch '<<E'; true <<'E'
create --title x
E\""                                                                                         "$BS"
  hook_case batch-unquoted-heredoc    2 "bd batch <<EOF
create --title \$(echo x)
EOF"                                                                                         "$BS"
  hook_case batch-unquoted-backslash  2 "bd batch <<EOF
clo\\
se t-bug-1
EOF"                                                                                         "$BS"
  hook_case batch-unquoted-literal-ok 0 "bd batch <<EOF
create --title x
EOF"
  # review of a3382ce: bash splits batch<<'EOF' into batch and a heredoc
  hook_case batch-glued-heredoc       2 "bd batch<<'EOF'
close t-bug-1
EOF"                                                                                         'bd batch closes'
  hook_case close-glued-heredoc       2 "bd close<<'EOF'
x
EOF"                                                                                         'no id'
  # ... a | after bd batch carries its stdout, a comment is not a pipe, an
  # unrelated literal heredoc is not an expansion
  hook_case batch-stdout-pipe-ok      0 "bd batch <<'EOF' 2>&1 | tail -5
create --title x
EOF"
  hook_case batch-later-pipe-ok       0 "bd batch <<'EOF'
create --title x
EOF
bd list --json | head -3"
  hook_case batch-comment-pipe-ok     0 "bd batch <<'EOF' # a | b < c
create --title x
EOF"
  hook_case batch-other-heredoc-ok    0 "cat > /dev/null <<EOF
notes
EOF
bd batch <<'EOF'
create --title x
EOF"
  # ... and what still refuses: a pipe into bd batch after --, a < or a pipe
  # visible only inside a bash -c string
  hook_case batch-dashdash-piped      2 "cat batch-close.txt | bd batch -- -f batch-create.txt" "$BS"
  hook_case batch-nested-redirect     2 "bash -c \"bd batch <<'E' < batch-close.txt
create --title x
E\""                                                                                         "$BS"
  hook_case batch-nested-pipe         2 "bash -c \"cat batch-close.txt | bd batch '<<E'; true <<'E'
create --title x
E\""                                                                                         "$BS"
  hook_case batch-glued-f-rewritten   2 "printf 'close t-bug-1\\n' > batch-create.txt; bd batch -fbatch-create.txt" 'named elsewhere'
  # review of 2e3e7f4: |& is a pipe too; x\ # is one word, not a comment
  # (bash: -m gets "x #", and the < after it still feeds stdin)
  hook_case batch-pipe-amp            2 "cat batch-close.txt |& { bd batch '<<E'; }; true <<'E'
create --title x
E"                                                                                           "$BS"
  hook_case batch-escaped-hash-redir  2 "bd batch <<'E' -m x\\ # < batch-close.txt
create --title x
E"                                                                                           "$BS"
  hook_case batch-escaped-hash-body   2 "bd batch -m x\\ # <<'E'
close t-bug-1
E
true <<'F'
create --title x
F"                                                                                           'bd batch closes'
  hook_case update-glued-heredoc-task 0 "bd update t-task-1 --status closed<<'EOF'
x
EOF"                                                                                         'status change allowed'
  hook_case update-glued-heredoc-bug  2 "bd update t-bug-1 --status closed<<'EOF'
x
EOF"                                                                                         'bd update has none'
  hook_case batch-quoted-close-word   2 "bd batch <<'EOF'
\"close\" t-bug-1
EOF"                                                                                         'bd batch closes'
  hook_case batch-status-quoted       2 "bd batch <<'EOF'
update t-bug-1 status=\"closed\"
EOF"                                                                                         'bd batch closes'
  hook_case batch-file-eq-close       2 "bd batch --file=batch-close.txt"                 'bd batch closes'
  hook_case batch-file-eq-create      0 "bd batch --file=batch-create.txt"
  hook_case batch-f-glued-close       2 "bd batch -fbatch-close.txt"                      'bd batch closes'
  hook_case batch-f-glued-create      0 "bd batch -fbatch-create.txt"
  hook_case batch-f-cluster-close     2 "bd batch -qf batch-close.txt"                    'bd batch closes'
  hook_case batch-f-cluster-create    0 "bd batch -qf=batch-create.txt"
  hook_case batch-dry-run             0 "bd batch -f batch-close.txt --dry-run"
  hook_case batch-dry-run-undone      2 "bd batch -f batch-close.txt --dry-run --dry-run=false" 'bd batch closes'
  # --- a path the shell would expand is refused, not guessed (agent-os-9oo5).
  # The hook runs before the shell, so it cannot know what $SP or ~ will be.
  # Two review rounds of tracking assignments found 31 ways for the hook to
  # read a complete file while bash hands bd an incomplete one. Each control
  # names a COMPLETE file, so a hook that expands the path exits 0.
  hook_case var-path-refused          2 "SP=$ST_DIR; bd close t-bug-1 --reason-file \$SP/complete.txt"   'literal path'
  hook_case var-braces-refused        2 "export SP=$ST_DIR && bd close t-bug-1 --reason-file \${SP}/complete.txt" 'literal path'
  hook_case var-hook-env-refused      2 "bd close t-bug-1 --reason-file \$ST_ENV_9OO5/complete.txt"      'literal path'
  hook_case var-reason-file-eq        2 "SP=$ST_DIR; bd close t-bug-1 --reason-file=\$SP/complete.txt"   'literal path'
  hook_case tilde-refused             2 "bd close t-bug-1 --reason-file ~/complete.txt"                 'literal path'
  hook_case var-batch-file-refused    2 "SP=$ST_DIR; bd batch -f \$SP/batch-create.txt"                'literal path'
  hook_case var-dash-C-refused        2 "SP=$ST_DIR; bd -C \$SP close t-bug-1 --reason-file complete.txt" 'which directory'
  hook_case var-cd-refused            2 "cd \$ST_ENV_9OO5 && bd close t-bug-1 --reason-file complete.txt" 'changes directory'
  hook_case literal-abs-path-good     0 "bd close t-bug-1 --reason-file $ST_DIR/complete.txt"
  hook_case same-call-file-refused    2 "printf x > new-9oo5.txt && bd close t-bug-1 --reason-file new-9oo5.txt" 'named elsewhere'
  # --- an unparseable line that mentions bd is refused: shlex cannot read
  # $'...' quoting, and a spelling like bd clos"e" hides the close from any
  # text pattern (review of agent-os-9oo5, OBSERVED fail-open when gated)
  hook_case unparseable-ansi-c-close  2 "bd clos\"e\" t-bug-1 -r \$'it\\'s fixed'"      'could not parse'
  # --- a mention of bd close that is not a call stays ungated
  hook_case grep-mention              0 "command grep -rn 'bd close' scripts"
  hook_case wrapper-no-close          0 "timeout 5 bd show t-bug-1"
  hook_case echo-mention              0 "echo 'bd close t-bug-1 -r fixed'"
  hook_case commit-msg-mention        0 "git commit -m \"bd close t-bug-1 -r fixed\""
  # --- a quoted or commented << is not a heredoc (agent-os-jox1): it must
  # not swallow the next line, and a real heredoc after it must still strip.
  hook_case heredoc-dq-mention-bad    2 "echo \"<<EOF\"
bd close t-bug-1 -r fixed
EOF"                                                                                         "$ALL"
  hook_case heredoc-sq-mention-bad    2 "echo '<<EOF'
bd close t-bug-1 -r fixed
EOF"                                                                                         "$ALL"
  hook_case heredoc-comment-bad       2 "echo x # <<EOF
bd close t-bug-1 -r fixed
EOF"                                                                                         "$ALL"
  hook_case heredoc-after-mention-ok  0 "cat <<'X' # see \"<<EOF\"
use \`bd close t-bug-1 -r fixed\`
X"
  hook_case heredoc-arith-shift-bad   2 "echo \$((1<<N))
bd close t-bug-1 -r fixed
N"                                                                                           "$ALL"
  hook_case heredoc-escaped-lt-bad    2 "echo x\\<<EOF
bd close t-bug-1 -r fixed
EOF"                                                                                         "$ALL"
  # a plain << ends only at the bare delimiter; <<- strips leading tabs
  hook_case heredoc-tab-delim-ok      0 "cat <<EOF
	EOF
it's data
EOF
bd close t-bug-1 --reason-file sub/r.txt"
  hook_case heredoc-dash-tab-delim-bad 2 "cat <<-EOF
	EOF
bd close t-bug-1 -r fixed
EOF"                                                                    "$ALL"
  hook_case heredoc-partial-quote-ok  0 "cat <<E\"OF\"
it's data
E
EOF
bd close t-bug-1 --reason-file sub/r.txt"
  hook_case heredoc-nested-bq-bad     2 "echo \"\`echo \"<<EOF\"\`\"
bd close t-bug-1 -r fixed
EOF"                                                                                         'heredoc body|missing'
  hook_case heredoc-param-default-bad 2 "echo \${x:-<<EOF}
bd close t-bug-1 -r fixed
EOF"                                                                                         'heredoc body|missing'
  hook_case dollar-quote-in-pattern-ok 0 "command grep -E '^[0-9]+\$' x; bd close t-bug-1 --reason-file sub/r.txt"
  hook_case ansi-c-loop-done-ok       0 "IFS=\$'\\n'; for id in \$(bd ready --json); do bd show \$id; done"
  hook_case ansi-c-status-closed-ok   0 "bd list --status closed | cut -d\$'\\t' -f1"
  hook_case ansi-c-quote-refused      2 "echo \$'\\'' ; bd close t-bug-1 -r fixed ; echo \$'\\''\\'" 'plain quotes'
  hook_case heredoc-in-dq-subst-ok    0 "git commit -m \"\$(cat <<'EOF'
it's \`bd close\` text
EOF
)\""
  hook_case heredoc-in-dq-subst-live  2 "x=\"\$(cat <<EOF
\$(bd close t-bug-1 -r fixed)
EOF
)\""                                                                                         "$H"
  # --- a command that changes directory (agent-os-jox1): the hook does not
  # track where bash keeps a cd, so a relative reason file is refused and
  # the type lookup needs an absolute bd -C. ./r.txt is incomplete and
  # sub/r.txt complete, so a hook that guessed "sub" would exit 0.
  local MV='changes directory'
  hook_case cd-subshell-refused       2 "(cd sub) && bd close t-bug-1 --reason-file r.txt"   "$MV"
  hook_case cd-background-refused     2 "cd sub & bd close t-bug-1 --reason-file r.txt"      "$MV"
  hook_case cd-pipeline-refused       2 "cd sub | cat; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case cd-cmd-subst-refused      2 "x=\$(cd sub); bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case cd-if-body-refused        2 "if false; then cd sub; fi; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case cd-brace-pipe-refused     2 "{ cd sub; } | cat; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case env-cd-refused            2 "env cd sub; bd close t-bug-1 --reason-file r.txt"   "$MV"
  hook_case popd-refused              2 "pushd sub; popd -n; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case func-def-refused          2 "f() {
cd $ST_DIR/sub
}; bd close t-bug-1 --reason-file r.txt"                                                      "$MV"
  hook_case eval-cd-refused           2 "eval 'cd sub'; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case var-command-word-refused  2 "C=cd; \$C sub; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case trap-refused              2 "trap 'cd sub' DEBUG; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case source-refused            2 ". ./x.sh; bd close t-bug-1 --reason-file r.txt"     "$MV"
  # the type lookup cannot run either: bd resolves the database from its cwd
  hook_case cd-then-close-inline      2 "cd sub && bd close t-task-1 -r 'Done.'"              'which directory'
  # what still works: an absolute reason file with an absolute -C, a
  # leading cd to an absolute directory, and moves that are not moves
  hook_case cd-abs-paths-good         0 "cd sub && bd -C $ST_DIR close t-bug-1 --reason-file $ST_DIR/sub/r.txt"
  hook_case cd-abs-paths-bad          2 "cd sub && bd -C $ST_DIR close t-bug-1 --reason-file $ST_DIR/r.txt" 'missing: Verdict'
  hook_case leading-abs-cd-good       0 "cd $ST_DIR/sub && bd close t-bug-1 --reason-file r.txt"
  hook_case leading-abs-cd-semi-good  0 "cd $ST_DIR/sub
bd close t-bug-1 --reason-file r.txt"
  hook_case leading-abs-cd-then-cd    2 "cd $ST_DIR && cd sub && bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case leading-missing-dir       2 "cd $ST_DIR/nope; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case abs-cd-after-preamble     0 "x=1; echo hi >/dev/null
cd $ST_DIR/sub && bd close t-bug-1 --reason-file r.txt"
  hook_case abs-cd-after-and          2 "false && cd $ST_DIR/sub; bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case abs-cd-in-body            2 "if false; then
cd $ST_DIR/sub
fi
bd close t-bug-1 --reason-file r.txt"                                                         "$MV"
  hook_case abs-cd-with-background    2 "cd $ST_DIR/sub && sleep 1 & bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case abs-cd-in-subst           2 "x=\$(true; cd $ST_DIR/sub; true); bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case body-without-cd-good      0 "if true; then echo; fi; bd close t-bug-1 --reason-file sub/r.txt"
  hook_case function-kw-body-cd       2 "function f {
cd $ST_DIR/sub
}
bd close t-bug-1 --reason-file r.txt"                                                         "$MV"
  hook_case function-kw-no-cd-good    0 "function f {
echo
}
bd close t-bug-1 --reason-file sub/r.txt"
  hook_case case-paren-then-cd       2 "( case x in a) ;; esac; cd $ST_DIR/sub; ); bd close t-bug-1 --reason-file r.txt" "$MV"
  hook_case quoted-fi-then-cd         2 "\"fi\"; if false; then
cd $ST_DIR/sub
fi
bd close t-bug-1 --reason-file r.txt"                                                         "$MV"
  hook_case case-without-cd-good      0 "case x in a) ;; esac; bd close t-bug-1 --reason-file sub/r.txt"
  hook_case dot-argument-not-a-move   0 "git add . && bd close t-bug-1 --reason-file sub/r.txt"
  hook_case eval-no-cd-good           0 "eval bd close t-bug-1 --reason-file sub/r.txt"
  # --- bd reads --reason-file, batch -f and import files from the SHELL's
  # directory; -C only picks the database (OBSERVED, bd 1.1.2, agent-os-jox1).
  hook_case dash-C-late-reason-file   2 "bd close t-bug-1 --reason-file r.txt -C sub"        'missing: Verdict'
  hook_case dash-C-path-good          0 "bd -C sub close t-bug-1 --reason-file sub/r.txt"
  command printf 'create --title x\n' > "$ST_DIR/sub/batch-close.txt"
  hook_case dash-C-batch-file         2 "bd -C sub batch -f batch-close.txt"                 'bd batch closes'
  : > "$ST_DIR/bin/lookups.log"
  hook_case dash-C-type-lookup        0 "bd -C sub close t-task-7 -r 'Done.'"                'not a bug bead'
  ST_RUN=$((ST_RUN + 1))
  if ! command grep -qx "$ST_DIR/sub t-task-7" "$ST_DIR/bin/lookups.log"; then
    echo "FAIL: close-reason self-test - control 'dash-C-type-lookup-dir': the type lookup did not run in $ST_DIR/sub; lookups:"; command sed 's/^/    /' "$ST_DIR/bin/lookups.log"
    ST_FAILS=$((ST_FAILS + 1))
  fi
  # --- bd import upserts rows, and a "closed" row closes the bead with no
  # reason (agent-os-vi6d). With no file it reads the export, which holds
  # closed rows; - reads stdin, which the hook cannot see.
  command printf '{"id":"t-bug-1","title":"x","status":"open"}\n{"_type":"memory","key":"k","value":"v"}\n' > "$ST_DIR/import-open.jsonl"
  command printf '{"id":"t-task-1","title":"y","status":"open"}\n{"id":"t-bug-1","title":"x","status":"closed"}\n' > "$ST_DIR/import-closed.jsonl"
  command cp "$ST_DIR/import-open.jsonl" "$ST_DIR/sub/import-closed.jsonl"
  local I='bd import'
  hook_case import-file-closed        2 "bd import -i import-closed.jsonl"                   "$I"
  hook_case import-file-open          0 "bd import -i import-open.jsonl"
  hook_case import-positional-closed  2 "bd import import-closed.jsonl"                      "$I"
  hook_case import-positional-open    0 "bd import import-open.jsonl"
  hook_case import-eq-closed          2 "bd import --input=import-closed.jsonl"              "$I"
  hook_case import-default-refused    2 "bd import"                                          'the export'
  hook_case import-stdin-refused      2 "cat import-open.jsonl | bd import -"                'reads stdin'
  command printf '[1]\n' > "$ST_DIR/import-array.jsonl"
  hook_case import-not-object         2 "bd import -i import-array.jsonl"                    'not readable'
  hook_case import-xargs              2 "ls import-open.jsonl | xargs bd import"             'xargs bd import'
  hook_case import-pipe-to-sh         2 "echo 'bd import -i import-open.jsonl' | sh"         "$H"
  hook_case import-unreadable         2 "bd import -i nope.jsonl"                            'not readable'
  hook_case import-var-refused        2 "bd import -i \$SP/import-open.jsonl"               'literal path'
  hook_case import-dash-C-cwd         2 "bd -C sub import -i import-closed.jsonl"            "$I"
  hook_case import-dry-run            0 "bd import -i import-closed.jsonl --dry-run"
  hook_case import-dry-run-undone     2 "bd import -i import-closed.jsonl --dry-run --dry-run=false" "$I"
  hook_case import-glued-heredoc-open 0 "bd import -i import-open.jsonl<<'EOF'
x
EOF"
  hook_case import-glued-heredoc-closed 2 "bd import -i import-closed.jsonl<<'EOF'
x
EOF"                                                                                         "$I"
  hook_case import-dashdash-not-flag  2 "bd import -i import-open.jsonl -- --dry-run"         'not readable'
  hook_case import-second-file        2 "bd import import-open.jsonl -i import-closed.jsonl"  "$I"
  hook_case import-wrapped            2 "timeout 5 bd import -i import-open.jsonl"           "$H"
  command printf '{"id":"t-bug-1","title":"x","Status":"closed"}\n' > "$ST_DIR/import-key-case.jsonl"
  command printf '{"id":"t-bug-1","title":"x","\xc5\xbftatus":"closed"}\n' > "$ST_DIR/import-key-fold.jsonl"
  command printf '{"id":"t-bug-1","title":"x","status":"open","status":"closed"}\n' > "$ST_DIR/import-key-dup.jsonl"
  hook_case import-key-case           2 "bd import -i import-key-case.jsonl"                 "$I"
  hook_case import-key-fold           2 "bd import -i import-key-fold.jsonl"                 "$I"
  hook_case import-key-dup            2 "bd import -i import-key-dup.jsonl"                  "$I"
  hook_case import-dev-stdin          2 "bd import -i /dev/stdin < import-closed.jsonl"      'device or process'
  hook_case import-same-call-copy     2 "cp import-closed.jsonl import-open.jsonl && bd import -i import-open.jsonl" 'named elsewhere'
  command cp "$ST_DIR/complete.txt" "$ST_DIR/reuse.txt"
  hook_case reason-rewritten-same-call 2 "printf 'Fixed.' > reuse.txt; bd close t-bug-1 --reason-file reuse.txt" 'named elsewhere'
  hook_case reason-dev-stdin          2 "bd close t-bug-1 --reason-file /dev/stdin < complete.txt" 'device or process'
  # bd's own reads are not "elsewhere" (qc-v3 finding 6): two closes may share
  # a file, and a/r.txt next to b/r.txt passes; any other mention of the name
  # still refuses, in any directory and any quoting.
  command mkdir -p "$ST_DIR/sub2"; command cp "$ST_DIR/complete.txt" "$ST_DIR/sub2/r.txt"
  local NE='named elsewhere'
  hook_case shared-reason-file        0 "bd close t-bug-1 --reason-file reuse.txt && bd close t-bug-2 --reason-file reuse.txt"
  hook_case shared-reason-file-write  2 "bd close t-bug-1 --reason-file reuse.txt && printf x > reuse.txt && bd close t-bug-2 --reason-file reuse.txt" "$NE"
  hook_case same-basename-two-dirs    0 "bd close t-bug-1 --reason-file sub/r.txt && bd close t-bug-2 --reason-file sub2/r.txt"
  hook_case same-basename-other-write 2 "cp complete.txt sub2/r.txt && bd close t-bug-1 --reason-file sub/r.txt" "$NE"
  hook_case reason-quoted-name-write  2 "printf x > 'reuse'.txt; bd close t-bug-1 --reason-file reuse.txt"  "$NE"
  hook_case reason-escaped-name-write 2 "printf x > reuse\\.txt; bd close t-bug-1 --reason-file reuse.txt"  "$NE"
  hook_case reason-rm-after-refused   2 "bd close t-bug-1 --reason-file reuse.txt && rm reuse.txt"       "$NE"

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
