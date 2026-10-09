#!/usr/bin/env bash
# scripts/check-arm-refusal.sh
#
# ONE invariant (agent-os-n8b4):
#
#   A scheduler arm function that refuses to arm on an error leaves a trace
#   the operator can see, not only a log line.
#
# WHY. agent-os-awfh and agent-os-7yjx fixed arm paths that gave up on an
# unreadable setting with only an ERROR log, so the UI showed a scheduler that
# looked configured and never ran. Each now calls a recorder that writes where
# the UI reads: recordSchedulerNotStarted (services/backup.go),
# recordUnstartedCycle (services/backup_scheduler.go,
# services/docker_cleanup_scheduler.go) and recordUpdateSchedulerNotStarted
# (cmd/server/main.go). This makes a new log-only refusal a red CI run.
#
# WHAT IS A VIOLATION. Scope: non-test Go files under backend/internal/services
# and backend/cmd/server. An ARM FUNCTION is one whose name (after any
# receiver) starts with Start, start, Arm, arm, Restart or restart. Inside one,
# outside any `go func` body, an if/else block is a refusal when
#   (its condition tests `<something>err != nil` OR its own body calls .Error()
#   AND (its body returns without passing an error on, OR it calls .Error()
# and it is a violation when that same body calls no record...() function and
# no start...() function (a fallback that arms something else is not a
# refusal). Only the innermost block's own lines count.
#
# "Returns without passing an error on" means a return line that does not
# name an err/Err identifier: `return`, `return false`, `return nil`. A
# `return err` or `return fmt.Errorf(...)` hands the refusal to the caller,
# which is outside this check (see LIMIT).
#
# ALLOW MARKER. A refusal that is not the operator's concern carries, on its
# `if` line, `// arm-refusal-ok: <reason>`. The reason is required; an empty
# one is still a violation.
#
# LIMIT. Line-based, so this is a floor, not a census:
#   - FALSE NEGATIVES: a refusal returned as an error that the CALLER then
#     only logs; an arm function not named Start/arm/Restart; a condition or
#     return split across lines so the err test or the return is not on the
#     line the check reads; a recorder not named record...().
#   - FALSE POSITIVES: braces inside a raw (backtick) string spanning lines;
#     a block whose recorder is called through a helper not named record...().
#
# USAGE
#   check-arm-refusal.sh              scan this repo
#   check-arm-refusal.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-arm-refusal.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files under the scan roots, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# The awk program reports "FILE:LINE: FUNC: TEXT" per violation.
# shellcheck disable=SC2016
AWK_PROG='
function strip(s) { gsub(/"([^"\\]|\\.)*"/, "\"\"", s); gsub(/`[^`]*`/, "``", s); sub(/\/\/.*/, "", s); return s }
function count(s, re,  t) { t = s; return gsub(re, "", t) }
function close_block(i) {
  if ((B_err[i] || B_log[i]) && (B_ret[i] || B_log[i]) && !B_rec[i] && !B_start[i] && !B_ok[i])
    printf "%s:%d: %s: %s\n", FILENAME, B_line[i], fname, B_text[i]
}
FNR == 1 { arm = 0; depth = 0; nb = 0; godepth = 0 }
{
  raw = $0; s = strip(raw)
  if (s ~ /^func /) {
    fname = s; sub(/^func (\([^)]*\) )?/, "", fname); sub(/\(.*/, "", fname)
    arm = (fname ~ /^([Ss]tart|[Aa]rm|[Rr]estart)/); depth = 0; nb = 0; godepth = 0
  }
  if (!arm) next
  opens = count(s, "\\{"); closes = count(s, "\\}")
  isopen = (s ~ /^[[:space:]]*(\}[[:space:]]*else[[:space:]]+)?if .*\{[[:space:]]*$/ || s ~ /^[[:space:]]*\}?[[:space:]]*else[[:space:]]*\{[[:space:]]*$/)

  # The body of the innermost open block, but never its own opening line.
  if (nb > 0 && !isopen && godepth == 0) {
    if (s ~ /(^|[^A-Za-z_])return([^A-Za-z_]|$)/ && s !~ /[Ee]rr/) B_ret[nb] = 1
    if (s ~ /\.Error\(/) B_log[nb] = 1
    if (s ~ /(^|[^A-Za-z_.])[Rr]ecord[A-Za-z]*\(|\.[Rr]ecord[A-Za-z]*\(/) B_rec[nb] = 1
    if (s ~ /(^|[^A-Za-z_])[Ss]tart[A-Za-z]*\(/) B_start[nb] = 1
  }

  depth -= closes
  if (godepth > 0 && depth < godepth) godepth = 0
  while (nb > 0 && depth < B_depth[nb]) { close_block(nb); nb-- }

  if (godepth == 0 && s ~ /(^|[^A-Za-z_])go func\(/ && opens > 0) godepth = depth + 1
  if (godepth == 0 && isopen) {
    nb++; B_depth[nb] = depth + 1; B_line[nb] = FNR
    B_text[nb] = raw; sub(/^[[:space:]]+/, "", B_text[nb])
    B_err[nb] = (s ~ /[A-Za-z_]*[Ee]rr[A-Za-z_]* != nil/)
    B_ret[nb] = 0; B_log[nb] = 0; B_rec[nb] = 0; B_start[nb] = 0
    B_ok[nb] = (raw ~ /\/\/ arm-refusal-ok: *[^[:space:]]/)
  }
  depth += opens
}'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

check_dir() {
  local dir="$1" root
  [ -d "$dir" ] || { echo "check-arm-refusal: $dir is not a directory" >&2; return 2; }

  local roots=()
  for root in backend/internal/services backend/cmd/server; do
    [ -d "$dir/$root" ] && roots+=("$root")
  done
  if [ "${#roots[@]}" -eq 0 ]; then
    echo "check-arm-refusal: neither backend/internal/services nor backend/cmd/server under $dir" >&2
    return 2
  fi

  local files=()
  mapfile -t files < <(cd "$dir" && command find "${roots[@]}" -maxdepth 1 -type f -name '*.go' ! -name '*_test.go' | sort)
  if [ "${#files[@]}" -eq 0 ]; then
    echo "check-arm-refusal: no non-test Go files under ${roots[*]} in $dir" >&2
    return 2
  fi

  local out status
  out=$(cd "$dir" && awk "$AWK_PROG" "${files[@]}" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "check-arm-refusal: awk failed ($status): $out" >&2
    return 2
  fi
  if [ -n "$out" ]; then
    echo "$out"
    echo "check-arm-refusal: an arm function refuses with only a log line; call a record...() function the UI reads, or mark it '// arm-refusal-ok: <reason>' (agent-os-n8b4)"
    return 1
  fi
  echo "check-arm-refusal: 0 violations in ${#files[@]} files"
  return 0
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
    echo "FAIL: arm-refusal self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_base writes a clean tree: every shape the real arm functions use.
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/services" "$d/backend/cmd/server"
  cat > "$d/backend/internal/services/sched.go" <<'EOF'
package services

func (s *S) StartScheduler() {
	if s.sched == nil {
		return
	}
	bc, err := s.resolve()
	if err != nil {
		s.logger.Error("not started", "error", err)
		s.recordSchedulerNotStarted(err)
		return
	}
	if bc.Mode == "scheduled" {
		sched, err := Parse(bc)
		if err != nil {
			s.logger.Error("bad schedule; falling back", "error", err)
			s.startIntervalScheduler(bc)
			return
		}
		_ = sched
	}
	go func() {
		if err := s.run(); err != nil {
			s.logger.Error("run failed", "error", err)
			return
		}
	}()
}

func (w *W) Start() error {
	if err := w.add(); err != nil {
		return err
	}
	if err := w.more(); err != nil {
		return fmt.Errorf("more: %w", err)
	}
	return nil
}

func (s *S) runCycle() {
	if err := s.scan(); err != nil {
		s.logger.Error("scan failed", "error", err)
		return
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

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: arm-refusal self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d svc=backend/internal/services/sched.go

  # GREEN: recorded refusal, fallback that arms, goroutine body, error
  # returned to the caller, and a non-arm function.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED: the bead's planted shape, log then return with no record call.
  d=$(fresh)
  printf 'func (s *S) StartPlanted() {\n\tv, err := s.db.GetSetting("k")\n\tif err != nil {\n\t\ts.logger.Error("not armed", "error", err)\n\t\treturn\n\t}\n\t_ = v\n}\n' >> "$d/$svc"
  selftest_case "log then return" 1 "sched\.go:[0-9]+: StartPlanted: if err != nil \{" "$d"

  # RED: a bare return on an error with the log elsewhere (backup.go:375 before #603).
  d=$(fresh)
  printf 'func (s *S) StartBare() {\n\tbc, err := s.resolveOrRefuse("start")\n\tif err != nil {\n\t\treturn\n\t}\n\t_ = bc\n}\n' >> "$d/$svc"
  selftest_case "bare return on err" 1 "StartBare: if err != nil" "$d"

  # RED: a log with no return, which falls through to not arming (main.go convErr before #603).
  d=$(fresh)
  printf 'func startPlanted(log *slog.Logger) bool {\n\tif n, convErr := strconv.Atoi(v); convErr != nil {\n\t\tlog.Error("not a number", "error", convErr)\n\t} else {\n\t\tm = n\n\t}\n\treturn false\n}\n' >> "$d/backend/cmd/server/main.go"
  selftest_case "log without return, in cmd/server" 1 "main\.go:[0-9]+: startPlanted: if n, convErr" "$d"

  # RED: a non-error condition that logs at ERROR and returns.
  d=$(fresh)
  printf 'func (s *S) StartScheduled() {\n\tnext, ok := s.next()\n\tif !ok {\n\t\ts.logger.Error("no weekdays")\n\t\treturn\n\t}\n\t_ = next\n}\n' >> "$d/$svc"
  selftest_case "non-error condition with ERROR log" 1 "StartScheduled: if !ok" "$d"

  # GREEN: the same refusal with a reasoned marker.
  d=$(fresh)
  printf 'func (s *S) StartMarked() {\n\tif err != nil { // arm-refusal-ok: off by configuration either way\n\t\ts.logger.Error("x", "error", err)\n\t\treturn\n\t}\n}\n' >> "$d/$svc"
  selftest_case "marker with a reason" 0 "0 violations" "$d"

  # RED: a marker with no reason does not count.
  d=$(fresh)
  printf 'func (s *S) StartEmptyMarker() {\n\tif err != nil { // arm-refusal-ok:\n\t\ts.logger.Error("x", "error", err)\n\t\treturn\n\t}\n}\n' >> "$d/$svc"
  selftest_case "marker without a reason" 1 "StartEmptyMarker" "$d"

  # GREEN: test files and comments are out of scope.
  d=$(fresh)
  printf 'package services\n\nfunc StartT() {\n\tif err != nil {\n\t\treturn\n\t}\n}\n' > "$d/backend/internal/services/sched_test.go"
  printf 'func (s *S) StartCommented() {\n\t// if err != nil { return } was the old shape\n}\n' >> "$d/$svc"
  selftest_case "test file and comment" 0 "0 violations" "$d"

  # ERROR: no scan roots, and scan roots with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan roots" 2 "neither backend/internal/services" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/backend/internal/services"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: arm-refusal self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "arm-refusal self-test: $ST_RUN control(s) passed (3 green, 5 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
