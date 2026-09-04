# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Agent OS Framework

This is an Agent OS configuration repository that provides structured workflows for AI agents to build products systematically. Agent OS is designed to help agents follow consistent patterns for product planning, specification creation, task management, and execution.

### Core Commands

Agent OS provides slash commands accessible through markdown files in the `.claude/commands/` directory:

**Core Workflow Commands:**
- `plan-product.md` - Plan new products and install Agent OS in codebases
- `analyze-product.md` - Analyze existing codebases and install Agent OS
- `create-spec.md` - Create detailed feature specifications with technical requirements
- `create-tasks.md` - Break down specifications into executable tasks
- `execute-tasks.md` - Execute individual tasks from the task list using **Orchestrated Parallel Execution**

**Maintenance & Upgrade Commands:**
- `upgrade-spec.md` - Fully modernize existing specs to v2.1 standards (complete re-evaluation and regeneration)
- `enhance-existing.md` - Add validation requirements to existing specs (preserves structure, adds validation)

**Validation Commands:**
- `validate-browser.md` - Mandatory browser testing for web components
- `validate-quality.md` - Comprehensive quality validation for specifications
- `validate-system.md` - System-wide validation and health checks

Each command references detailed instructions in `instructions/core/` that provide step-by-step workflows.

### Orchestrated Parallel Execution (v2.0)

Agent OS v2.0 introduces **Orchestrated Parallel Task Execution**, providing:

- **60-80% faster task completion** through intelligent parallel processing
- **Specialist agent coordination** for testing, implementation, integration, quality, security, and documentation
- **Advanced error handling** with automatic recovery and intelligent escalation
- **Context optimization** for maximum efficiency and relevance
- **Deliverable Verification (v2.5+)**: Mandatory verification of all files and deliverables before task completion

The orchestrated system uses a master `task-orchestrator` that coordinates multiple specialist agents working in parallel, each with optimized context windows and focused responsibilities.

### Deliverable Verification Framework (v2.5+)

Agent OS v2.5+ includes **Mandatory Deliverable Verification** that ensures orchestrators verify all subagent deliverables before marking tasks complete:

- **100% file verification** - All expected files verified to exist using Read tool
- **Test execution validation** - All tests verified to pass using test-runner
- **Acceptance criteria evidence** - All criteria verified with tangible proof
- **Integration verification** - All integration points verified to work correctly
- **Automatic blocking** - Tasks cannot be marked complete without passing verification

### Task File Structure Optimization (v2.1)

Agent OS v2.1 introduces **Optimized Task File Structure**, providing:

- **90%+ reduction in context consumption** through split file architecture
- **Lightweight master tasks.md** (~50-100 lines) for quick overview and task selection
- **Individual task detail files** (tasks/task-*.md) loaded only when executing specific tasks
- **Scales efficiently** to 50+, 100+, or 200+ tasks without context bloat

### Automatic Quality Enforcement (v2.2+)

Agent OS v2.2+ includes an **Automatic Quality Hooks System** that validates and auto-fixes every file write and edit operation using **Claude Code's native PostToolUse hooks**:

- **Zero manual intervention** - formatters, linters, and validators run automatically after every file operation
- **7 integrated validators** - syntax, formatting, linting, imports, type checking, security, test generation
- **Auto-fix capabilities** - automatically corrects formatting, imports, and safe lint issues
- **60% context token savings** - catches errors early, preventing iteration cycles
- **~0.8s overhead per file** - parallel execution optimized for performance
- **Language-agnostic** - supports JavaScript, TypeScript, Python, CSS, JSON, YAML, Markdown, and more
- **Native integration** - Uses Claude Code's built-in hook system for reliable, automatic triggering

Every file creation and modification automatically triggers validation through Claude Code's PostToolUse hooks (configured in `.claude/settings.json`). Issues are detected and often auto-fixed immediately, ensuring consistent code quality without manual effort.

### Configuration Structure

The repository follows this organizational pattern:

```
.agent-os/
├── config.yml              # Agent OS version and project type configuration
├── instructions/           # Detailed workflow instructions
│   ├── core/              # Main workflow instructions
│   ├── meta/              # Pre/post-flight checks
│   ├── utilities/         # Utility guides and helpers
│   └── agents/            # 28 execution role definitions
├── standards/             # Code and development standards
│   ├── best-practices.md  # Development guidelines
│   ├── global/            # Language-specific style guides
│   ├── frontend/          # Frontend standards
│   ├── backend/           # Backend standards
│   └── testing/           # Testing standards
└── templates/             # Project templates
```

### Development Standards

#### Tech Stack Defaults
- **App Framework**: Next.js latest stable
- **Language**: TypeScript
- **Database**: PostgreSQL 17+
- **ORM**: Active Record
- **Frontend**: React latest stable
- **Build Tool**: Vite
- **Package Manager**: pnpm
- **Node Version**: 22 LTS
- **CSS**: TailwindCSS 4.0+
- **UI Components**: shadcn latest
- **Icons**: Lucide React components
- **Hosting**: Self-hosted Docker Compose stacks

#### Code Style Guidelines
- **Indentation**: 2 spaces (never tabs)
- **Methods/Variables**: snake_case
- **Classes/Modules**: PascalCase
- **Constants**: UPPER_SNAKE_CASE
- **Strings**: Single quotes ('') unless interpolation needed
- **Comments**: Explain "why" not "what", keep concise and accurate

#### Development Principles
- Keep implementations simple and avoid over-engineering
- Optimize for code readability over micro-optimizations
- Follow DRY principles - extract repeated logic to reusable components
- Choose well-maintained, popular libraries when adding dependencies
- Maintain consistent file organization and naming conventions

#### Automatic Quality Assurance
- **Quality hooks run automatically** on every file write and edit via Claude Code's native PostToolUse hooks
- **No manual validation needed** - syntax, formatting, linting, security checks happen automatically
- **Auto-fix enabled** - code formatting, import organization, and safe lint issues are corrected on-the-fly
- **Test generation** - basic test scaffolding is created for new source files
- **Configured in `.claude-settings.json`** - uses Claude Code's built-in hook system for reliability

### Workflow Integration

When working with this Agent OS installation:

1. Use slash commands (e.g., `/execute-tasks`) which are in `.claude/commands/`
2. Each command will reference the appropriate instruction file in `instructions/core/`
3. Follow the standards defined in `standards/` for consistent code quality
4. The configuration in `config.yml` determines which agents are enabled and project structure

This framework enables systematic, repeatable workflows for product development while maintaining consistency across different projects and team members.

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

### The one principle behind it

A zero, or a short list, is trustworthy only when a positive control shows the
instrument actually fires on the pattern it's meant to catch. An unproven zero is
not evidence of absence, it's evidence the search never ran. This shows up in three
distinct ways, and receiver-only fixes for the first one still miss the other two:

- **Name and receiver variation.** A sweep pinned to one identifier misses
  siblings that spell the same thing differently. `agent-os-iz9w`: grep for
  `defer conn.Conn.Close()` misses `logs.go`'s `defer conn.Close()`, a different
  receiver on the same underlying bug class. `agent-os-jtax`: `durableRun`'s
  methods use receiver `r`, not `dr`, so `grep "func (dr \*durableRun)"` returns a
  false zero while the real methods sit under `func (r *durableRun)`.
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

Because of the third failure mode, every sweep in a close reason runs as
`command grep` (or the local equivalent that bypasses shell rewriting). Because of
the first and second, every zero or short list needs a positive control run
alongside it: plant or point at one known instance of the pattern, confirm the
same command catches it, and only then trust a zero returned elsewhere. A sweep
that has not been shown to fire is not a sweep, it's an assumption with a command
line attached.
