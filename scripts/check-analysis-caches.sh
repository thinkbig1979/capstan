#!/usr/bin/env bash
# scripts/check-analysis-caches.sh
#
# ONE invariant (agent-os-ldtp):
#
#   In a workflow job that can run on a self-hosted runner, every step that
#   runs a Go analyzer (golangci-lint, go vet, staticcheck) runs it over a
#   per-run Go build cache: GOCACHE under RUNNER_TEMP (runner.temp).
#   golangci-lint also needs GOLANGCI_LINT_CACHE there, and the
#   golangci-lint-action needs `skip-cache: true`.
#
# WHY. On these self-hosted runners HOME persists between jobs, so the default
# GOCACHE and golangci-lint cache are runner-local state shared by every job on
# that runner. On 2026-10-02 runner capstan-3 produced 6 false staticcheck
# SA5011 findings and replayed them from cache on the next job, while capstan-2
# passed on the same keys (agent-os-p01q, PR #602). #602 isolated the lint job;
# the unit job's `go vet` and the geterrors vettool ran over the same shared
# cache until agent-os-ldtp. An analyzer verdict is the one output that must
# not come from state an earlier job left behind. This check keeps the next
# analysis step on a per-run cache.
#
# HOW. Each workflow is read line by line (comments dropped):
#   job     a key at two-space indent under `jobs:`; self-hosted when its
#           runs-on line names `self-hosted` (the fork fallback to
#           ubuntu-latest sits in the same expression and does not exempt it)
#   step    a list item under the job's `steps:`
#   cache   a line assigning GOCACHE (or GOLANGCI_LINT_CACHE) whose value names
#           RUNNER_TEMP or runner.temp, in the step itself, in the job's own
#           env, or in an EARLIER step's `>> "$GITHUB_ENV"` line
#
# NOT COVERED (blind spots, deliberate):
#   * jobs outside self-hosted runners (a GitHub-hosted VM starts empty)
#   * reusable workflows and composite actions (their steps are not here)
#   * an analyzer started through a script or make target
#   * a cache variable set through `${{ env.X }}` indirection, or a job whose
#     runs-on is built from a matrix or a variable
#   * govulncheck: it keeps no per-package verdict cache; its verdict comes
#     from the vulnerability database fetched on every run (INFERRED, not
#     tested), so it is not an analysis step here
#   * go build / go test: -count=1 turns result caching off and the build
#     cache is content-addressed; INFERRED lower risk, as agent-os-ldtp says
#   * golangci-lint is recognised as the golangci-lint-action or the words
#     `golangci-lint run`; another subcommand (fmt, linters) is not an analysis
#     step here. A path such as $RUNNER_TEMP/golangci-lint is not a run (the
#     first draft read it as one and flagged the isolation step itself).
#     go vet and staticcheck are matched on the command words, so a comment-free
#     echo or heredoc that spells them is read as a run (a false positive, in
#     the safe direction)
#
# USAGE
#   check-analysis-caches.sh              scan this repo
#   check-analysis-caches.sh DIR          scan DIR/.github/workflows/*.yml
#   check-analysis-caches.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with no
# workflow, or no analysis step on a self-hosted job: a check that found
# nothing to compare against must not read as clean).
#
# awk: POSIX only, no gawk extensions. It runs under mawk on CI and
# scripts/check-docs.sh awk-portability runs --self-test under mawk locally.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Prints one violation per line, then a final "STATS jobs selfhosted steps".
AWK_PROG='
function flush_step(   ok, why) {
  if (instep && an) {
    if (selfhosted) {
      checked++
      why = ""
      if (!(jobgo || persistgo || stepgo)) why = "GOCACHE is not under RUNNER_TEMP/runner.temp"
      if (lint && !(joblint || persistlint || steplint)) why = why (why == "" ? "" : "; ") "GOLANGCI_LINT_CACHE is not under RUNNER_TEMP/runner.temp"
      if (act && !stepskip) why = why (why == "" ? "" : "; ") "golangci-lint-action has no skip-cache: true"
      if (why != "") printf "%s:%d: job \"%s\" step \"%s\" runs %s on a self-hosted runner over a persistent cache: %s\n", FILENAME, anline, job, (stepname == "" ? "(unnamed)" : stepname), what, why
    }
  }
  if (instep) { persistgo = persistgo || pendgo; persistlint = persistlint || pendlint }
  instep = 0; an = 0; lint = 0; act = 0; what = ""; stepgo = 0; steplint = 0; stepskip = 0; pendgo = 0; pendlint = 0; stepname = ""; named = 0
}
function flush_job() {
  flush_step()
  if (job != "") { jobs++; if (selfhosted) shjobs++ }
  job = ""; selfhosted = 0; jobgo = 0; joblint = 0; persistgo = 0; persistlint = 0; insteps = 0; stepind = -1
}
function note_cache(c, instepnow,   viaenv, temp) {
  temp = (c ~ /RUNNER_TEMP/ || c ~ /runner\.temp/)
  viaenv = (c ~ /GITHUB_ENV/)
  if (c ~ /(^|[^A-Za-z0-9_])GOCACHE[[:space:]]*[:=]/ && temp) {
    if (instepnow) { if (viaenv) pendgo = 1; else stepgo = 1 } else jobgo = 1
  }
  if (c ~ /(^|[^A-Za-z0-9_])GOLANGCI_LINT_CACHE[[:space:]]*[:=]/ && temp) {
    if (instepnow) { if (viaenv) pendlint = 1; else steplint = 1 } else joblint = 1
  }
}
function note_analysis(c) {
  if (c ~ /^[[:space:]]*name:/) return
  if (c ~ /golangci-lint-action/) { lint = 1; act = 1; mark("golangci-lint") }
  else if (c ~ /golangci-lint[[:space:]]+run([[:space:]]|$)/) { lint = 1; mark("golangci-lint") }
  if (c ~ /(^|[^A-Za-z0-9_-])go[[:space:]]+vet([^A-Za-z0-9_]|$)/) mark("go vet")
  if (c ~ /staticcheck/) mark("staticcheck")
}
function mark(w) {
  if (!an) { an = 1; anline = FNR; what = w } else if (index(what, w) == 0) what = what "+" w
}
BEGIN { job = ""; selfhosted = 0; stepind = -1; injobs = 0 }
FNR == 1 { flush_job(); injobs = 0 }
{
  line = $0
  if (line ~ /^[[:space:]]*#/) next
  sub(/[[:space:]]+#.*$/, "", line)
  if (line ~ /^[[:space:]]*$/) next
  match(line, /^ */); ind = RLENGTH
  if (ind == 0) {
    if (line ~ /^jobs:/) { injobs = 1 } else { flush_job(); injobs = 0 }
    next
  }
  if (!injobs) next
  if (ind == 2 && line ~ /^  [A-Za-z0-9_-]+:[[:space:]]*$/) {
    flush_job(); job = line; sub(/^[[:space:]]+/, "", job); sub(/:.*$/, "", job); next
  }
  if (job == "") next
  if (line ~ /^[[:space:]]*steps:[[:space:]]*$/) { insteps = 1; stepind = -1; next }
  if (!insteps) {
    if (line ~ /runs-on:/ && line ~ /self-hosted/) selfhosted = 1
    note_cache(line, 0)
    next
  }
  if (line ~ /^[[:space:]]*-[[:space:]]/ && (stepind < 0 || ind == stepind)) {
    if (stepind < 0) stepind = ind
    flush_step(); instep = 1
    sub(/^[[:space:]]*-[[:space:]]+/, "", line)
  }
  if (!instep) next
  if (!named && line ~ /^[[:space:]]*name:/) {
    named = 1; stepname = line; sub(/^[[:space:]]*name:[[:space:]]*/, "", stepname); gsub(/^["]|["]$/, "", stepname)
  }
  note_cache(line, 1)
  if (line ~ /skip-cache:[[:space:]]*.?true/) stepskip = 1
  note_analysis(line)
}
END { flush_job(); printf "STATS %d %d %d\n", jobs, shjobs, checked }
'

# scan_dir DIR: prints violations and a final "STATS ..." line; returns 2 when
# DIR has no workflow file.
scan_dir() {
  local dir="$1/.github/workflows" files=()
  local f
  for f in "$dir"/*.yml "$dir"/*.yaml; do
    [ -f "$f" ] && files+=("$f")
  done
  if [ "${#files[@]}" -eq 0 ]; then
    echo "check-analysis-caches: no workflow files under $dir" >&2
    return 2
  fi
  local out='' o
  for f in "${files[@]}"; do
    o=$(command awk "$AWK_PROG" "$f") || return 2
    out="${out}${o}"$'\n'
  done
  # Fold the per-file STATS lines into one, keep violations.
  printf '%s' "$out" | command awk '
    /^STATS / { j += $2; s += $3; c += $4; n++; next }
    NF { print }
    END { printf "STATS %d %d %d %d\n", n, j, s, c }'
}

run_scan() {
  local root="$1" out viol stats
  out=$(scan_dir "$root") || return 2
  stats=$(command grep '^STATS ' <<<"$out")
  viol=$(command grep -v '^STATS ' <<<"$out" | command grep -v '^$' || true)
  set -- $stats
  local nfiles="$2" njobs="$3" nsh="$4" nsteps="$5"
  if [ "$nsteps" -eq 0 ]; then
    echo "check-analysis-caches: no analysis step on a self-hosted job under $root ($nfiles workflow file(s), $njobs job(s), $nsh self-hosted); the scan reached nothing" >&2
    return 2
  fi
  if [ -n "$viol" ]; then
    echo "check-analysis-caches: an analyzer step on a self-hosted runner has no per-run cache (agent-os-p01q, agent-os-ldtp). Set GOCACHE (and GOLANGCI_LINT_CACHE) under RUNNER_TEMP in the step, the job env, or an earlier step's >> \"\$GITHUB_ENV\":"
    echo "${viol//$REPO_ROOT\//}"
    return 1
  fi
  echo "check-analysis-caches: $nfiles workflow file(s), $nsh self-hosted job(s) of $njobs, $nsteps analysis step(s) checked, 0 violations"
  return 0
}

# --- self-test -------------------------------------------------------------

ST_FAILS=0
ST_N=0

# st_case NAME WANT_EXIT PATTERN: the fixture workflow is on stdin.
st_case() {
  local name="$1" want="$2" pat="$3" dir out status
  dir=$(mktemp -d) || { echo "FAIL: could not create a temp directory"; ST_FAILS=$((ST_FAILS + 1)); return; }
  mkdir -p "$dir/.github/workflows"
  cat > "$dir/.github/workflows/w.yml"
  out=$(run_scan "$dir" 2>&1)
  status=$?
  rm -rf "$dir"
  ST_N=$((ST_N + 1))
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: analysis-caches self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

self_test() {
  # RED: the planted shapes this check exists to catch.
  st_case "golangci-lint on self-hosted, no cache env" 1 'job "lint" step "golangci-lint" runs golangci-lint .*GOCACHE is not under' <<'YML'
jobs:
  lint:
    runs-on: ${{ (github.event_name == 'pull_request') && 'ubuntu-latest' || 'self-hosted' }}
    steps:
      - uses: actions/checkout@v7
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v9
        with:
          version: v2.14.0
YML
  st_case "go vet -vettool on self-hosted" 1 'job "unit" step "Analyzer" runs go vet ' <<'YML'
jobs:
  unit:
    runs-on: self-hosted
    steps:
      - name: Analyzer
        run: |
          go build -o "$RUNNER_TEMP/tool" ./cmd/tool
          go vet -vettool="$RUNNER_TEMP/tool" ./...
YML
  st_case "staticcheck on self-hosted" 1 'runs staticcheck ' <<'YML'
jobs:
  sc:
    runs-on: self-hosted
    steps:
      - name: Static
        run: staticcheck ./...
YML
  st_case "GOCACHE named only in a comment" 1 'step "Vet" runs go vet ' <<'YML'
jobs:
  unit:
    runs-on: self-hosted
    steps:
      # GOCACHE: ${{ runner.temp }}/go-build
      - name: Vet
        run: go vet ./...
YML
  st_case "GOCACHE under the workspace, not RUNNER_TEMP" 1 'step "Vet" runs go vet ' <<'YML'
jobs:
  unit:
    runs-on: self-hosted
    steps:
      - name: Vet
        env:
          GOCACHE: ${{ github.workspace }}/.cache/go-build
        run: go vet ./...
YML
  st_case "cache set by a LATER step" 1 'step "Vet" runs go vet ' <<'YML'
jobs:
  unit:
    runs-on: self-hosted
    steps:
      - name: Vet
        run: go vet ./...
      - name: Per-run cache
        run: echo "GOCACHE=$RUNNER_TEMP/go-build" >> "$GITHUB_ENV"
YML
  st_case "lint action without skip-cache" 1 'golangci-lint-action has no skip-cache' <<'YML'
jobs:
  lint:
    runs-on: self-hosted
    env:
      GOCACHE: ${{ runner.temp }}/go-build
      GOLANGCI_LINT_CACHE: ${{ runner.temp }}/golangci-lint
    steps:
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v9
        with:
          version: v2.14.0
YML
  st_case "lint with GOCACHE but no GOLANGCI_LINT_CACHE" 1 'GOLANGCI_LINT_CACHE is not under' <<'YML'
jobs:
  lint:
    runs-on: self-hosted
    steps:
      - name: Cache
        run: echo "GOCACHE=$RUNNER_TEMP/go-build" >> "$GITHUB_ENV"
      - name: golangci-lint
        run: golangci-lint run ./...
YML
  # GREEN: the isolated shapes, and what is out of scope.
  st_case "the #602 lint shape (GITHUB_ENV + skip-cache)" 0 '1 analysis step\(s\) checked, 0 violations' <<'YML'
jobs:
  lint:
    runs-on: self-hosted
    steps:
      - name: Per-run Go and golangci-lint caches
        run: |
          echo "GOCACHE=$RUNNER_TEMP/go-build" >> "$GITHUB_ENV"
          echo "GOLANGCI_LINT_CACHE=$RUNNER_TEMP/golangci-lint" >> "$GITHUB_ENV"
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v9 # v9.3.0
        with:
          version: v2.14.0
          skip-cache: true
YML
  st_case "step-level env under runner.temp" 0 '3 analysis step\(s\) checked, 0 violations' <<'YML'
jobs:
  unit:
    runs-on: ${{ (github.event_name == 'pull_request') && 'ubuntu-latest' || 'self-hosted' }}
    steps:
      - name: Build
        run: go build ./...
      - name: Vet
        env:
          GOCACHE: ${{ runner.temp }}/go-build-vet
        run: go vet ./...
      - name: Vet tagged
        env:
          GOCACHE: ${{ runner.temp }}/go-build-vet
        run: go vet -tags=x ./...
      - name: Analyzer
        env:
          GOCACHE: ${{ runner.temp }}/go-build-vet
        run: |
          go test -C tools/a ./...
          go vet -vettool="$RUNNER_TEMP/a" ./...
YML
  st_case "job-level env under RUNNER_TEMP" 0 '1 analysis step\(s\) checked, 0 violations' <<'YML'
jobs:
  unit:
    runs-on: self-hosted
    env:
      GOCACHE: ${{ runner.temp }}/go-build
    steps:
      - name: Vet
        run: go vet ./...
  other:
    runs-on: self-hosted
    steps:
      - name: Build only
        run: go build ./...
YML
  st_case "hosted-only job is out of scope, a checked job still counts" 0 '1 analysis step\(s\) checked, 0 violations' <<'YML'
jobs:
  hosted:
    runs-on: ubuntu-latest
    steps:
      - name: Vet
        run: go vet ./...
  selfhosted:
    runs-on: self-hosted
    steps:
      - name: Vet
        env:
          GOCACHE: ${{ runner.temp }}/go-build
        run: go vet ./...
YML
  st_case "step name mentioning golangci-lint is not a run" 2 'no analysis step on a self-hosted job' <<'YML'
jobs:
  lint:
    name: Lint (golangci-lint)
    runs-on: self-hosted
    steps:
      - name: golangci-lint
        run: echo nothing
YML
  # ERROR: a tree with no workflow at all is not a clean tree.
  local empty out status
  empty=$(mktemp -d) || { echo "FAIL: could not create a temp directory"; ST_FAILS=$((ST_FAILS + 1)); }
  out=$(run_scan "$empty" 2>&1)
  status=$?
  rm -rf "$empty"
  ST_N=$((ST_N + 1))
  if [ "$status" -ne 2 ]; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: analysis-caches self-test case \"no workflow files\": want exit 2, got exit $status: $out"
  fi

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: analysis-caches self-test - $ST_FAILS of $ST_N control(s) failed"
    return 1
  fi
  echo "analysis-caches self-test: $ST_N control(s) passed (8 red, 4 green, 2 error)"
  return 0
}

main() {
  case "${1:-}" in
    --self-test) self_test; exit $? ;;
    -h|--help)
      echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
      exit 0
      ;;
    "") run_scan "$REPO_ROOT" ;;
    -*) echo "Unknown argument: $1" >&2; exit 2 ;;
    *) run_scan "$1" ;;
  esac
}

main "$@"
