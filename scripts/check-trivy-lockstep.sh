#!/usr/bin/env bash
# scripts/check-trivy-lockstep.sh
#
# Fails when the comment above the "Install Trivy" step in
# .github/workflows/security.yml no longer states the pins its `uses:` lines
# carry (agent-os-028z, after agent-os-ya30).
#
# WHY: that step pins aquasecurity/setup-trivy to one release while
# aquasecurity/trivy-action pins a DIFFERENT setup-trivy internally. The
# comment says the difference is deliberate and gives two `gh api` commands
# that verify the pair. Dependabot bumps either action on its own and cannot
# touch the comment, so the comment can go stale without anything failing.
#
# WHAT IT COMPARES (fixed strings, no prose parsing). From the structured
# `uses:` lines:
#   A  the 40-hex SHA of `uses: aquasecurity/setup-trivy@<A> # <B>`
#   B  the `# <B>` tag trailing that line (e.g. v0.3.1)
#   C  the ref of every `uses: aquasecurity/trivy-action@<C>` (the file has
#      more than one; they must all agree)
# and requires the contiguous `#` comment block directly above the setup-trivy
# step's `- name:` line to contain:
#   A                        the setup-trivy SHA the step pins
#   tags/B                   the `gh api .../git/ref/tags/<B>` verify command
#   trivy-action@C           the prose naming the action
#   ?ref=C                   the `gh api .../action.yaml?ref=<C>` verify command
#
# WHAT IT CANNOT CHECK, and does not pretend to: the setup-trivy pin that
# trivy-action's own action.yaml carries (the "3fb12ec... (v0.2.6)" in the
# comment). That lives in another repository and reading it needs the
# network, which CI must not need here. So this check does NOT prove the
# comment's claim about trivy-action's internal pin is true. What it does is
# keep the comment and the `uses:` lines in step: a Dependabot bump of either
# action fails this check until whoever merges it re-runs the two `gh api`
# commands from the comment and updates the comment to match.
#
# Absence is an error, never a pass: a pin that cannot be read (reworded
# `uses:` line, no comment block) would otherwise compare empty strings and
# report green over a guard that stopped looking.
#
# Dependency-free by design: bash, grep, sed, sort. No awk, no network.
#
# Usage:
#   check-trivy-lockstep.sh [WORKFLOW]   check the workflow (default: the repo's
#                                        .github/workflows/security.yml)
#   check-trivy-lockstep.sh --self-test  prove the check fires both ways
#
# Exit: 0 comment and uses: lines agree, 1 they differ (each stale pin named),
#       2 a pin or the comment block could not be read.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [WORKFLOW | --self-test]

With no argument, checks .github/workflows/security.yml. --self-test runs the
check over fixture workflows (red and green controls) and exits non-zero if
any control misbehaves.
USAGE
}

# check_workflow FILE prints the verdict and returns 0, 1 or 2 as in the header.
check_workflow() {
  local wf="$1"
  if [ ! -r "$wf" ] || [ ! -f "$wf" ]; then
    echo "check-trivy-lockstep: $wf is not a readable file" >&2
    return 2
  fi

  # A and B come from the one setup-trivy `uses:` line. -m1 on a pattern that
  # requires the 40-hex pin: a floating tag (`@v0.3.1`) is not a pin and is
  # reported as unreadable rather than compared.
  local uses_line setup_sha setup_tag
  uses_line="$(command grep -nE '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*aquasecurity/setup-trivy@[0-9a-f]{40}[[:space:]]+#[[:space:]]*v[0-9]' "$wf" | command head -n 1)"
  if [ -z "$uses_line" ]; then
    echo "check-trivy-lockstep: no 'uses: aquasecurity/setup-trivy@<40-hex sha> # v<tag>' line found in $wf" >&2
    return 2
  fi
  setup_sha="$(sed -E 's/^[0-9]+:.*setup-trivy@([0-9a-f]{40}).*/\1/' <<<"$uses_line")"
  setup_tag="$(sed -E 's/^[0-9]+:.*#[[:space:]]*(v[0-9][0-9A-Za-z.+-]*).*/\1/' <<<"$uses_line")"
  local uses_lineno="${uses_line%%:*}"

  # C: every trivy-action ref, deduplicated; more than one distinct value is
  # itself a failure (two scan steps on different releases).
  local action_refs
  action_refs="$(command grep -E '^[[:space:]]*(-[[:space:]]+)?uses:[[:space:]]*aquasecurity/trivy-action@' "$wf" \
    | sed -E 's/.*trivy-action@([^[:space:]#]+).*/\1/' | command sort -u)"
  if [ -z "$action_refs" ]; then
    echo "check-trivy-lockstep: no 'uses: aquasecurity/trivy-action@<ref>' line found in $wf" >&2
    return 2
  fi
  if [ "$(wc -l <<<"$action_refs")" -ne 1 ]; then
    echo "FAIL: trivy-lockstep - $wf uses aquasecurity/trivy-action at more than one ref: $(paste -sd' ' - <<<"$action_refs")" >&2
    echo "Bump every trivy-action step to one release in the same PR, then re-check the comment above the Install Trivy step." >&2
    return 1
  fi
  local action_ref="$action_refs"

  # The comment block: walk up from the setup-trivy `uses:` line over its
  # `- name:` line (and any other step keys between), then collect the
  # contiguous run of `#` lines directly above.
  local -a lines=()
  mapfile -t lines < "$wf"
  local i=$((uses_lineno - 1)) # 0-based index of the uses: line
  # Step keys sitting between `- name:` and `uses:` (none today) are skipped
  # until the `- name:` line itself.
  while [ "$i" -gt 0 ] && ! [[ "${lines[$i]}" =~ ^[[:space:]]*-[[:space:]]+name: ]]; do
    i=$((i - 1))
  done
  local block="" j=$((i - 1))
  while [ "$j" -ge 0 ] && [[ "${lines[$j]}" =~ ^[[:space:]]*# ]]; do
    block="${lines[$j]}"$'\n'"$block"
    j=$((j - 1))
  done
  if [ -z "$block" ]; then
    echo "check-trivy-lockstep: no comment block directly above the setup-trivy step (line $uses_lineno) in $wf" >&2
    return 2
  fi

  local -a stale=()
  has() { command grep -qF -- "$1" <<<"$block"; }
  has "$setup_sha" || stale+=("setup-trivy SHA $setup_sha (the uses: line)")
  has "tags/$setup_tag" || stale+=("the setup-trivy verify command's 'tags/$setup_tag' (the uses: line's # $setup_tag)")
  has "trivy-action@$action_ref" || stale+=("'trivy-action@$action_ref' (the trivy-action uses: lines)")
  has "?ref=$action_ref" || stale+=("the trivy-action verify command's '?ref=$action_ref'")

  if [ "${#stale[@]}" -gt 0 ]; then
    echo "FAIL: trivy-lockstep - the comment above the Install Trivy step in $wf no longer states:" >&2
    printf '  - %s\n' "${stale[@]}" >&2
    echo "Re-run the two gh api commands in that comment, then update it to match the uses: lines." >&2
    echo "(This check cannot read trivy-action's internal setup-trivy pin offline; that part stays a manual re-check.)" >&2
    return 1
  fi

  echo "PASS: trivy-lockstep - the comment states setup-trivy $setup_tag ($setup_sha) and trivy-action $action_ref, matching the uses: lines (trivy-action's internal setup-trivy pin is not checked offline)"
  return 0
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_STATUS WANT_OUTPUT_REGEX FILE [ABSENT_REGEX]
# Output is flattened to one line before matching, so a pattern can span the
# report's bullet lines. ABSENT_REGEX, when given, must NOT match.
selftest_case() {
  local name="$1" want="$2" pat="$3" out flat status
  ST_RUN=$((ST_RUN + 1))
  out=$(check_workflow "$4" 2>&1)
  status=$?
  flat="$(tr '\n' ' ' <<<"$out")"
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$flat"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: trivy-lockstep self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  elif [ -n "${5:-}" ] && command grep -qE -- "$5" <<<"$flat"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: trivy-lockstep self-test case \"$name\": output matched /$5/ but must not:"
    echo "$out"
  fi
}

# write_fixture FILE SETUP_SHA SETUP_TAG ACTION_REF_1 ACTION_REF_2 COMMENT_SHA COMMENT_TAG COMMENT_ACTION_REF
# The comment is parameterised separately from the uses: lines, so a case can
# plant exactly one mismatch between them.
write_fixture() {
  cat > "$1" <<EOF
jobs:
  scan:
    steps:
      # An unrelated comment above the block.
      - name: Something else
        run: true

      # trivy-action@$8 resolves setup-trivy internally, but not the same
      # pin: it pins setup-trivy@3fb12ec12f41e471780db15c232d5dd185dcb514
      # (v0.2.6), and this step pins $6 ($7).
      #   gh api 'repos/aquasecurity/trivy-action/contents/action.yaml?ref=$8' --jq .content
      #   gh api repos/aquasecurity/setup-trivy/git/ref/tags/$7 --jq .object.sha
      - name: Install Trivy
        uses: aquasecurity/setup-trivy@$2 # $3
        with:
          version: v0.70.0

      - name: Scan image
        uses: aquasecurity/trivy-action@$4
      - name: Scan image (JSON)
        uses: aquasecurity/trivy-action@$5
EOF
}

selftest() {
  local tmp
  tmp=$(mktemp -d) || { echo "FAIL: trivy-lockstep self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$tmp"' RETURN
  local sha=81e514348e19b6112ce2a7e3ecbafe19c1e1f567 other=0123456789abcdef0123456789abcdef01234567 f

  # GREEN: comment and uses: lines agree.
  f="$tmp/green.yml"; write_fixture "$f" $sha v0.3.1 v0.36.0 v0.36.0 $sha v0.3.1 v0.36.0
  selftest_case "comment matches uses: lines" 0 "PASS: trivy-lockstep - .*setup-trivy v0\.3\.1 .*trivy-action v0\.36\.0" "$f"

  # RED: Dependabot bumped setup-trivy's SHA and tag, comment untouched.
  f="$tmp/red-setup.yml"; write_fixture "$f" $other v0.3.2 v0.36.0 v0.36.0 $sha v0.3.1 v0.36.0
  selftest_case "setup-trivy bumped, comment stale" 1 "no longer states:.*setup-trivy SHA $other.*tags/v0\.3\.2" "$f"

  # RED: the SHA moved but the tag did not (a re-pin of the same tag).
  f="$tmp/red-sha-only.yml"; write_fixture "$f" $other v0.3.1 v0.36.0 v0.36.0 $sha v0.3.1 v0.36.0
  selftest_case "setup-trivy SHA only" 1 "setup-trivy SHA $other" "$f" "tags/v0\.3\.1"

  # RED: Dependabot bumped trivy-action, comment untouched; both comment
  # mentions (prose and the gh api command) are reported.
  f="$tmp/red-action.yml"; write_fixture "$f" $sha v0.3.1 v0.37.0 v0.37.0 $sha v0.3.1 v0.36.0
  selftest_case "trivy-action bumped, comment stale" 1 "trivy-action@v0\.37\.0.*\?ref=v0\.37\.0" "$f"

  # RED: the two trivy-action steps disagree.
  f="$tmp/red-split.yml"; write_fixture "$f" $sha v0.3.1 v0.36.0 v0.37.0 $sha v0.3.1 v0.36.0
  selftest_case "trivy-action steps on different refs" 1 "more than one ref: v0\.36\.0 v0\.37\.0" "$f"

  # ERROR: no setup-trivy uses: line, and a floating tag instead of a pin.
  printf 'jobs:\n  s:\n    steps:\n      - uses: aquasecurity/trivy-action@v0.36.0\n' > "$tmp/no-setup.yml"
  selftest_case "no setup-trivy line" 2 "no 'uses: aquasecurity/setup-trivy@" "$tmp/no-setup.yml"
  f="$tmp/floating.yml"; write_fixture "$f" v0.3.1 v0.3.1 v0.36.0 v0.36.0 $sha v0.3.1 v0.36.0
  selftest_case "floating setup-trivy tag, not a pin" 2 "no 'uses: aquasecurity/setup-trivy@" "$f"

  # ERROR: no trivy-action line.
  printf '      # c\n      - name: Install Trivy\n        uses: aquasecurity/setup-trivy@%s # v0.3.1\n' $sha > "$tmp/no-action.yml"
  selftest_case "no trivy-action line" 2 "no 'uses: aquasecurity/trivy-action@" "$tmp/no-action.yml"

  # ERROR: no comment block above the step: an empty guard is not a pass.
  printf 'steps:\n  - name: Install Trivy\n    uses: aquasecurity/setup-trivy@%s # v0.3.1\n  - uses: aquasecurity/trivy-action@v0.36.0\n' $sha > "$tmp/no-comment.yml"
  selftest_case "no comment block" 2 "no comment block directly above" "$tmp/no-comment.yml"

  # ERROR: an unreadable file.
  selftest_case "missing file" 2 "not a readable file" "$tmp/does-not-exist.yml"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: trivy-lockstep self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "trivy-lockstep self-test: $ST_RUN control(s) passed (1 green, 5 red, 5 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_workflow "$REPO_ROOT/.github/workflows/security.yml"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_workflow "$1"; exit $? ;;
esac
