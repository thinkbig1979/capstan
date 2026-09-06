#!/usr/bin/env bash
# scripts/check-trivy-coverage.sh
#
# Fails when a Trivy image-scan report does not contain a Results entry for
# the Capstan server binary (`app/server`) or for the runtime base's OS
# packages (`Class == "os-pkgs"`).
#
# WHY: Trivy's `format: table` omits a target that produced no rows, so a
# target's ABSENCE from the table is ambiguous between "scanned, found
# nothing" and "never scanned at all". The image-scan job in
# .github/workflows/security.yml is advisory (`exit-code: '0'`), so it is
# green either way, and a reader takes the missing section as good news. That
# is the failure this guards: a Dockerfile change that stops Trivy resolving
# the Go binary (a stripped/packed build, a different COPY target path) or
# swaps the runtime base for one whose package DB Trivy cannot read would
# REMOVE findings from the report and look like an improvement.
#
# OBSERVED 2026-09-06 on main bbfeaa2 with trivy 0.70.0: both targets ARE
# present, each with zero vulnerabilities at HIGH,CRITICAL with
# ignore-unfixed. That hand measurement is what this script replaces — it goes
# stale the next time the base image or the COPY moves; an assertion does not.
#
# `--format json` is the instrument on purpose: it emits a Results entry per
# target scanned, INCLUDING empty ones, which is the only way to tell the two
# readings apart. Do not "simplify" this to grepping trivy.txt.
#
# Deliberately says nothing about vulnerability COUNTS. This is a coverage
# assertion, not a vulnerability gate; the vulnerability gate stays advisory
# by Edwin's decision (agent-os-h1hr, 2026-09-06). Adding a count threshold
# here would graduate that gate by the side door.
#
# Usage:
#   check-trivy-coverage.sh REPORT.json     assert coverage in that report
#   check-trivy-coverage.sh --self-test     prove the assertion fires
#
# Exit: 0 coverage present, 1 a required target is missing, 2 usage or
#       environment error (no file, no jq, unparseable JSON).

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
export REPO_ROOT

# The Capstan binary, as Trivy names it: the in-image path without its leading
# slash. Dockerfile: `COPY --from=backend-build /app/server /app/`.
readonly SERVER_TARGET='app/server'

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") REPORT.json
       $(basename "$0") --self-test

Asserts that a Trivy JSON image-scan report covers both the Capstan server
binary ('${SERVER_TARGET}') and the runtime base's OS packages
(a Results entry with Class == "os-pkgs").

An absent target means the scan never looked, which a table-format report
cannot distinguish from "looked and found nothing".
USAGE
}

die() {
  echo "check-trivy-coverage: $*" >&2
  exit 2
}

# Prints one "Target<TAB>Class<TAB>Type<TAB>N" line per scanned target.
inventory() {
  jq -r '.Results[] | [.Target, .Class, .Type, (.Vulnerabilities | length)] | @tsv' "$1"
}

check_report() {
  local report="$1"

  [ -f "$report" ] || die "report not found: $report"
  [ -s "$report" ] || die "report is empty: $report — a broken scan, not a clean one"

  jq -e . "$report" >/dev/null 2>&1 || die "report is not valid JSON: $report"

  # A report with no Results key at all is a broken scan, not a clean one, and
  # must not be allowed to read as "no targets were required".
  local has_results
  has_results="$(jq -r 'has("Results") and (.Results | type == "array")' "$report")"
  [ "$has_results" = "true" ] || die "report has no Results array: $report"

  local n_results
  n_results="$(jq -r '.Results | length' "$report")"
  if [ "$n_results" -eq 0 ]; then
    echo "FAIL: ${report} contains zero scanned targets." >&2
    echo "      Trivy scanned nothing. Treat this as a broken scan." >&2
    return 1
  fi

  local has_server has_os
  has_server="$(jq -r --arg t "$SERVER_TARGET" '[.Results[] | select(.Target == $t)] | length' "$report")"
  has_os="$(jq -r '[.Results[] | select(.Class == "os-pkgs")] | length' "$report")"

  local missing=0
  if [ "$has_server" -eq 0 ]; then
    echo "FAIL: no Results entry for '${SERVER_TARGET}' in ${report}." >&2
    echo "      The Capstan server binary was NOT scanned. Either the COPY" >&2
    echo "      target moved in docker/Dockerfile, or Trivy can no longer" >&2
    echo "      resolve the Go binary's module graph." >&2
    missing=1
  fi
  if [ "$has_os" -eq 0 ]; then
    echo "FAIL: no Results entry with Class == \"os-pkgs\" in ${report}." >&2
    echo "      The runtime base's OS packages were NOT scanned. Either the" >&2
    echo "      base image changed to one Trivy has no package analyzer for," >&2
    echo "      or the package database was removed from the final layer." >&2
    missing=1
  fi

  echo "targets scanned (Target/Class/Type/findings):"
  inventory "$report"

  if [ "$missing" -ne 0 ]; then
    echo >&2
    echo "A target missing from the report is NOT the same as a target with no" >&2
    echo "findings. See scripts/check-trivy-coverage.sh's header." >&2
    return 1
  fi

  echo "OK: ${report} covers '${SERVER_TARGET}' and an os-pkgs target (${n_results} targets scanned)."
  return 0
}

# --- self-test -------------------------------------------------------------
# A gate only ever observed green has not been shown to be a gate. These arms
# run in CI on every scan, so the RED direction is proven on the same commit
# the GREEN one is claimed on, not once by hand in a close reason.

self_test() {
  local tmp rc failures=0
  tmp="$(mktemp -d)" || die "mktemp failed"
  # shellcheck disable=SC2064  # expand $tmp now, deliberately
  trap "rm -rf '$tmp'" EXIT

  # A minimally realistic complete report: the two required targets plus one
  # extra gobinary, mirroring the real shape (an os-pkgs entry with zero
  # findings and an app/server entry with zero findings both appear).
  cat >"$tmp/complete.json" <<'JSON'
{
  "SchemaVersion": 2,
  "ArtifactName": "capstan:scan",
  "Results": [
    { "Target": "capstan:scan (debian 13.6)", "Class": "os-pkgs", "Type": "debian" },
    { "Target": "app/server", "Class": "lang-pkgs", "Type": "gobinary" },
    { "Target": "usr/local/bin/rclone", "Class": "lang-pkgs", "Type": "gobinary",
      "Vulnerabilities": [ { "VulnerabilityID": "CVE-0000-0000" } ] }
  ]
}
JSON

  jq 'del(.Results[] | select(.Target == "app/server"))' "$tmp/complete.json" >"$tmp/no-server.json"
  jq 'del(.Results[] | select(.Class == "os-pkgs"))' "$tmp/complete.json" >"$tmp/no-os.json"
  jq '.Results = []' "$tmp/complete.json" >"$tmp/no-targets.json"
  jq 'del(.Results)' "$tmp/complete.json" >"$tmp/no-results-key.json"

  arm() {
    local label="$1" file="$2" want="$3"
    # Subshell, not a plain call: check_report's environment-error path uses
    # `die`, which EXITS. Called directly it would take the self-test down with
    # it after the first exit-2 arm instead of scoring it.
    ( check_report "$file" ) >/dev/null 2>&1
    rc=$?
    if [ "$rc" -eq "$want" ]; then
      echo "  ok   ${label}: exit ${rc} (expected ${want})"
    else
      echo "  FAIL ${label}: exit ${rc}, expected ${want}"
      failures=$((failures + 1))
    fi
  }

  echo "self-test:"
  arm 'complete report is accepted'            "$tmp/complete.json"       0
  arm 'app/server removed is rejected'         "$tmp/no-server.json"      1
  arm 'os-pkgs target removed is rejected'     "$tmp/no-os.json"          1
  arm 'empty Results array is rejected'        "$tmp/no-targets.json"     1
  arm 'missing Results key is an env error'    "$tmp/no-results-key.json" 2
  arm 'nonexistent file is an env error'       "$tmp/does-not-exist.json" 2

  if [ "$failures" -ne 0 ]; then
    echo "self-test: ${failures} arm(s) failed — the coverage assertion is not trustworthy." >&2
    return 1
  fi
  echo "self-test: 6 arm(s) passed (both directions proven)."
  return 0
}

main() {
  command -v jq >/dev/null 2>&1 || die "jq is required and was not found on PATH"

  case "${1:-}" in
    ''|-h|--help)
      usage
      [ -z "${1:-}" ] && exit 2
      exit 0
      ;;
    --self-test)
      self_test
      exit $?
      ;;
    -*)
      usage
      exit 2
      ;;
  esac

  check_report "$1"
  exit $?
}

main "$@"
