# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Agent OS

This repository has Agent OS installed. The commands it provides, and which one to use
for a given piece of work, are in the installer-managed block at the end of this file.
That block is regenerated on every rollout, so it is the authoritative routing table —
prefer it over any command list you have seen elsewhere.

Agent OS itself lives in `.agent-os/`, which is gitignored and therefore ABSENT from git
worktrees. Workflow procedures are in `.agent-os/instructions/core/`, standards in
`.agent-os/standards/`, and the installed version is recorded in `.agent-os/config.yml`.
`.claude/` is gitignored too, so `.claude/commands/` and `.claude/settings.json` are
likewise absent from a worktree; read them from the main tree.

## This repository

Capstan is a web-based Docker Compose stack manager with Git integration, backups, and a
built-in terminal. It ships as a single multi-arch container serving both the API and the
web UI on one port.

- **Backend**: Go, in `backend/`. Embedded SQLite via `modernc.org/sqlite`.
- **Frontend**: React with TypeScript, in `frontend/`. TailwindCSS for styling,
  Radix-based UI primitives, Lucide icons.
- **Hosting**: self-hosted Docker Compose (`docker-compose.yaml`,
  `docker-compose.prod.yaml`).

Read the toolchain and dependency versions from `backend/go.mod` and
`frontend/package.json` rather than from prose here. Prose goes stale; the manifests do
not.

### Code style

- **Go**: whatever `gofmt` produces. Go's own naming applies — exported identifiers are
  `PascalCase`, unexported are `camelCase`. Never `snake_case` for Go identifiers.
- **TypeScript / React**: 2-space indentation. `camelCase` for values and functions,
  `PascalCase` for components and types.
- **Constants**: `UPPER_SNAKE_CASE` where the language convention allows it.
- **Comments**: explain "why", not "what". Keep them concise and accurate.

### Development principles

- Keep implementations simple and avoid over-engineering.
- Optimise for readability over micro-optimisation.
- Follow DRY — extract repeated logic into reusable helpers or components.
- Choose well-maintained, widely used libraries when adding dependencies.
- Keep file organisation and naming consistent with what is already there.

## Beads: closing a bug bead

A bug bead does not close on a passing fix alone. The fix is scoped to one diff, but
most defects in this codebase are scoped to a class: the same mistake repeated at
every call site that shares its shape. The 2026-08-21..09-04 bug corpus showed this
concretely, five families covering 22 of 32 filed bugs, and in every family the
sibling was found only after the first instance was already fixed and merged, each
one costing a full bead cycle that a grep at close time would have caught in
minutes. Closing a bug bead therefore requires a class sweep, not just a green test.

### The required close-time block

Every bug bead's close reason states four fields:

1. **Class statement** — one sentence naming the defect class, not this instance
   ("a WebSocket handler that upgrades but doesn't guarantee close on every exit
   path", not "dashboard.go leaks a connection").
2. **Sweep command** — the exact command run, receiver-agnostic (see below).
3. **Verbatim output** — trimmed to the relevant lines, not paraphrased.
4. **Verdict** — either "0 further sites" or the list of follow-up bead IDs filed
   for the sites the sweep found.

A count in any of these four fields is pinned to the SHA it was measured on.
Re-measure at close time and name the SHA: `agent-os-nho7`'s brief carried
"10 lines, 5 in class" from `3dbaef2`, and the same command returned 14 and 8 on
`25f192c` three commits later.

### The one principle behind it

A zero, or a short list, is trustworthy only when the instrument has been run in
the direction where it MUST report hits, and did. An unproven zero is not evidence
of absence, it's evidence the search never ran. A pattern cannot tell you it is
blind; an arm pointed the other way can. This shows up in four distinct ways, and
receiver-only fixes for the first one still miss the other three:

- **Name and receiver variation.** A sweep pinned to one identifier misses
  siblings that spell the same thing differently. `agent-os-iz9w`: grep for
  `defer conn.Conn.Close()` misses `logs.go`'s `defer conn.Close()`, a different
  receiver on the same underlying bug class. `agent-os-jtax`: `durableRun`'s
  methods use receiver `r`, not `dr`, so `grep "func (dr \*durableRun)"` returns a
  false zero while the real methods sit under `func (r *durableRun)`. `logs.go`
  is worth reading closed, not skimmed, for the same reason: it has two different
  receiver expressions in one file, an undeferred `wsConn.Conn.Close()` inside an
  error-return branch and the real guard, a deferred `conn.Close()` a few lines
  later. A sweep that stops at the first hit, or a reader who eyeballs only that
  line, misclassifies a correct file as defective.
- **Truncated multi-value fields.** Piping a wrapped or multi-line field through
  `head` reads only its first entry. A `FILES:` field wrapped across several
  paths, read with `grep "^ *FILES:" | head -2`, returned only the first path of
  four affected beads and produced two wrong scope conclusions.
- **The command didn't run as typed.** The shell environment can silently rewrite
  or swallow a command before it reaches the tool it names. `grep -rln "bd show"
  --include=*.md .` returned nothing in this repo because a shell hook mangled
  the compound grep; `command grep -rln "bd show" --include=*.md .` returned 10
  files. A tool wrapping `grep` in this repo means every sweep command in a close
  reason must use `command grep`, not bare `grep`.
- **Scope too narrow, result generalized.** The sweep runs correctly and returns
  a true answer about the wrong scope, then gets reported as if it covered more
  than it did. A `Loading` string was checked in one file and reported as unique
  across the app; it wasn't: `command grep -rln "Loading" frontend/src
  --include=*.tsx` returns 58 files. The command worked, so neither
  receiver-agnosticism nor `command grep` catches this one — the fix is stating
  the scope actually swept, not the scope the claim implies.

Because of the third failure mode, every sweep in a close reason runs as
`command grep` (or the local equivalent that bypasses shell rewriting), and the
command printed in the close reason is the command that was run: copy it from the
shell, never retype it. Because of the fourth, a verdict names the exact directory
or file set the sweep covered, not a generalization from it.

Because of the first and second, a positive control is not enough. A control that
fires on one known instance proves the pattern matches SOMETHING; it does not prove
it covers the CLASS. `agent-os-o1jp.7` is the proof: its
`NewWithMigrations(":memory:")` control fired, and the class was 33 sites, not 26,
the extra seven sitting under the sibling constructor
`NewWithMigrationsAndEncryptor`. So every zero or short list carries a **negative
arm**, in three parts:

1. **RECALL over the known set.** Run the same instrument against the pre-fix
   state, a `git show <base>:<file>` or a `git archive <base>`, and require it to
   report EVERY site the diff fixed, by line, not one of them. Cheap, mechanical,
   always possible. A control that fires on a single site is what let
   `directories.go:219` through.
2. **CLASS COVERAGE over the unknown set.** Run a deliberately WIDER sibling of
   the verdict instrument (receiver-agnostic, anchor-relaxed, one identifier
   looser, or a different syntactic route to the same thing, such as a struct tag
   where the first read a literal map key) and READ its extra hits one by one,
   dispositioning each. Every class this repo has found larger was found larger
   this way and never by part 1. Count the extras only after you have read them:
   an over-wide instrument fires correctly on everything it matches, so it
   manufactures false positives that no arm will catch for you (`agent-os-8ett`).
3. **BOUND THE AGGREGATION.** If the instrument decides membership by proximity
   (a `-A6`/`-A12` window, a "within N lines" rule, a count over a shared buffer),
   state the rule and show it truncating. A window must stop at the next call of
   the same shape, or it borrows the NEXT site's guard and reports a defective
   site as clean: that is exactly what made `handlers/directories.go:219` read
   GUARDED in `agent-os-7lg1`'s close reason, in a handoff, and in the
   `agent-os-3h9x` brief, where it was nominated as the positive control while
   being a third unguarded site. Both arms above were present and neither could
   see it. A corollary: never nominate an in-class site as a control; it flips
   when the fix lands, which is the opposite of a control.

A sweep that has not been shown to fire, that has not been probed wider than its
own verdict, or whose membership rule has never been shown to stop where it
claims, is not a sweep. It is an assumption with a command line attached.

<!-- BEGIN AGENT OS — managed by install-agent-os.sh, do not hand-edit -->
## Agent OS (7.8.9) — the six commands, and which one to use

**There is no auto-discovery. This table is the routing.**

| The work is… | Invoke |
|---|---|
| an existing codebase with no Agent OS product docs | `/analyze-product` |
| a new product to plan | `/plan-product` |
| a feature carrying **product decisions** (new user-facing behaviour) | `/create-spec` |
| beads or tasks to execute — **or a freeform goal**: migration, refactor, cleanup, "make X work" | **`/run`** |
| end-to-end tests to run or repair | `/e2e` |
| dead code, unused deps, type errors, stale references | `/sweep` |

**`/run` picks its own weight.** Invoking it does not commit you to worktrees or subagents: it
triages first and recommends **Direct** for one small task. A one-file bug fix is a `/run` job;
you will just be told to do it directly.

If you are about to dispatch subagents, review a deliverable, or merge and clean up a branch by
hand, that is `/run`'s job — it carries the worktree isolation, the dispatch contract, the freeze
and the re-execution gate. Full procedure: `.agent-os/instructions/core/run.md`.

These six are the whole set. Any other Agent OS command you have seen named — `create-tasks`,
`execute-tasks`, `upgrade-spec`, `enhance-existing`, `validate-browser`, `validate-quality`,
`validate-system`, `orchestrate` — was removed in v7.0.0 and is deleted on every rollout.
<!-- END AGENT OS -->
