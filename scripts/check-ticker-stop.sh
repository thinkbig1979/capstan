#!/usr/bin/env bash
# scripts/check-ticker-stop.sh
#
# ONE invariant (agent-os-z91e.31, safe-defaults rule 3):
#
#   No non-test Go file under backend/internal or backend/cmd waits on a
#   ticker or timer channel in a shape that has no way to stop.
#
# WHY. A boot-time goroutine exits only when its context ends. A loop of the
# shape `for range ticker.C { ... }` has no stop case at all, so main()'s
# shutdown sequence has nothing to stop it with. agent-os-z91e.17 fixed the
# last one (internal/middleware/ratelimit.go, the rate-limiter cleanup loop);
# this makes the shape coming back a red CI run instead of a review find.
#
# WHAT IS A VIOLATION. A line that, outside a comment:
#   1. ranges over a channel field named C: `for range t.C`, `for x := range t.C`;
#   2. ranges over time.Tick(...), which returns a ticker that can never stop;
#   3. receives from a .C channel as a bare statement or assignment, not as a
#      `case` of a select (`<-t.C` on its own blocks with no ctx.Done() arm).
# The fix is a select with a ctx.Done() (or stop channel) case beside the
# ticker case, as every other ticker loop in the backend has.
#
# LIMIT. Line-based. A receive split across lines, or a channel not named C,
# is invisible to this check. There is no allow marker, on purpose.
#
# USAGE
#   check-ticker-stop.sh              scan this repo
#   check-ticker-stop.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-ticker-stop.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files under the scan roots, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CHAN='[A-Za-z_][A-Za-z0-9_.]*\.C\b'
RANGE_C="^[[:space:]]*for\b.*\brange[[:space:]]+$CHAN"
RANGE_TICK='^[[:space:]]*for\b.*\brange[[:space:]]+time\.Tick\('
IDENT='[A-Za-z_][A-Za-z0-9_]*'
BARE_RECV="^[[:space:]]*($IDENT([[:space:]]*,[[:space:]]*$IDENT)*[[:space:]]*:?=[[:space:]]*)?<-$CHAN"

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

check_dir() {
  local dir="$1" root
  [ -d "$dir" ] || { echo "check-ticker-stop: $dir is not a directory" >&2; return 2; }

  local roots=()
  for root in backend/internal backend/cmd; do
    [ -d "$dir/$root" ] && roots+=("$root")
  done
  if [ "${#roots[@]}" -eq 0 ]; then
    echo "check-ticker-stop: neither backend/internal nor backend/cmd under $dir" >&2
    return 2
  fi

  local nfiles
  nfiles=$(cd "$dir" && command find "${roots[@]}" -type f -name '*.go' ! -name '*_test.go' | wc -l)
  if [ "$nfiles" -eq 0 ]; then
    echo "check-ticker-stop: no non-test Go files under ${roots[*]} in $dir" >&2
    return 2
  fi

  local out status
  out=$(cd "$dir" && command grep -rnE --include='*.go' --exclude='*_test.go' \
    -e "$RANGE_C" -e "$RANGE_TICK" -e "$BARE_RECV" -- "${roots[@]}" 2>&1)
  status=$?
  case "$status" in
    0)
      echo "$out"
      echo "check-ticker-stop: a ticker or timer wait with no stop case (select on ctx.Done() beside it, safe-defaults rule 3)"
      return 1
      ;;
    1) echo "check-ticker-stop: 0 violations in $nfiles files"; return 0 ;;
    *) echo "check-ticker-stop: grep failed ($status): $out" >&2; return 2 ;;
  esac
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX DIR
selftest_case() {
  local name="$1" want="$2" pat="$3" dir="$4" out status
  ST_RUN=$((ST_RUN + 1))
  out=$(check_dir "$dir" 2>&1)
  status=$?
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: ticker-stop self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_base writes a clean tree: the select shapes every real loop uses.
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/svc" "$d/backend/cmd/server"
  cat > "$d/backend/internal/svc/loop.go" <<'EOF'
package svc

func run(ctx context.Context, ticker *time.Ticker) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case t := <-ticker.C:
			_ = t
		}
	}
}
EOF
  printf 'package main\n\nfunc main() {}\n' > "$d/backend/cmd/server/main.go"
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

# plant DIR FILE LINE appends LINE inside a function in DIR/FILE.
plant() {
  printf 'func planted() {\n%s\n}\n' "$3" >> "$1/$2"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: ticker-stop self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: select cases, including an assigning case.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED: the ratelimit.go shape z91e.17 fixed.
  d=$(fresh); plant "$d" backend/internal/svc/loop.go "	for range cleanupTicker.C {"
  selftest_case "for range x.C" 1 "backend/internal/svc/loop.go:[0-9]+:" "$d"

  # RED: ranging with a value, and a dotted receiver.
  d=$(fresh); plant "$d" backend/internal/svc/loop.go "	for t := range s.ticker.C {"
  selftest_case "for t := range s.ticker.C" 1 "loop.go:[0-9]+:.*range s\.ticker\.C" "$d"

  # RED: time.Tick can never be stopped.
  d=$(fresh); plant "$d" backend/internal/svc/loop.go "	for range time.Tick(time.Minute) {"
  selftest_case "for range time.Tick" 1 "loop.go:[0-9]+:.*time\.Tick" "$d"

  # RED: a bare receive outside a select, and an assigning one.
  d=$(fresh); plant "$d" backend/internal/svc/loop.go "		<-timer.C"
  selftest_case "bare <-timer.C" 1 "loop.go:[0-9]+:[[:space:]]+<-timer\.C" "$d"
  d=$(fresh); plant "$d" backend/internal/svc/loop.go "		now := <-ticker.C"
  selftest_case "now := <-ticker.C" 1 "loop.go:[0-9]+:.*now := <-ticker\.C" "$d"

  # RED: backend/cmd is in scope too.
  d=$(fresh); plant "$d" backend/cmd/server/main.go "	for range t.C {"
  selftest_case "backend/cmd" 1 "backend/cmd/server/main.go:[0-9]+:" "$d"

  # GREEN: test files and comments are out of scope.
  d=$(fresh)
  printf 'package svc\n\nfunc f() {\n\tfor range t.C {\n\t}\n}\n' > "$d/backend/internal/svc/loop_test.go"
  plant "$d" backend/internal/svc/loop.go "	// for range t.C { was the old shape"
  selftest_case "test file and comment" 0 "0 violations" "$d"

  # ERROR: no scan roots, and scan roots with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan roots" 2 "neither backend/internal" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/backend/internal"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: ticker-stop self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "ticker-stop self-test: $ST_RUN control(s) passed (2 green, 6 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
