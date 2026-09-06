#!/usr/bin/env bash
# scripts/check-getter-errors.sh
#
# ONE invariant, the ratchet for agent-os-zhe9 and (third kind) agent-os-8f2g:
#
#   backend/ grows NO new site where the error returned by a call is
#   discarded (`x, _ := f()`), softened (`x, e := f()` whose `e` is only ever
#   compared `e == nil`), or MERGED into a value test (`if e != nil || <value>`,
#   in either operand order).
#
# WHY THE THIRD KIND WAS ADDED (agent-os-8f2g). DISCARD and SOFT both describe
# an error weakened at the ASSIGNMENT. MERGE describes one checked correctly and
# then fused by `||` to a value test at the BRANCH, so "I could not read it" and
# "I read it and the answer is no" produce the same branch and the caller cannot
# tell them apart. Neither existing kind could ever see it: agent-os-koy9, 91u2,
# 89ut and rb6f are four beads of that ONE class, and at c8c5c6b none of
# services/scheduler.go, services/monitor.go, services/docker_lifecycle.go or
# services/backup_runner.go appeared in a DISCARD or SOFT row while all four
# carried the defect. The class therefore regrew between beads with this
# required gate green over it, which is the complaint agent-os-jar5 makes one
# level up. Every count printed here now names all three kinds for that reason.
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
# ---------------------------------------------------------------------------
# HOW TO RUN IT (agent-os-egjr). Every command below is copy-pasteable from the
# REPO ROOT and was run there. The five hazards above say how this instrument
# lies; this section says how to invoke it, which is the other half and was
# missing.
#
#   $ bash scripts/check-getter-errors.sh
#   getter-errors: no file exceeds its baseline (scope backend/ (cmd, internal), TOTAL FILES=... DISCARD=... SOFT=... MERGE=...)
#
#     The ratchet, and what the required "Docs structure" CI job runs via
#     scripts/check-docs.sh. Exit 0 clean, 1 on growth.
#
#   $ bash scripts/check-getter-errors.sh --scan
#   getter-errors: scope backend/ (cmd, internal)
#   DISCARD cmd/server/admin.go:71 WriteString (runAdminCommand)
#   ... one row per site ...
#   MERGE   internal/handlers/auth.go:346 ||(err=1,value=1) (Login)
#   TOTAL SITES=... DISCARD=... SOFT=... MERGE=...
#
#   A MERGE row's third column is not a callee: it is the operand census of the
#   `||` chain, `||(err=N,value=M)`. Both N and M are >= 1 by the membership
#   rule -- a chain of errors alone (`if timeErr != nil || daysErr != nil`) is a
#   different question and is deliberately NOT a MERGE site. See main.go's
#   header for the rule and for the four false-zero mechanisms that made an AST
#   detector necessary rather than a grep.
#
#   $ bash scripts/check-getter-errors.sh --self-test
#   $ bash scripts/check-getter-errors.sh --cross-check   # needs golangci-lint
#   $ bash scripts/check-getter-errors.sh --update-baseline
#
# THE SCOPE IS backend/, WHICH IS backend/internal AND backend/cmd. It was
# backend/internal alone until agent-os-pcnh, and every mode prints the scope it
# swept precisely so that a number lifted out of this output cannot be quoted as
# a statement about a wider tree than it measured. If you cite a count from here,
# cite the scope line with it.
#
# RUNNING THE SCANNER DIRECTLY, and the ONE form that reads as a clean zero.
# The scanner is a standalone go/ast program and the repo root has no go.mod, so
# it must be invoked as a FILE, never as a package directory:
#
#   WORKS:
#     $ go run scripts/getter-errors/main.go scan backend/cmd
#     DISCARD server/admin.go:71 WriteString (runAdminCommand)
#     ...
#     TOTAL SITES=3 DISCARD=3 SOFT=0 MERGE=0
#     (paths are relative to the directory you point it at, not to the repo)
#
#   FAILS, and this is the trap -- OBSERVED at 8104a65 from the repo root:
#     $ go run ./scripts/getter-errors scan backend/cmd
#     go: go.mod file not found in current directory or any parent directory; see 'go help modules'
#     $ echo $?
#     1
#
#   The refusal goes to STDERR and stdout is EMPTY. So the directory-shaped form
#   inside `count=$(go run ./scripts/getter-errors scan "$dir" 2>/dev/null | wc -l)`
#   yields 0 with rc 0 -- `wc`'s rc, not `go run`'s -- and a caller that does not
#   read ${PIPESTATUS[0]} scores a refusal as a clean tree. That is the same
#   false-zero shape this whole file exists to prevent, and cross_check below
#   shipped with it (`2>/dev/null |`) until agent-os-pcnh. If you wrap this
#   scanner: never redirect its stderr, and never pipe before reading its exit
#   code.
#
#   `reach` takes a coverage profile and files, not a directory:
#     $ go run scripts/getter-errors/main.go reach coverage.out backend/internal/services/backup.go
#
# Exit: 0 clean, 1 violation or an unusable input, 2 usage error.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TOOL="$SCRIPT_DIR/getter-errors/main.go"
FIXTURES="$SCRIPT_DIR/getter-errors/testdata"
BASELINE="$SCRIPT_DIR/check-getter-errors-baseline.txt"
# TARGET_REL is the ONE scope constant. It was 'backend/internal' until
# agent-os-pcnh: backend/cmd is a SIBLING of backend/internal, not a child, so
# the boot path and the admin CLI had never been swept at all, and the number
# this check printed was quoted in close reasons as a statement about "the
# backend". It was 86; the tree-wide number was 96. That is the repo's own
# "scope too narrow, result generalized" failure mode, committed.
#
# 'backend' is the whole of the Go source and nothing else. OBSERVED at 8104a65,
# in a clean checkout:
#
#   $ command find backend -name '*.go' -type f | command grep -v '^backend/internal/' | command grep -v '^backend/cmd/'
#   (no output)
#
# and goFiles() skips vendor/, testdata/ and node_modules/ by directory name
# (getter-errors/main.go:480-484), so backend/testdata's compose fixtures and
# backend/frontend's built assets cannot enter the count. Widen this constant
# and every mode below follows it, INCLUDING cross_check's second arm -- see
# the note there, which is a scope constant this one does not reach.
TARGET_REL='backend'

# scope_line names the tree that was actually swept, and it is computed from
# the scan's OWN output rather than restated by hand, so it cannot drift from
# what ran. Every mode prints it. That is the substance of agent-os-pcnh: this
# check printed "TOTAL SITES=86" with no scope attached, and the number was then
# quoted in close reasons as a fact about the backend while it only ever covered
# backend/internal. A count that does not carry its scope WILL be generalized.
# The field number is a parameter because the two output shapes put the path in
# different columns: `counts` prints "internal/services/x.go DISCARD=1 SOFT=2"
# (field 1) and `scan` prints "DISCARD internal/services/x.go:44 Foo (bar)"
# (field 2). Hardcoding field 1 would have produced an EMPTY scope for --scan,
# which is the failure this function exists to prevent, one level down.
scope_line() {
  local rows="$1" field="$2" dirs
  dirs=$(awk -v f="$field" '$1 != "TOTAL" { n = index($f, "/"); if (n > 1) print substr($f, 1, n - 1) }' "$rows" |
    sort -u | tr '\n' ' ')
  dirs="${dirs% }"
  if [ -z "$dirs" ]; then
    printf 'scope %s/' "$TARGET_REL"
    return
  fi
  printf 'scope %s/ (%s)' "$TARGET_REL" "$(echo "$dirs" | sed 's/ /, /g')"
}

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [--scan | --self-test | --cross-check | --update-baseline]

Fails when $TARGET_REL contains MORE discarded (\`x, _ := f()\`), softened
(\`x, e := f()\` used only as \`e == nil\`) or MERGED
(\`if e != nil || <value>\`, either operand order) call sites in any one file
than $(basename "$BASELINE") records. Detection is by AST shape only: not the
receiver expression, not the callee prefix, and -- except for the error's own
name, which without go/types is the only thing that can say an operand IS an
error -- not an identifier.
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

  # ARM 1, MUST FIRE: sixteen sites. Eight are one per anchor the DISCARD/SOFT
  # family's sweeps evaded; eight are the MERGE shape (agent-os-8f2g), one per
  # false-zero mechanism a text sweep for THAT class has actually hit on this
  # tree -- operand order, error name, selector, IF-INIT, three-operand chain,
  # `nil != err`, and a TAGLESS SWITCH CASE. The if-init row is the one most
  # likely to be deleted as
  # redundant. It is not: it is the mechanism that hid handlers/compose.go:452
  # and services/backup_restic.go:351 from every sweep ever run on this class,
  # and neither site had been dispositioned when this fixture was written. The
  # switch-case row is the one that was MISSING: the first detector passed every
  # other arm here and then stayed green when a known in-class site was
  # reintroduced into the real tree as a switch case -- the shape agent-os-89ut's
  # own fix is written in. Neither arm below could have caught that; only
  # probing the verdict wider than itself did.
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
MERGE   merge.go:17 ||(err=1,value=1) (mergeLeftAnchored)
MERGE   merge.go:29 ||(err=1,value=1) (mergeRightAnchored)
MERGE   merge.go:39 ||(err=1,value=1) (mergeUnusualErrorName)
MERGE   merge.go:54 ||(err=1,value=1) (mergeSelectorError)
MERGE   merge.go:66 ||(err=1,value=1) (mergeIfInit)
MERGE   merge.go:78 ||(err=1,value=2) (mergeThreeOperands)
MERGE   merge.go:89 ||(err=1,value=1) (mergeNilOnTheLeft)
MERGE   merge.go:105 ||(err=1,value=1) (mergeTaglessSwitchCase)
TOTAL SITES=16 DISCARD=3 SOFT=5 MERGE=8'
  if [ "$out" != "$want_fire" ]; then
    echo "self-test: FAIL - the MUST-FIRE fixture no longer reports its eight sites."
    echo "  Every missing row is an anchor this scanner has silently regrown."
    diff <(echo "$want_fire") <(echo "$out") | sed 's/^/  /'
    failed=1
  fi

  # ARM 2, MUST NOT FIRE: same callees, same names, same `== nil` text, all
  # handled. Without this arm, a scanner that matched everything would pass.
  # For MERGE it also pins the three things that must NOT count: a two-error
  # `||` with no value operand (agent-os-rltu's shape, which a ratchet must not
  # bless as accepted), an `&&`, a TAGGED switch (whose case expression is a
  # value compared against the tag, not a branch condition), and the class
  # written out in a COMMENT --
  # which every text-based arm counts and an AST walk cannot see. If clean/
  # ever reports MERGE=1, the detector has been reimplemented on text.
  out=$(run_tool scan "$FIXTURES/clean" 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "self-test: FAIL - the scanner errored on the clean fixture:"
    echo "$out"
    return 1
  fi
  if [ "$out" != 'TOTAL SITES=0 DISCARD=0 SOFT=0 MERGE=0' ]; then
    echo "self-test: FAIL - the MUST-NOT-FIRE fixture reported a site, so the"
    echo "  scanner is now matching handled errors too:"
    echo "$out" | sed 's/^/  /'
    failed=1
  fi

  # ARM 3, the RATCHET COMPARISON itself, both ways. A correct scanner wired to
  # a comparison that never fails is still a clean-looking tree.
  local tmp
  tmp=$(mktemp -d) || return 1
  printf 'a.go DISCARD=1 SOFT=1 MERGE=1\nTOTAL FILES=1 DISCARD=1 SOFT=1 MERGE=1\n' >"$tmp/base"
  printf 'a.go DISCARD=1 SOFT=1 MERGE=1\nTOTAL FILES=1 DISCARD=1 SOFT=1 MERGE=1\n' >"$tmp/same"
  printf 'a.go DISCARD=2 SOFT=1 MERGE=1\nTOTAL FILES=1 DISCARD=2 SOFT=1 MERGE=1\n' >"$tmp/grown"
  printf 'b.go DISCARD=1 SOFT=0 MERGE=0\nTOTAL FILES=1 DISCARD=1 SOFT=0 MERGE=0\n' >"$tmp/newfile"
  # MERGE gets its OWN growth arm. Without it the comparison could ignore the
  # new column entirely and every arm above would still pass: a ratchet wired
  # to a kind it detects but never compares is exactly the clean-looking tree
  # this file exists to prevent, one column over.
  printf 'a.go DISCARD=1 SOFT=1 MERGE=2\nTOTAL FILES=1 DISCARD=1 SOFT=1 MERGE=2\n' >"$tmp/mergegrown"
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
  compare_counts "$tmp/base" "$tmp/mergegrown" >/dev/null 2>&1
  if [ $? -eq 0 ]; then
    echo "self-test: FAIL - the ratchet comparison accepted a GROWN MERGE count,"
    echo "  so the MERGE column is detected but never gated."
    failed=1
  fi
  rm -rf "$tmp"

  if [ "$failed" -ne 0 ]; then
    return 1
  fi
  echo "self-test: 16 sites found in the must-fire fixture (3 DISCARD, 5 SOFT, 8 MERGE), 0 in the must-not-fire fixture, ratchet rejects DISCARD growth, MERGE growth and a new file, and accepts no change"
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
        if (n != 4) continue
        sub(/^DISCARD=/, "", f[2]); sub(/^SOFT=/, "", f[3]); sub(/^MERGE=/, "", f[4])
        bd[f[1]] = f[2] + 0; bs[f[1]] = f[3] + 0; bm[f[1]] = f[4] + 0
      }
      close(basefile)
      bad = 0
    }
    /^TOTAL / { next }
    {
      n = split($0, f, " ")
      if (n != 4) next
      sub(/^DISCARD=/, "", f[2]); sub(/^SOFT=/, "", f[3]); sub(/^MERGE=/, "", f[4])
      d = f[2] + 0; s = f[3] + 0; m = f[4] + 0
      if (d > bd[f[1]]) { printf "  %s DISCARD %d -> %d\n", f[1], bd[f[1]], d; bad = 1 }
      if (s > bs[f[1]]) { printf "  %s SOFT %d -> %d\n", f[1], bs[f[1]], s; bad = 1 }
      if (m > bm[f[1]]) { printf "  %s MERGE %d -> %d\n", f[1], bm[f[1]], m; bad = 1 }
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
  local totals scope
  totals=$(command grep '^TOTAL ' "$cur")
  # Computed BEFORE the temp file goes away, and printed on the FAIL path too:
  # a verdict that names its scope only when it passes is exactly as quotable
  # out of context as one that never names it.
  scope=$(scope_line "$cur" 1)
  rm -f "$cur"
  if [ "$status" -ne 0 ]; then
    echo "getter-errors: FAIL - a new discarded, softened or merged call site was added ($scope)."
    echo "  A discarded error turns a database or daemon fault into a silent"
    echo "  default; a softened one turns it into 'not found'; a MERGED one"
    echo "  (\`if err != nil || <value>\`) makes 'I could not read it' and 'I read"
    echo "  it and the answer is no' the same branch, so the caller cannot tell"
    echo "  them apart and nothing is logged. Handle the error separately from"
    echo "  the value, or if the site is genuinely fine, say so at the site and"
    echo "  refresh the baseline with scripts/check-getter-errors.sh --update-baseline."
    echo "$grown"
    return 1
  fi
  echo "getter-errors: no file exceeds its baseline ($scope, $totals)"
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
  # THE OLD FORM HERE WAS `run_tool scan ... 2>/dev/null | awk ... >"$ast"`:
  # stderr discarded, and piped before the exit code could be read, inside the
  # gate whose entire purpose is preventing exactly that. If the scanner refused
  # to run, its complaint went to /dev/null, $ast came out empty and the
  # comparison below reported a clean AST side. Fixed under agent-os-pcnh; see
  # the invocation section in the header for the `go run` refusal that makes it
  # concrete.
  local astraw="$tmp/ast.raw"
  run_tool scan "$REPO_ROOT/$TARGET_REL" >"$astraw" 2>&1
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "cross-check: FAIL - the AST scanner errored, so its side of this comparison would be" >&2
    echo "  EMPTY rather than clean. Its output, which this line used to discard:" >&2
    sed 's/^/  /' "$astraw" >&2
    rm -rf "$tmp"
    return 1
  fi
  awk '$1 == "DISCARD" { print $2 }' "$astraw" | sort -u >"$ast"
  # An ISOLATED cache: a shared one is not merely slow here, it is unsound
  # across worktrees of the same module (see the header).
  # ./... , NOT ./internal/... . This is a SECOND scope constant that TARGET_REL
  # does not reach, and widening TARGET_REL alone would have desynchronised the
  # two arms: the AST side would report cmd/server/main.go while errcheck never
  # looked there, so every backend/cmd site would surface in the "AST only"
  # column below as an instrument disagreement that does not exist. The brief
  # for agent-os-pcnh listed four TARGET_REL inheritors and missed this one.
  (cd "$backend" && GOLANGCI_LINT_CACHE="$tmp/cache" golangci-lint run -c "$(basename "$derived")" ./... >"$tmp/out" 2>&1)
  status=$?
  if [ "$status" -eq 3 ]; then
    echo "cross-check: FAIL - golangci-lint refused to run (another instance holds its lock)." >&2
    rm -rf "$tmp"
    return 1
  fi
  # Both arms are now relative to backend/, so the `sed 's#^internal/##'` that
  # used to strip the prefix off the errcheck side is GONE: with TARGET_REL
  # widened the AST side carries that prefix too, and stripping one side only
  # would make every pair mismatch.
  command grep -oE '^(internal|cmd)/[A-Za-z0-9_./-]+\.go:[0-9]+' "$tmp/out" |
    command grep -v '_test\.go' | sort -u >"$lint"
  echo "cross-check: instrument = committed backend/.golangci.yml + errcheck check-blank, $(scope_line "$astraw" 2), non-test"
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
  local scan_out scan_status
  case "${1:-}" in
    '')
      ratchet
      ;;
    --scan)
      # Buffered rather than streamed so the scope header can be computed from
      # the rows themselves, and so a scanner error is a FAILURE here instead of
      # an empty listing that reads as a clean tree.
      scan_out=$(mktemp) || return 1
      run_tool scan "$REPO_ROOT/$TARGET_REL" >"$scan_out" 2>&1
      scan_status=$?
      if [ "$scan_status" -ne 0 ]; then
        echo "getter-errors: FAIL - the scanner errored:" >&2
        sed 's/^/  /' "$scan_out" >&2
        rm -f "$scan_out"
        return 1
      fi
      echo "getter-errors: $(scope_line "$scan_out" 2)"
      cat "$scan_out"
      rm -f "$scan_out"
      ;;
    --self-test)
      self_test
      ;;
    --cross-check)
      cross_check
      ;;
    --update-baseline)
      run_tool counts "$REPO_ROOT/$TARGET_REL" >"$BASELINE" || return 1
      # The scope is stated here and not written INTO the baseline: a comment
      # line in that file would split into three fields and compare_counts would
      # read it as a file named "#" with a count of 0. The paths themselves carry
      # the scope instead -- they are relative to TARGET_REL, so a baseline
      # written against backend/ has an internal/ or cmd/ prefix on every row and
      # one written against backend/internal/ does not.
      echo "getter-errors: baseline rewritten -> ${BASELINE#$REPO_ROOT/} ($(scope_line "$BASELINE" 1), $(command grep '^TOTAL ' "$BASELINE"))"
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
