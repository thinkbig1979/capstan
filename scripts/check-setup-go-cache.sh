#!/usr/bin/env bash
# scripts/check-setup-go-cache.sh
#
# Fails when an `actions/setup-go` step in .github/workflows/*.yml has no
# explicit `cache:` input (agent-os-nzxh).
#
# WHY: setup-go caches by default. On a self-hosted capstan runner the restore
# is a multi-GB download plus unpack on top of Go caches that persist anyway;
# PR #632's "Set up Go" step took 8m50s that way and the job hit its 15 minute
# limit. The default is right on a GitHub-hosted runner, wrong on ours, and a
# new job that copies a neighbouring step inherits whichever it copied. Making
# the choice explicit forces whoever adds the step to make it on purpose.
#
# WHAT IT CHECKS: every step whose `uses:` is actions/setup-go@... carries a
# `cache:` key at line start (a comment such as `# cache: false` does not
# count, and neither does `cache-dependency-path:`). The value is not judged:
# `false`, `true` and an expression such as
# `${{ runner.environment == 'github-hosted' }}` are all explicit choices.
#
# A step is the run of lines from its `- ` line to the next line indented less
# than the step's keys, so a `cache:` belonging to the NEXT step is never
# borrowed.
#
# Absence is an error, never a pass: finding no setup-go step at all means the
# pattern stopped matching, not that the tree is clean.
#
# Dependency-free by design: bash and grep only. No awk, no network.
#
# Usage:
#   check-setup-go-cache.sh [FILE...]   check these workflows (default:
#                                       .github/workflows/*.yml)
#   check-setup-go-cache.sh --self-test prove the check fires both ways
#
# Exit: 0 every setup-go step names its cache, 1 some do not (file:line of each),
#       2 nothing could be checked (unreadable file, or no setup-go step found).

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SG_FOUND=0
SG_MISSING=()

# indent_of LINE prints the number of leading spaces.
indent_of() {
  local s="$1"
  [[ "$s" =~ ^([[:space:]]*) ]]
  echo "${#BASH_REMATCH[1]}"
}

# is_filler LINE succeeds for blank and comment-only lines, which carry no
# indentation information about the step they sit in.
is_filler() {
  [[ "$1" =~ ^[[:space:]]*(#.*)?$ ]]
}

# scan_file FILE records into SG_FOUND / SG_MISSING; returns 2 if unreadable.
scan_file() {
  local wf="$1"
  if [ ! -r "$wf" ] || [ ! -f "$wf" ]; then
    echo "check-setup-go-cache: $wf is not a readable file" >&2
    return 2
  fi
  local -a lines=()
  mapfile -t lines < "$wf"
  local n="${#lines[@]}" i
  for ((i = 0; i < n; i++)); do
    [[ "${lines[$i]}" =~ ^([[:space:]]*)(-[[:space:]]+)?uses:[[:space:]]*actions/setup-go@ ]] || continue
    SG_FOUND=$((SG_FOUND + 1))
    # Indent of the step's own keys: the dash and its trailing spaces count.
    local key_indent=$(( ${#BASH_REMATCH[1]} + ${#BASH_REMATCH[2]} ))

    local start=$i end=$i k
    for ((k = i - 1; k >= 0; k--)); do
      is_filler "${lines[$k]}" && continue
      [ "$(indent_of "${lines[$k]}")" -ge "$key_indent" ] || break
      start=$k
    done
    for ((k = i + 1; k < n; k++)); do
      is_filler "${lines[$k]}" && continue
      [ "$(indent_of "${lines[$k]}")" -ge "$key_indent" ] || break
      end=$k
    done

    local has=0
    for ((k = start; k <= end; k++)); do
      if [[ "${lines[$k]}" =~ ^[[:space:]]+cache:[[:space:]]*[^[:space:]#] ]]; then
        has=1
        break
      fi
    done
    [ "$has" -eq 1 ] || SG_MISSING+=("$wf:$((i + 1))")
  done
  return 0
}

# check_files FILE... prints the verdict and returns 0, 1 or 2 as in the header.
check_files() {
  SG_FOUND=0
  SG_MISSING=()
  local f rc=0
  for f in "$@"; do
    scan_file "$f" || rc=2
  done
  [ "$rc" -eq 0 ] || return 2
  if [ "$SG_FOUND" -eq 0 ]; then
    echo "check-setup-go-cache: no 'uses: actions/setup-go@' step found in $# file(s); the pattern stopped matching or the workflows moved" >&2
    return 2
  fi
  if [ "${#SG_MISSING[@]}" -gt 0 ]; then
    echo "FAIL: setup-go-cache - setup-go step(s) without an explicit 'cache:' input:" >&2
    printf '  - %s\n' "${SG_MISSING[@]}" >&2
    echo "Add 'cache: false' (self-hosted runner), 'cache: true' (GitHub-hosted only) or" >&2
    echo "\"cache: \${{ runner.environment == 'github-hosted' }}\" (both), with a one-line reason (agent-os-nzxh)." >&2
    return 1
  fi
  echo "PASS: setup-go-cache - $SG_FOUND setup-go step(s), each with an explicit cache: input"
  return 0
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_STATUS WANT_OUTPUT_REGEX FILE...
selftest_case() {
  local name="$1" want="$2" pat="$3" out flat status
  shift 3
  ST_RUN=$((ST_RUN + 1))
  out=$(check_files "$@" 2>&1)
  status=$?
  flat="$(tr '\n' ' ' <<<"$out")"
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$flat"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: setup-go-cache self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

selftest() {
  local tmp
  tmp=$(mktemp -d) || { echo "FAIL: setup-go-cache self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$tmp"' RETURN
  local f

  # GREEN: false, true and an expression are all explicit; the later steps
  # also use the `- uses:` first-key form and a with-block comment.
  f="$tmp/green.yml"
  cat > "$f" <<'EOF'
jobs:
  a:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
          cache: false
      - name: Set up Go (hosted)
        uses: actions/setup-go@v7
        with:
          cache-dependency-path: backend/go.sum
          # why
          cache: true
      - uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
          cache: ${{ runner.environment == 'github-hosted' }}
EOF
  selftest_case "false, true and expression" 0 "PASS: setup-go-cache - 3 setup-go step" "$f"

  # RED: only cache-dependency-path, which is not the cache: input.
  f="$tmp/red-dependency-path.yml"
  cat > "$f" <<'EOF'
jobs:
  a:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
          cache-dependency-path: backend/go.sum
EOF
  selftest_case "cache-dependency-path is not cache:" 1 "FAIL: setup-go-cache.*red-dependency-path\.yml:5" "$f"

  # RED: the key exists only in a comment.
  f="$tmp/red-comment.yml"
  cat > "$f" <<'EOF'
jobs:
  a:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
          # cache: false
EOF
  selftest_case "cache: only in a comment" 1 "red-comment\.yml:5" "$f"

  # RED: the step has no with-block at all, in the `- uses:` form.
  f="$tmp/red-bare.yml"
  printf 'jobs:\n  a:\n    steps:\n      - uses: actions/setup-go@v7\n      - run: true\n' > "$f"
  selftest_case "bare - uses: step" 1 "red-bare\.yml:4" "$f"

  # RED: the step without the key is followed by one that has it. A window
  # that runs on to the next match would borrow that key and pass.
  f="$tmp/red-borrow.yml"
  cat > "$f" <<'EOF'
jobs:
  a:
    steps:
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
      - name: Set up Go again
        uses: actions/setup-go@v7
        with:
          cache: false
EOF
  selftest_case "next step's cache: is not borrowed" 1 "red-borrow\.yml:5.*" "$f"
  # ... and it names only the first step, not the second.
  ST_RUN=$((ST_RUN + 1))
  if check_files "$f" 2>&1 | command grep -q 'red-borrow\.yml:9'; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: setup-go-cache self-test case \"borrow names the keyed step too\""
  fi

  # RED: the key sits one step too early (belongs to a non-setup-go step).
  f="$tmp/red-other-step.yml"
  cat > "$f" <<'EOF'
jobs:
  a:
    steps:
      - name: Cache something
        uses: actions/cache@v5
        with:
          cache: false
      - name: Set up Go
        uses: actions/setup-go@v7
        with:
          go-version-file: backend/go.mod
EOF
  selftest_case "previous step's cache: is not borrowed" 1 "red-other-step\.yml:9" "$f"

  # ERROR: no setup-go step anywhere: an empty guard is not a pass.
  f="$tmp/none.yml"
  printf 'jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v7\n' > "$f"
  selftest_case "no setup-go step" 2 "no 'uses: actions/setup-go@' step found" "$f"

  # ERROR: an unreadable file.
  selftest_case "missing file" 2 "not a readable file" "$tmp/does-not-exist.yml"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: setup-go-cache self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "setup-go-cache self-test: $ST_RUN control(s) passed (1 green, 5 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) echo "Usage: $(basename "$0") [FILE... | --self-test]" >&2; exit 0 ;;
  --self-test) selftest; exit $? ;;
  -*) echo "Usage: $(basename "$0") [FILE... | --self-test]" >&2; exit 2 ;;
  '') check_files "$REPO_ROOT"/.github/workflows/*.yml; exit $? ;;
  *) check_files "$@"; exit $? ;;
esac
