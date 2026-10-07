#!/usr/bin/env bash
# scripts/check-project-name-lookup.sh
#
# ONE invariant (agent-os-z91e.39):
#
#   No non-test Go file under backend/internal or backend/cmd filters a query
#   on project_name, except GetStackByProjectName in
#   backend/internal/database/stacks.go.
#
# WHY. stacks.project_name has no UNIQUE constraint, and two stacks can share
# a compose project name. A lookup keyed on it decides which stack's lock a
# container action takes (handlers/resources.go lockContainerStack,
# handlers/updates.go updateContainer). Written with QueryRow, it silently
# picks the first row. agent-os-z91e.19 made GetStackByProjectName read every
# match and return AmbiguousProjectNameError for 2+ rows; this makes a second,
# first-row-wins lookup coming back a red CI run instead of a review find.
#
# WHAT IS A VIOLATION. A line, not starting with //, where project_name is
# followed by a comparison: =, !=, <>, IN, LIKE or IS (any case), in any
# string form. The check is line-based, so a multi-line query
# (`WHERE\n  project_name = ?`) is seen on the line that holds the filter.
# The enclosing function is the last `func` line above the hit.
#
# LIMIT. It cannot see:
#   - a column name built at runtime ("project_" + col, fmt.Sprintf("%s = ?", c));
#   - a query builder call (Where("project_name", x));
#   - a quoted identifier ("project_name" = ?) or a filter written after a
#     /* */ comment opener on an earlier line.
# A `SET project_name = ?` in an UPDATE would be reported as a false
# positive; none exists today. The table is not checked, so a project_name
# filter on any table counts. There is no allow marker, on purpose: a new
# legitimate lookup changes the allowed function list here, in review.
#
# USAGE
#   check-project-name-lookup.sh              scan this repo
#   check-project-name-lookup.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-project-name-lookup.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files under the scan roots, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ALLOWED_FILE='backend/internal/database/stacks.go'
ALLOWED_FUNC='GetStackByProjectName'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# scan_files prints path:line: text for every violation in the files named
# as arguments (paths relative to the current directory). Exit 1 means a
# violation, anything above 1 is awk's own failure.
scan_files() {
  awk -v allowed_file="$ALLOWED_FILE" -v allowed_func="$ALLOWED_FUNC" '
    FNR == 1 { fn = "" }
    /^func[ \t]/ {
      name = $0
      sub(/^func[ \t]+/, "", name)
      sub(/^\([^)]*\)[ \t]*/, "", name)
      sub(/[[(].*/, "", name)
      fn = name
    }
    /^[ \t]*\/\// { next }
    {
      l = tolower($0)
      if (l ~ /(^|[^a-z0-9_])project_name[ \t]*(=|!=|<>|(in|like|is)([^a-z0-9_]|$))/) {
        if (FILENAME == allowed_file && fn == allowed_func) next
        print FILENAME ":" FNR ":" $0
        bad = 1
      }
    }
    END { exit bad ? 1 : 0 }
  ' "$@"
}

check_dir() {
  local dir="$1" root
  [ -d "$dir" ] || { echo "check-project-name-lookup: $dir is not a directory" >&2; return 2; }

  local roots=()
  for root in backend/internal backend/cmd; do
    [ -d "$dir/$root" ] && roots+=("$root")
  done
  if [ "${#roots[@]}" -eq 0 ]; then
    echo "check-project-name-lookup: neither backend/internal nor backend/cmd under $dir" >&2
    return 2
  fi

  local files=() nfiles
  mapfile -t files < <(cd "$dir" && command find "${roots[@]}" -type f -name '*.go' ! -name '*_test.go' | LC_ALL=C sort)
  nfiles=${#files[@]}
  if [ "$nfiles" -eq 0 ]; then
    echo "check-project-name-lookup: no non-test Go files under ${roots[*]} in $dir" >&2
    return 2
  fi

  local out status
  out=$(cd "$dir" && scan_files "${files[@]}" 2>&1)
  status=$?
  case "$status" in
    0) echo "check-project-name-lookup: 0 violations in $nfiles files"; return 0 ;;
    1)
      if [ -z "$out" ]; then
        echo "check-project-name-lookup: awk failed with no output" >&2
        return 2
      fi
      echo "$out"
      echo "check-project-name-lookup: a query filtered on project_name outside $ALLOWED_FUNC (use db.GetStackByProjectName, which reports a shared name as ambiguous)"
      return 1
      ;;
    *) echo "check-project-name-lookup: scan failed ($status): $out" >&2; return 2 ;;
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
  if [ "$status" -ne "$want" ] || ! echo "$out" | command grep -qE -- "$pat"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: project-name-lookup self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_base writes a clean tree: the allowed lookup, a read of the column
# with no filter, and a comment naming the shape.
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/database" "$d/backend/cmd/server"
  cat > "$d/backend/internal/database/stacks.go" <<'EOF'
package database

func (d *DB) ListStacks() {
	_ = `SELECT id, project_name, status FROM stacks ORDER BY project_name`
}

// A lookup on project_name = ? belongs in GetStackByProjectName only.
func (d *DB) GetStackByProjectName(projectName string) (*models.Stack, error) {
	query := `SELECT id, directory
	          FROM stacks WHERE project_name = ? ORDER BY id`
	_ = query
	return nil, nil
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

# plant DIR FILE TEXT appends a function holding TEXT to DIR/FILE.
plant() {
  printf '\nfunc planted() {\n%s\n}\n' "$3" >> "$1/$2"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: project-name-lookup self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: the allowed function, an unfiltered column read, a comment.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED: the bead's reproduction, a second lookup in the same file.
  d=$(fresh); plant "$d" backend/internal/database/stacks.go '	_ = `SELECT id FROM stacks WHERE project_name = ?`'
  selftest_case "second lookup in stacks.go" 1 "backend/internal/database/stacks.go:[0-9]+:.*project_name = \?" "$d"

  # RED: an interpreted string in another file, no spaces around =.
  d=$(fresh); mkdir -p "$d/backend/internal/handlers"
  printf 'package handlers\n' > "$d/backend/internal/handlers/x.go"
  plant "$d" backend/internal/handlers/x.go '	_ = "SELECT id FROM stacks WHERE s.project_name=?"'
  selftest_case "other file, alias, no spaces" 1 "backend/internal/handlers/x.go:[0-9]+:" "$d"

  # RED: multi-line query, the filter on its own line, upper case.
  d=$(fresh); plant "$d" backend/internal/database/stacks.go '	_ = `SELECT id FROM stacks
	WHERE
	  PROJECT_NAME = ?`'
  selftest_case "multi-line WHERE" 1 "stacks.go:[0-9]+:[[:space:]]+PROJECT_NAME = \?" "$d"

  # RED: IN and LIKE.
  d=$(fresh); plant "$d" backend/internal/database/stacks.go '	_ = `SELECT id FROM stacks WHERE project_name IN (?, ?)`'
  selftest_case "IN" 1 "stacks.go:[0-9]+:.*project_name IN" "$d"
  d=$(fresh); plant "$d" backend/internal/database/stacks.go '	_ = `SELECT id FROM stacks WHERE project_name like ?`'
  selftest_case "LIKE" 1 "stacks.go:[0-9]+:.*project_name like" "$d"

  # RED: backend/cmd is in scope too.
  d=$(fresh); plant "$d" backend/cmd/server/main.go '	_ = "DELETE FROM stacks WHERE project_name != ?"'
  selftest_case "backend/cmd" 1 "backend/cmd/server/main.go:[0-9]+:" "$d"

  # GREEN: test files and comments are out of scope; a longer identifier
  # (project_name_old) is not the column.
  d=$(fresh)
  printf 'package database\n\nfunc f() { _ = "WHERE project_name = ?" }\n' > "$d/backend/internal/database/stacks_test.go"
  plant "$d" backend/internal/database/stacks.go '	// WHERE project_name = ? was the old shape
	_ = `SELECT id FROM t WHERE project_name_old = ?`'
  selftest_case "test file, comment, longer identifier" 0 "0 violations" "$d"

  # ERROR: no scan roots, and scan roots with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan roots" 2 "neither backend/internal" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/backend/internal"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: project-name-lookup self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "project-name-lookup self-test: $ST_RUN control(s) passed (2 green, 6 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
