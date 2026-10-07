#!/usr/bin/env bash
# scripts/check-trusted-networks.sh
#
# ONE invariant (agent-os-qags.10, safe-defaults rule 12):
#
#   No template or doc recommends all of RFC 1918 (10.0.0.0/8, 172.16.0.0/12,
#   192.168.0.0/16) as TRUSTED_NETWORKS or AUTH_DISABLED_ALLOWED_NETWORKS.
#
# WHY. Network position is not identity. Trusting a whole private range means
# any host on the LAN, any other container on the Docker network, can be
# trusted as a reverse proxy (spoofing X-Forwarded-For) or skip authentication.
# agent-os-n4ca.5 narrowed every template and doc; this makes the broad value
# coming back a red CI run instead of a review find.
#
# WHAT IS A VIOLATION. A line that names either variable and also carries one
# of the three RFC 1918 ranges, in either order, COMMENTED OR NOT. Comments are
# deliberately included: people copy "# TRUSTED_NETWORKS=10.0.0.0/8" out of
# .env.example and uncomment it. The name-then-range arm uses `.*`, not
# `[^#]*`, so a range after a trailing # on the same line is caught too.
#
# WARNINGS. Prose that warns against the broad value must not put a variable
# name and a range on one line: write "never trust a whole private range" or
# put the name and the range on separate lines. There is no allow marker, on
# purpose: an escape hatch is how the broad value would come back.
#
# LIMIT. Same line only. A YAML block value or a continued line with the
# ranges on the line after the variable name is invisible to this check.
#
# SCOPE. docker-compose*.yaml, README.md, docs/ and every env template
# (.env.example, */.env.example, *.env.example at any depth: backend/.env.example
# is what the dev compose copies to backend/.env) under the scanned directory. A single narrow range (e.g. 172.18.0.0/16) is fine.
#
# USAGE
#   check-trusted-networks.sh              scan this repo
#   check-trusted-networks.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-trusted-networks.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# none of the scoped files, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

NAMES='(TRUSTED_NETWORKS|AUTH_DISABLED_ALLOWED_NETWORKS)'
RANGES='(10\.0\.0\.0/8|172\.16\.0\.0/12|192\.168\.0\.0/16)'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# env_templates DIR prints every env template under DIR, relative to DIR: the
# tracked ones when DIR is the root of a git tree (so node_modules is never
# walked), otherwise a find that prunes node_modules and .git. Both arms match
# .env.example, */.env.example and *.env.example at any depth.
env_templates() {
  local dir="$1" top
  top=$(git -C "$dir" rev-parse --show-toplevel 2>/dev/null)
  if [ -n "$top" ] && [ "$(cd "$top" && pwd -P)" = "$(cd "$dir" && pwd -P)" ]; then
    git -C "$dir" ls-files -- '.env.example' '*/.env.example' '*.env.example'
  else
    (cd "$dir" && command find . \( -name node_modules -o -name .git \) -prune -o \
      -type f \( -name '.env.example' -o -name '*.env.example' \) -print | sed 's|^\./||')
  fi
}

check_dir() {
  local dir="$1" f
  [ -d "$dir" ] || { echo "check-trusted-networks: $dir is not a directory" >&2; return 2; }

  local files=()
  for f in "$dir"/docker-compose*.yaml "$dir/README.md"; do
    [ -f "$f" ] && files+=("${f#"$dir"/}")
  done
  while IFS= read -r f; do
    [ -n "$f" ] && files+=("$f")
  done < <(env_templates "$dir")
  [ -d "$dir/docs" ] && files+=("docs")
  if [ "${#files[@]}" -eq 0 ]; then
    echo "check-trusted-networks: no scoped files under $dir" >&2
    return 2
  fi

  local out
  out=$(cd "$dir" && command grep -rnE -e "$NAMES.*$RANGES" -e "$RANGES.*$NAMES" \
    -- "${files[@]}" 2>&1)
  local status=$?
  case "$status" in
    0)
      echo "$out"
      echo "check-trusted-networks: all of RFC 1918 recommended as a trusted network (ship the narrow range, safe-defaults rule 12; to warn against the broad value in prose, do not put the variable name and a range on one line)"
      return 1
      ;;
    1) echo "check-trusted-networks: 0 violations"; return 0 ;;
    *) echo "check-trusted-networks: grep failed ($status): $out" >&2; return 2 ;;
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
    echo "FAIL: trusted-networks self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

write_base() {
  local d="$1"
  mkdir -p "$d/docs/how-to"
  cat > "$d/docker-compose.yaml" <<'EOF'
services:
  app:
    environment:
      - TRUSTED_NETWORKS=172.18.0.0/16,127.0.0.1,::1
EOF
  cat > "$d/.env.example" <<'EOF'
# TRUSTED_NETWORKS=172.18.0.0/16,127.0.0.1,::1
# AUTH_DISABLED_ALLOWED_NETWORKS=127.0.0.1,::1
EOF
  echo "Set TRUSTED_NETWORKS to your proxy's address." > "$d/README.md"
  echo "AUTH_DISABLED_ALLOWED_NETWORKS=192.168.1.10/32" > "$d/docs/how-to/deploy-production.md"
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: trusted-networks self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: narrow ranges and prose naming the variables with no range.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED: the 7c0a141 compose line.
  d=$(fresh)
  echo "      - TRUSTED_NETWORKS=172.16.0.0/12,10.0.0.0/8,192.168.0.0/16,127.0.0.1" >> "$d/docker-compose.yaml"
  selftest_case "broad value in compose" 1 "docker-compose.yaml:[0-9]+:" "$d"

  # RED: the acceptance line, commented.
  d=$(fresh)
  echo "# TRUSTED_NETWORKS=10.0.0.0/8" >> "$d/.env.example"
  selftest_case "commented example line" 1 "\.env\.example:[0-9]+:# TRUSTED_NETWORKS=10" "$d"

  # RED: AUTH_DISABLED_ALLOWED_NETWORKS, a single range of the three.
  d=$(fresh)
  echo "# AUTH_DISABLED_ALLOWED_NETWORKS=192.168.0.0/16" >> "$d/.env.example"
  selftest_case "AUTH_DISABLED_ALLOWED_NETWORKS" 1 "\.env\.example:[0-9]+:# AUTH_DISABLED" "$d"

  # RED: a range after a trailing # on the same line (the bead's [^#]* blind spot).
  d=$(fresh)
  echo "TRUSTED_NETWORKS=127.0.0.1 # or 172.16.0.0/12" >> "$d/.env.example"
  selftest_case "range after a trailing comment" 1 "\.env\.example:[0-9]+:TRUSTED_NETWORKS=127" "$d"

  # RED: range first, name second.
  d=$(fresh)
  echo "Use 10.0.0.0/8 for TRUSTED_NETWORKS on a LAN." >> "$d/README.md"
  selftest_case "range before name" 1 "README.md:[0-9]+:Use 10" "$d"

  # RED: a nested env template (backend/.env.example, which the dev compose copies).
  d=$(fresh)
  mkdir -p "$d/backend"
  echo "# TRUSTED_NETWORKS=172.18.0.0/16" > "$d/backend/.env.example"
  selftest_case "clean nested env template" 0 "0 violations" "$d"
  echo "# TRUSTED_NETWORKS=10.0.0.0/8" >> "$d/backend/.env.example"
  selftest_case "nested env template" 1 "backend/\.env\.example:[0-9]+:# TRUSTED_NETWORKS=10" "$d"

  # RED: a prefixed template name.
  d=$(fresh)
  echo "TRUSTED_NETWORKS=192.168.0.0/16" > "$d/prod.env.example"
  selftest_case "*.env.example" 1 "prod\.env\.example:[0-9]+:" "$d"

  # RED: a docs page below a subdirectory (the recursive arm).
  d=$(fresh)
  echo "TRUSTED_NETWORKS=192.168.0.0/16" >> "$d/docs/how-to/deploy-production.md"
  selftest_case "nested docs page" 1 "docs/how-to/deploy-production.md:[0-9]+:" "$d"

  # RED: docker-compose.prod.yaml is in scope through the glob.
  d=$(fresh)
  echo "      - TRUSTED_NETWORKS=10.0.0.0/8" > "$d/docker-compose.prod.yaml"
  selftest_case "docker-compose.prod.yaml" 1 "docker-compose.prod.yaml:[0-9]+:" "$d"

  # ERROR: a tree with no scoped file is not a clean tree.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "empty tree" 2 "no scoped files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: trusted-networks self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "trusted-networks self-test: $ST_RUN control(s) passed (2 green, 9 red, 1 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
