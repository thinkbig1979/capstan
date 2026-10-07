#!/usr/bin/env bash
# scripts/check-path-containment.sh
#
# ONE invariant (agent-os-qags.2, safe-defaults rule 6):
#
#   Outside backend/internal/pathutil/, no Go code decides path containment
#   with a lexical prefix test. Containment goes through pathutil.IsContained
#   (or config.MatchStacksRoot, which is built on it), which resolves symlinks.
#
# WHY. A lexical check passes root/a/app when root/a is a symlink to /outside,
# and the removal or write that follows walks through the link. That is
# exactly how Delete's stackDirIsInsideRoot could RemoveAll a directory outside
# the stacks root (agent-os-z91e.22). The helper existed; the call site didn't
# use it. This check makes the next one a red CI run instead of a review find.
#
# WHAT IS A CANDIDATE. Every non-test, non-comment line under backend/ that has
#
#   1. filepath.Rel( or filepath.HasPrefix(  -- the Rel + ".." idiom, and the
#      deprecated lexical helper;
#   2. strings.HasPrefix( / strings.CutPrefix( on the same line as a path
#      separator: filepath.Separator, os.PathSeparator, "/" , "../" or "..";
#   3. strings.HasSuffix( with a path separator -- the two-step form that
#      appends the separator on one line and prefix-tests on the next. That
#      form is what hid stackDirIsInsideRoot from the single-line grep the
#      bead was filed with.
#
# TrimPrefix is not a candidate: it returns no verdict, so it cannot BE a
# containment check (docker.go trims "/" off container names).
#
# ALLOWLIST BY FUNCTION, never by line. A candidate is excused only when its
# enclosing func (file + name, derived from the `^func` line above it) is
# listed below with a reason. Every entry must still match at least one
# candidate: a stale entry fails the check, which also proves on every run
# that the scan reached the files it claims to cover.
#
# USAGE
#   check-path-containment.sh              scan backend/ in this repo
#   check-path-containment.sh DIR          scan DIR (a backend/ tree)
#   check-path-containment.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation or stale allowlist, 2 usage/internal error.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# file (relative to the scanned backend/ dir) | func | reason
ALLOWLIST='
internal/config/config.go|inspectStacksMount|parses /proc/self/mountinfo text to find which mount holds STACKS_DIR; not a containment decision
internal/services/scanner.go|rootContainsPath|chooses WHICH configured root a scanned path is filed under (StackID naming); gates no I/O, and must stay lexical to agree with the lexical StackID and isValidStacksDir
internal/services/scanner.go|expectedStackID|filepath.Rel derives the stack ID path segment, not a containment verdict
internal/services/scanner.go|ScanDirectoryWithRoot|filepath.Rel derives the stack ID path segment, not a containment verdict
internal/handlers/git.go|isValidLogFile|validates a git pathspec passed after "--"; no filesystem I/O, and git itself refuses paths outside the repository
'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# candidates DIR: print "relfile|func|line|text" for every candidate line.
candidates() {
  local dir="$1" f rel
  while IFS= read -r f; do
    rel=${f#"$dir"/}
    awk -v rel="$rel" '
      /^func / {
        s = $0
        sub(/^func[ \t]+/, "", s)
        if (s ~ /^\(/) sub(/^\([^)]*\)[ \t]*/, "", s)
        sub(/[\[(].*$/, "", s)
        fn = s
      }
      /^}/ { fn_end = 1 }
      {
        line = $0
        if (line ~ /^[ \t]*\/\//) next
        sub(/[ \t]\/\/.*$/, "", line)
        sep = "(filepath\\.Separator|os\\.PathSeparator|\"/\"|\"\\.\\./\"|\"\\.\\.\")"
        hit = 0
        if (line ~ /filepath\.(Rel|HasPrefix)\(/) hit = 1
        if (line ~ /strings\.(HasPrefix|CutPrefix)\(/ && line ~ sep) hit = 1
        if (line ~ /strings\.HasSuffix\(/ && line ~ sep) hit = 1
        if (hit) printf "%s|%s|%d|%s\n", rel, (fn == "" ? "<top-level>" : fn), NR, line
        if (fn_end) { fn = ""; fn_end = 0 }
      }
    ' "$f"
  done < <(command grep -rl --include='*.go' '' "$dir" \
             | command grep -v '_test\.go$' \
             | command grep -v '/internal/pathutil/' \
             | sort)
}

check_dir() {
  local dir="$1"
  if [ ! -d "$dir" ]; then
    echo "path-containment: $dir is not a directory" >&2
    return 2
  fi
  local nfiles
  nfiles=$(command grep -rl --include='*.go' '' "$dir" | command grep -vc '_test\.go$')
  if [ "$nfiles" -eq 0 ]; then
    echo "path-containment: no Go files under $dir; refusing to report a clean tree" >&2
    return 2
  fi

  local cands
  cands=$(candidates "$dir")

  local bad="" used="" file fn ln text key
  while IFS='|' read -r file fn ln text; do
    [ -z "$file" ] && continue
    key="$file|$fn|"
    if printf '%s\n' "$ALLOWLIST" | command grep -qF "$key"; then
      used+="$key"$'\n'
    else
      bad+="  $file:$ln ($fn): $(echo "$text" | sed 's/^[ \t]*//')"$'\n'
    fi
  done <<< "$cands"

  local stale="" entry
  while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    key="$(echo "$entry" | cut -d'|' -f1-2)|"
    if ! printf '%s' "$used" | command grep -qF "$key"; then
      stale+="  stale allowlist entry: $(echo "$entry" | cut -d'|' -f1) ($(echo "$entry" | cut -d'|' -f2))"$'\n'
    fi
  done <<< "$ALLOWLIST"

  local rc=0
  if [ -n "$bad" ]; then
    echo "path-containment: lexical path containment outside pathutil. Use pathutil.IsContained (resolves symlinks), or add the enclosing func to ALLOWLIST in $(basename "$0") with a reason if it is not a containment decision:"
    printf '%s' "$bad"
    rc=1
  fi
  if [ -n "$stale" ]; then
    echo "path-containment: stale ALLOWLIST entries match no candidate (remove them, or the scan did not reach these files):"
    printf '%s' "$stale"
    rc=1
  fi
  if [ "$rc" -eq 0 ]; then
    local n
    n=$(printf '%s' "$used" | command grep -c .)
    echo "path-containment: $nfiles Go files scanned, $n allowlisted candidate(s), 0 violations"
  fi
  return "$rc"
}

# --- self-test -------------------------------------------------------------

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_MSG_REGEX DIR
selftest_case() {
  local name="$1" want="$2" want_msg="$3" dir="$4" out rc
  ST_RUN=$((ST_RUN + 1))
  out=$(check_dir "$dir" 2>&1)
  rc=$?
  if [ "$rc" -ne "$want" ]; then
    echo "FAIL: path-containment self-test - '$name' expected exit $want, got $rc:"
    echo "$out"
    ST_FAILS=$((ST_FAILS + 1))
  elif ! echo "$out" | command grep -qE "$want_msg"; then
    echo "FAIL: path-containment self-test - '$name' exited $want but output does not match /$want_msg/:"
    echo "$out"
    ST_FAILS=$((ST_FAILS + 1))
  fi
}

# write_base DIR: a tree holding one candidate for every allowlist entry, and
# nothing else, so it is clean.
write_base() {
  local d="$1"
  mkdir -p "$d/internal/config" "$d/internal/services" "$d/internal/handlers" "$d/internal/pathutil"
  cat > "$d/internal/config/config.go" <<'GO'
package config

func inspectStacksMount(stacksDir, mp string) bool {
	return strings.HasPrefix(stacksDir, mp+"/")
}
GO
  cat > "$d/internal/services/scanner.go" <<'GO'
package services

func rootContainsPath(root, path string) bool {
	rel, _ := filepath.Rel(root, path)
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *ScannerService) expectedStackID(dirPath, effectiveRoot string) string {
	rel, _ := filepath.Rel(effectiveRoot, dirPath)
	return rel
}

func (s *ScannerService) ScanDirectoryWithRoot(path string, rootDir string) error {
	rel, err := filepath.Rel(rootDir, path) // a trailing comment
	_ = rel
	return err
}
GO
  cat > "$d/internal/handlers/git.go" <<'GO'
package handlers

func isValidLogFile(file string) bool {
	cleaned := filepath.Clean(file)
	return cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
GO
  cat > "$d/internal/pathutil/pathutil.go" <<'GO'
package pathutil

func IsContained(root, target string) bool {
	return strings.HasPrefix(target, root+string(filepath.Separator))
}
GO
  cat > "$d/internal/handlers/stacks.go" <<'GO'
package handlers

// strings.HasPrefix(p, root+"/") in a comment is not code.
func ok(name string) string {
	return strings.TrimPrefix(name, "/")
}
GO
  cat > "$d/internal/handlers/stacks_test.go" <<'GO'
package handlers

func helper(p, root string) bool { return strings.HasPrefix(p, root+"/") }
GO
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: path-containment self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: allowlisted funcs, pathutil, a comment and a _test.go file.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED: the bead's acceptance line, planted in a handlers file.
  d=$(fresh)
  cat >> "$d/internal/handlers/stacks.go" <<'GO'

func planted(p, root string) bool {
	return strings.HasPrefix(p, root+"/")
}
GO
  selftest_case "HasPrefix with \"/\" in a handler" 1 "stacks.go:[0-9]+ \(planted\)" "$d"

  # RED: the Rel + ".." idiom outside the allowlist.
  d=$(fresh)
  cat >> "$d/internal/handlers/stacks.go" <<'GO'

func (h *StacksHandler) inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".."
}
GO
  selftest_case "filepath.Rel in a method" 1 "stacks.go:[0-9]+ \(inside\)" "$d"

  # RED: the two-step separator form (stackDirIsInsideRoot before z91e.22).
  d=$(fresh)
  cat >> "$d/internal/handlers/stacks.go" <<'GO'

func twoStep(absDir, absRoot string) bool {
	root := absRoot
	if !strings.HasSuffix(root, string(filepath.Separator)) {
		root += string(filepath.Separator)
	}
	return strings.HasPrefix(absDir, root)
}
GO
  selftest_case "HasSuffix separator two-step" 1 "stacks.go:[0-9]+ \(twoStep\)" "$d"

  # RED: an allowlisted NAME in the wrong file is not excused.
  d=$(fresh)
  cat >> "$d/internal/handlers/stacks.go" <<'GO'

func rootContainsPath(root, p string) bool {
	return strings.HasPrefix(p, root+string(os.PathSeparator))
}
GO
  selftest_case "allowlisted name, wrong file" 1 "stacks.go:[0-9]+ \(rootContainsPath\)" "$d"

  # RED: a stale allowlist entry (its func no longer has a candidate).
  d=$(fresh)
  rm "$d/internal/handlers/git.go"
  selftest_case "stale allowlist entry" 1 "stale allowlist entry: internal/handlers/git.go \\(isValidLogFile\\)" "$d"

  # ERROR: an empty tree is not a clean tree.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "empty tree" 2 "no Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: path-containment self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "path-containment self-test: $ST_RUN control(s) passed (1 green, 5 red, 1 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT/backend"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
