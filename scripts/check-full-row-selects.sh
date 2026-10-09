#!/usr/bin/env bash
# scripts/check-full-row-selects.sh
#
# ONE invariant (agent-os-w068):
#
#   In a non-test, non-migration Go file under backend/internal/database, a
#   full-row column list for a table with a column constant appears only in
#   that constant, and a full-row Scan target list only in its scan function.
#
# WHY. agent-os-rh7m gave seven tables one column constant and one scan
# function each (stackColumns/scanStack in stacks.go, actionLogColumns/
# scanActionLog in audit.go, userColumns/scanUser in users.go, and the
# directories, backup_policies, auto_update_policies and backup_run_items
# pairs), like backupRunColumns from agent-os-13xd. A reader that hand-copies
# the SELECT list again, with its own positional Scan, compiles and passes
# every test until a column is added to one copy and not the other; then the
# Scan puts values in the wrong struct fields with no error.
#
# HOW. The constants and scan functions are read from the tree, not listed
# here, so a table that gains a pair is covered without editing this script.
#   constants   `const <x>Columns = \`...` : its first three columns
#   scan funcs  `func scan<X>(`            : the first three &<v>.<Field>
#                                            destinations of its Scan call
# A column is normalised before comparing: COALESCE(c, ...) and IFNULL(c, ...)
# read as c, a `table.` or `alias.` prefix is dropped, case is ignored.
#
# WHAT IS A VIOLATION.
#   1. a SELECT whose first three columns equal a constant's first three
#      (the list may start on the line after SELECT), outside the constant;
#   2. a .Scan( whose first three destinations name a scan function's first
#      three fields (any receiver variable), outside that scan function.
# MEMBERSHIP: full row = the same three leading columns. Every deliberate
# subset in the tree today starts differently, so none needs a marker:
#   GetDirectoryCredentials  SELECT path, git_auth_type, ...   (credentials)
#   DistinctActionLogActions SELECT DISTINCT action
#   COUNT queries            SELECT COUNT(*)
#   CreateFirstUser          WHERE NOT EXISTS (SELECT 1 FROM users)
# A subset that does keep the leading three carries
#   //fullrow:ignore <reason>
# on its line or alone on the line above. The reason is mandatory.
#
# LIMIT. Line-based. Columns listed in another order, a list whose first three
# columns are split across Go string concatenation, or one built at run time,
# are invisible. Tables with no constant (update_history, sessions,
# cached_updates, docker_cleanup_runs: one full-row reader each) are out of
# scope: there is no second copy to drift against. migrations.go is skipped:
# its readers are historical and one-shot.
#
# USAGE
#   check-full-row-selects.sh              scan this repo
#   check-full-row-selects.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-full-row-selects.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files, or no column constant, under the scan root: a check that found
# nothing to compare against must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SCAN_ROOT=backend/internal/database

# Shared awk functions: a paren-aware split of a column list, and the
# normalisation described in the header.
# shellcheck disable=SC2016
AWK_LIB='
function norm(c) {
  gsub(/^[[:space:]`"]+|[[:space:]`"]+$/, "", c)
  c = tolower(c)
  if (c ~ /^(coalesce|ifnull)[[:space:]]*\(/) { sub(/^[a-z]+[[:space:]]*\([[:space:]]*/, "", c); sub(/[[:space:]]*[,)].*$/, "", c) }
  sub(/^[a-z_][a-z0-9_]*\./, "", c)
  sub(/[[:space:]].*$/, "", c)
  return c
}
# first3 returns the first three columns of list, normalised, joined by ",";
# "" when the list has fewer than three.
function first3(list,    i, ch, depth, cur, n, out) {
  depth = 0; cur = ""; n = 0; out = ""
  for (i = 1; i <= length(list) && n < 3; i++) {
    ch = substr(list, i, 1)
    if (ch == "(") depth++
    if (ch == ")") depth--
    if (ch == "," && depth == 0) { out = out (n ? "," : "") norm(cur); n++; cur = ""; continue }
    if (ch == "`" && depth == 0) break
    cur = cur ch
  }
  if (n < 3 && cur ~ /[a-z_]/ && n == 2) { out = out "," norm(cur); n++ }
  return n == 3 ? out : ""
}
function scan3(s,    n, out, f) {
  n = 0; out = ""
  while (n < 3 && match(s, /&[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*/)) {
    f = substr(s, RSTART, RLENGTH); sub(/^&[^.]*\./, "", f)
    out = out (n ? "," : "") f; n++
    s = substr(s, RSTART + RLENGTH)
  }
  return n == 3 ? out : ""
}
'

# Pass 1 prints "COL <name> <a,b,c>" and "SCAN <name> <A,B,C>".
# shellcheck disable=SC2016
EXTRACT_PROG='
/^const [A-Za-z_][A-Za-z0-9_]*Columns[[:space:]]*=[[:space:]]*`/ {
  name = $2; s = $0; sub(/^[^`]*`/, "", s)
  t = first3(s); if (t != "") print "COL " name " " t
}
/^func scan[A-Z][A-Za-z0-9_]*\(/ { fn = $2; sub(/\(.*/, "", fn); inscan = 1; buf = ""; next }
inscan {
  if ($0 ~ /\.Scan\(/ || buf != "") buf = buf " " $0
  if (buf != "") { t = scan3(buf); if (t != "") { print "SCAN " fn " " t; inscan = 0; buf = "" } }
  if ($0 ~ /^}/) { inscan = 0; buf = "" }
}'

# Pass 2 reports "FILE:LINE: reason" per violation. SIGS holds pass 1's output.
# shellcheck disable=SC2016
DETECT_PROG='
BEGIN {
  n = split(SIGS, L, "\n")
  for (i = 1; i <= n; i++) {
    split(L[i], p, " ")
    if (p[1] == "COL") col[p[3]] = (p[3] in col ? col[p[3]] "/" : "") p[2]
    if (p[1] == "SCAN") { scn[p[3]] = (p[3] in scn ? scn[p[3]] "/" : "") p[2]; owner[p[2]] = 1 }
  }
}
FNR == 1 { fn = ""; pendsel = 0; pendscan = ""; ign = 0; prevign = 0 }
{
  line = $0
  prevign = ign; ign = 0
  if (match(line, /\/\/fullrow:ignore/)) {
    rest = substr(line, RSTART + RLENGTH)
    if (rest !~ /[^[:space:]]/) { printf "%s:%d: //fullrow:ignore needs a reason\n", FILENAME, FNR; next }
    ign = 1
    pre = substr(line, 1, RSTART - 1)
    standalone = (pre !~ /[^[:space:]]/)
  }
  covered = ign || (prevign && prevstandalone)
  prevstandalone = ign && standalone
  if (line ~ /^func /) { fn = line; sub(/^func (\([^)]*\) )?/, "", fn); sub(/\(.*/, "", fn) }

  # 1. a SELECT list, possibly starting on the next line.
  list = ""
  if (pendsel) { list = line; pendsel = 0; selcov = selcov || covered }
  else if (match(toupper(line), /SELECT[[:space:]]+/)) {
    list = substr(line, RSTART + RLENGTH); selcov = covered
    if (list !~ /[A-Za-z]/) { pendsel = 1; list = "" }
  }
  else if (toupper(line) ~ /SELECT[[:space:]]*$/) { pendsel = 1; selcov = covered }
  if (list != "") {
    t = first3(list)
    if (t in col && !selcov) printf "%s:%d: a literal full-row column list for %s; select %s instead\n", FILENAME, FNR, col[t], col[t]
  }

  # 2. a Scan target list, possibly wrapped over the next lines.
  if (pendscan == "" && line ~ /\.Scan\(/) { pendscan = line; scanline = FNR; scancov = covered; scanlines = 0 }
  else if (pendscan != "") { pendscan = pendscan " " line; scanlines++ }
  if (pendscan != "") {
    t = scan3(pendscan)
    if (t != "" || pendscan ~ /\)[[:space:]]*(;|$|\{|,)/ || scanlines >= 3) {
      if (t in scn && !scancov && !(fn in owner && index("/" scn[t] "/", "/" fn "/")))
        printf "%s:%d: a hand-copied Scan target list for %s; call %s instead\n", FILENAME, scanline, scn[t], scn[t]
      pendscan = ""
    }
  }
}'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

check_dir() {
  local dir="$1"
  [ -d "$dir" ] || { echo "check-full-row-selects: $dir is not a directory" >&2; return 2; }
  [ -d "$dir/$SCAN_ROOT" ] || { echo "check-full-row-selects: no $SCAN_ROOT under $dir" >&2; return 2; }
  local files=()
  mapfile -t files < <(cd "$dir" && command find "$SCAN_ROOT" -maxdepth 1 -type f -name '*.go' ! -name '*_test.go' ! -name 'migrations*.go' | sort)
  if [ "${#files[@]}" -eq 0 ]; then
    echo "check-full-row-selects: no non-test Go files under $SCAN_ROOT in $dir" >&2
    return 2
  fi

  local sigs status
  sigs=$(cd "$dir" && awk "$AWK_LIB$EXTRACT_PROG" "${files[@]}" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "check-full-row-selects: awk failed reading the constants ($status): $sigs" >&2
    return 2
  fi
  local ncol nscan
  ncol=$(command grep -c '^COL ' <<<"$sigs")
  nscan=$(command grep -c '^SCAN ' <<<"$sigs")
  if [ "$ncol" -eq 0 ] || [ "$nscan" -eq 0 ]; then
    echo "check-full-row-selects: found $ncol column constant(s) and $nscan scan function(s) under $SCAN_ROOT; nothing to compare against" >&2
    return 2
  fi

  local out
  out=$(cd "$dir" && awk -v SIGS="$sigs" "$AWK_LIB$DETECT_PROG" "${files[@]}" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "check-full-row-selects: awk failed ($status): $out" >&2
    return 2
  fi
  if [ -n "$out" ]; then
    echo "$out"
    echo "check-full-row-selects: a reader copies a full-row column or Scan list; use the table's <x>Columns constant and scan<X> function (agent-os-rh7m, agent-os-w068)"
    return 1
  fi
  echo "check-full-row-selects: 0 violations in ${#files[@]} files ($ncol column constants, $nscan scan functions)"
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
    echo "FAIL: full-row-selects self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_base writes a clean tree: two tables, each with its constant, its scan
# function and a reader using both, plus a subset and a COUNT that must pass.
write_base() {
  local d="$1"
  mkdir -p "$d/$SCAN_ROOT"
  cat > "$d/$SCAN_ROOT/stacks.go" <<'EOF'
package database

const stackColumns = `id, directory, compose_file, COALESCE(env_file, ''), status`

func scanStack(row interface{ Scan(dest ...any) error }) (models.Stack, error) {
	var s models.Stack
	err := row.Scan(&s.ID, &s.Directory, &s.ComposeFile,
		&s.EnvFile, &s.Status)
	return s, err
}

func (d *DB) GetStack(id string) (models.Stack, error) {
	query := `SELECT ` + stackColumns + ` FROM stacks WHERE id = ?`
	return scanStack(d.db.QueryRow(query, id))
}

func (d *DB) CountStacks() (n int, err error) {
	err = d.db.QueryRow(`SELECT COUNT(*) FROM stacks`).Scan(&n)
	return n, err
}
EOF
  cat > "$d/$SCAN_ROOT/backup.go" <<'EOF'
package database

const backupRunItemColumns = `backup_run_items.id, backup_run_items.run_id, backup_run_items.stack_id, ` +
	`backup_run_items.status`

func scanBackupRunItem(row interface{ Scan(dest ...any) error }) (models.BackupRunItem, error) {
	var item models.BackupRunItem
	err := row.Scan(&item.ID, &item.RunID, &item.StackID, &item.Status)
	return item, err
}

func (d *DB) StackPaths() ([]string, error) {
	rows, err := d.db.Query(`SELECT id, compose_file FROM stacks`)
	_ = rows
	return nil, err
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
  ST_TMP=$(mktemp -d) || { echo "FAIL: full-row-selects self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d f

  # GREEN: readers on their constant and scan function, a COUNT, a subset.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations in 2 files \(2 column constants, 2 scan functions\)" "$d"

  # RED (a): a new reader hand-copies the full-row list (rh7m's old shape).
  d=$(fresh); f="$d/$SCAN_ROOT/stacks.go"
  printf 'func (d *DB) ListStacks() {\n\tquery := `SELECT id, directory, compose_file, COALESCE(env_file, '"''"'), status\n\t          FROM stacks`\n\t_ = query\n}\n' >> "$f"
  selftest_case "copied full-row list" 1 "stacks\.go:[0-9]+: a literal full-row column list for stackColumns" "$d"

  # RED: the copy starts on the line after SELECT.
  d=$(fresh)
  printf 'func (d *DB) ListAll() {\n\tquery := `SELECT\n\t\tid, directory, compose_file FROM stacks`\n\t_ = query\n}\n' >> "$d/$SCAN_ROOT/stacks.go"
  selftest_case "list on the next line" 1 "stacks\.go:[0-9]+: a literal full-row column list for stackColumns" "$d"

  # RED: an alias-prefixed copy with a COALESCE (backup_run_items' old join).
  d=$(fresh)
  printf 'func (d *DB) Items() {\n\tquery := `SELECT bri.id, COALESCE(bri.run_id, ''), bri.stack_id FROM backup_run_items bri`\n\t_ = query\n}\n' >> "$d/$SCAN_ROOT/backup.go"
  selftest_case "alias-prefixed copy" 1 "backup\.go:[0-9]+: a literal full-row column list for backupRunItemColumns" "$d"

  # RED (b): a duplicated Scan target list, other receiver, wrapped.
  d=$(fresh)
  printf 'func (d *DB) Other(rows *sql.Rows) {\n\tvar x models.Stack\n\t_ = rows.Scan(\n\t\t&x.ID, &x.Directory,\n\t\t&x.ComposeFile, &x.EnvFile, &x.Status)\n}\n' >> "$d/$SCAN_ROOT/backup.go"
  selftest_case "duplicated Scan list" 1 "backup\.go:[0-9]+: a hand-copied Scan target list for scanStack" "$d"

  # GREEN (c): a subset keeping the leading three, marked with its reason;
  # the trailing form covers its own line.
  d=$(fresh)
  printf 'func (d *DB) Paths() {\n\t//fullrow:ignore only the three path columns the scanner diff needs\n\tq := `SELECT id, directory, compose_file FROM stacks`\n\tr := `SELECT id, directory, compose_file FROM stacks` //fullrow:ignore same subset\n\t_, _ = q, r\n}\n' >> "$d/$SCAN_ROOT/stacks.go"
  selftest_case "marked subset" 0 "0 violations" "$d"

  # RED: a marker without a reason is reported, and silences nothing.
  d=$(fresh)
  printf 'func (d *DB) Paths() {\n\t//fullrow:ignore\n\tq := `SELECT id, directory, compose_file FROM stacks`\n\t_ = q\n}\n' >> "$d/$SCAN_ROOT/stacks.go"
  selftest_case "marker without a reason" 1 "fullrow:ignore needs a reason" "$d"

  # GREEN: test files and migrations are out of scope.
  d=$(fresh)
  printf 'package database\n\nconst q = `SELECT id, directory, compose_file FROM stacks`\n' > "$d/$SCAN_ROOT/stacks_test.go"
  cp "$d/$SCAN_ROOT/stacks_test.go" "$d/$SCAN_ROOT/migrations.go"
  selftest_case "test file and migrations" 0 "0 violations" "$d"

  # ERROR: no scan root, no Go files, and no constant to compare against.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan root" 2 "no backend/internal/database" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/$SCAN_ROOT"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"
  d=$(mktemp -d "$ST_TMP/noconst.XXXXXX"); mkdir -p "$d/$SCAN_ROOT"
  printf 'package database\n\nfunc f() { _ = `SELECT id, directory, compose_file FROM stacks` }\n' > "$d/$SCAN_ROOT/x.go"
  selftest_case "no column constant" 2 "found 0 column constant" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: full-row-selects self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "full-row-selects self-test: $ST_RUN control(s) passed (3 green, 5 red, 3 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
