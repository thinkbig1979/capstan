#!/usr/bin/env bash
# scripts/check-toolchain-lockstep.sh
#
# Fails when the Go release that CI tests and the Go release that ships are
# not the same one. Three pins must carry one version:
#
#   docker/Dockerfile                  FROM ... golang:<v>-...   (ships)
#   backend/go.mod                     toolchain go<v>           (CI tests)
#   backend/tools/geterrors/go.mod     toolchain go<v>           (vettool; its
#                                      header explains why it must match)
#
# WHY: a Dependabot docker PR bumps the Dockerfile alone and cannot know about
# the other two. CI then tests one standard library, the shipped binary is
# compiled against another, and govulncheck evaluates the one that does not
# ship (agent-os-k4ad, after agent-os-bmw and agent-os-vkr). Nothing
# enforced the pairing before this script; a comment in
# .github/dependabot.yml asked reviewers to remember it.
#
# Absence is an error, never a pass: a pin that cannot be read (renamed
# directive, reworded FROM line) would otherwise compare two empty strings
# and report green over a guard that stopped looking.
#
# Usage:
#   check-toolchain-lockstep.sh                           check the repo this
#                                                         script lives in
#   check-toolchain-lockstep.sh DOCKERFILE GOMOD VETMOD   check these files
#                                                         (used to prove RED)
#
# Exit: 0 all three agree, 1 they differ (both values printed),
#       2 a pin could not be read.

set -u

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dockerfile="${1:-$root/docker/Dockerfile}"
gomod="${2:-$root/backend/go.mod}"
vetmod="${3:-$root/backend/tools/geterrors/go.mod}"

# Every golang builder stage must agree with the others too; collect them all.
docker_versions="$(sed -nE 's/^FROM[[:space:]].*golang:([0-9]+\.[0-9]+(\.[0-9]+)?)[-[:space:]].*/\1/p' "$dockerfile" | sort -u)"
gomod_version="$(sed -nE 's/^toolchain go([0-9]+\.[0-9]+(\.[0-9]+)?)[[:space:]]*$/\1/p' "$gomod")"
vetmod_version="$(sed -nE 's/^toolchain go([0-9]+\.[0-9]+(\.[0-9]+)?)[[:space:]]*$/\1/p' "$vetmod")"

missing=0
[ -n "$docker_versions" ] || { echo "check-toolchain-lockstep: no 'FROM ... golang:<version>-' line found in $dockerfile" >&2; missing=1; }
[ -n "$gomod_version" ] || { echo "check-toolchain-lockstep: no 'toolchain go<version>' line found in $gomod" >&2; missing=1; }
[ -n "$vetmod_version" ] || { echo "check-toolchain-lockstep: no 'toolchain go<version>' line found in $vetmod" >&2; missing=1; }
[ "$missing" -eq 0 ] || exit 2

echo "Dockerfile golang tag:      $(echo "$docker_versions" | tr '\n' ' ')($dockerfile)"
echo "backend go.mod toolchain:   go$gomod_version ($gomod)"
echo "geterrors go.mod toolchain: go$vetmod_version ($vetmod)"

if [ "$docker_versions" = "$gomod_version" ] && [ "$gomod_version" = "$vetmod_version" ]; then
  echo "OK: Go $gomod_version everywhere"
  exit 0
fi

echo "FAIL: the Go builder image and the go.mod toolchain pins differ." >&2
echo "Bump docker/Dockerfile, backend/go.mod and backend/tools/geterrors/go.mod to one version in the same PR." >&2
exit 1
