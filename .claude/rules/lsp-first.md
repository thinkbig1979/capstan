# Semantic-First Code Navigation

<!-- Instantiated from .agent-os/templates/rules/lsp-first.md.template by
     install-agent-os.sh. PROJECT-OWNED: rollouts never overwrite this file.
     When this project's MCP servers change, update the tool table by hand —
     ready-to-paste mappings for other servers live in the template. -->

## Principle

Prefer semantic code intelligence over raw text search for symbol navigation:
definitions, references, symbol overviews, implementations, and call tracing.
Grep/Glob are the fallback when semantic tools return nothing or the target is
non-symbol text (config, markdown, string literals, comments).

If no semantic server is connected in this project, text search IS the primary
navigation tool — using Grep is then correct, not a violation.

## Worktree caveat

Semantic tools that resolve paths against a registered project root are READ-ONLY
inside a git worktree: their edit operations write to the main tree, not the
worktree. Workers in worktrees use plain path-based edit tools and keep semantic
tools for reads only (see .agent-os/instructions/core/run.md, step 2).

## Tool table (project slot — reflects the servers connected here)

<!-- The installer keeps only the mapping(s) for servers it detects; correct
     this section if detection guessed wrong. -->
### cclsp (LSP bridge)

| Task | Tool |
|------|------|
| Find definition | `find_definition` |
| Find references | `find_references` |
| Symbol search | `find_workspace_symbols` |
| Implementations | `find_implementation` |
| Call hierarchy | `get_incoming_calls` / `get_outgoing_calls` |
| Type info | `get_hover` |
| Diagnostics | `get_diagnostics` |
### codebase-memory-mcp

| Task | Tool |
|------|------|
| Find functions/classes/routes | `search_graph` |
| Exact symbol source | `get_code_snippet` |
| Call chains / data flow | `trace_path` |
| Architecture overview | `get_architecture` |
| Complex graph queries | `query_graph` |
| Text search (graph-augmented) | `search_code` |

## Discovery by graph, verdict by grep (added 2026-09-20)

The two halves of a class sweep want different instruments, and using one for
both is what cost a full bead cycle on 2026-09-20.

**Disposition questions — use the graph.** "Who calls this?" "Where does this
value end up?" "Does this output reach a parser or a log blob?" `trace_path`
answers these directly, and `mode: "data_flow"` prints the ARGUMENT EXPRESSION
at each hop, so it shows the value flowing, not merely that two functions are
called from the same place.

Worked example, the exact question `agent-os-vwi7` answered wrongly by reasoning
about what "logs output" is FOR rather than following it:

    trace_path(function_name: "Logs",    direction: "inbound")
      -> callers_total: 1, GetLogs
    trace_path(function_name: "GetLogs", direction: "outbound", mode: "data_flow")
      -> parseLogLines  hop 1  args: [ output, containerFilter ]
         services.Logs  hop 1  args: [ *stack, tail ]

`output` is what `Logs` returns and it is the first argument to `parseLogLines`.
Two calls. The manual route was a widened grep sweep plus reading eleven call
sites, and it only happened on the SECOND bead in the class. See
[[name-is-not-disposition-follow-callers]].

**Verdict questions — use `command grep`.** "0 further sites in scope X" must be
a reproducible command with a positive control, because that is what a close
reason has to carry. The graph cannot supply it: `index_status`'s own coverage
note says absence from the graph is NOT a completeness guarantee, and this repo
had 11 `parse_partial` files when this was written. A graph zero is not evidence
of absence.

So: **graph to find and disposition, grep to prove and to cite.** Never the
reverse, and never grep alone for a "does this reach a parser" question.

## This file DOES reach workers, and it is the only thing under `.claude/` that does

It did not until 2026-09-21. `.claude/` was gitignored wholesale, so this file
was absent from every git worktree — which is where dispatched workers run — and
the rule above reached the orchestrator in the main tree and nobody else, for its
whole existence. `.gitignore` now carves `rules/` out (`.claude/*` plus
`!.claude/rules/`, because git cannot re-include a file inside an excluded
directory), so this file is tracked and a worktree gets it.

**Nothing else under `.claude/` is tracked**, and that is deliberate: `agents/`,
`commands/`, `hooks/`, `skills/`, `settings.local.json`, `state/` and
`worktrees/` are all still ignored. So a rule written anywhere else under
`.claude/` still reaches nobody, and the same holds for all of `.agent-os/`.
Doctrine a WORKER must obey belongs in the tracked root `CLAUDE.md` or here;
doctrine the ORCHESTRATOR applies can live under `.agent-os/`. When in doubt,
inline it in the dispatch brief — a brief is the one artifact guaranteed to
arrive. See [[gitignored-dirs-absent-in-worktrees]].
