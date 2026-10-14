#!/usr/bin/env bash
# scripts/ecr-public-login.sh
#
# Logs the job's Docker client in to public.ecr.aws so image pulls are
# AUTHENTICATED (agent-os-rcbn, Edwin's decision D102). Anonymous pulls from
# ECR Public share a per-IP data cap, and GitHub-hosted runners share IPs: on
# 2026-10-14 the Trivy image scan and the Playwright backup-flow suite failed
# with `toomanyrequests: Data limit exceeded` with nothing under test wrong.
# An authenticated pull gets the higher per-account allowance.
#
# Credentials come from the environment, set by the workflow step from the
# repo secrets AWS_ECR_PUBLIC_ACCESS_KEY_ID / AWS_ECR_PUBLIC_SECRET_ACCESS_KEY
# (one-time setup: CONTRIBUTING.md, "ECR Public login"):
#
#   AWS_ACCESS_KEY_ID  AWS_SECRET_ACCESS_KEY
#
# It never fails the job. With no credentials (a fork PR, whose runs get no
# secrets, or before the secrets exist) it says so and the job pulls
# anonymously, exactly as before. The same goes for a missing aws CLI, a
# rejected key or a failed `docker login`: a ::warning:: and an anonymous
# pull, because a login problem must not turn an unrelated PR red.
#
# The token goes straight from `aws` into `docker login --password-stdin`.
# It is never echoed, never put on a command line, and is masked in the log
# (::add-mask::) in case anything later prints it. No `set -x`.
#
# `docker login` writes ~/.docker/config.json, which both the daemon pulls
# (`docker pull`, `docker compose up`) and buildx's docker-container builder
# read for registry credentials, so run this before the first pull or build.
# scripts/check-dockerhub-pulls.sh enforces that order (rule 2e).
#
# USAGE
#   ecr-public-login.sh              log in (or skip, see above)
#   ecr-public-login.sh --self-test  prove each outcome with fake aws/docker

set -uo pipefail

REGISTRY="public.ecr.aws"
# ECR Public's API exists only in us-east-1, wherever the runner is.
REGION="us-east-1"

login() {
  if [ -z "${AWS_ACCESS_KEY_ID:-}" ] || [ -z "${AWS_SECRET_ACCESS_KEY:-}" ]; then
    echo "ecr-public-login: no credentials (fork PR, or the AWS_ECR_PUBLIC_* secrets are not set); pulling anonymously"
    return 0
  fi
  if ! command -v aws >/dev/null 2>&1; then
    echo "::warning::ecr-public-login: aws CLI not found on this runner; pulling anonymously"
    return 0
  fi
  local pw err
  err=$(mktemp) || { echo "::warning::ecr-public-login: no temp file; pulling anonymously"; return 0; }
  if ! pw=$(aws ecr-public get-login-password --region "$REGION" 2>"$err") || [ -z "$pw" ]; then
    echo "::warning::ecr-public-login: could not get a login token ($(head -c 300 "$err" | tr '\n' ' ')); pulling anonymously"
    rm -f "$err"
    return 0
  fi
  rm -f "$err"
  echo "::add-mask::$pw"
  if ! printf '%s' "$pw" | docker login --username AWS --password-stdin "$REGISTRY" >/dev/null 2>&1; then
    echo "::warning::ecr-public-login: docker login $REGISTRY failed; pulling anonymously"
    return 0
  fi
  # Presence only, never the value.
  if command grep -q "\"$REGISTRY\"" "${DOCKER_CONFIG:-$HOME/.docker}/config.json" 2>/dev/null; then
    echo "ecr-public-login: logged in to $REGISTRY (credential entry present in docker config); pulls are authenticated"
  else
    echo "::warning::ecr-public-login: docker login succeeded but no $REGISTRY entry is in the docker config; pulls may be anonymous"
  fi
  return 0
}

ST_RUN=0
ST_FAILS=0

# st_case NAME WANT_STDOUT_REGEX WANT_DOCKER_CALLS ENV...  (runs the script in a sandbox)
st_case() {
  local name="$1" pat="$2" want_docker="$3"; shift 3
  local out status calls unmasked
  rm -f "$ST_DIR/docker.calls" "$ST_DIR/docker.stdin" "$ST_DIR/home/.docker/config.json"
  ST_RUN=$((ST_RUN + 1))
  out=$(env -i PATH="$ST_PATH" HOME="$ST_DIR/home" FAKE_DIR="$ST_DIR" "$@" bash "$ST_SELF" 2>&1)
  status=$?
  calls=0
  [ -f "$ST_DIR/docker.calls" ] && calls=$(wc -l < "$ST_DIR/docker.calls" | tr -d ' ')
  if [ "$status" -ne 0 ] || ! command grep -qE -- "$pat" <<<"$out" || [ "$calls" != "$want_docker" ]; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: ecr-public-login self-test \"$name\": want exit 0, /$pat/, $want_docker docker call(s); got exit $status, $calls call(s):"
    echo "$out"
  fi
  # The token must never reach the log, whatever the outcome.
  unmasked=$(command grep -v '::add-mask::' <<<"$out")
  if command grep -q "SECRETTOKEN" <<<"$unmasked"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: ecr-public-login self-test \"$name\": the token is printed outside ::add-mask::"
  fi
}

selftest() {
  ST_DIR=$(mktemp -d) || { echo "FAIL: ecr-public-login self-test - no temp dir"; return 2; }
  trap 'rm -rf "$ST_DIR"' RETURN
  ST_SELF="$ST_DIR/ecr-public-login.sh"
  cp "${BASH_SOURCE[0]}" "$ST_SELF"
  mkdir -p "$ST_DIR/bin" "$ST_DIR/bare" "$ST_DIR/home/.docker"
  # The sandbox PATH holds only fakes plus the few tools the script calls,
  # so a real aws or docker on the host can never answer.
  local t
  for t in bash env grep head tr rm mktemp cat wc printf; do
    command -v "$t" >/dev/null 2>&1 && ln -s "$(command -v "$t")" "$ST_DIR/bin/$t" 2>/dev/null
    command -v "$t" >/dev/null 2>&1 && ln -s "$(command -v "$t")" "$ST_DIR/bare/$t" 2>/dev/null
  done
  cat > "$ST_DIR/bin/aws" <<'EOF'
#!/bin/bash
echo "aws $*" >> "$FAKE_DIR/aws.calls"
[ -n "${FAKE_AWS_FAIL:-}" ] && { echo "An error occurred (InvalidClientTokenId)" >&2; exit 255; }
echo "SECRETTOKEN"
EOF
  cat > "$ST_DIR/bin/docker" <<'EOF'
#!/bin/bash
echo "docker $*" >> "$FAKE_DIR/docker.calls"
cat > "$FAKE_DIR/docker.stdin"
[ -n "${FAKE_DOCKER_FAIL:-}" ] && exit 1
[ -n "${FAKE_NO_CONFIG:-}" ] || echo '{"auths":{"public.ecr.aws":{"auth":"x"}}}' > "$HOME/.docker/config.json"
echo "Login Succeeded"
EOF
  chmod +x "$ST_DIR/bin/aws" "$ST_DIR/bin/docker"
  ST_PATH="$ST_DIR/bin"

  st_case "no credentials: skip, no docker call" "pulling anonymously" 0
  st_case "only the key id set: still skip" "pulling anonymously" 0 AWS_ACCESS_KEY_ID=AKIAFAKE
  st_case "credentials: logs in, authenticated" "pulls are authenticated" 1 AWS_ACCESS_KEY_ID=AKIAFAKE AWS_SECRET_ACCESS_KEY=fake
  if ! command grep -q "docker login --username AWS --password-stdin public.ecr.aws" "$ST_DIR/docker.calls" \
     || [ "$(cat "$ST_DIR/docker.stdin")" != "SECRETTOKEN" ]; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: ecr-public-login self-test: docker login was not called with --password-stdin and the token on stdin:"
    cat "$ST_DIR/docker.calls" "$ST_DIR/docker.stdin" 2>&1
  fi
  st_case "aws fails: warning, anonymous, no docker call" "::warning::.*could not get a login token.*InvalidClientTokenId" 0 AWS_ACCESS_KEY_ID=AKIAFAKE AWS_SECRET_ACCESS_KEY=fake FAKE_AWS_FAIL=1
  st_case "docker login fails: warning, anonymous" "::warning::.*docker login public.ecr.aws failed" 1 AWS_ACCESS_KEY_ID=AKIAFAKE AWS_SECRET_ACCESS_KEY=fake FAKE_DOCKER_FAIL=1
  st_case "login ok but no config entry: warning" "::warning::.*no public.ecr.aws entry" 1 AWS_ACCESS_KEY_ID=AKIAFAKE AWS_SECRET_ACCESS_KEY=fake FAKE_NO_CONFIG=1
  ST_PATH="$ST_DIR/bare"
  st_case "aws missing: warning, anonymous, no docker call" "::warning::.*aws CLI not found" 0 AWS_ACCESS_KEY_ID=AKIAFAKE AWS_SECRET_ACCESS_KEY=fake

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "ecr-public-login self-test: $ST_FAILS failure(s) in $ST_RUN cases"
    return 1
  fi
  echo "ecr-public-login self-test: $ST_RUN cases passed"
  return 0
}

case "${1:-}" in
  --self-test) selftest; exit $? ;;
  "") login; exit 0 ;;
  *) echo "usage: $(basename "$0") [--self-test]" >&2; exit 2 ;;
esac
