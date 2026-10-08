#!/usr/bin/env bash
# scripts/check-rclone-delete-argv.sh
#
# ONE invariant (agent-os-qags.17, keeps agent-os-z91e.9's delete cap unbypassable):
#
#   No non-test Go file under backend/internal or backend/cmd builds an rclone
#   argv whose verb can delete (sync, move, moveto, delete, deletefile, purge,
#   rmdir, rmdirs, cleanup, bisync) outside the allowlisted functions
#   RcloneManager.Sync and RcloneManager.RestoreRepo.
#
# WHY. z91e.9 bounded the off-site rclone sync: Sync refuses above a pre-flight
# delete cap and passes --max-delete. The bound lives in Sync, and the tests pin
# Sync's argv. Nothing stopped a NEW mirror-with-delete call site from running
# `rclone sync` (or `purge`, `delete`, ...) without the cap.
#
# WHAT IS A VIOLATION. A line outside a comment and outside an `import (` block
# that has either
#   1. a delete verb literal as a complete element of an argv on a line with
#      `[]string{` or `append(`:  []string{"sync"}, append(a, "purge"), or the
#      verb in any later position;
#   2. nothing but a delete verb literal ("sync",): the element line of a
#      multi-line []string{ literal;
#   in a func that is not on the ALLOWLIST below; or
#   3. (consumer arm) a literal "rclone" passed as a command argument,
#      `, "rclone",`, in any file other than internal/services/backup_rclone.go.
#      That file is the one place rclone is run, so a second runner of rclone is
#      a new argv builder this script cannot see into.
#
# ALLOWLIST BY FUNCTION, never by line, file + name from the `^func` line above
# (a method's receiver is dropped). Every entry must still match at least one
# candidate: a stale entry fails the check, which also proves on every run that
# the scan reached the file it names.
#
# LIMIT. Line-based. A verb held in a variable ([]string{verb}), an argv built by
# strings.Fields or a join, and an rclone run through a non-literal command name
# (cfg.RclonePath) are invisible to arms 1 and 2; arm 3 narrows that to "rclone
# is only ever run from backup_rclone.go". The verb strings "sync" and "delete"
# also appear in Go as labels (Kind: "sync", the "sync" import); arm 1 keys on the
# argv shape and arm 2 skips import blocks, so those do not fire. Observed on
# bc0bf5d: exactly the two allowlisted sites (backup_rclone.go Sync, RestoreRepo).
#
# USAGE
#   check-rclone-delete-argv.sh              scan this repo
#   check-rclone-delete-argv.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-rclone-delete-argv.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation or stale allowlist, 2 usage/internal error
# (including a tree with no Go files under the scan roots).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

RCLONE_FILE='backend/internal/services/backup_rclone.go'

# file (relative to the scanned dir) | func | reason
ALLOWLIST='
backend/internal/services/backup_rclone.go|Sync|the off-site mirror: syncPreflight refuses above the delete cap and the argv carries --max-delete (agent-os-z91e.9)
backend/internal/services/backup_rclone.go|RestoreRepo|disaster-recovery restore into a local path: its deletions hit only local-only files, which --backup-dir preserves
'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# candidates DIR ROOT...: print "relfile|func|line|arm|text" for every hit.
candidates() {
  local dir="$1" f; shift
  while IFS= read -r f; do
    awk -v rel="${f#"$dir"/}" -v rclonefile="$RCLONE_FILE" '
      # code(l): l with comments removed, string contents kept.
      function code(l,   out, i, c, instr) {
        out = ""; instr = ""
        for (i = 1; i <= length(l); i++) {
          c = substr(l, i, 1)
          if (instr != "") {
            out = out c
            if (instr == "\"" && c == "\\") { i++; out = out substr(l, i, 1); continue }
            if (c == instr) instr = ""
            continue
          }
          if (c == "\"" || c == "`" || c == "\047") { instr = c; out = out c; continue }
          if (c == "/" && substr(l, i + 1, 1) == "/") break
          out = out c
        }
        return out
      }
      BEGIN {
        verbs = "(sync|move|moveto|delete|deletefile|purge|rmdir|rmdirs|cleanup|bisync)"
        elem = "[{(,][ \t]*\"" verbs "\"[ \t]*[,})]"
        solo = "^[ \t]*\"" verbs "\",?[ \t]*$"
      }
      /^func / {
        s = $0
        sub(/^func[ \t]+/, "", s)
        if (s ~ /^\(/) sub(/^\([^)]*\)[ \t]*/, "", s)
        sub(/[\[(].*$/, "", s)
        fn = s
      }
      /^import[ \t]*\(/ { inimport = 1; next }
      inimport { if ($0 ~ /^\)/) inimport = 0; next }
      {
        line = code($0)
        arm = ""
        if (line ~ /(\[\]string\{|append\()/ && line ~ elem) arm = "argv"
        else if (line ~ solo) arm = "solo"
        else if (line ~ /,[ \t]*"rclone"[ \t]*,/ && rel != rclonefile) arm = "consumer"
        if (arm != "") printf "%s|%s|%d|%s|%s\n", rel, (fn == "" ? "<top-level>" : fn), NR, arm, $0
        if ($0 ~ /^}/) fn = ""
      }
    ' "$f"
  done < <(command grep -rl --include='*.go' '' "$@" 2>/dev/null \
             | command grep -v '_test\.go$' \
             | sort)
}

check_dir() {
  local dir="$1" root
  [ -d "$dir" ] || { echo "rclone-delete-argv: $dir is not a directory" >&2; return 2; }

  local roots=()
  for root in backend/internal backend/cmd; do
    [ -d "$dir/$root" ] && roots+=("$dir/$root")
  done
  if [ "${#roots[@]}" -eq 0 ]; then
    echo "rclone-delete-argv: neither backend/internal nor backend/cmd under $dir" >&2
    return 2
  fi

  local nfiles
  nfiles=$(command grep -rl --include='*.go' '' "${roots[@]}" | command grep -vc '_test\.go$')
  if [ "$nfiles" -eq 0 ]; then
    echo "rclone-delete-argv: no non-test Go files under backend/internal or backend/cmd in $dir" >&2
    return 2
  fi

  local cands
  cands=$(candidates "$dir" "${roots[@]}")

  local bad="" used="" file fn ln arm text key
  while IFS='|' read -r file fn ln arm text; do
    [ -z "$file" ] && continue
    key="$file|$fn|"
    if [ "$arm" != "consumer" ] && printf '%s\n' "$ALLOWLIST" | command grep -qF "$key"; then
      used+="$key"$'\n'
    else
      bad+="  $file:$ln ($fn, $arm arm): $(echo "$text" | sed 's/^[ \t]*//')"$'\n'
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
    echo "rclone-delete-argv: an rclone delete-capable argv outside Sync/RestoreRepo, or rclone run outside backup_rclone.go. Route it through RcloneManager.Sync (delete cap), or add the enclosing func to ALLOWLIST in $(basename "$0") with a reason:"
    printf '%s' "$bad"
    rc=1
  fi
  if [ -n "$stale" ]; then
    echo "rclone-delete-argv: stale ALLOWLIST entries match no candidate (remove them, or the scan did not reach these files):"
    printf '%s' "$stale"
    rc=1
  fi
  if [ "$rc" -eq 0 ]; then
    local n
    n=$(printf '%s' "$used" | command grep -c .)
    echo "rclone-delete-argv: $nfiles Go files scanned, $n allowlisted delete-capable argv(s), 0 violations"
  fi
  return "$rc"
}

# --- self-test -------------------------------------------------------------

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX DIR
selftest_case() {
  local name="$1" want="$2" pat="$3" dir="$4" out rc
  ST_RUN=$((ST_RUN + 1))
  out=$(check_dir "$dir" 2>&1)
  rc=$?
  if [ "$rc" -ne "$want" ] || ! echo "$out" | command grep -qE -- "$pat"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: rclone-delete-argv self-test case \"$name\": want exit $want matching /$pat/, got exit $rc:"
    echo "$out"
  fi
}

# write_base DIR: the two real shapes (Sync, RestoreRepo), the consumer calls,
# the labels that share a verb string, and nothing else, so it is clean.
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/services" "$d/backend/internal/handlers" "$d/backend/cmd/server"
  cat > "$d/backend/internal/services/backup_rclone.go" <<'GO'
package services

import (
	"context"
	"sync"
)

func (m *RcloneManager) TestConnectivity(ctx context.Context) error {
	args := []string{"lsd", "--max-depth", "1", "--", "r:"}
	return runDrained(ctx, m.runner, "rclone", args, nil)
}

func (m *RcloneManager) Sync(ctx context.Context, repoPath string) error {
	args := append([]string{"sync"}, syncOptions(4)...)
	args = append(args, "--max-delete", "3")
	return m.runner.Run(ctx, "rclone", args, nil, nil)
}

func (m *RcloneManager) RestoreRepo(ctx context.Context, localPath string) error {
	args := append([]string{"sync"}, syncOptions(4)...) // a trailing comment
	return m.runner.Run(ctx, "rclone", args, nil, nil)
}
GO
  cat > "$d/backend/internal/handlers/labels.go" <<'GO'
package handlers

import (
	"sync"
)

// []string{"sync"} in a comment is not code, nor is  "rclone", here.
func labels(c *Context) {
	c.respond("sync", nil)
	log(map[string]interface{}{"kind": "sync"})
	_ = []string{"lsf", "-R"}
}
GO
  cat > "$d/backend/internal/handlers/labels_test.go" <<'GO'
package handlers

func fixture() []string { return []string{"purge"} }
GO
  printf 'package main\n\nfunc main() {}\n' > "$d/backend/cmd/server/main.go"
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: rclone-delete-argv self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: the two real sites, the labels, a comment, an import block, a _test.go file.
  d=$(fresh); selftest_case "clean base tree" 0 "2 allowlisted delete-capable argv\(s\), 0 violations" "$d"

  # RED: the bead's acceptance line, a planted third {"sync"} argv.
  d=$(fresh)
  cat >> "$d/backend/internal/handlers/labels.go" <<'GO'

func planted(ctx context.Context, r Runner) error {
	args := append([]string{"sync"}, "--", "a", "b")
	return r.Run(ctx, "other", args, nil, nil)
}
GO
  selftest_case "third {\"sync\"} argv" 1 "labels.go:[0-9]+ \(planted, argv arm\)" "$d"

  # RED: every other delete verb, and the verb in a later position.
  local verb
  for verb in move moveto delete deletefile purge rmdir rmdirs cleanup bisync; do
    d=$(fresh)
    printf '\nfunc planted() {\n\t_ = []string{"%s", "--", "r:"}\n}\n' "$verb" >> "$d/backend/internal/handlers/labels.go"
    selftest_case "verb $verb" 1 "labels.go:[0-9]+ \(planted, argv arm\)" "$d"
  done
  d=$(fresh)
  printf '\nfunc planted(a []string) {\n\ta = append(a, "--flag", "purge", "r:")\n}\n' >> "$d/backend/internal/handlers/labels.go"
  selftest_case "verb in a later append position" 1 "labels.go:[0-9]+ \(planted, argv arm\)" "$d"

  # RED: a multi-line argv, the verb on its own line.
  d=$(fresh)
  cat >> "$d/backend/internal/handlers/labels.go" <<'GO'

func planted() []string {
	return []string{
		"lsf",
		"purge",
	}
}
GO
  selftest_case "multi-line argv, solo verb line" 1 "labels.go:[0-9]+ \(planted, solo arm\)" "$d"

  # RED: an allowlisted NAME in the wrong file is not excused.
  d=$(fresh)
  printf '\nfunc (m *Other) Sync() {\n\t_ = []string{"sync"}\n}\n' >> "$d/backend/internal/handlers/labels.go"
  selftest_case "allowlisted name, wrong file" 1 "labels.go:[0-9]+ \(Sync, argv arm\)" "$d"

  # RED: a delete argv in backup_rclone.go but in a func that is not allowlisted.
  d=$(fresh)
  printf '\nfunc (m *RcloneManager) Mirror() {\n\t_ = []string{"sync"}\n}\n' >> "$d/backend/internal/services/backup_rclone.go"
  selftest_case "new func in the rclone file" 1 "backup_rclone.go:[0-9]+ \(Mirror, argv arm\)" "$d"

  # RED (consumer arm): rclone run from another file.
  d=$(fresh)
  printf '\nfunc other(ctx context.Context, r Runner) error {\n\treturn r.Run(ctx, "rclone", []string{"lsf"}, nil, nil)\n}\n' >> "$d/backend/internal/handlers/labels.go"
  selftest_case "rclone run outside backup_rclone.go" 1 "labels.go:[0-9]+ \(other, consumer arm\)" "$d"

  # RED: a stale allowlist entry (its func no longer has a candidate).
  d=$(fresh)
  printf 'package services\n\nfunc (m *RcloneManager) Sync() {}\n\nfunc (m *RcloneManager) RestoreRepo() {\n\t_ = []string{"sync"}\n}\n' > "$d/backend/internal/services/backup_rclone.go"
  selftest_case "stale allowlist entry" 1 "stale allowlist entry: backend/internal/services/backup_rclone.go \(Sync\)" "$d"

  # ERROR: no scan roots, and scan roots with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan roots" 2 "neither backend/internal" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/backend/internal"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: rclone-delete-argv self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "rclone-delete-argv self-test: $ST_RUN control(s) passed (1 green, 16 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
