#!/usr/bin/env bash
# scripts/check-ws-registration.sh
#
# ONE invariant, the ratchet for agent-os-o1jp.1 (agent-os-o1jp.2):
#
#   backend/internal/handlers/ has EXACTLY ONE WebSocket upgrade site, reached
#   ONLY through the serveWS helper, and ZERO ConnectionManager registrations
#   (Add / AddUnmetered) outside serveWS.
#
# WHY. Before serveWS existed the upgrade + register + close boilerplate was
# written eight times across seven handler files and carried five separately
# filed defects (teop, tvh6, 1jzj, 14gr, iz9w -- see agent-os-o1jp.1's close
# reason). serveWS made that class unrepresentable: a handler cannot forget a
# close it does not write. Nothing else stops the boilerplate growing back one
# handler at a time; this check does.
#
# WHY A BASH CHECK AND NOT A LINTER RULE. agent-os-kvxs built three static
# scanners for a different rule and deleted all three for failing in both
# directions on a required check. What survived is the shape used here: a
# narrow rule, a loud message that names file:line, and a --self-test that
# proves the check still fires BOTH ways before its verdict is believed
# (scripts/check-docs.sh runs the self-test first, and fails the whole check
# if it fails).
#
# THREE COUNTS, each derived on every run:
#
#   1. `.Upgrade(` call sites, any receiver: exactly 1, inside serveWS or
#      inside the one named helper UPGRADE_FN (upgradeConnection).
#   2. `UPGRADE_FN(` call sites (its `func` line excluded): exactly 1, inside
#      serveWS. Count 1 alone is not enough -- a handler calling
#      upgradeConnection directly gets an upgraded socket with no
#      registration and no close policy while `.Upgrade(` stays at 1.
#   3. `.Add` / `.AddUnmetered` registrations: every one inside serveWS.
#
# "Inside serveWS" is a FUNCTION BOUNDARY, derived on every run: the line
# matching `^func [receiver ]serveWS(` up to the next line that is exactly
# `}` at column 0 (gofmt puts a top-level closing brace there). Line numbers
# rot -- agent-os-94yx moved everything below ws.go:560 by 33 lines the day
# before this was written -- so nothing here is a line range.
#
# HOW COUNT 3 CLASSIFIES A LINE. Include broadly, exclude narrowly, fail
# closed on anything ambiguous. Two traps, in opposite directions, both
# measured on this tree (cb1ab4e, 2026-09-05):
#
#   - RECEIVER-PINNED = FALSE ZERO. `cm.Add(` misses `h.cm.Add(`, `x.Add(`,
#     `reg.AddUnmetered(`. A receiver-qualified grep produced a false zero on
#     this exact code twice (agent-os-iz9w: `conn.Conn.Close()` missed
#     logs.go's `conn.Close()`; agent-os-jtax: durableRun's receiver is `r`,
#     not `dr`). So every pattern here matches ANY receiver, including the
#     `func serveWS(` / `func upgradeConnection(` definitions, which are
#     matched with or without a method receiver.
#   - SHAPE-PINNED = FALSE ZERO TOO. An earlier draft anchored on the
#     two-argument call `.Add(x, y)`. That evades on `cm.Add(connID(c), conn)`
#     (the nested call's parens break the argument class), on a future
#     three-argument signature, on a gofmt-wrapped multi-line call, and on a
#     method value `add := cm.Add`. Each is a false zero, the worse failure.
#
#   So: every non-test, non-comment line containing `.Add` or `.AddUnmetered`
#   at a word boundary is a CANDIDATE. The ONLY exclusion is the one known
#   false-positive family, matched by its own signature: `time.Now().Add(` or
#   a single-argument call whose argument mentions `time.` (a deadline). At
#   cb1ab4e that is 13 lines (auth.go x3, terminal.go x1, ws.go x9), and a
#   bare receiver-agnostic `\.Add(` returns those 13 plus one comment plus the
#   2 real sites: 16 in all. The self-test carries those 13 lines verbatim and
#   pins the count, so a new false-positive shape shows up as a red
#   classification, never as a silent widening of the exclusion.
#   Everything that is neither excluded nor a call closed on its own line
#   FAILS CLOSED with a message naming the line: an `.Add(` whose parentheses
#   do not balance on the line (multi-line call), an `.Add` not followed by
#   `(` (method value or field), or two `.Add`s on one line. A blind spot is
#   worse than asking the author to write the call on one line.
#
# WHAT IS DELIBERATELY NOT CAUGHT, stated rather than chased:
#   - a trailing `// ... cm.Add(id, conn)` comment AFTER code on the same
#     line, or a block comment: only full-line `//` comments are stripped.
#     Such a line goes RED and names itself, which is actionable, not silent.
#   - `.Upgrade(` and `.Add` are matched by shape, so a NEW unrelated type in
#     handlers/ with an Add method goes red too. Today there is none.
#     Red-and-named is the failure mode this accepts; green-and-wrong is the
#     one it refuses.
#
# Dependency-free by design: bash, awk, find, grep, mktemp.
#
# Usage:
#   check-ws-registration.sh [DIR]        check DIR (default backend/internal/handlers)
#   check-ws-registration.sh --self-test  prove the check still fires, both ways
#
# Exit: 0 clean, 1 violation or an unreadable input, 2 usage error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

DEFAULT_DIR_REL='backend/internal/handlers'
SERVE_FN='serveWS'
UPGRADE_FN='upgradeConnection'

# Candidate: `.Add` or `.AddUnmetered` at a word boundary, any receiver.
# (POSIX ERE has no \b, and in gawk \b is backspace; the class does the job.)
CAND_RE='\.(Add|AddUnmetered)([^A-Za-z0-9_]|$)'
# The ONLY excluded family: a time deadline. Either `time.Now().Add(` or a
# single-argument call whose argument mentions `time.` (no comma, no nested
# parens: `d.Add(5 * time.Second)`).
DEADLINE_RE='time\.Now\(\)\.Add\(|\.Add\([^,()]*time\.[A-Za-z]+[^,()]*\)'
# Any receiver's Upgrade call. `websocket.Upgrader{` has a brace, not a paren.
UPGRADE_RE='\.Upgrade\('

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [DIR | --self-test]

Fails unless DIR (default $DEFAULT_DIR_REL) has exactly one
WebSocket upgrade site (\`.Upgrade(\`) inside $SERVE_FN() or its helper
$UPGRADE_FN(), exactly one call to $UPGRADE_FN() and that inside $SERVE_FN(),
and no \`.Add\` / \`.AddUnmetered\` registration outside $SERVE_FN(). Test
files and full-line comments are ignored; time-deadline \`.Add(\` calls are
the one excluded shape; any \`.Add\` the line cannot classify fails closed.

--self-test runs the check against fixtures in a temp directory and asserts
the result both ways, so that a check which has silently stopped working is
not mistaken for a clean tree. check-docs.sh runs it before every real check.
USAGE
}

# ---------------------------------------------------------------------------
# the check
# ---------------------------------------------------------------------------

# List the non-test Go files of $1, one per line, sorted for stable output.
go_sources() {
  command find "$1" -maxdepth 1 -type f -name '*.go' ! -name '*_test.go' | command sort
}

# Print "file:start:end" for the top-level function NAME in the files of $1,
# matched with or without a method receiver. Empty output = not found.
fn_span() {
  local dir=$1 name=$2 f
  while IFS= read -r f; do
    command awk -v name="$name" -v file="$f" '
      !start && $0 ~ ("^func (\\([^)]*\\) )?" name "\\(") { start = NR; next }
      start && /^}/ { print file ":" start ":" NR; exit }
    ' "$f"
  done < <(go_sources "$dir")
}

# Print every "file:line:text" in the files of $1 whose text matches ERE $2,
# skipping full-line comments. The pattern goes in via the environment, not
# `-v`: awk -v processes backslash escapes in the value, which turns `\.`
# into `.` and `\(` into a bare `(`, and the regex then either matches
# everything or fails to compile (OBSERVED on the first run of this script).
hits() {
  local dir=$1 re=$2 f
  while IFS= read -r f; do
    HITS_RE="$re" command awk -v file="$f" '
      BEGIN { re = ENVIRON["HITS_RE"] }
      /^[[:space:]]*\/\// { next }
      $0 ~ re { print file ":" NR ":" $0 }
    ' "$f"
  done < <(go_sources "$dir")
}

# Classify every registration candidate in the files of $1. One record per
# candidate line: "file:line:CLASS:text", where CLASS is
#   DEADLINE  the excluded time-deadline shape
#   CALL      an .Add(...)/.AddUnmetered(...) whose parens balance on the line
#   MULTI     an .Add( whose parens do NOT balance on the line (wrapped call)
#   NOCALL    .Add / .AddUnmetered not followed by `(` (method value, field)
#   TWICE     more than one candidate on the line; cannot be told apart
classify_registrations() {
  local dir=$1 f
  while IFS= read -r f; do
    CAND="$CAND_RE" DEADLINE="$DEADLINE_RE" command awk -v file="$f" '
      BEGIN { cand = ENVIRON["CAND"]; deadline = ENVIRON["DEADLINE"] }
      /^[[:space:]]*\/\// { next }
      $0 !~ cand { next }
      {
        # Count candidates on the line.
        n = 0; s = $0
        while (match(s, cand)) { n++; s = substr(s, RSTART + 1) }
        if (n > 1) { print file ":" NR ":TWICE:" $0; next }
        if ($0 ~ deadline) { print file ":" NR ":DEADLINE:" $0; next }
        if (!match($0, /\.(Add|AddUnmetered)\(/)) { print file ":" NR ":NOCALL:" $0; next }
        # Walk from the opening paren; balanced on this line = a call.
        depth = 0; closed = 0
        for (i = RSTART + RLENGTH - 1; i <= length($0); i++) {
          c = substr($0, i, 1)
          if (c == "(") depth++
          else if (c == ")") { depth--; if (depth == 0) { closed = 1; break } }
        }
        print file ":" NR ":" (closed ? "CALL" : "MULTI") ":" $0
      }
    ' "$f"
  done < <(go_sources "$dir")
}

# True when hit "file:line:..." ($1) lies within span "file:start:end" ($2).
in_span() {
  local hit_file=${1%%:*} rest=${1#*:} hit_line
  hit_line=${rest%%:*}
  local span_file=${2%%:*} span_rest=${2#*:}
  local span_start=${span_rest%%:*} span_end=${span_rest#*:}
  [ "$hit_file" = "$span_file" ] && [ "$hit_line" -ge "$span_start" ] && [ "$hit_line" -le "$span_end" ]
}

# Print "file:line" of a "file:line:..." record, relative to $REPO_ROOT when
# it lives there, so messages read as paths a reader can open.
loc() {
  local h=$1 f l
  f=${h%%:*}
  h=${h#*:}
  l=${h%%:*}
  case "$f" in
    "$REPO_ROOT"/*) f=${f#"$REPO_ROOT"/} ;;
  esac
  printf '%s:%s' "$f" "$l"
}

# The source text of a "file:line:CLASS:text" record.
rec_text() {
  local r=$1
  r=${r#*:}; r=${r#*:}; r=${r#*:}
  printf '%s' "$r"
}

check_dir() {
  local dir=$1 failed=0 h

  if [ ! -d "$dir" ]; then
    echo "FAIL: ws-registration - $dir is not a directory; nothing to check"
    return 1
  fi

  # 1. Find serveWS. A renamed helper must be loud: with no span, every
  #    registration would be "outside" it, and the message must say WHICH
  #    footing was lost rather than list every site as a violation.
  local serve_span
  serve_span=$(fn_span "$dir" "$SERVE_FN")
  if [ -z "$serve_span" ]; then
    echo "FAIL: ws-registration - could not find func $SERVE_FN( in $dir; if the helper was renamed, update SERVE_FN in $(basename "$0") -- the invariant it ratchets is 'every WS handler registers through $SERVE_FN'"
    return 1
  fi
  local serve_loc
  serve_loc="$(loc "$serve_span")-${serve_span##*:}"

  # 2. Classify every .Add candidate. Unclassifiable lines fail closed, in
  #    or out of serveWS; calls outside serveWS are violations; deadline
  #    lines are counted so the exclusion stays visible.
  local records inside=0 deadlines=0 outside=() unclass=() cls
  records=$(classify_registrations "$dir")
  while IFS= read -r h; do
    [ -z "$h" ] && continue
    cls=${h#*:*:}
    cls=${cls%%:*}
    case "$cls" in
      DEADLINE) deadlines=$((deadlines + 1)) ;;
      CALL)
        if in_span "$h" "$serve_span"; then
          inside=$((inside + 1))
        else
          outside+=("$h")
        fi
        ;;
      *) unclass+=("$h:$cls") ;;
    esac
  done <<< "$records"

  if [ "${#unclass[@]}" -gt 0 ]; then
    echo "FAIL: ws-registration - ${#unclass[@]} .Add/.AddUnmetered line(s) this check cannot classify (a call wrapped across lines, a method value, or two on one line); write each as a single-line call so it can be checked, or it is a blind spot:"
    for h in "${unclass[@]}"; do
      cls=${h##*:}
      h=${h%:*}
      echo "    $(loc "$h") [$cls]: $(rec_text "$h")"
    done
    failed=1
  fi

  if [ "${#outside[@]}" -gt 0 ]; then
    echo "FAIL: ws-registration - ${#outside[@]} ConnectionManager registration(s) outside $SERVE_FN() (agent-os-o1jp.1 routes every WS handler through the helper; register via serveWS(..., wsRegistration{...}) instead):"
    for h in "${outside[@]}"; do
      echo "    $(loc "$h"): $(rec_text "$h")"
    done
    failed=1
  fi

  # 3. serveWS must itself contain a registration call. This is the positive
  #    control on the span derivation: a span that matched a stub or the
  #    wrong brace would report zero violations for the wrong reason.
  if [ "$inside" -eq 0 ]; then
    echo "FAIL: ws-registration - $SERVE_FN() at $serve_loc contains no .Add(/.AddUnmetered( call: the helper lost its registration footing, so 'zero registrations outside it' would be vacuous"
    return 1
  fi

  # 4. Exactly one upgrade site. Zero means the helper was gutted; two or
  #    more means a handler (or a second helper) upgrades on its own.
  local up_hits up_count=0 up_list=()
  up_hits=$(hits "$dir" "$UPGRADE_RE")
  while IFS= read -r h; do
    [ -z "$h" ] && continue
    up_count=$((up_count + 1))
    up_list+=("$h")
  done <<< "$up_hits"

  if [ "$up_count" -ne 1 ]; then
    echo "FAIL: ws-registration - want exactly 1 WebSocket upgrade site (.Upgrade() in $dir, found $up_count:"
    for h in "${up_list[@]}"; do
      echo "    $(loc "$h"): ${h#*:*:}"
    done
    return 1
  fi

  # 5. That one site must be in serveWS or in its named upgrade helper.
  local up_span up_where
  up_span=$(fn_span "$dir" "$UPGRADE_FN")
  if in_span "${up_list[0]}" "$serve_span"; then
    up_where="$SERVE_FN()"
  elif [ -n "$up_span" ] && in_span "${up_list[0]}" "$up_span"; then
    up_where="$UPGRADE_FN()"
  else
    echo "FAIL: ws-registration - the upgrade site at $(loc "${up_list[0]}") is in neither $SERVE_FN() nor $UPGRADE_FN(); if the upgrade helper was renamed, update UPGRADE_FN in $(basename "$0")"
    return 1
  fi

  # 6. The helper has exactly one caller, and it is serveWS. Count 4 alone
  #    lets a handler call upgradeConnection directly and get an upgraded
  #    socket with no registration and no close policy.
  local call_hits call_count=0 call_list=() call_loc=""
  call_hits=$(hits "$dir" "$UPGRADE_FN\\(" | command grep -vE '^[^:]*:[0-9]+:func ')
  while IFS= read -r h; do
    [ -z "$h" ] && continue
    call_count=$((call_count + 1))
    call_list+=("$h")
  done <<< "$call_hits"
  if [ "$call_count" -ne 1 ]; then
    echo "FAIL: ws-registration - want exactly 1 call to $UPGRADE_FN( (from $SERVE_FN), found $call_count; a handler that upgrades through the helper directly gets a socket with no registration and no close policy:"
    for h in "${call_list[@]}"; do
      echo "    $(loc "$h"): ${h#*:*:}"
    done
    return 1
  fi
  if ! in_span "${call_list[0]}" "$serve_span"; then
    echo "FAIL: ws-registration - the only call to $UPGRADE_FN( is at $(loc "${call_list[0]}"), outside $SERVE_FN() ($serve_loc); $SERVE_FN no longer owns the upgrade"
    return 1
  fi
  call_loc=$(loc "${call_list[0]}")

  [ "$failed" -ne 0 ] && return 1

  echo "ws-registration: 1 upgrade site ($(loc "${up_list[0]}"), in $up_where) called once from $SERVE_FN() ($call_loc); $inside registration call(s) all inside $SERVE_FN() ($serve_loc), 0 outside; $deadlines time-deadline .Add line(s) excluded"
  return 0
}

# ---------------------------------------------------------------------------
# --self-test: the check's own regression suite
# ---------------------------------------------------------------------------

# WHY THIS EXISTS. This runs as a REQUIRED status check (via check-docs.sh in
# .github/workflows/docs.yml), and a check that has silently stopped working
# produces output identical to a clean tree. Every control here is TWO-SIDED:
# each passing fixture has a twin whose only difference is the thing being
# checked, and every failing fixture must ALSO explain itself with a message
# naming the file (and, for a lost footing, which footing), because exit
# status alone cannot tell "refused for the right reason" from "fell through
# to some other branch".
selftest_case() {
  local name=$1 want=$2 dir=$3 want_msg=${4:-} out rc
  out=$(check_dir "$dir" 2>&1)
  rc=$?
  ST_RUN=$((ST_RUN + 1))
  if [ "$rc" = "$want" ]; then
    if [ -z "$want_msg" ] || command printf '%s\n' "$out" | command grep -qE "$want_msg"; then
      return 0
    fi
    echo "FAIL: ws-registration self-test - control '$name' exited $rc as expected but did not explain itself; wanted a message matching /$want_msg/"
  else
    echo "FAIL: ws-registration self-test - control '$name' expected exit $want, got $rc"
  fi
  echo "  check said:"
  command printf '%s\n' "$out" | command sed 's/^/    /'
  ST_FAILS=$((ST_FAILS + 1))
}

# Number of time-deadline lines the base fixture carries: the 13 lines of
# backend/internal/handlers at cb1ab4e, verbatim, in deadlines.go below.
# If this number changes, the exclusion has widened or narrowed -- decide
# that on purpose, here, not by accident in DEADLINE_RE.
FIXTURE_DEADLINES=13

# Write the clean base fixture into $1: a ws.go shaped like the real one
# (upgrade helper + serveWS), a dashboard.go registered through serveWS, a
# deadlines.go carrying the real tree's 13 false-positive lines verbatim, a
# backup.go with the real tree's comment-only mention, and a test file with
# registrations that must be ignored.
write_base() {
  local d=$1
  command mkdir -p "$d"
  command cat > "$d/ws.go" <<'GO'
package handlers

func upgradeConnection(c *gin.Context) (*Connection, error) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}
	return &Connection{Conn: conn}, nil
}

func serveWS(c *gin.Context, cm *ConnectionManager, reg wsRegistration) (*Connection, func(), error) {
	conn, err := upgradeConnection(c)
	if err != nil {
		return nil, nil, err
	}
	if reg.unmetered {
		cm.AddUnmetered(conn.ID, conn)
	} else if err := cm.Add(conn.ID, conn); err != nil {
		return nil, nil, err
	}
	return conn, func() { cm.Remove(conn.ID) }, nil
}
GO
  command cat > "$d/dashboard.go" <<'GO'
package handlers

func (h *DashboardHandler) metrics(c *gin.Context) {
	conn, release, err := serveWS(c, h.cm, wsRegistration{refuseCode: CloseCodeRateLimit})
	if err != nil {
		return
	}
	defer release()
	_ = conn
}
GO
  command cat > "$d/deadlines.go" <<'GO'
package handlers

func deadlines(conn *websocket.Conn, c *Connection) {
	expiresAt := time.Now().Add(24 * time.Hour)
	expiresAt := time.Now().Add(24 * time.Hour)
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
			_ = conn.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			_ = c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	deadline := time.Now().Add(5 * time.Second)
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
}
GO
  command cat > "$d/backup.go" <<'GO'
package handlers

func (h *BackupHandler) wsAttach(c *gin.Context) {
	// Before AddUnmetered existed, the soft
	// `if err := h.cm.Add(...); err == nil { defer h.cm.Remove(...) }` here
	// had both costs.
	conn, release, err := serveWS(c, h.cm, wsRegistration{unmetered: true})
	if err != nil {
		return
	}
	defer release()
	_ = conn
}
GO
  command cat > "$d/ws_test.go" <<'GO'
package handlers

func TestIgnored(t *testing.T) {
	cm.Add(id, c)
	_, _ = upgrader.Upgrade(w, r, nil)
	_, _ = upgradeConnection(c)
}
GO
}

# Copy the base fixture to $1 and append $3 to file $2 inside it.
derive() {
  local d=$1 file=$2 extra=$3
  command rm -rf "$d"
  command cp -r "$ST_DIR/clean" "$d"
  command printf '%s\n' "$extra" >> "$d/$file"
}

selftest() {
  ST_DIR="$(command mktemp -d)" || {
    echo "FAIL: ws-registration self-test - could not create a temp directory"
    exit 2
  }
  trap 'command rm -rf "$ST_DIR"' EXIT
  ST_RUN=0
  ST_FAILS=0

  write_base "$ST_DIR/clean"

  # --- the false-positive controls: shapes that MUST stay green
  derive "$ST_DIR/deadline-shapes" dashboard.go '
func (h *DashboardHandler) more(conn *websocket.Conn, d time.Time) {
	_ = conn.SetWriteDeadline(d.Add(10 * time.Second))
	exp := time.Now().Add(24 * time.Hour).Unix()
	_ = exp
}'
  # Include broadly: a sync.WaitGroup's Add is NOT excluded and goes red,
  # named. handlers/ has none today (OBSERVED cb1ab4e); if one appears, the
  # author sees this line and either moves it or widens DEADLINE_RE here, on
  # purpose. This control pins that it is red-and-named, not silently green.
  derive "$ST_DIR/waitgroup-add" dashboard.go '
func (h *DashboardHandler) fanout() {
	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()
}'
  derive "$ST_DIR/comment-only" dashboard.go '
// A doc comment that spells the call out: cm.Add(conn.ID, conn)
	// and an indented one: h.cm.AddUnmetered(conn.ID, conn)
	// and a method value: add := cm.Add'
  derive "$ST_DIR/test-file-only" other_test.go 'package handlers

func TestAlsoIgnored(t *testing.T) {
	h.cm.AddUnmetered(id, c)
	_, _ = up.Upgrade(w, r, nil)
	_, _ = upgradeConnection(c)
}'

  # --- the violations: each red AND naming its file:line
  derive "$ST_DIR/second-upgrade" dashboard.go '
func (h *DashboardHandler) legacy(c *gin.Context) {
	conn, err := up.Upgrade(c.Writer, c.Request, nil)
	_, _ = conn, err
}'
  derive "$ST_DIR/add-outside" dashboard.go '
func (h *DashboardHandler) legacy(c *gin.Context, conn *Connection) {
	if err := h.cm.Add(conn.ID, conn); err != nil {
		return
	}
}'
  derive "$ST_DIR/other-receiver" dashboard.go '
func (h *DashboardHandler) legacy(id string, c *Connection) {
	x.Add(id, c)
}'
  derive "$ST_DIR/unmetered-outside" logs.go 'package handlers

func (h *LogsHandler) legacy(id string, c *Connection) {
	reg.AddUnmetered(id, c)
}'
  derive "$ST_DIR/nested-arg" dashboard.go '
func (h *DashboardHandler) legacy(c *gin.Context, conn *Connection) {
	_ = h.cm.Add(connID(c), conn)
}'
  derive "$ST_DIR/three-args" dashboard.go '
func (h *DashboardHandler) legacy(conn *Connection) {
	_ = h.cm.Add(conn.ID, conn, addOptions{})
}'
  derive "$ST_DIR/single-arg-no-time" dashboard.go '
func (h *DashboardHandler) legacy(conn *Connection) {
	set.Add(conn)
}'
  derive "$ST_DIR/second-caller" dashboard.go '
func (h *DashboardHandler) legacy(c *gin.Context) {
	conn, err := upgradeConnection(c)
	_, _ = conn, err
}'

  # --- fail-closed: lines the check cannot classify are red, in or out
  derive "$ST_DIR/multiline-add" dashboard.go '
func (h *DashboardHandler) legacy(conn *Connection) {
	err := h.cm.Add(
		conn.ID,
		conn,
	)
	_ = err
}'
  derive "$ST_DIR/method-value" dashboard.go '
func (h *DashboardHandler) legacy(conn *Connection) {
	add := h.cm.Add
	_ = add(conn.ID, conn)
}'
  derive "$ST_DIR/two-on-a-line" dashboard.go '
func (h *DashboardHandler) legacy(conn *Connection, c *websocket.Conn) {
	_ = h.cm.Add(conn.ID, conn); _ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
}'
  derive "$ST_DIR/multiline-inside" ws.go ''
  command sed -i 's/cm\.AddUnmetered(conn\.ID, conn)/cm.AddUnmetered(\n\t\t\tconn.ID, conn)/' "$ST_DIR/multiline-inside/ws.go"

  # --- every way the check can lose its footing must be loud and must say
  # --- WHICH footing it lost (the fn-renamed case of check-networkidle-probes)
  derive "$ST_DIR/serve-renamed" ws.go ''
  command sed -i 's/^func serveWS(/func serveWebSocket(/' "$ST_DIR/serve-renamed/ws.go"
  derive "$ST_DIR/upgrade-renamed" ws.go ''
  command sed -i 's/upgradeConnection/doUpgrade/g' "$ST_DIR/upgrade-renamed/ws.go"
  derive "$ST_DIR/orphan-helper" ws.go ''
  command sed -i 's/conn, err := upgradeConnection(c)/conn, err := someOtherThing(c)/' "$ST_DIR/orphan-helper/ws.go"
  derive "$ST_DIR/caller-outside" dashboard.go '
func (h *DashboardHandler) legacy(c *gin.Context) {
	conn, err := upgradeConnection(c)
	_, _ = conn, err
}'
  command sed -i 's/conn, err := upgradeConnection(c)/conn, err := someOtherThing(c)/' "$ST_DIR/caller-outside/ws.go"
  derive "$ST_DIR/no-upgrade" ws.go ''
  command sed -i 's/upgrader\.Upgrade(c\.Writer, c\.Request, nil)/fakeUpgrade(c)/' "$ST_DIR/no-upgrade/ws.go"
  derive "$ST_DIR/hollow-serve" ws.go ''
  command sed -i -e 's/cm\.AddUnmetered(conn\.ID, conn)/register(conn)/' -e 's/cm\.Add(conn\.ID, conn)/registerMetered(conn)/' "$ST_DIR/hollow-serve/ws.go"
  derive "$ST_DIR/deadline-drift" deadlines.go ''
  command sed -i '0,/time\.Now()\.Add(24 \* time\.Hour)$/s//nowPlus(24 * time.Hour)/' "$ST_DIR/deadline-drift/deadlines.go"

  # clean tree, and its false-positive twins
  selftest_case clean            0 "$ST_DIR/clean"           "1 upgrade site \(.*ws\.go:4, in upgradeConnection\(\)\) called once from serveWS\(\) \(.*ws\.go:12\); 2 registration call\(s\) all inside serveWS\(\) \(.*ws\.go:11-22\), 0 outside; $FIXTURE_DEADLINES time-deadline"
  selftest_case deadline-shapes  0 "$ST_DIR/deadline-shapes" "$((FIXTURE_DEADLINES + 2)) time-deadline"
  selftest_case comment-only     0 "$ST_DIR/comment-only"    '0 outside'
  selftest_case test-file-only   0 "$ST_DIR/test-file-only"  '0 outside'

  # violations, each named
  selftest_case second-upgrade   1 "$ST_DIR/second-upgrade"  'want exactly 1 WebSocket upgrade site.*found 2'
  selftest_case second-upgrade-named 1 "$ST_DIR/second-upgrade" 'dashboard\.go:[0-9]+: .*up\.Upgrade\('
  selftest_case add-outside      1 "$ST_DIR/add-outside"     'outside serveWS'
  selftest_case add-outside-named 1 "$ST_DIR/add-outside"    'dashboard\.go:[0-9]+: .*h\.cm\.Add\(conn\.ID, conn\)'
  selftest_case other-receiver   1 "$ST_DIR/other-receiver"  'dashboard\.go:[0-9]+: .*x\.Add\(id, c\)'
  selftest_case unmetered-outside 1 "$ST_DIR/unmetered-outside" 'logs\.go:[0-9]+: .*reg\.AddUnmetered\(id, c\)'
  selftest_case nested-arg       1 "$ST_DIR/nested-arg"      'dashboard\.go:[0-9]+: .*h\.cm\.Add\(connID\(c\), conn\)'
  selftest_case three-args       1 "$ST_DIR/three-args"      'dashboard\.go:[0-9]+: .*addOptions'
  selftest_case single-arg-no-time 1 "$ST_DIR/single-arg-no-time" 'dashboard\.go:[0-9]+: .*set\.Add\(conn\)'
  selftest_case waitgroup-add    1 "$ST_DIR/waitgroup-add"   'dashboard\.go:[0-9]+: .*wg\.Add\(1\)'
  selftest_case second-caller    1 "$ST_DIR/second-caller"   'want exactly 1 call to upgradeConnection\(.*found 2'
  selftest_case second-caller-named 1 "$ST_DIR/second-caller" 'dashboard\.go:[0-9]+: .*upgradeConnection\(c\)'

  # fail-closed, each naming the line and its class
  selftest_case multiline-add    1 "$ST_DIR/multiline-add"   'cannot classify'
  selftest_case multiline-add-named 1 "$ST_DIR/multiline-add" 'dashboard\.go:[0-9]+ \[MULTI\]: .*h\.cm\.Add\($'
  selftest_case method-value     1 "$ST_DIR/method-value"    'dashboard\.go:[0-9]+ \[NOCALL\]: .*add := h\.cm\.Add$'
  selftest_case two-on-a-line    1 "$ST_DIR/two-on-a-line"   'dashboard\.go:[0-9]+ \[TWICE\]'
  selftest_case multiline-inside 1 "$ST_DIR/multiline-inside" 'ws\.go:[0-9]+ \[MULTI\]'

  # lost footings, each saying which
  selftest_case serve-renamed    1 "$ST_DIR/serve-renamed"   'could not find func serveWS\('
  selftest_case upgrade-renamed  1 "$ST_DIR/upgrade-renamed" 'in neither serveWS\(\) nor upgradeConnection\(\)'
  selftest_case orphan-helper    1 "$ST_DIR/orphan-helper"   'want exactly 1 call to upgradeConnection\(.*found 0'
  selftest_case caller-outside   1 "$ST_DIR/caller-outside"  'only call to upgradeConnection\( is at .*dashboard\.go:[0-9]+, outside serveWS'
  selftest_case no-upgrade       1 "$ST_DIR/no-upgrade"      'want exactly 1 WebSocket upgrade site.*found 0'
  selftest_case hollow-serve     1 "$ST_DIR/hollow-serve"    'lost its registration footing'
  selftest_case deadline-drift   0 "$ST_DIR/deadline-drift"  "$((FIXTURE_DEADLINES - 1)) time-deadline"
  selftest_case dir-missing      1 "$ST_DIR/does-not-exist"  'is not a directory'

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "FAIL: ws-registration self-test - $ST_FAILS of $ST_RUN control(s) failed; the check does not behave as documented"
    return 1
  fi
  echo "ws-registration self-test: $ST_RUN control(s) passed, each proven both ways"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT/$DEFAULT_DIR_REL" ;;
  -*)
    echo "ERROR: unknown option '$1'" >&2
    usage
    exit 2
    ;;
  *)
    if [ "$#" -ne 1 ]; then
      echo "ERROR: expected at most one directory argument" >&2
      usage
      exit 2
    fi
    check_dir "$1"
    ;;
esac
