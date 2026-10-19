#!/usr/bin/env bash
# scripts/check-push-switch-writers.sh
#
# ONE invariant (agent-os-k81p.6):
#
#   Outside the one setter, SetDirectoryPushEnabled in
#   backend/internal/database/directories.go, no non-test Go file under
#   backend/ writes directories.git_push_enabled.
#
# WHY. The switch arms a later network write (the push action refuses unless it
# is on). UpsertDirectory, InsertDirectoryIfAbsent and UpdateDirectoryCredentials
# leave the column out on purpose, so a rescan or a credential save can neither
# arm nor disarm it. Nothing enforced that: a later edit adding the column to
# another SET list or column list was caught only if some behavioural test
# happened to set the switch to TRUE first.
#
# WHAT IS A VIOLATION. Default-deny. After stripping // comments, any line that
# names git_push_enabled as a whole word (so the migration Name
# "directories_git_push_enabled" is not a mention) is a violation unless it is
# one of these:
#   a. inside func (d *DB) SetDirectoryPushEnabled in
#      backend/internal/database/directories.go;
#   b. inside `const directoryColumns` in that file, the one SELECT column list;
#   c. a line that has SELECT and none of INSERT, UPDATE, REPLACE (a reader);
#   d. the `ADD COLUMN git_push_enabled` line in
#      backend/internal/database/migrations.go. A later migration that
#      backfills the column is flagged on purpose: it writes the switch.
# Two arms need no mention of the column:
#   - `directoryColumns` on any line that is not its definition and is not a
#     SELECT: INSERT INTO directories (` + directoryColumns + `) would write
#     the column without naming it. This is the consumer arm of (b).
#   - `REPLACE INTO directories` (also `INSERT OR REPLACE INTO`): it deletes
#     and re-creates the row, which resets the switch to 0.
#
# LIMIT. Line-based. A multi-line SELECT naming the column on a continuation
# line trips the default-deny (use directoryColumns); SQL assembled at run time
# is invisible; /* */ comments are not stripped; a // inside a string literal
# truncates that line. There is no allow marker: a reasoned exception is an
# edit to this script. The scan must also see the setter, or it fails: that
# proves it reached directories.go and did not read a rename as clean.
#
# USAGE
#   check-push-switch-writers.sh              scan this repo
#   check-push-switch-writers.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-push-switch-writers.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files under backend/, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SCAN_ROOT=backend
DIR_FILE=backend/internal/database/directories.go
MIG_FILE=backend/internal/database/migrations.go

# The awk program reports "FILE:LINE: FUNC: reason" per violation. It is run
# with -v dirfile=... -v migfile=... and prints "SETTER-SEEN" in END only when
# the setter was reached.
# shellcheck disable=SC2016
AWK_PROG='
function strip(s) { sub(/\/\/.*/, "", s); return s }
FNR == 1 { fname = ""; inconst = 0 }
{
  s = strip($0)
  u = toupper(s)
  if (s ~ /^func /) { fname = s; sub(/[[:space:]]*\{[[:space:]]*$/, "", fname) }
  isdir = (FILENAME == dirfile)
  ismig = (FILENAME == migfile)
  if (isdir && s ~ /^(const|var) directoryColumns[[:space:]=]/) inconst = 1
  reads = (u ~ /SELECT/ && u !~ /(INSERT|UPDATE|REPLACE)/)

  if (s ~ /(^|[^A-Za-z0-9_])git_push_enabled([^A-Za-z0-9_]|$)/) {
    insetter = (isdir && fname ~ /^func \([A-Za-z_][A-Za-z0-9_]* \*?DB\) SetDirectoryPushEnabled\(/)
    if (insetter) seen = 1
    ok = insetter || (isdir && inconst) || reads || (ismig && s ~ /ADD COLUMN git_push_enabled/)
    if (!ok) printf "%s:%d: %s: names git_push_enabled outside SetDirectoryPushEnabled\n", FILENAME, FNR, (fname == "" ? "(file scope)" : fname)
  }
  if (s ~ /(^|[^A-Za-z0-9_])directoryColumns([^A-Za-z0-9_]|$)/ && s !~ /^(const|var) directoryColumns[[:space:]=]/ && !(inconst) && !reads)
    printf "%s:%d: %s: directoryColumns used outside a SELECT (it names git_push_enabled)\n", FILENAME, FNR, (fname == "" ? "(file scope)" : fname)
  if (u ~ /REPLACE[[:space:]]+INTO[[:space:]]+DIRECTORIES/)
    printf "%s:%d: %s: REPLACE INTO directories re-creates the row and resets the push switch\n", FILENAME, FNR, (fname == "" ? "(file scope)" : fname)

  if (inconst && s !~ /\+[[:space:]]*$/) inconst = 0
}
END { if (seen) print "SETTER-SEEN" }'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

check_dir() {
  local dir="$1"
  [ -d "$dir" ] || { echo "check-push-switch-writers: $dir is not a directory" >&2; return 2; }
  [ -d "$dir/$SCAN_ROOT" ] || { echo "check-push-switch-writers: no $SCAN_ROOT under $dir" >&2; return 2; }
  local files=()
  mapfile -t files < <(cd "$dir" && command find "$SCAN_ROOT" -type f -name '*.go' ! -name '*_test.go' | sort)
  if [ "${#files[@]}" -eq 0 ]; then
    echo "check-push-switch-writers: no non-test Go files under $SCAN_ROOT in $dir" >&2
    return 2
  fi

  local out status
  out=$(cd "$dir" && awk -v dirfile="$DIR_FILE" -v migfile="$MIG_FILE" "$AWK_PROG" "${files[@]}" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "check-push-switch-writers: awk failed ($status): $out" >&2
    return 2
  fi
  local seen=0
  if [[ "$out" == *SETTER-SEEN* ]]; then
    seen=1
    out=$(command grep -v '^SETTER-SEEN$' <<<"$out")
  fi
  if [ -n "$out" ]; then
    echo "$out"
    echo "check-push-switch-writers: git_push_enabled is written only by SetDirectoryPushEnabled; keep the column out of every other SET list, column list and REPLACE (agent-os-k81p.6)"
  fi
  if [ "$seen" -eq 0 ]; then
    echo "check-push-switch-writers: the scan never reached SetDirectoryPushEnabled in $DIR_FILE, so a clean result proves nothing (renamed, moved, or the scan root is wrong)"
    return 1
  fi
  [ -n "$out" ] && return 1
  echo "check-push-switch-writers: 0 violations in ${#files[@]} files"
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
    echo "FAIL: push-switch-writers self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

DB_DIR=backend/internal/database

# write_base writes a clean tree shaped like the real one: the one setter, the
# SELECT column list, an upsert and a credentials update that leave the column
# out, a reader, and the migration that adds the column.
write_base() {
  local d="$1"
  mkdir -p "$d/$DB_DIR" "$d/backend/internal/handlers"
  cat > "$d/$DIR_FILE" <<'EOF'
package database

const directoryColumns = `path, name, COALESCE(git_remote, ''), ` +
	`git_auth_type, git_push_enabled, scanned_at`

func (d *DB) UpsertDirectory(path string) error {
	query := `INSERT INTO directories (path, name, scanned_at)
		VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name = excluded.name,
			scanned_at = excluded.scanned_at`
	_, err := d.db.Exec(query, path)
	return err
}

func (d *DB) ListDirectories() error {
	query := `SELECT ` + directoryColumns + ` FROM directories ORDER BY name`
	_, err := d.db.Query(query)
	return err
}

func (d *DB) UpdateDirectoryCredentials(path string) error {
	query := `UPDATE directories SET git_auth_type = ?, git_https_user = ? WHERE path = ?`
	_, err := d.db.Exec(query, path)
	return err
}

// SetDirectoryPushEnabled is the only writer of git_push_enabled.
func (d *DB) SetDirectoryPushEnabled(path string, enabled bool) error {
	res, err := d.db.Exec(`UPDATE directories SET git_push_enabled = ? WHERE path = ?`, enabled, path)
	_ = res
	return err
}
EOF
  cat > "$d/$MIG_FILE" <<'EOF'
package database

var migrations = []Migration{
	{
		Name: "directories_git_push_enabled",
		SQL: `
ALTER TABLE directories ADD COLUMN git_push_enabled INTEGER NOT NULL DEFAULT 0;
`,
	},
}
EOF
  printf 'package handlers\n\nfunc (h *H) get() {\n\t// reads git_push_enabled through the setter only\n}\n' > "$d/backend/internal/handlers/h.go"
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

# plant DIR FILE_REL TEXT appends TEXT to a file of a fresh tree.
plant() {
  printf '%s\n' "$3" >> "$1/$2"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: push-switch-writers self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: the base tree, with the setter, the column list and the migration.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # GREEN: a narrow reader, a comment and a test file may name the column.
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (h *H) read() {
	_ = db.Query(`SELECT git_push_enabled FROM directories WHERE path = ?`)
	// UPDATE directories SET git_push_enabled = 1
}'
  printf 'package handlers\n\nfunc f() {\n\t_ = db.Exec(`UPDATE directories SET git_push_enabled = 1`)\n}\n' > "$d/backend/internal/handlers/h_test.go"
  selftest_case "reader, comment, test file" 0 "0 violations" "$d"

  # RED: the UPDATE form, in another func of the setter's own file (the recall
  # shape: UpdateDirectoryCredentials gaining the column).
  d=$(fresh)
  sed -i 's/SET git_auth_type = ?, git_https_user = ? WHERE/SET git_auth_type = ?, git_push_enabled = ?, git_https_user = ? WHERE/' "$d/$DIR_FILE"
  selftest_case "UPDATE form in UpdateDirectoryCredentials" 1 "directories\.go:[0-9]+: func \(d \*DB\) UpdateDirectoryCredentials\(path string\) error: names git_push_enabled" "$d"

  # RED: the UPDATE form in another file.
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (h *H) arm() {
	_ = db.Exec(`UPDATE directories SET git_push_enabled = 1 WHERE path = ?`)
}'
  selftest_case "UPDATE form in another file" 1 "h\.go:[0-9]+: func \(h \*H\) arm\(\): names git_push_enabled" "$d"

  # RED: the INSERT form (column list names the column).
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (h *H) add() {
	_ = db.Exec(`INSERT INTO directories (path, git_push_enabled) VALUES (?, 1)`)
}'
  selftest_case "INSERT column list" 1 "h\.go:[0-9]+: func \(h \*H\) add\(\): names git_push_enabled" "$d"

  # RED: the multi-line upsert, SET on one line and the column on the next.
  d=$(fresh)
  sed -i 's/^\t\t\tscanned_at = excluded.scanned_at`/\t\t\tscanned_at = excluded.scanned_at,\n\t\t\tgit_push_enabled = excluded.git_push_enabled`/' "$d/$DIR_FILE"
  selftest_case "multi-line upsert" 1 "directories\.go:[0-9]+: func \(d \*DB\) UpsertDirectory\(path string\) error: names git_push_enabled" "$d"

  # RED: rule (c) is not a hole: an INSERT ... SELECT on one line writes the
  # column from another row.
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (h *H) clone() {
	_ = db.Exec(`INSERT INTO directories (path, git_push_enabled) SELECT path || "-copy", git_push_enabled FROM directories`)
}'
  selftest_case "INSERT ... SELECT git_push_enabled" 1 "h\.go:[0-9]+: func \(h \*H\) clone\(\): names git_push_enabled" "$d"

  # RED: consumer arm. directoryColumns reused in an INSERT writes the column
  # without naming it.
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (h *H) copyRow() {
	_ = db.Exec(`INSERT INTO directories (` + directoryColumns + `) VALUES (?)`)
}'
  selftest_case "directoryColumns in an INSERT" 1 "h\.go:[0-9]+: func \(h \*H\) copyRow\(\): directoryColumns used outside a SELECT" "$d"

  # RED: REPLACE re-creates the row and resets the switch without naming it.
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (h *H) swap() {
	_ = db.Exec(`INSERT OR REPLACE INTO directories (path, name) VALUES (?, ?)`)
	_ = db.Exec(`REPLACE INTO directories (path, name) VALUES (?, ?)`)
}'
  selftest_case "REPLACE INTO directories" 1 "h\.go:[0-9]+: func \(h \*H\) swap\(\): REPLACE INTO directories" "$d"

  # RED: the setter is allowed by file AND name; a second function of the same
  # name elsewhere is not the setter.
  d=$(fresh)
  plant "$d" backend/internal/handlers/h.go 'func (d *DB) SetDirectoryPushEnabled(path string) error {
	_, err := d.db.Exec(`UPDATE directories SET git_push_enabled = 1`)
	return err
}'
  selftest_case "same-named setter in another file" 1 "h\.go:[0-9]+: func \(d \*DB\) SetDirectoryPushEnabled" "$d"

  # RED: a migration that writes the switch, not just adds the column.
  d=$(fresh)
  plant "$d" "$MIG_FILE" 'func backfill(tx *sql.Tx) { tx.Exec(`UPDATE directories SET git_push_enabled = 1`) }'
  selftest_case "migration backfill" 1 "migrations\.go:[0-9]+: func backfill.*names git_push_enabled" "$d"

  # RED: the scan never reaching the setter is not a clean tree.
  d=$(fresh)
  sed -i 's/SetDirectoryPushEnabled/SetPushSwitch/' "$d/$DIR_FILE"
  selftest_case "setter renamed away" 1 "never reached SetDirectoryPushEnabled" "$d"

  # ERROR: no scan root, and a scan root with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan root" 2 "no backend under" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/$SCAN_ROOT"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: push-switch-writers self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "push-switch-writers self-test: $ST_RUN control(s) passed (2 green, 10 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
