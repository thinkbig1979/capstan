#!/usr/bin/env bash
# scripts/check-compose-parity.sh
#
# ONE invariant (agent-os-qags.6, safe-defaults rule 17):
#
#   docker-compose.yaml (dev) and docker-compose.prod.yaml (prod) agree on the
#   three things that broke before, for the `app` service:
#     1. init: true                       (agent-os-a1ye.7, missing init)
#     2. the stacks mount is an identical-path mount: a bind whose source and
#        target both equal STACKS_DIR, and the STACKS_DIR / HOST_STACKS_DIR
#        env values are derived from the same variable
#                                         (agent-os-a1ye.1, prod mount defect)
#     3. the same set of `environment:` keys, except the intended differences
#        listed in INTENDED_ENV_DIFF below
#
# WHY. Both defects were found by an external review, not by CI, and the July
# evaluation had already flagged the two files drifting. The helper (the
# identical-path mount, init) exists in each file; nothing made a new edit to
# one file remember the other.
#
# HOW. `docker compose config` is the ONLY parser: it applies compose's own
# interpolation, defaults, anchors and merge rules, and its normalised output
# is read with awk. There is deliberately no text-parse fallback. A second
# parser nobody tests against compose's real rules would let CI silently run
# the weaker instrument. When the compose plugin is missing the check FAILS
# (exit 1), it never skips.
#
# Each file is checked twice: with STACKS_DIR unset (the defaults) and with
# STACKS_DIR=/srv/x. A mount like `${STACKS_DIR:-/opt/stacks}:/opt/stacks`
# is identical-path at the default and wrong only under the override.
#
# Both files declare a required env_file that a clean checkout lacks
# (./backend/.env, .env). The files are copied into a temp project directory
# with EMPTY stubs for both, so the worktree is never touched (root .env is not
# gitignored) and an empty stub cannot supply a value that hides a default.
#
# WHAT IT DOES NOT COMPARE. Secrets (JWT_SECRET, STORAGE_KEY) arrive through
# env_file, not `environment:`, so they are outside the compared key set.
#
# USAGE
#   check-compose-parity.sh              check this repo's two compose files
#   check-compose-parity.sh DIR          check DIR/docker-compose{,.prod}.yaml
#   check-compose-parity.sh --self-test  prove the check fires both ways
#
# Exit: 0 parity, 1 violation (or compose plugin absent), 2 usage/internal error.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

DEV_FILE="docker-compose.yaml"
PROD_FILE="docker-compose.prod.yaml"
SERVICE="app"
OVERRIDE_STACKS_DIR="/srv/x"

# INTENDED_ENV_DIFF: "KEY|only-in-file|reason". A key listed here must be
# present in exactly that file; a listed key that no longer differs is itself a
# failure, so the list cannot rot into an allow-list for nothing.
INTENDED_ENV_DIFF=(
  "DATA_DIR|$DEV_FILE|prod takes DATA_DIR from the image (docker/Dockerfile: ENV DATA_DIR=/app/data) and mounts ./data at /app/data; dev sets it explicitly"
)

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# Variables the host environment could use to change what compose resolves.
# Unset for every `config` call so a CI runner's TZ or COMPOSE_FILE cannot
# change the verdict.
SCRUBBED_VARS=(STACKS_DIR HOST_STACKS_DIR DATA_DIR PUID PGID TZ PORT
  COMPOSE_FILE COMPOSE_PROFILES COMPOSE_PROJECT_NAME COMPOSE_ENV_FILES)

# compose_config PROJECT_DIR FILE [STACKS_DIR] prints the normalised config.
compose_config() {
  local proj="$1" file="$2" stacks="${3:-}" v
  local -a envargs=()
  for v in "${SCRUBBED_VARS[@]}"; do envargs+=(-u "$v"); done
  [ -n "$stacks" ] && envargs+=("STACKS_DIR=$stacks")
  (cd "$proj" && env "${envargs[@]}" docker compose --project-name parity -f "$file" config 2>&1)
}

# facts SERVICE reads normalised compose YAML on stdin and prints:
#   service=1            the service exists
#   init=<value>
#   env=<KEY>=<VALUE>    one per environment entry
#   mount=<source>|<target>
# Keyed on indentation, not on key order, which compose does not promise.
facts() {
  awk -v svc="$1" '
    function unq(s) { sub(/^"/, "", s); sub(/"$/, "", s); return s }
    function flush() { if (inmount) { nm++; src[nm] = cs; tgt[nm] = ct } inmount = 0; cs = ""; ct = "" }
    {
      match($0, /^ */); ind = RLENGTH; line = substr($0, ind + 1)
      if (line == "") next
    }
    ind == 0 { flush(); intop = (line == "services:"); insvc = 0; prop = ""; next }
    intop && ind == 2 { flush(); insvc = (line == svc ":"); if (insvc) found = 1; prop = ""; next }
    !insvc { next }
    ind == 4 {
      flush()
      prop = line; sub(/:.*/, "", prop)
      val = line; sub(/^[^:]*: ?/, "", val)
      if (prop == "init") init = unq(val)
      next
    }
    prop == "environment" && ind == 6 {
      k = line; sub(/:.*/, "", k)
      v = line; sub(/^[^:]*: ?/, "", v)
      print "env=" k "=" unq(v)
      next
    }
    prop == "volumes" {
      if (ind == 6 && line ~ /^- /) { flush(); inmount = 1; line = substr(line, 3); ind = 8 }
      if (ind == 8) {
        k = line; sub(/:.*/, "", k)
        v = line; sub(/^[^:]*: ?/, "", v)
        if (k == "source") cs = unq(v)
        if (k == "target") ct = unq(v)
      }
    }
    END {
      flush()
      if (found) print "service=1"
      print "init=" init
      for (i = 1; i <= nm; i++) print "mount=" src[i] "|" tgt[i]
    }'
}

# fact_get KEY FACTS prints the value of the first KEY=... line.
fact_get() { printf '%s\n' "$2" | sed -n "s/^$1=//p" | head -n 1; }

VIOLATIONS=0
violation() {
  VIOLATIONS=$((VIOLATIONS + 1))
  echo "check-compose-parity: $1: $2"
}

# check_one PROJECT_DIR FILE STACKS_DIR_OR_EMPTY EXPECTED_STACKS
# Prints the env keys of this run to ENV_KEYS_<tag> via the caller's variable.
check_one() {
  local proj="$1" file="$2" stacks="$3" expected="$4" label out facts_out
  label="STACKS_DIR ${stacks:-unset}"

  if ! out=$(compose_config "$proj" "$file" "$stacks"); then
    violation "$file" "docker compose config failed (STACKS_DIR ${stacks:-unset}): $out"
    return 1
  fi
  facts_out=$(printf '%s\n' "$out" | facts "$SERVICE")

  if [ "$(fact_get service "$facts_out")" != "1" ]; then
    violation "$file" "no service named '$SERVICE' in the resolved config"
    return 1
  fi

  local init
  init=$(fact_get init "$facts_out")
  [ "$init" = "true" ] || violation "$file" "init is '${init:-unset}', want true ($label)"

  local stacks_env host_env
  stacks_env=$(printf '%s\n' "$facts_out" | sed -n 's/^env=STACKS_DIR=//p')
  host_env=$(printf '%s\n' "$facts_out" | sed -n 's/^env=HOST_STACKS_DIR=//p')
  [ "$stacks_env" = "$expected" ] ||
    violation "$file" "env STACKS_DIR resolves to '${stacks_env:-unset}', want '$expected', so it is not derived from STACKS_DIR ($label)"
  [ "$host_env" = "$expected" ] ||
    violation "$file" "env HOST_STACKS_DIR resolves to '${host_env:-unset}', want '$expected' (it must default to STACKS_DIR) ($label)"

  if ! command grep -qxF "mount=$expected|$expected" <<<"$facts_out"; then
    violation "$file" "no stacks mount with source == target == STACKS_DIR '$expected' ($label); mounts: $(printf '%s\n' "$facts_out" | sed -n 's/^mount=//p' | tr '\n' ' ')"
  fi

  CHECK_ONE_KEYS=$(printf '%s\n' "$facts_out" | sed -n 's/^env=\([^=]*\)=.*/\1/p' | sort -u)
  return 0
}

check_dir() {
  local dir="$1" f
  [ -d "$dir" ] || { echo "check-compose-parity: $dir is not a directory" >&2; return 2; }
  for f in "$DEV_FILE" "$PROD_FILE"; do
    [ -f "$dir/$f" ] || { echo "check-compose-parity: $dir/$f not found" >&2; return 2; }
  done

  if ! docker compose version >/dev/null 2>&1; then
    echo "check-compose-parity: docker compose plugin not found: this check needs it (\`docker compose version\` failed; it does not fall back to a text parse and does not skip)"
    return 1
  fi

  local proj rc
  proj=$(mktemp -d) || { echo "check-compose-parity: could not create a temp directory" >&2; return 2; }
  cp "$dir/$DEV_FILE" "$dir/$PROD_FILE" "$proj/" || { rm -rf "$proj"; return 2; }
  mkdir -p "$proj/backend"
  : > "$proj/.env"
  : > "$proj/backend/.env"

  compare_files "$proj"
  rc=$?
  rm -rf "$proj"
  return "$rc"
}

# compare_files PROJECT_DIR runs every assertion; PROJECT_DIR holds both files
# and the empty env_file stubs.
compare_files() {
  local proj="$1"
  VIOLATIONS=0
  local dev_keys="" prod_keys=""
  check_one "$proj" "$DEV_FILE" "" "/opt/stacks";            dev_keys=$CHECK_ONE_KEYS
  check_one "$proj" "$DEV_FILE" "$OVERRIDE_STACKS_DIR" "$OVERRIDE_STACKS_DIR"
  check_one "$proj" "$PROD_FILE" "" "/opt/stacks";           prod_keys=$CHECK_ONE_KEYS
  check_one "$proj" "$PROD_FILE" "$OVERRIDE_STACKS_DIR" "$OVERRIDE_STACKS_DIR"

  # Env key sets, from the default run of each file.
  local key entry ekey efile ereason only_dev only_prod allowed
  only_dev=$(comm -23 <(printf '%s\n' "$dev_keys") <(printf '%s\n' "$prod_keys"))
  only_prod=$(comm -13 <(printf '%s\n' "$dev_keys") <(printf '%s\n' "$prod_keys"))

  for key in $only_dev; do
    allowed=0
    for entry in "${INTENDED_ENV_DIFF[@]}"; do
      IFS='|' read -r ekey efile ereason <<<"$entry"
      [ "$ekey" = "$key" ] && [ "$efile" = "$DEV_FILE" ] && allowed=1
    done
    [ "$allowed" -eq 1 ] || violation "$DEV_FILE" "env key $key is set here and not in $PROD_FILE (add it to both, or list it in INTENDED_ENV_DIFF with a reason)"
  done
  for key in $only_prod; do
    allowed=0
    for entry in "${INTENDED_ENV_DIFF[@]}"; do
      IFS='|' read -r ekey efile ereason <<<"$entry"
      [ "$ekey" = "$key" ] && [ "$efile" = "$PROD_FILE" ] && allowed=1
    done
    [ "$allowed" -eq 1 ] || violation "$PROD_FILE" "env key $key is set here and not in $DEV_FILE (add it to both, or list it in INTENDED_ENV_DIFF with a reason)"
  done
  for entry in "${INTENDED_ENV_DIFF[@]}"; do
    IFS='|' read -r ekey efile ereason <<<"$entry"
    local in_dev in_prod
    in_dev=$(printf '%s\n' "$dev_keys" | command grep -cxF "$ekey")
    in_prod=$(printf '%s\n' "$prod_keys" | command grep -cxF "$ekey")
    if { [ "$efile" = "$DEV_FILE" ] && { [ "$in_dev" -ne 1 ] || [ "$in_prod" -ne 0 ]; }; } ||
       { [ "$efile" = "$PROD_FILE" ] && { [ "$in_prod" -ne 1 ] || [ "$in_dev" -ne 0 ]; }; }; then
      violation "$efile" "INTENDED_ENV_DIFF lists $ekey as only in $efile, but it no longer differs (dev: $in_dev, prod: $in_prod); drop the entry"
    fi
  done

  if [ "$VIOLATIONS" -ne 0 ]; then
    echo "check-compose-parity: $VIOLATIONS violation(s); dev and prod compose must agree on init, the identical-path stacks mount and env keys (safe-defaults rule 17)"
    return 1
  fi
  echo "check-compose-parity: OK ($(docker compose version --short 2>/dev/null | sed 's/^/compose /'); 2 files x 2 STACKS_DIR settings; intended env differences: $(printf '%s ' "${INTENDED_ENV_DIFF[@]%%|*}"))"
  return 0
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX DIR [PATH_PREFIX]
selftest_case() {
  local name="$1" want="$2" pat="$3" dir="$4" pathprefix="${5:-}" out status
  ST_RUN=$((ST_RUN + 1))
  if [ -n "$pathprefix" ]; then
    out=$(PATH="$pathprefix:$PATH" check_dir "$dir" 2>&1)
  else
    out=$(check_dir "$dir" 2>&1)
  fi
  status=$?
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: compose-parity self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# fresh_copy prints a new fixture dir holding the real compose files.
fresh_copy() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  cp "$REPO_ROOT/$DEV_FILE" "$REPO_ROOT/$PROD_FILE" "$d/"
  echo "$d"
}

# mutate FILE OLD NEW replaces one literal string, and fails the self-test
# itself when OLD is absent: a mutation that did not apply would make its red
# case a vacuous pass of the green path.
mutate() {
  local file="$1" old="$2" new="$3" content
  content=$(cat "$file"; echo x); content=${content%x}
  if [[ "$content" != *"$old"* ]]; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: compose-parity self-test - mutation target not found in $file: $old"
    return 1
  fi
  printf '%s' "${content/"$old"/"$new"}" > "$file"
}

selftest() {
  command -v docker >/dev/null 2>&1 || { echo "FAIL: compose-parity self-test - no docker CLI"; return 2; }
  ST_TMP=$(mktemp -d) || { echo "FAIL: compose-parity self-test - could not create a temp directory"; return 2; }
  selftest_cases
  local rc=$?
  rm -rf "$ST_TMP"
  return "$rc"
}

selftest_cases() {
  local d

  # GREEN: the two real files, copied untouched.
  d=$(fresh_copy); selftest_case "real files, unmodified" 0 "check-compose-parity: OK" "$d"

  # RED 1: init removed from prod.
  d=$(fresh_copy)
  mutate "$d/$PROD_FILE" $'    init: true\n' "" &&
    selftest_case "init removed from prod" 1 "docker-compose.prod.yaml: init is 'unset', want true" "$d"

  # RED 2: stacks mount with source != target. Green at the default, red only
  # under the STACKS_DIR override.
  d=$(fresh_copy)
  mutate "$d/$PROD_FILE" '- ${STACKS_DIR:-/opt/stacks}:${STACKS_DIR:-/opt/stacks}' '- ${STACKS_DIR:-/opt/stacks}:/opt/stacks' &&
    selftest_case "mount target hardcoded (red only under override)" 1 "docker-compose.prod.yaml: no stacks mount with source == target == STACKS_DIR '/srv/x'" "$d"

  # RED 2b: source hardcoded, so it is wrong at the default too.
  d=$(fresh_copy)
  mutate "$d/$PROD_FILE" '- ${STACKS_DIR:-/opt/stacks}:${STACKS_DIR:-/opt/stacks}' '- /host/stacks:/opt/stacks' &&
    selftest_case "mount source hardcoded to another path" 1 "docker-compose.prod.yaml: no stacks mount with source == target == STACKS_DIR '/opt/stacks'" "$d"

  # RED 3: an env key in one file only (dev), not on the intended list.
  d=$(fresh_copy)
  mutate "$d/$DEV_FILE" '      - TZ=${TZ:-UTC}' $'      - TZ=${TZ:-UTC}\n      - ONLY_IN_DEV=1' &&
    selftest_case "env key only in dev" 1 "docker-compose.yaml: env key ONLY_IN_DEV is set here and not in docker-compose.prod.yaml" "$d"

  # RED 3b: the intended difference stops differing; the list must shrink.
  d=$(fresh_copy)
  mutate "$d/$PROD_FILE" '      - TZ=${TZ:-UTC}' $'      - TZ=${TZ:-UTC}\n      - DATA_DIR=/app/data' &&
    selftest_case "intended difference no longer differs" 1 "INTENDED_ENV_DIFF lists DATA_DIR" "$d"

  # RED 4: the compose plugin is absent. A shim `docker` that fails every
  # call stands in for a runner without the plugin; the check must fail, not skip.
  d=$(fresh_copy)
  local shim="$ST_TMP/shim"
  mkdir -p "$shim"
  printf '#!/bin/sh\nexit 1\n' > "$shim/docker"
  chmod +x "$shim/docker"
  selftest_case "docker compose absent" 1 "docker compose plugin not found: this check needs it" "$d" "$shim"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: compose-parity self-test - $ST_FAILS control(s) failed (of $ST_RUN run)"
    return 1
  fi
  echo "compose-parity self-test: $ST_RUN control(s) passed (1 green, 6 red)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
