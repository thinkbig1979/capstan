#!/usr/bin/env bash
# scripts/check-toolchain-lockstep.sh
#
# Fails when two pins that must carry one version do not. Three groups:
#
# 1. Go release (CI tests one, the image ships one):
#
#   docker/Dockerfile                  FROM ... golang:<v>-...   (ships)
#   backend/go.mod                     toolchain go<v>           (CI tests)
#   backend/tools/geterrors/go.mod     toolchain go<v>           (vettool; its
#                                      header explains why it must match)
#
# 2. react-doctor release:
#
#   frontend/package.json              "react-doctor": "<v>"     (local `doctor`
#                                      script, resolved by the lockfile)
#   .github/workflows/react-doctor.yml with: version: "<v>"      (what CI runs;
#                                      NOT the `uses: ...@<sha> # v2.2.9` line,
#                                      which pins the action, a different thing)
#
# 3. restic release and its linux/amd64 archive checksum:
#
#   docker/Dockerfile                  ARG RESTIC_VER=<v>, ARG RESTIC_SHA256_AMD64=<sha>
#                                      (ships)
#   .github/workflows/e2e-backup.yml   RESTIC_VER=<v>, RESTIC_SHA256=<sha>
#                                      (the restic the backup e2e drives)
#
# rclone is deliberately absent: docker/Dockerfile (ARG RCLONE_VER) is its only
# real pin. Every other mention of its version is a comment, and a guard over
# comments would be a doc-drift check, not a pin check.
#
# WHY: a Dependabot docker PR bumps the Dockerfile alone and cannot know about
# the other pins. For Go, CI then tests one standard library, the shipped
# binary is compiled against another, and govulncheck evaluates the one that
# does not ship (agent-os-k4ad, after agent-os-bmw and agent-os-vkr). For the
# other two, comments in react-doctor.yml and e2e-backup.yml asked reviewers
# to keep the pins level, and nothing enforced it (agent-os-qags.7).
#
# Absence is an error, never a pass: a pin that cannot be read (renamed
# directive, reworded FROM line, reindented `version:` key) would otherwise
# compare two empty strings and report green over a guard that stopped looking.
#
# Usage:
#   check-toolchain-lockstep.sh                    check the repo this script
#                                                  lives in
#   check-toolchain-lockstep.sh DOCKERFILE GOMOD VETMOD [PKGJSON RDWORKFLOW E2EWORKFLOW]
#                                                  check these files (used to
#                                                  prove RED); omitted ones
#                                                  default to the repo's own
#
# Exit: 0 all pins agree, 1 some differ (both values printed),
#       2 a pin could not be read.

set -u

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dockerfile="${1:-$root/docker/Dockerfile}"
gomod="${2:-$root/backend/go.mod}"
vetmod="${3:-$root/backend/tools/geterrors/go.mod}"
pkgjson="${4:-$root/frontend/package.json}"
rdworkflow="${5:-$root/.github/workflows/react-doctor.yml}"
e2eworkflow="${6:-$root/.github/workflows/e2e-backup.yml}"

# Every golang builder stage must agree with the others too; collect them all.
docker_versions="$(sed -nE 's/^FROM[[:space:]].*golang:([0-9]+\.[0-9]+(\.[0-9]+)?)[-[:space:]].*/\1/p' "$dockerfile" | sort -u)"
gomod_version="$(sed -nE 's/^toolchain go([0-9]+\.[0-9]+(\.[0-9]+)?)[[:space:]]*$/\1/p' "$gomod")"
vetmod_version="$(sed -nE 's/^toolchain go([0-9]+\.[0-9]+(\.[0-9]+)?)[[:space:]]*$/\1/p' "$vetmod")"

# The key must be exactly "react-doctor" (the `"doctor": "react-doctor"` script
# line must not match), and the value a plain version, not a range.
pkg_rd_version="$(sed -nE 's/^[[:space:]]*"react-doctor":[[:space:]]*"([0-9][^"]*)".*/\1/p' "$pkgjson" | sort -u)"
# `version:` as the action input; `node-version:` has a different key.
wf_rd_version="$(sed -nE 's/^[[:space:]]*version:[[:space:]]*"?([0-9][0-9A-Za-z.+-]*)"?[[:space:]]*(#.*)?$/\1/p' "$rdworkflow" | sort -u)"

dock_restic_version="$(sed -nE 's/^ARG RESTIC_VER=([0-9][0-9A-Za-z.+-]*)[[:space:]]*$/\1/p' "$dockerfile" | sort -u)"
dock_restic_sha="$(sed -nE 's/^ARG RESTIC_SHA256_AMD64=([0-9a-f]{64})[[:space:]]*$/\1/p' "$dockerfile" | sort -u)"
e2e_restic_version="$(sed -nE 's/^[[:space:]]*RESTIC_VER=([0-9][0-9A-Za-z.+-]*)[[:space:]]*$/\1/p' "$e2eworkflow" | sort -u)"
e2e_restic_sha="$(sed -nE 's/^[[:space:]]*RESTIC_SHA256=([0-9a-f]{64})[[:space:]]*$/\1/p' "$e2eworkflow" | sort -u)"

missing=0
need() { # need VALUE "what" FILE
  [ -n "$1" ] || { echo "check-toolchain-lockstep: no $2 found in $3" >&2; missing=1; }
}
need "$docker_versions" "'FROM ... golang:<version>-' line" "$dockerfile"
need "$gomod_version" "'toolchain go<version>' line" "$gomod"
need "$vetmod_version" "'toolchain go<version>' line" "$vetmod"
need "$pkg_rd_version" "'\"react-doctor\": \"<version>\"' entry" "$pkgjson"
need "$wf_rd_version" "'version: \"<version>\"' action input" "$rdworkflow"
need "$dock_restic_version" "'ARG RESTIC_VER=<version>' line" "$dockerfile"
need "$dock_restic_sha" "'ARG RESTIC_SHA256_AMD64=<64 hex>' line" "$dockerfile"
need "$e2e_restic_version" "'RESTIC_VER=<version>' line" "$e2eworkflow"
need "$e2e_restic_sha" "'RESTIC_SHA256=<64 hex>' line" "$e2eworkflow"
[ "$missing" -eq 0 ] || exit 2

echo "Dockerfile golang tag:      $(echo "$docker_versions" | tr '\n' ' ')($dockerfile)"
echo "backend go.mod toolchain:   go$gomod_version ($gomod)"
echo "geterrors go.mod toolchain: go$vetmod_version ($vetmod)"
echo "package.json react-doctor:  $(echo "$pkg_rd_version" | tr '\n' ' ')($pkgjson)"
echo "react-doctor.yml version:   $(echo "$wf_rd_version" | tr '\n' ' ')($rdworkflow)"
echo "Dockerfile RESTIC_VER:      $(echo "$dock_restic_version" | tr '\n' ' ')($dockerfile)"
echo "e2e-backup.yml RESTIC_VER:  $(echo "$e2e_restic_version" | tr '\n' ' ')($e2eworkflow)"
echo "Dockerfile restic sha amd64: $dock_restic_sha"
echo "e2e-backup.yml restic sha:   $e2e_restic_sha"

status=0

if [ "$docker_versions" = "$gomod_version" ] && [ "$gomod_version" = "$vetmod_version" ]; then
  echo "OK: Go $gomod_version everywhere"
else
  echo "FAIL: the Go builder image and the go.mod toolchain pins differ." >&2
  echo "Bump docker/Dockerfile, backend/go.mod and backend/tools/geterrors/go.mod to one version in the same PR." >&2
  status=1
fi

if [ "$pkg_rd_version" = "$wf_rd_version" ]; then
  echo "OK: react-doctor $pkg_rd_version in package.json and react-doctor.yml"
else
  echo "FAIL: react-doctor differs: package.json '$(echo "$pkg_rd_version" | paste -sd, -)' vs react-doctor.yml '$(echo "$wf_rd_version" | paste -sd, -)'." >&2
  echo "Bump frontend/package.json (and its lockfile) and the version: input in .github/workflows/react-doctor.yml in the same PR." >&2
  status=1
fi

if [ "$dock_restic_version" = "$e2e_restic_version" ]; then
  echo "OK: restic $dock_restic_version in the Dockerfile and e2e-backup.yml"
else
  echo "FAIL: restic version differs: Dockerfile '$(echo "$dock_restic_version" | paste -sd, -)' vs e2e-backup.yml '$(echo "$e2e_restic_version" | paste -sd, -)'." >&2
  echo "Bump RESTIC_VER and the amd64 checksum in docker/Dockerfile and .github/workflows/e2e-backup.yml in the same PR." >&2
  status=1
fi

if [ "$dock_restic_sha" = "$e2e_restic_sha" ]; then
  echo "OK: restic amd64 checksum agrees"
else
  echo "FAIL: restic amd64 checksum differs between docker/Dockerfile (RESTIC_SHA256_AMD64) and .github/workflows/e2e-backup.yml (RESTIC_SHA256)." >&2
  status=1
fi

exit "$status"
