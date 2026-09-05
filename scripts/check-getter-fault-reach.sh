#!/usr/bin/env bash
# scripts/check-getter-fault-reach.sh
#
# LAYER C of agent-os-zhe9. Layers A and B (scripts/check-getter-errors.sh)
# sweep the SOURCE and answer "is every site converted". This sweeps the TESTS
# and answers the DIFFERENT question: does any test actually drive a fault INTO
# each converted site?
#
# WHY IT EXISTS, and it is not hypothetical. On 2026-09-05 the agent-os-l42o
# diff converted all ten sites in services/backup_config.go and every
# source-side sweep read clean at 0 -- while EIGHT of the ten were pinned by
# nothing. Both fault fixtures tripped on read #1 (restic_repository, closed
# DB) or read #2 (restic_password, rotated key), so no error was ever driven
# into resolveIntSetting / resolveStringSetting / resolveBoolSetting. Mutating
# all three to swallow their error left the suite GREEN. "Every site is
# converted" and "every converted site has a fault arm that reaches it" are
# different sweeps, and layers A and B only ever answer the first.
#
# MECHANISM: (i) COVERAGE, not (ii) per-site mutation. Both are allowed by the
# bead. (i) is one compiled test binary per package; (ii) is one compile per
# site, minutes on a shared box, with the well-known trap that a non-compiling
# mutant prints a package-level FAIL with ZERO `--- FAIL` lines and reads as a
# kill. The bead's own control is already stated in coverage terms.
#
# THE SELECTION IS STRUCTURAL, because nothing in this repo marks a test as
# fault-only and a standing tool cannot hand-enumerate -run names: every
# `func Test*` in a `*_dbfault_test.go` file, GROUPED BY PACKAGE.
#
# Grouping by package rather than by file is not cosmetic. A package can hold
# several of these files -- backend/internal/services holds FOUR on 7f94bd1,
# one added by each of agent-os-obgr, agent-os-g482 and agent-os-r1by -- and
# running each file's tests as its own selection produces one coverage profile
# per file over the SAME sources, so every shared source file is counted once
# per profile. Measured: with per-file selection this reported CONVERTED=208
# against a true 102, and it inverted the acceptance's own control, showing
# resolveIntSetting/resolveStringSetting/resolveBoolSetting as MISS because
# one of the four selections did not reach them. A run_files_disjoint check
# below now fails loudly if the file sets from two selections ever overlap
# again.
#
# The SITE set is derived structurally too, and NOT by a filename convention: the site set is every
# non-test file in that package of which the fault selection executed at least
# one block. A name-pairing rule (`X_dbfault_test.go` -> `X.go`) was tried
# first and is WRONG on this tree -- handlers/session_lookup_dbfault_test.go
# has no session_lookup.go at all, its nine tests drive faults into auth.go,
# settings.go and ws.go, and the rule failed closed on it. Deriving the file
# set from the profile needs no convention and cannot miss a file the tests
# actually run.
#
# THE MEMBERSHIP RULE IS BOUNDED, and the bound is tested. A coverage block
# counts as evidence for a site only when it lies WHOLLY inside that site's
# `if err != nil` body. The obvious looser rule (block starts inside the body)
# is wrong: Go's cover profile starts the block AFTER the body at the closing
# brace's own line, so it would report a never-executed error branch as covered
# whenever the statement after it ran -- the same borrowed-guard mistake that
# made handlers/directories.go:219 read GUARDED in three separate documents
# (agent-os-3h9x). testdata/reach/cov_partial.txt pins the bound: the second
# site's own branch is 0 while the block immediately after it is 1, and the
# self-test requires MISS.
#
# IT IS A REPORTING TOOL, NOT A CI GATE, and that is a deliberate escalation
# rather than a silent narrowing. It needs `go test` with a coverage build --
# tens of seconds and a resolved module -- while the required check that runs
# scripts/check-docs.sh is documented as needing a checkout and nothing else
# (.github/workflows/docs.yml). So it runs on demand, its verdict is committed
# as a baseline, and check-docs.sh is not made to depend on it.
#
# ITS OWN RECALL CONTROL, which is not a boolean. Over the five sites the
# agent-os-zhe9 acceptance names, run at two real SHAs:
#   a78637de5974edc2a5a6e696bce9be7b5a62004c -> 5 of 5 MISS
#   f92dc34f81f7caabc8bc8afc43a5e63458dfdb0a -> exactly 1 MISS, and on main too
# The one survivor is RepoSettingSources' PASSWORD read: its own test
# TestRepoSettingSources_RefusesOnUnreadableDB uses a CLOSED db, which trips on
# the FIRST read and returns before reaching the password read -- the exact
# failure mode this layer exists to catch, reproduced one iteration later
# inside the fix meant to close it. It is tracked as agent-os-a6bc. This tool
# REPORTS it; it does not fix it, and a version of this tool that reported
# clean at both SHAs would be measuring nothing.
#
# Usage:
#   check-getter-fault-reach.sh                 report, and fail on growth
#   check-getter-fault-reach.sh --self-test     prove the check still fires, both ways
#   check-getter-fault-reach.sh --update-baseline
#
# Exit: 0 clean, 1 growth or an unusable input, 2 usage error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TOOL="$SCRIPT_DIR/getter-errors/main.go"
FIXTURES="$SCRIPT_DIR/getter-errors/testdata/reach"
BASELINE="$SCRIPT_DIR/check-getter-fault-reach-baseline.txt"

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [--self-test | --update-baseline]

For every backend package holding a *_dbfault_test.go file, runs that file's
Test* functions under -coverprofile and reports, for each converted
\`x, err := f(); if err != nil {...}\` site in the paired source file, whether
any test drove a fault into it. Fails when the MISS count for a function
exceeds $(basename "$BASELINE").
USAGE
}

require_go() {
  if ! command -v go >/dev/null 2>&1; then
    echo "getter-fault-reach: FAIL - 'go' is not on PATH." >&2
    return 1
  fi
}

# report emits `<file> <func> MISS=<n>` rows plus one TOTAL row.
report() {
  require_go || return 1
  local backend="$REPO_ROOT/backend"
  local faults dirs
  faults=$(find "$backend/internal" -name '*_dbfault_test.go' | grep -v '/testdata/' | sort)
  if [ -z "$faults" ]; then
    echo "getter-fault-reach: FAIL - no *_dbfault_test.go file found under backend/internal." >&2
    echo "  The selection is structural, so an empty selection is a blind instrument," >&2
    echo "  not a clean result." >&2
    return 1
  fi
  dirs=$(for f in $faults; do dirname "$f"; done | sort -u)
  local tmp
  tmp=$(mktemp -d) || return 1
  local rc=0 dir i=0
  : >"$tmp/raw"
  for dir in $dirs; do
    i=$((i + 1))
    local pkg names prof srcs
    pkg="./${dir#"$backend"/}"
    # EVERY dbfault file in this package, as ONE selection and ONE profile.
    names=$(cat "$dir"/*_dbfault_test.go | grep -oE '^func (Test[A-Za-z0-9_]+)' |
      awk '{print $2}' | sort -u | paste -sd'|')
    if [ -z "$names" ]; then
      echo "getter-fault-reach: FAIL - no Test* function in $dir/*_dbfault_test.go" >&2
      rc=1
      continue
    fi
    prof="$tmp/$(basename "$dir").cov"
    if ! (cd "$backend" && go test "$pkg" -run "^($names)\$" -count=1 -coverprofile="$prof" >"$tmp/test.log" 2>&1); then
      echo "getter-fault-reach: FAIL - the fault selection did not pass in $pkg:" >&2
      sed 's/^/  /' "$tmp/test.log" >&2
      rc=1
      continue
    fi
    # The site set: every non-test file the selection actually executed a
    # block of. Profile paths are import paths, so map them back through the
    # one path component every one of them shares, /backend/.
    srcs=$(awk 'NR > 1 && $NF > 0 { split($1, a, ":"); print a[1] }' "$prof" |
      sort -u | sed 's#^.*/backend/#'"$backend"'/#' | grep -v '_test\.go$')
    if [ -z "$srcs" ]; then
      echo "getter-fault-reach: FAIL - $pkg executed no coverable statement." >&2
      rc=1
      continue
    fi
    printf '%s\n' "$srcs" >"$tmp/files.$i"
    # shellcheck disable=SC2086
    go run "$TOOL" reach "$prof" $srcs >>"$tmp/raw" || rc=1
  done

  # A source file reached by two selections would be counted twice. Package
  # grouping makes that impossible; this asserts it rather than assuming it,
  # because the per-file version of this loop silently doubled every count.
  if [ "$(cat "$tmp"/files.* 2>/dev/null | wc -l)" -ne "$(cat "$tmp"/files.* 2>/dev/null | sort -u | wc -l)" ]; then
    echo "getter-fault-reach: FAIL - two selections cover the same source file, so" >&2
    echo "  every shared site would be counted once per selection:" >&2
    cat "$tmp"/files.* | sort | uniq -d | sed 's/^/  /' >&2
    rc=1
  fi

  if [ "$rc" -ne 0 ]; then
    rm -rf "$tmp"
    return 1
  fi
  awk '
    /^MISSLINE / { split($2, a, ":"); fn = $NF; gsub(/[()]/, "", fn); key = a[1] " " fn; miss[key]++; order[++n] = key }
    /^TOTAL / { conv += $2 + 0; reach += $3 + 0 }
    END {
      for (i = 1; i <= n; i++) { k = order[i]; if (!(k in seen)) { seen[k] = 1; keys[++m] = k } }
      for (i = 1; i <= m; i++) print keys[i] " MISS=" miss[keys[i]]
    }
  ' FS='[ =]' OFS=' ' "$tmp/raw" | sort >"$tmp/rows"
  grep -h '^TOTAL ' "$tmp/raw" | awk -F'[ =]' '{c += $3; r += $5; m += $7} END {printf "TOTAL CONVERTED=%d REACHED=%d MISS=%d\n", c, r, m}' >>"$tmp/rows"
  cat "$tmp/rows"
  rm -rf "$tmp"
  return 0
}

compare_miss() {
  local base="$1" cur="$2"
  awk -v basefile="$base" '
    BEGIN {
      while ((getline line < basefile) > 0) {
        if (line ~ /^TOTAL /) continue
        n = split(line, f, " ")
        if (n != 3) continue
        sub(/^MISS=/, "", f[3])
        b[f[1] " " f[2]] = f[3] + 0
      }
      close(basefile)
      bad = 0
    }
    /^TOTAL / { next }
    {
      n = split($0, f, " ")
      if (n != 3) next
      sub(/^MISS=/, "", f[3])
      k = f[1] " " f[2]
      if (f[3] + 0 > b[k]) { printf "  %s MISS %d -> %d\n", k, b[k], f[3] + 0; bad = 1 }
    }
    END { exit bad }
  ' "$cur"
}

self_test() {
  require_go || return 1
  local out failed=0

  # ARM 1, MUST REPORT CLEAN: every error branch covered.
  out=$(go run "$TOOL" reach "$FIXTURES/cov_all.txt" "$FIXTURES/reach.go" 2>&1)
  if [ "$out" != 'REACHED reach.go:19 GetThing (firstRead)
REACHED reach.go:27 GetThing (secondRead)
TOTAL CONVERTED=2 REACHED=2 MISS=0' ]; then
    echo "self-test: FAIL - a fully covered fixture was not reported clean:"
    echo "$out" | sed 's/^/  /'
    failed=1
  fi

  # ARM 2, MUST REPORT THE MISS: the second site's own branch is 0 while the
  # block immediately after it is 1. A window that does not stop at the closing
  # brace reports this as REACHED, which is the borrowed-guard failure.
  out=$(go run "$TOOL" reach "$FIXTURES/cov_partial.txt" "$FIXTURES/reach.go" 2>&1)
  if [ "$out" != 'REACHED reach.go:19 GetThing (firstRead)
MISS    reach.go:27 GetThing (secondRead)
TOTAL CONVERTED=2 REACHED=1 MISS=1
MISSLINE reach.go:27 GetThing (secondRead)' ]; then
    echo "self-test: FAIL - an unreached error branch was not reported as MISS,"
    echo "  or the coverage window borrowed the following block's evidence:"
    echo "$out" | sed 's/^/  /'
    failed=1
  fi

  # ARM 3, the COMPARISON, both ways.
  local tmp
  tmp=$(mktemp -d) || return 1
  printf 'a.go f MISS=1\nTOTAL CONVERTED=2 REACHED=1 MISS=1\n' >"$tmp/base"
  printf 'a.go f MISS=1\nTOTAL CONVERTED=2 REACHED=1 MISS=1\n' >"$tmp/same"
  printf 'a.go f MISS=2\nTOTAL CONVERTED=2 REACHED=0 MISS=2\n' >"$tmp/grown"
  compare_miss "$tmp/base" "$tmp/same" >/dev/null 2>&1 || {
    echo "self-test: FAIL - the comparison rejected an unchanged report."
    failed=1
  }
  if compare_miss "$tmp/base" "$tmp/grown" >/dev/null 2>&1; then
    echo "self-test: FAIL - the comparison accepted a grown MISS count."
    failed=1
  fi
  rm -rf "$tmp"

  [ "$failed" -ne 0 ] && return 1
  echo "self-test: fully-covered fixture reports 0 misses, partially-covered fixture reports exactly 1 and is not rescued by the block after it, comparison rejects growth and accepts no change"
  return 0
}

main() {
  case "${1:-}" in
    '')
      if [ ! -f "$BASELINE" ]; then
        echo "getter-fault-reach: FAIL - baseline $BASELINE is missing." >&2
        exit 1
      fi
      local cur status
      cur=$(mktemp) || exit 1
      if ! report >"$cur"; then
        rm -f "$cur"
        exit 1
      fi
      cat "$cur"
      local grown
      grown=$(compare_miss "$BASELINE" "$cur")
      status=$?
      rm -f "$cur"
      if [ "$status" -ne 0 ]; then
        echo "getter-fault-reach: FAIL - a converted site lost its fault arm."
        echo "  Converting a site without a test that drives a fault into it"
        echo "  reinstates the original defect silently: the source sweep reads"
        echo "  clean and nothing exercises the branch."
        echo "$grown"
        exit 1
      fi
      echo "getter-fault-reach: no function exceeds its baseline MISS count"
      ;;
    --self-test)
      self_test
      ;;
    --update-baseline)
      report >"$BASELINE" || exit 1
      echo "getter-fault-reach: baseline rewritten -> ${BASELINE#"$REPO_ROOT"/}"
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
      exit 2
      ;;
  esac
}

main "$@"
