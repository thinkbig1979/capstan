#!/usr/bin/env bash
# scripts/check-settings-writes.sh
#
# ONE invariant (agent-os-u0nd):
#
#   No function in a non-test Go file under backend/internal/handlers or
#   backend/internal/services writes more than one setting through the raw
#   single-key SetSetting.
#
# WHY. A handler that saves several settings with one SetSetting call each
# commits the earlier keys before a later write fails, so a storage fault
# leaves the form half-applied and answers 500. The safe helper is
# SetSettings (backend/internal/database/settings.go): every key in one
# transaction, sensitive keys encrypted exactly as SetSetting does. Five
# writers were moved onto it; this keeps a sixth from coming back. The
# scheduler's scan result (update_scan_last_run plus the cleared
# update_scan_last_error) had the same shape in services and moved onto it
# too (agent-os-qz9b), so services is scanned as well.
#
# WHAT IS A VIOLATION. Inside one func (a line starting `func ` opens the
# next one), outside comments:
#   1. a second `.SetSetting(` call, or
#   2. a `.SetSetting(` call inside a `for` block, which writes several keys
#      from a single call site (UpdateLogRetention's old shape).
# The fix is to collect the values and call SetSettings once.
#
# LIMIT. Line-based. A func literal inside a func counts as part of it; a
# call split so `.SetSetting(` is not on one line is invisible; a helper that
# wraps SetSetting and is itself called twice is invisible. There is no allow
# marker: a handler that really means two independent saves can call
# SetSettings twice.
#
# USAGE
#   check-settings-writes.sh              scan this repo
#   check-settings-writes.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-settings-writes.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files under the scan root, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SCAN_ROOTS=(backend/internal/handlers backend/internal/services)

# The awk program reports "FILE:LINE: FUNC: reason" per violation.
# shellcheck disable=SC2016
AWK_PROG='
function strip(s) { gsub(/"([^"\\]|\\.)*"/, "\"\"", s); gsub(/`[^`]*`/, "``", s); sub(/\/\/.*/, "", s); return s }
FNR == 1 { fname = ""; n = 0; depth = 0; nf = 0 }
{
  s = strip($0)
  if (s ~ /^func /) { fname = s; sub(/[[:space:]]*\{[[:space:]]*$/, "", fname); n = 0; depth = 0; nf = 0 }
  t = s; opens = gsub(/\{/, "", t); t = s; closes = gsub(/\}/, "", t)
  if (s ~ /\.SetSetting\(/ && fname != "") {
    n++
    if (n > 1) printf "%s:%d: %s: a second raw SetSetting in one function\n", FILENAME, FNR, fname
    if (nf > 0) printf "%s:%d: %s: a raw SetSetting inside a for loop\n", FILENAME, FNR, fname
  }
  depth -= closes
  while (nf > 0 && depth < fdepth[nf]) nf--
  if (s ~ /^[[:space:]]*for([[:space:]]|\{)/ && opens > 0) { nf++; fdepth[nf] = depth + 1 }
  depth += opens
}'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

check_dir() {
  local dir="$1"
  [ -d "$dir" ] || { echo "check-settings-writes: $dir is not a directory" >&2; return 2; }
  local root found files=()
  for root in "${SCAN_ROOTS[@]}"; do
    [ -d "$dir/$root" ] || { echo "check-settings-writes: no $root under $dir" >&2; return 2; }
    mapfile -t found < <(cd "$dir" && command find "$root" -type f -name '*.go' ! -name '*_test.go' | sort)
    if [ "${#found[@]}" -eq 0 ]; then
      echo "check-settings-writes: no non-test Go files under $root in $dir" >&2
      return 2
    fi
    files+=("${found[@]}")
  done

  local out status
  out=$(cd "$dir" && awk "$AWK_PROG" "${files[@]}" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "check-settings-writes: awk failed ($status): $out" >&2
    return 2
  fi
  if [ -n "$out" ]; then
    echo "$out"
    echo "check-settings-writes: a function writes several settings one key at a time; collect them and call SetSettings once (agent-os-u0nd, agent-os-qz9b)"
    return 1
  fi
  echo "check-settings-writes: 0 violations in ${#files[@]} files"
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
    echo "FAIL: settings-writes self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

HANDLERS_ROOT=${SCAN_ROOTS[0]}
SERVICES_ROOT=${SCAN_ROOTS[1]}

# write_base writes a clean tree: one raw write per func, a batch write, and
# a loop that only collects values; services holds one single-key writer.
write_base() {
  local d="$1"
  mkdir -p "$d/$HANDLERS_ROOT" "$d/$SERVICES_ROOT"
  printf 'package services\n\nfunc (s *S) record(key, msg string) {\n\t_ = s.db.SetSetting(key, msg)\n}\n' > "$d/$SERVICES_ROOT/s.go"
  cat > "$d/$HANDLERS_ROOT/h.go" <<'EOF'
package handlers

func (h *H) one(c *gin.Context) {
	if err := h.db.SetSetting("scan_depth", "3"); err != nil {
		return
	}
}

func (h *H) other(c *gin.Context) {
	_ = h.db.SetSetting("default_stacks_dir", "/opt")
}

func (h *H) batch(c *gin.Context) {
	var values []database.SettingValue
	for _, u := range updates {
		values = append(values, database.SettingValue{Key: u.key, Value: u.value})
	}
	_ = h.db.SetSettings(values)
}
EOF
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: settings-writes self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: one raw write per func, SetSettings after a collecting loop.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED: two raw writes in one func (the five writers' old shape).
  d=$(fresh)
  printf 'func (h *H) two(c *gin.Context) {\n\tif err := h.db.SetSetting("a", "1"); err != nil {\n\t\treturn\n\t}\n\tif err := db.SetSetting("b", "2"); err != nil {\n\t\treturn\n\t}\n}\n' >> "$d/$HANDLERS_ROOT/h.go"
  selftest_case "two raw writes" 1 "h\.go:[0-9]+: func \(h \*H\) two\(c \*gin\.Context\): a second raw SetSetting" "$d"

  # RED: one raw write inside a loop (UpdateLogRetention's old shape).
  d=$(fresh)
  printf 'func (h *H) loop(c *gin.Context) {\n\tfor _, u := range updates {\n\t\tif err := h.db.SetSetting(u.key, u.v); err != nil {\n\t\t\treturn\n\t\t}\n\t}\n}\n' >> "$d/$HANDLERS_ROOT/h.go"
  selftest_case "raw write in a for loop" 1 "loop\(c \*gin\.Context\): a raw SetSetting inside a for loop" "$d"

  # RED: two raw writes in one func under services (performScan's old shape).
  d=$(fresh)
  printf 'func (s *S) scan() {\n\t_ = s.db.SetSetting("update_scan_last_run", "now")\n\t_ = s.db.SetSetting("update_scan_last_error", "")\n}\n' >> "$d/$SERVICES_ROOT/s.go"
  selftest_case "two raw writes in services" 1 "internal/services/s\.go:[0-9]+: func \(s \*S\) scan\(\): a second raw SetSetting" "$d"

  # GREEN: a write AFTER a closed loop is not inside it.
  d=$(fresh)
  printf 'func (h *H) after(c *gin.Context) {\n\tfor _, u := range updates {\n\t\t_ = u\n\t}\n\t_ = h.db.SetSetting("a", "1")\n}\n' >> "$d/$HANDLERS_ROOT/h.go"
  selftest_case "write after a closed loop" 0 "0 violations" "$d"

  # GREEN: test files and comments are out of scope.
  d=$(fresh)
  printf 'package handlers\n\nfunc f() {\n\t_ = db.SetSetting("a", "1")\n\t_ = db.SetSetting("b", "2")\n}\n' > "$d/$HANDLERS_ROOT/h_test.go"
  printf 'func (h *H) commented() {\n\t_ = h.db.SetSetting("a", "1")\n\t// was: h.db.SetSetting("b", "2")\n}\n' >> "$d/$HANDLERS_ROOT/h.go"
  selftest_case "test file and comment" 0 "0 violations" "$d"

  # ERROR: no scan root, and a scan root with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan root" 2 "no backend/internal/handlers" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/$HANDLERS_ROOT" "$d/$SERVICES_ROOT"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"
  d=$(fresh); rm -rf "${d:?}/$SERVICES_ROOT"
  selftest_case "no services root" 2 "no backend/internal/services" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: settings-writes self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "settings-writes self-test: $ST_RUN control(s) passed (3 green, 3 red, 3 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
