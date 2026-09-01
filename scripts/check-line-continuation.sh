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
#
# Exit: 0 clean, 1 violations found, 2 usage/environment error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [FILE...]

With no arguments, every tracked shell script, Dockerfile and GitHub Actions
workflow is scanned. With arguments, exactly those paths are scanned.

Fails when a line ending in '\' is followed by a comment line, which silently
truncates the command at the comment.
USAGE
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
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
