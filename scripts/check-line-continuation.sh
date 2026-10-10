#!/usr/bin/env bash
# scripts/check-line-continuation.sh
#
# Fails when a line ending in a backslash continuation is followed by a
# comment line.
#
# WHY: the comment ENDS the command there. Everything before it that looked
# like a prefix assignment (`FOO=1 \`) stays an unexported shell variable and
# never reaches the program that was supposed to run on the continued line.
# The result is syntactically valid — `bash -n` passes, actionlint has no
# model for it, and YAML parses fine — so nothing else in this repo's CI can
# see it.
#
# OBSERVED 2026-09-01 (PR #230, commit 55e49bb): a comment inserted between
# `CORS_ORIGINS=... \` and `RATE_LIMIT_API_PER_MIN=2000 \ nohup ./server` in
# both "Start backend" steps of .github/workflows/e2e-backup.yml dropped
# PORT, JWT_SECRET and AUTH_DISABLED from the backend's environment; it died
# at boot with `JWT_SECRET: required when AUTH_DISABLED is not set`. Fixed in
# 7b7171c by moving the comments above the command. The trap that let it
# through review: the newly-added var sits AFTER the comment, so it DOES
# arrive — a check scoped to "did my new setting land" goes green while the
# eight variables before the comment are silently lost.
#
# Deliberately not scoped to a step, a shape, or a file type: the same
# mistake truncates a Dockerfile RUN and a shell script exactly as it
# truncated the workflow, so shell scripts, Dockerfiles and workflows are all
# swept. Dependency-free by design — git, grep, sort and awk only.
#
# Usage:
#   check-line-continuation.sh              scan every tracked file in scope
#   check-line-continuation.sh FILE...      scan exactly these paths
#   check-line-continuation.sh --self-test  prove the scan fires both ways
#
# Exit: 0 clean, 1 violations found, 2 usage/environment error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [FILE... | --self-test]

--self-test runs the scan over fixture files (red and green controls) and
exits non-zero if any control misbehaves.

With no arguments, every tracked shell script, Dockerfile and GitHub Actions
workflow is scanned. With arguments, exactly those paths are scanned.

Fails when a line ending in '\' is followed by a comment line, which silently
truncates the command at the comment.
USAGE
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX FILE...
# Runs this script on the fixture paths, so the awk that runs is the real one.
selftest_case() {
  local name="$1" want="$2" pat="$3" out status
  shift 3
  ST_RUN=$((ST_RUN + 1))
  out=$(bash "${BASH_SOURCE[0]}" "$@" 2>&1)
  status=$?
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: line-continuation self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# Fixtures are real files in a temp dir; the scan takes explicit paths, so it
# never touches git. They are built with printf and a $bs variable, never as
# literal source lines ending in a backslash: this file is itself tracked and
# scanned, so a heredoc holding the red shape would redden the real scan.
selftest() {
  local tmp bs='\'
  tmp=$(mktemp -d) || { echo "FAIL: line-continuation self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$tmp"' RETURN

  # RED: the e2e-backup.yml shape, a comment between a prefix assignment and
  # the command it was meant to prefix.
  printf '#!/usr/bin/env bash\nFOO=1 %s\n# why FOO matters\n  ./run\n' "$bs" > "$tmp/red-script.sh"
  selftest_case "comment after continuation (shell)" 1 "red-script\.sh:3: comment interrupts the backslash continuation started on line 2" "$tmp/red-script.sh"

  # RED: an indented comment, in a Dockerfile RUN.
  printf 'FROM scratch\nRUN apt-get update && %s\n    # install the thing\n    apt-get install -y x\n' "$bs" > "$tmp/Dockerfile"
  selftest_case "indented comment in Dockerfile RUN" 1 "Dockerfile:3: comment interrupts" "$tmp/Dockerfile"

  # RED: trailing whitespace after the backslash still continues the line.
  printf 'A=1 %s \t\n# note\nrun\n' "$bs" > "$tmp/red-trailing.sh"
  selftest_case "backslash then trailing whitespace" 1 "red-trailing\.sh:2:" "$tmp/red-trailing.sh"

  # RED: CRLF line endings are stripped before the test.
  printf 'A=1 %s\r\n# note\r\nrun\r\n' "$bs" > "$tmp/red-crlf.sh"
  selftest_case "CRLF file" 1 "red-crlf\.sh:2:" "$tmp/red-crlf.sh"

  # RED: two violations are both counted.
  printf 'A=1 %s\n# one\nrun\nB=2 %s\n# two\nrun\n' "$bs" "$bs" > "$tmp/red-two.sh"
  selftest_case "two violations counted" 1 "FAIL: line-continuation - 2 violation" "$tmp/red-two.sh"

  # GREEN: comment above the command, and a comment after a finished command
  # (only the very next line after a continuation is a violation).
  printf '# comment above is fine\nFOO=1 %s\n  ./run\n\nBAR=1 %s\n  ./run\n# comment after a finished command is fine\n' "$bs" "$bs" > "$tmp/green.sh"
  selftest_case "comment above and after" 0 "1 file\(s\) scanned, no comment interrupts" "$tmp/green.sh"

  # GREEN: a '#' inside the continued line is not a comment line.
  printf 'echo a %s\n  "# not a comment"\n' "$bs" > "$tmp/green-hash.sh"
  selftest_case "# inside continued line" 0 "no comment interrupts" "$tmp/green-hash.sh"

  # GREEN: the lookback resets per file; a file ending in a backslash does
  # not taint the next file's first line.
  printf 'A=1 %s\n' "$bs" > "$tmp/end-backslash.sh"
  printf '# first line comment\nrun\n' > "$tmp/starts-comment.sh"
  selftest_case "file boundary resets lookback" 0 "2 file\(s\) scanned" "$tmp/end-backslash.sh" "$tmp/starts-comment.sh"

  # RED beside GREEN in one run: a violation in the second file is found.
  selftest_case "violation in second file" 1 "red-script\.sh:3:" "$tmp/green.sh" "$tmp/red-script.sh"

  # SKIP: an unreadable path is reported, not fatal, and with nothing left
  # to scan the script says so rather than printing a clean scan count.
  selftest_case "unreadable path only" 0 "no files to scan" "$tmp/does-not-exist.sh"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: line-continuation self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "line-continuation self-test: $ST_RUN control(s) passed (6 red, 3 green, 1 skip)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
esac

# Tracked files in scope. `git ls-files` rather than `find` on purpose:
# find sweeps node_modules and other untracked vendored trees, and an
# untracked vendored script is not something this gate should redden the
# build over.
#
# The grep arm catches both Dockerfile naming conventions a fixed pathspec
# list would miss: suffixed (Dockerfile.dev, Dockerfile-ci) and prefixed
# (web.dockerfile). The suffix may not itself contain a dot, which is what
# keeps prose files like docs/dockerfile-notes.md out of scope -- a markdown
# file swept as a Dockerfile could redden the build over a code fence.
tracked_files() {
  {
    command git -C "$REPO_ROOT" ls-files -- '*.sh' '*.bash'
    command git -C "$REPO_ROOT" ls-files -- '.github/workflows/*.yml' '.github/workflows/*.yaml'
    command git -C "$REPO_ROOT" ls-files | command grep -iE '(^|/)Dockerfile([.-][^/.]*)?$|(^|/)[^/]*\.dockerfile$'
  } | command sort -u
}

files=()
if [ "$#" -gt 0 ]; then
  files=("$@")
else
  if ! command git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
    echo "ERROR: $REPO_ROOT is not a git repository; pass file paths explicitly" >&2
    exit 2
  fi
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    files+=("$REPO_ROOT/$f")
  done < <(tracked_files)
fi

# Unreadable paths are reported and skipped rather than failing the gate:
# `git ls-files` can name a file the working tree does not currently have
# (a sparse checkout, a broken symlink), and that is a checkout problem, not
# a line-continuation problem.
readable=()
for f in "${files[@]}"; do
  if [ -r "$f" ] && [ -f "$f" ]; then
    readable+=("$f")
  else
    echo "SKIP: $f (not a readable regular file)" >&2
  fi
done

if [ "${#readable[@]}" -eq 0 ]; then
  echo "line-continuation: no files to scan"
  exit 0
fi

# Two-line lookback. `prev` is reset on EVERY line, so a comment two lines
# after a continuation is not a violation — only a comment on the line
# immediately following one is. Trailing whitespace after the backslash is
# tolerated when deciding "this line continues", matching the way the
# mistake actually appears rather than the way bash tokenises it.
command awk '
  FNR == 1 { prev = ""; prevline = 0 }
  {
    line = $0
    sub(/\r$/, "", line)
    if (prevline > 0 && line ~ /^[ \t]*#/) {
      printf "%s:%d: comment interrupts the backslash continuation started on line %d\n", FILENAME, FNR, prevline
      printf "    %d: %s\n", prevline, prev
      printf "    %d: %s\n", FNR, line
      bad++
    }
    if (line ~ /\\[ \t]*$/) { prev = line; prevline = FNR } else { prevline = 0 }
  }
  END {
    if (bad > 0) {
      printf "FAIL: line-continuation - %d violation(s); the comment ends the command, dropping every assignment before it\n", bad
      exit 1
    }
    printf "line-continuation: %d file(s) scanned, no comment interrupts a backslash continuation\n", ARGC - 1
  }
' "${readable[@]}"
