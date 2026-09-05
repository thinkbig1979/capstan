#!/usr/bin/env bash
# scripts/check-getter-errors.sh
#
# ONE invariant, the ratchet for agent-os-zhe9:
#
#   backend/internal/ grows NO new site where the error returned by a call is
#   discarded (`x, _ := f()`) or softened (`x, e := f()` whose `e` is only ever
#   compared `e == nil`).
#
# WHY. Nine beads filed across 2026-09-04/05 are one defect family, and every
# sweep their close reasons cited was anchored on an identifier, so every one
# of them has already returned a FALSE ZERO on this tree:
#
#   - the error NAME: sErr (docker_update.go:471), dbErr (scanner.go:491),
#     pErr (backup.go:1084) evaded every `err`-anchored regex (g482/obgr/r1by).
#   - the callee VERB: `List*` evaded every `\.Get[A-Z]` sweep
#     (directories.go:55/:79/:81), and `UserCount` has no verb at all
#     (auth.go:127/:144).
#   - the SHAPE: `if v, e := f(); e == nil {` with no `&& v != nil` was
#     invisible to both literal patterns the family used
#     (git_credentials.go:158).
#   - and bud5's go/ast prototype added a FIFTH anchor without meaning to: it
#     walked the whole enclosing function body, so any later ident of the same
#     spelling -- including an unrelated `if err := ...` in a different scope --
#     marked the site handled. That made it blind precisely where the family's
#     commonest spelling, plain `err`, lives: at 4b569a6 it missed
#     docker_update.go:204 and updates.go:179/:325/:812.
#
# A prose warning in a close reason does not prevent the next one. This is the
# standing command those close reasons cite instead of a bespoke grep.
#
# WHY A RATCHET AND NOT A GATE. 135 sites exist in backend/internal today. A
# blocking gate would be red on arrival, so the committed baseline records what
# is there and the check fails only on GROWTH -- the same shape as
# scripts/check-ws-registration.sh.
#
# WHY THE BASELINE IS COUNTS AND NOT file:line. Line numbers rot:
# services/backup.go:1084 at 4b569a6 is :1172 on 5e7bbc5, pure code motion, and
# a line-keyed baseline would call that a new site every time anything above it
# moved. The baseline is per-file counts per kind. The stated cost: deleting
# one site and adding another in the SAME file nets zero and passes.
#
# WHY THE SELF-TEST RUNS FIRST, and why it is two-sided. A scanner that has
# silently stopped firing looks exactly like a clean tree, and this whole bead
# exists because a tool that reports clean because it is blind is worse than no
# tool -- a close reason will cite it. So scripts/check-docs.sh runs
# --self-test first and fails the whole check if it fails, and the self-test
# asserts BOTH a fixture that must fire (testdata/fire, eight sites, one per
# evaded anchor) and a fixture that must stay silent (testdata/clean, whose
# functions use the same callees, the same variable names and the same
# `== nil` text while handling their errors properly). A control that fires on
# one known instance proves the pattern matches SOMETHING, never that it
# covers the class.
#
# LAYER A, the type-aware second instrument. The scanner has no type
# information on purpose: go/types needs the module's dependencies resolved and
# the required check that runs this has a checkout and nothing else. So it
# cannot tell a discarded error from a discarded bool, and it reports three
# `strings.Cut` sites (exec_env.go:81, redact_url.go:244/:410) that are not
# errors at all -- OBSERVED at 4b569a6, and the reason --cross-check exists.
#
# WHY THE LAYER A CONFIG IS DERIVED AT RUN TIME AND NOT COMMITTED. This looks
# indirect and it is deliberate; both simpler alternatives were built, measured
# and rejected, so do not "simplify" it back to a committed file.
#
#   - Adding `check-blank: true` to the COMMITTED backend/.golangci.yml turns
#     the lint job RED. Measured at 5e7bbc5, `cd backend && golangci-lint run
#     ./...`: that file as it stands gives `0 issues.` rc 0; with the setting
#     added, 117 issues rc 1, all 117 attributed to errcheck and no other
#     linter moving. 137 blank discards exist in this tree. The bead requires
#     layer A to be a RATCHET, never a blocking gate, so it cannot live there.
#   - A hand-written STANDALONE config at a non-discovered filename keeps the
#     gate green but silently DRIFTS from the committed nine-linter set
#     (errcheck, govet, ineffassign, staticcheck, unused, misspell, bodyclose,
#     contextcheck, gosec) and its five reasoned gosec exclusions. Nothing
#     would catch the drift: `Lint (golangci-lint)` is not a required check.
#     A first draft of this script did exactly that and measured a DIFFERENT
#     population as a result -- see the instrument-labelled counts below.
#
#   Deriving it makes that drift structurally impossible instead of merely
#   watched: the layer A config IS the committed config, plus one setting,
#   recomputed on every run. It is written inside backend/ because
#   golangci-lint treats a `-c` path's directory as the reporting base (a
#   config in /tmp makes every reported path relative to /tmp), and it is
#   removed by an EXIT trap.
#
# EVERY COUNT IN THIS FAMILY CARRIES ITS INSTRUMENT. An unlabelled count is how
# three separate readers spent a night disagreeing about one number. All with
# `GOLANGCI_LINT_CACHE` isolated, `check-blank` on, test files excluded:
#
#   instrument                       scope                4b569a6   5e7bbc5
#   committed 9-linter config        ./...      total       127        117
#   committed 9-linter config        ./...      non-test     96         86
#   committed 9-linter config        ./internal/... non-test 89         79
#   standard linter set              ./internal/... non-test 137       127
#
# The bead's "137" is the LAST row, not the first: golangci-lint's standard set
# with default exclusions. The committed config reports 89 for that same scope
# and SHA because it applies `exclusions.presets: [comments,
# std-error-handling]`. Both numbers were always real and were measuring
# different things. The delta is exactly 10 in every row and it is
# services/backup_config.go alone -- the ten sites agent-os-l42o converted,
# which the AST scanner independently reports as DISCARD 11 -> 1.
#
# NO SEPARATE CONTROL CONFIG IS NEEDED to isolate the blank subset: the
# committed config without this setting reports `0 issues.` rc 0, so every
# issue the derived config reports is caused by check-blank. An earlier draft
# used a second config and a set difference; that is unnecessary and it
# silently loses any line carrying two findings.
#
# TWO WAYS THIS INSTRUMENT LIES ABOUT A ZERO, both hit while building it:
#   - golangci-lint takes a global lock. A contended run exits **3** and prints
#     NO issues at all, which a `tail`-read renders as a clean tree. Never
#     believe a count without its exit code; treat 3 as "did not run".
#   - a shared cache across worktrees of one module is unsound. Measuring two
#     SHAs in sequence returned one pair of counts on one pass and the two
#     trees' verdicts SWAPPED on the next; `GOLANGCI_LINT_CACHE` per run
#     returned the same pair twice. `golangci-lint cache clean` was not enough.
#
# The same class of lie, from a third direction, is why this file exists at
# all: the brief that commissioned it reported `find . -name ".golangci*"` = 0
# and concluded no config existed. The repo's `rtk` wrapper REFUSES `find` with
# compound predicates and prints its refusal on **stderr**, so a `2>/dev/null`
# turned a refused command into an apparently clean result -- and the file it
# missed is the very one this layer edits. Never redirect stderr on a sweep
# whose zero you intend to believe, and use `git ls-tree` or `ls` on an
# absolute path for existence questions.
#
# Usage:
#   check-getter-errors.sh              ratchet: no file may exceed its baseline
#   check-getter-errors.sh --scan       list every site (file:line, kind, callee)
#   check-getter-errors.sh --self-test  prove the check still fires, both ways
#   check-getter-errors.sh --cross-check   layer A: AST vs errcheck check-blank
#   check-getter-errors.sh --update-baseline   rewrite the committed baseline
#
# Exit: 0 clean, 1 violation or an unusable input, 2 usage error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TOOL="$SCRIPT_DIR/getter-errors/main.go"
FIXTURES="$SCRIPT_DIR/getter-errors/testdata"
BASELINE="$SCRIPT_DIR/check-getter-errors-baseline.txt"
TARGET_REL='backend/internal'

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [--scan | --self-test | --cross-check | --update-baseline]

Fails when $TARGET_REL contains MORE discarded (\`x, _ := f()\`) or softened
(\`x, e := f()\` used only as \`e == nil\`) call sites in any one file than
$(basename "$BASELINE") records. Detection is by AST shape only: not the
receiver expression, not the error-variable name, not the callee prefix.
USAGE
}

# run_tool invokes the scanner. Go is a hard requirement, and its absence is a
# FAILURE rather than a skip: a check that quietly does nothing is the exact
# failure mode this bead was filed for.
run_tool() {
  if ! command -v go >/dev/null 2>&1; then
    echo "getter-errors: FAIL - 'go' is not on PATH, so the scanner cannot run." >&2
    echo "  This is a failure, not a skip: a scanner that silently stops firing" >&2
    echo "  looks exactly like a clean tree. CI installs Go for this check" >&2
    echo "  (.github/workflows/docs.yml)." >&2
    return 1
  fi
  go run "$TOOL" "$@"
}

self_test() {
  local out status failed=0

  # ARM 1, MUST FIRE: eight sites, one per anchor the family's sweeps evaded.
  out=$(run_tool scan "$FIXTURES/fire" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "self-test: FAIL - the scanner errored on the fire fixture:"
    echo "$out"
    return 1
  fi
  local want_fire='DISCARD fire.go:24 GetThing (discardPlain)
DISCARD fire.go:35 UserCount (discardNoVerbPrefix)
DISCARD fire.go:41 Status (discardThreeValues)
SOFT    fire.go:48 GetThing (softUnusualErrorName)
SOFT    fire.go:58 ListThings (softListCallee)
SOFT    fire.go:68 GetThing (softBareEqNil)
SOFT    fire.go:83 GetThing (softThenShadowed)
SOFT    fire.go:97 GetThing (softInNestedBlock)
TOTAL SITES=8 DISCARD=3 SOFT=5'
  if [ "$out" != "$want_fire" ]; then
    echo "self-test: FAIL - the MUST-FIRE fixture no longer reports its eight sites."
    echo "  Every missing row is an anchor this scanner has silently regrown."
    diff <(echo "$want_fire") <(echo "$out") | sed 's/^/  /'
    failed=1
  fi

  # ARM 2, MUST NOT FIRE: same callees, same names, same `== nil` text, all
  # handled. Without this arm, a scanner that matched everything would pass.
  out=$(run_tool scan "$FIXTURES/clean" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "self-test: FAIL - the scanner errored on the clean fixture:"
    echo "$out"
    return 1
  fi
  if [ "$out" != 'TOTAL SITES=0 DISCARD=0 SOFT=0' ]; then
    echo "self-test: FAIL - the MUST-NOT-FIRE fixture reported a site, so the"
    echo "  scanner is now matching handled errors too:"
    echo "$out" | sed 's/^/  /'
    failed=1
  fi

  # ARM 3, the RATCHET COMPARISON itself, both ways. A correct scanner wired to
  # a comparison that never fails is still a clean-looking tree.
  local tmp
  tmp=$(mktemp -d) || return 1
  printf 'a.go DISCARD=1 SOFT=1\nTOTAL FILES=1 DISCARD=1 SOFT=1\n' >"$tmp/base"
  printf 'a.go DISCARD=1 SOFT=1\nTOTAL FILES=1 DISCARD=1 SOFT=1\n' >"$tmp/same"
  printf 'a.go DISCARD=2 SOFT=1\nTOTAL FILES=1 DISCARD=2 SOFT=1\n' >"$tmp/grown"
  printf 'b.go DISCARD=1 SOFT=0\nTOTAL FILES=1 DISCARD=1 SOFT=0\n' >"$tmp/newfile"
  compare_counts "$tmp/base" "$tmp/same" >/dev/null 2>&1
  if [ $? -ne 0 ]; then
    echo "self-test: FAIL - the ratchet comparison rejected an UNCHANGED tree."
    failed=1
  fi
  compare_counts "$tmp/base" "$tmp/grown" >/dev/null 2>&1
  if [ $? -eq 0 ]; then
    echo "self-test: FAIL - the ratchet comparison accepted a GROWN count."
    failed=1
  fi
  compare_counts "$tmp/base" "$tmp/newfile" >/dev/null 2>&1
  if [ $? -eq 0 ]; then
    echo "self-test: FAIL - the ratchet comparison accepted a site in a file the"
    echo "  baseline does not mention at all."
    failed=1
  fi
  rm -rf "$tmp"

  if [ "$failed" -ne 0 ]; then
    return 1
  fi
  echo "self-test: 8 sites found in the must-fire fixture, 0 in the must-not-fire fixture, ratchet rejects growth and a new file and accepts no change"
  return 0
}

# compare_counts prints every file whose current count exceeds the baseline.
compare_counts() {
  local base="$1" cur="$2"
  awk -v basefile="$base" '
    BEGIN {
      while ((getline line < basefile) > 0) {
        if (line ~ /^TOTAL /) continue
        n = split(line, f, " ")
        if (n != 3) continue
        sub(/^DISCARD=/, "", f[2]); sub(/^SOFT=/, "", f[3])
        bd[f[1]] = f[2] + 0; bs[f[1]] = f[3] + 0
      }
      close(basefile)
      bad = 0
    }
    /^TOTAL / { next }
    {
      n = split($0, f, " ")
      if (n != 3) next
      sub(/^DISCARD=/, "", f[2]); sub(/^SOFT=/, "", f[3])
      d = f[2] + 0; s = f[3] + 0
      if (d > bd[f[1]]) { printf "  %s DISCARD %d -> %d\n", f[1], bd[f[1]], d; bad = 1 }
      if (s > bs[f[1]]) { printf "  %s SOFT %d -> %d\n", f[1], bs[f[1]], s; bad = 1 }
    }
    END { exit bad }
  ' "$cur"
}

ratchet() {
  if [ ! -f "$BASELINE" ]; then
    echo "getter-errors: FAIL - baseline $BASELINE is missing. Regenerate with --update-baseline."
    return 1
  fi
  local target="$REPO_ROOT/$TARGET_REL"
  if [ ! -d "$target" ]; then
    echo "getter-errors: FAIL - $TARGET_REL not found under $REPO_ROOT"
    return 1
  fi
  local cur status
  cur=$(mktemp) || return 1
  run_tool counts "$target" >"$cur" 2>&1
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "getter-errors: FAIL - the scanner errored:"
    sed 's/^/  /' "$cur"
    rm -f "$cur"
    return 1
  fi
  local grown
  grown=$(compare_counts "$BASELINE" "$cur")
  status=$?
  local totals
  totals=$(grep '^TOTAL ' "$cur")
  rm -f "$cur"
  if [ "$status" -ne 0 ]; then
    echo "getter-errors: FAIL - a new discarded or softened call site was added."
    echo "  A discarded error turns a database or daemon fault into a silent"
    echo "  default; a softened one turns it into 'not found'. Handle the error,"
    echo "  or if the site is genuinely fine, say so at the site and refresh the"
    echo "  baseline with scripts/check-getter-errors.sh --update-baseline."
    echo "$grown"
    return 1
  fi
  echo "getter-errors: no file exceeds its baseline ($totals)"
  return 0
}

# derive_config writes the committed golangci-lint config plus errcheck's
# check-blank into DEST, and fails loudly rather than falling back to anything.
# A layer A that quietly runs a default config is the same false-zero class as
# every other failure this bead exists to end.
derive_config() {
  local src="$1" dest="$2"
  if [ ! -f "$src" ]; then
    echo "cross-check: FAIL - $src not found; refusing to run layer A against a" >&2
    echo "  config golangci-lint would pick by default." >&2
    return 1
  fi
  if grep -q '^[[:space:]]*check-blank:[[:space:]]*true' "$src"; then
    # Already set upstream: use the committed file unchanged rather than
    # producing a duplicate key.
    cp "$src" "$dest" || return 1
    return 0
  fi
  # The insertion anchors on the `linters:` line at column 0, never on a linter
  # NAME: the enable list is expected to change and an anchor on one of its
  # entries would rot into a silent no-op.
  if ! grep -qE '^linters:[[:space:]]*$' "$src"; then
    echo "cross-check: FAIL - $src has no top-level 'linters:' key to extend." >&2
    return 1
  fi
  if awk '/^linters:[[:space:]]*$/ {inl = 1; next} /^[^[:space:]#]/ {inl = 0} inl && /^  settings:/ {found = 1} END {exit !found}' "$src"; then
    echo "cross-check: FAIL - $src already defines linters.settings; merging into" >&2
    echo "  it blind would produce a duplicate key. Add check-blank there by hand" >&2
    echo "  and this script will use the file unchanged." >&2
    return 1
  fi
  awk '
    { print }
    /^linters:[[:space:]]*$/ && !done {
      print "  settings:"
      print "    errcheck:"
      print "      check-blank: true"
      done = 1
    }
  ' "$src" >"$dest" || return 1
  if ! (cd "$(dirname "$dest")" && golangci-lint config verify -c "$(basename "$dest")" >/dev/null 2>&1); then
    echo "cross-check: FAIL - the derived config does not validate:" >&2
    (cd "$(dirname "$dest")" && golangci-lint config verify -c "$(basename "$dest")" 2>&1) | sed 's/^/  /' >&2
    return 1
  fi
  return 0
}

cross_check() {
  local backend="$REPO_ROOT/backend"
  local cfg="$backend/.golangci.yml"
  if ! command -v golangci-lint >/dev/null 2>&1; then
    echo "cross-check: golangci-lint is not on PATH; layer A cannot run here." >&2
    return 1
  fi
  local derived="$backend/.golangci-derived-$$.yml"
  # shellcheck disable=SC2064
  trap "rm -f '$derived'" EXIT INT TERM
  rm -f "$derived"
  derive_config "$cfg" "$derived" || return 1

  local tmp ast lint status
  tmp=$(mktemp -d) || return 1
  ast="$tmp/ast"
  lint="$tmp/lint"
  run_tool scan "$REPO_ROOT/$TARGET_REL" 2>/dev/null |
    awk '$1 == "DISCARD" { print $2 }' | sort -u >"$ast"
  # An ISOLATED cache: a shared one is not merely slow here, it is unsound
  # across worktrees of the same module (see the header).
  (cd "$backend" && GOLANGCI_LINT_CACHE="$tmp/cache" golangci-lint run -c "$(basename "$derived")" ./internal/... >"$tmp/out" 2>&1)
  status=$?
  if [ "$status" -eq 3 ]; then
    echo "cross-check: FAIL - golangci-lint refused to run (another instance holds its lock)." >&2
    rm -rf "$tmp"
    return 1
  fi
  grep -oE '^internal/[A-Za-z0-9_./-]+\.go:[0-9]+' "$tmp/out" |
    grep -v '_test\.go' | sed 's#^internal/##' | sort -u >"$lint"
  echo "cross-check: instrument = committed backend/.golangci.yml + errcheck check-blank, scope ./internal/... non-test"
  echo "  AST DISCARD sites: $(wc -l <"$ast"); errcheck blank sites: $(wc -l <"$lint") (golangci-lint rc=$status)"
  echo "-- AST only: no type information, so a discarded non-error last value looks the same."
  echo "   OBSERVED: StartVerified, gitCmdWithCreds and ResolveComposeProjectName all return"
  echo "   (T, string); files behind the integration build tag are here too, because errcheck"
  echo "   does not analyse them and the AST scanner does. --"
  comm -23 "$ast" "$lint" | sed 's/^/  /'
  echo "-- errcheck only: the single-value form \`_ = f()\` (e.g. \`_ = tx.Rollback()\`,"
  echo "   \`_ = conn.SetWriteDeadline(...)\`), which is a deliberate discard of a call's"
  echo "   ONLY return rather than this family's shape, so the AST scanner skips it. --"
  comm -13 "$ast" "$lint" | sed 's/^/  /'
  rm -rf "$tmp"
  return 0
}

main() {
  case "${1:-}" in
    '')
      ratchet
      ;;
    --scan)
      run_tool scan "$REPO_ROOT/$TARGET_REL"
      ;;
    --self-test)
      self_test
      ;;
    --cross-check)
      cross_check
      ;;
    --update-baseline)
      run_tool counts "$REPO_ROOT/$TARGET_REL" >"$BASELINE" || return 1
      echo "getter-errors: baseline rewritten -> ${BASELINE#$REPO_ROOT/}"
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
