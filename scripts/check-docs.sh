#!/usr/bin/env bash
# scripts/check-docs.sh
#
# Structural gate for the docs restructure epic (agent-os-yg1.8).
#
# This script checks facts, not prose quality: do the expected files exist,
# is the README free of headings that moved elsewhere, do relative markdown
# links resolve, is every backend env var documented. It is written BEFORE
# the target docs exist, so on the current tree it is expected to fail —
# every check below turns green only when the task that owns it lands. A
# check that passes today for the wrong reason (e.g. a link-extraction regex
# that matches nothing) is worse than no check at all, so see the inline
# notes on `readme-clean` and `links` for how each check was proven to be
# able to fail.
#
# Dependency-free by design: only grep, sed, find, wc, test (plus bash
# builtins for string handling, arrays and loops) — no markdown parser, no
# npm packages, no network access.
#
# This environment has been observed (project memory: eslint-exit-code-via-
# rtk) to rewrite commands via a shell hook that can return a bogus exit
# code for at least one tool. `command` below forces the real binary
# regardless of what may be aliased/shimmed in the calling shell.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

CHECK_NAMES="readme-size contributing readme-clean docs-tree links env-coverage"

REQUIRED_DOCS="docs/getting-started.md
docs/how-to/deploy-production.md
docs/how-to/configure-backups.md
docs/how-to/restore-a-backup.md
docs/how-to/migrate-from-dockge.md
docs/how-to/recover-admin-access.md
docs/how-to/upgrade-and-roll-back.md
docs/reference/configuration.md
docs/reference/api.md
docs/explanation/security-model.md
docs/explanation/architecture.md"

# Headings that belong to CONTRIBUTING.md / docs/ once the restructure lands,
# and must no longer appear in README.md.
FORBIDDEN_HEADINGS="Project Structure
Quick Commands
Development
Branch protection
Versioning
Rolling back"

# env-coverage exceptions, one "VAR|reason" per line.
#
# ENV_EXCLUDE: read via os.Getenv() in backend/ but not Capstan configuration
# (OS/runtime-provided), so forcing docs/reference/configuration.md to list
# them as operator settings would be documentation produced to satisfy the
# gate rather than to inform an operator.
ENV_EXCLUDE="HOME|OS-provided; sole use is a default-path join for GitSSHKey (backend/internal/config/config.go:74), not an operator-facing setting"

# ENV_INCLUDE: real Capstan configuration that the literal os.Getenv("...")
# scan below can't see (e.g. read via a named constant instead of a string
# literal) but that must still be documented. Enforced explicitly rather
# than resolved heuristically from Go source.
ENV_INCLUDE="CAPSTAN_ALLOW_SCHEMA_DOWNGRADE|read via os.Getenv(allowSchemaDowngradeEnv) at backend/internal/database/migrations.go:614; the documented escape hatch for forward-compat schema checks (migrations.go:11-18)"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

# Portable line count: `wc -l` trimmed of leading whitespace some wc builds emit.
line_count() {
  local n
  n=$(command wc -l < "$1")
  n="${n//[[:space:]]/}"
  printf '%s' "$n"
}

# Approximate GitHub heading-anchor slugification, without a markdown parser:
# lowercase, drop everything but [a-z0-9 -], turn spaces into hyphens. Good
# enough for this repo's headings (verified against README.md's own
# self-links, e.g. "Docker Socket & Security" -> "docker-socket--security");
# does not handle duplicate-heading disambiguation (-1, -2 suffixes) since
# this repo's docs don't have duplicate headings within a file.
slugify() {
  local s="$1"
  s="${s,,}"
  s=$(printf '%s' "$s" | command sed -E 's/[^a-z0-9 -]//g')
  s="${s// /-}"
  printf '%s' "$s"
}

# Does $1 appear in file $2 as a whole token (not merely as a substring of a
# longer identifier, e.g. "PORT" inside "EXPORTED_SETTINGS")? Bounded on both
# sides by "not [A-Za-z0-9_]" or start/end of line -- deliberately loose
# about what counts as a boundary (backticks, pipes, spaces, punctuation all
# qualify) since env vars appear in markdown wrapped in all of those.
match_whole_token() {
  local needle="$1" file="$2"
  command grep -qE "(^|[^A-Za-z0-9_])${needle}([^A-Za-z0-9_]|\$)" "$file"
}

# Does $2 (an already-slugified anchor, no leading '#') match a heading in file $1?
heading_exists() {
  local file="$1" anchor="$2" heading slug
  while IFS= read -r heading; do
    [ -z "$heading" ] && continue
    slug=$(slugify "$heading")
    if [ "$slug" = "$anchor" ]; then
      return 0
    fi
  done < <(command grep -E '^#+[[:space:]]' "$file" | command sed -E 's/^#+[[:space:]]*//')
  return 1
}

# ---------------------------------------------------------------------------
# checks
# ---------------------------------------------------------------------------

check_readme_size() {
  local file="$REPO_ROOT/README.md"
  if [ ! -f "$file" ]; then
    echo "FAIL: readme-size - README.md not found at repo root"
    return 1
  fi
  local lines
  lines=$(line_count "$file")
  if [ "$lines" -lt 200 ]; then
    echo "PASS: readme-size - README.md is $lines lines (< 200)"
    return 0
  fi
  echo "FAIL: readme-size - README.md is $lines lines (must be under 200)"
  return 1
}

check_contributing() {
  local contrib="$REPO_ROOT/CONTRIBUTING.md"
  local testing="$REPO_ROOT/TESTING.md"
  local ok=0 reasons=""

  if [ ! -f "$contrib" ]; then
    reasons="${reasons}CONTRIBUTING.md does not exist at repo root; "
    ok=1
  else
    local lines
    lines=$(line_count "$contrib")
    if [ "$lines" -le 30 ]; then
      reasons="${reasons}CONTRIBUTING.md is only $lines lines (must exceed 30); "
      ok=1
    fi
  fi

  if [ -f "$testing" ]; then
    reasons="${reasons}TESTING.md still exists at repo root (must be moved/removed); "
    ok=1
  fi

  if [ "$ok" -eq 0 ]; then
    echo "PASS: contributing - CONTRIBUTING.md exists and is substantive; TESTING.md absent from root"
    return 0
  fi
  echo "FAIL: contributing - $reasons"
  return 1
}

check_readme_clean() {
  local file="$REPO_ROOT/README.md"
  if [ ! -f "$file" ]; then
    echo "FAIL: readme-clean - README.md not found at repo root"
    return 1
  fi
  local found="" heading
  while IFS= read -r heading; do
    [ -z "$heading" ] && continue
    if command grep -qE "^#+[[:space:]]+${heading}[[:space:]]*\$" "$file"; then
      found="${found}${heading}; "
    fi
  done <<< "$FORBIDDEN_HEADINGS"

  if [ -z "$found" ]; then
    echo "PASS: readme-clean - README.md contains none of the contributor-only headings"
    return 0
  fi
  echo "FAIL: readme-clean - README.md still contains heading(s): $found"
  return 1
}

check_docs_tree() {
  local ok=0 reasons="" rel
  while IFS= read -r rel; do
    [ -z "$rel" ] && continue
    local f="$REPO_ROOT/$rel"
    if [ ! -f "$f" ]; then
      reasons="${reasons}missing: $rel; "
      ok=1
      continue
    fi
    local lines
    lines=$(line_count "$f")
    if [ "$lines" -le 30 ]; then
      reasons="${reasons}stub ($lines lines): $rel; "
      ok=1
    fi
  done <<< "$REQUIRED_DOCS"

  if [ "$ok" -eq 0 ]; then
    echo "PASS: docs-tree - all required docs/ pages exist and exceed 30 lines"
    return 0
  fi
  echo "FAIL: docs-tree - $reasons"
  return 1
}

# Resolves one $2=url found in $1=source file, updating the caller's `ok`
# and `reasons` (bash dynamic scoping: these are locals in check_links and
# this is only ever called from there, in the same shell -- no subshell).
resolve_link() {
  local src="$1" url="$2" path anchor target rel_src rel_target

  case "$url" in
    http://*|https://*|mailto:*)
      return 0
      ;;
  esac

  rel_src="${src#"$REPO_ROOT"/}"
  path="${url%%#*}"
  case "$url" in
    *#*) anchor="${url#*#}" ;;
    *) anchor="" ;;
  esac

  if [ -z "$path" ]; then
    target="$src"
  else
    target="$(dirname "$src")/$path"
  fi
  rel_target="${target#"$REPO_ROOT"/}"

  if [ ! -f "$target" ]; then
    reasons="${reasons}${rel_src}: link '$url' does not resolve (no file at $rel_target); "
    ok=1
    return 0
  fi

  if [ -n "$anchor" ] && ! heading_exists "$target" "$anchor"; then
    reasons="${reasons}${rel_src}: anchor '#$anchor' not found as a heading in $rel_target; "
    ok=1
  fi
}

check_links() {
  local ok=0 reasons="" total_links=0
  local -a sources=()

  [ -f "$REPO_ROOT/README.md" ] && sources+=("$REPO_ROOT/README.md")
  [ -f "$REPO_ROOT/CONTRIBUTING.md" ] && sources+=("$REPO_ROOT/CONTRIBUTING.md")
  if [ -d "$REPO_ROOT/docs" ]; then
    while IFS= read -r f; do
      sources+=("$f")
    done < <(command find "$REPO_ROOT/docs" -type f -name '*.md')
  fi

  # Extraction is line-by-line (rather than grep over the whole file) so we
  # can track fence state: link-like syntax inside a ``` or ~~~ fenced code
  # block is example text, not a real markdown link, and must not be scanned.
  #
  # Trimming and matching use bash's builtin [[ =~ ]] / parameter expansion
  # instead of a sed/grep subprocess per line: a ~1,000-line doc times
  # several doc sources means thousands of forked processes if each line
  # spawns one, which measured 6.6x slower than the whole-file grep this
  # replaced. Builtins keep the per-line cost to zero forks.
  local src line trimmed url rel_src marker rest match is_footnote
  local in_fence=0 fence_char="" fence_open_line=0 lineno
  # bash's own parser trips on a literal \( \) inside a [[ =~ <pattern> ]]
  # written inline, so the patterns live in variables and are referenced
  # unquoted (quoting here would make =~ match them as a literal string).
  local inline_link_re='\[[^]]*\]\(([^)]+)\)'
  local refdef_re='^\[[^]]+\]:[[:space:]]*<?([^[:space:]>]+)>?.*'
  for src in "${sources[@]}"; do
    in_fence=0
    fence_char=""
    fence_open_line=0
    lineno=0
    rel_src="${src#"$REPO_ROOT"/}"
    while IFS= read -r line || [ -n "$line" ]; do
      lineno=$((lineno + 1))

      if [[ "$line" =~ ^[[:space:]]*(.*)$ ]]; then
        trimmed="${BASH_REMATCH[1]}"
      else
        trimmed="$line"
      fi

      # Fence delimiters: ``` or ~~~, 3+ chars, optionally indented, opening
      # may carry an info string (e.g. ```bash). A fence only closes against
      # its own character -- a ~~~ block is not closed by ```.
      if [[ "$trimmed" =~ ^(\`\`\`+|~~~+) ]]; then
        marker="${BASH_REMATCH[1]}"
        if [ "$in_fence" -eq 1 ]; then
          [ "${marker:0:1}" = "$fence_char" ] && { in_fence=0; fence_char=""; fence_open_line=0; }
        else
          in_fence=1
          fence_char="${marker:0:1}"
          fence_open_line="$lineno"
        fi
        continue
      fi
      [ "$in_fence" -eq 1 ] && continue

      # Footnote definitions ([^1]: prose...) are not reference-style links --
      # the marker matches that regex and its body is prose, not a URL -- but
      # the prose can still hold a genuine inline link, so only the
      # reference-definition pass below is skipped for these lines, not the
      # inline pass. Matched against $trimmed so an indented footnote is
      # still recognized.
      is_footnote=0
      [[ "$trimmed" =~ ^\[\^ ]] && is_footnote=1

      # Inline links: [text](url). Looped so a line with more than one link
      # is fully scanned, same as the old per-line grep -oE did.
      rest="$line"
      while [[ "$rest" =~ $inline_link_re ]]; do
        url="${BASH_REMATCH[1]}"
        match="${BASH_REMATCH[0]}"
        total_links=$((total_links + 1))
        resolve_link "$src" "$url"
        rest="${rest#*"$match"}"
      done

      # Reference-style link definitions: [label]: url  (optionally <url>,
      # optionally followed by a "title" or 'title' -- title text is discarded).
      if [ "$is_footnote" -eq 0 ] && [[ "$line" =~ $refdef_re ]]; then
        url="${BASH_REMATCH[1]}"
        total_links=$((total_links + 1))
        resolve_link "$src" "$url"
      fi
    done < "$src"

    if [ "$in_fence" -eq 1 ]; then
      reasons="${reasons}${rel_src}: unterminated code fence opened at line $fence_open_line (rest of file not checked for links); "
      ok=1
    fi
  done

  if [ "$ok" -eq 0 ]; then
    echo "PASS: links - all $total_links relative link(s)/anchor(s)/reference-definition(s) resolved across README.md, CONTRIBUTING.md and docs/**"
    return 0
  fi
  echo "FAIL: links - $reasons"
  return 1
}

check_env_coverage() {
  local config_doc="$REPO_ROOT/docs/reference/configuration.md"
  local backend_dir="$REPO_ROOT/backend"

  if [ ! -d "$backend_dir" ]; then
    echo "FAIL: env-coverage - backend/ directory not found"
    return 1
  fi

  # Literal os.Getenv("NAME") calls, excluding _test.go files.
  local -a vars=()
  local line fname var already
  while IFS= read -r line; do
    fname="${line%%:*}"
    case "$fname" in
      *_test.go) continue ;;
    esac
    var=$(printf '%s\n' "$line" | command sed -E 's/^[^:]+:[0-9]+:.*Getenv\("([A-Za-z_][A-Za-z0-9_]*)"\).*/\1/')
    already=0
    for v in "${vars[@]:-}"; do
      [ "$v" = "$var" ] && already=1 && break
    done
    [ "$already" -eq 0 ] && vars+=("$var")
  done < <(command grep -rnE 'os\.Getenv\("[A-Za-z_][A-Za-z0-9_]*"\)' "$backend_dir" --include='*.go')

  # Non-literal Getenv(IDENT) calls: extraction can't resolve the variable
  # name without evaluating Go, so these are reported but not enforced. This
  # is the extraction's known blind spot (see e.g.
  # backend/internal/database/migrations.go's allowSchemaDowngradeEnv) —
  # surfaced here rather than silently dropped.
  local -a nonliteral=()
  while IFS= read -r line; do
    fname="${line%%:*}"
    case "$fname" in
      *_test.go) continue ;;
    esac
    nonliteral+=("${line#"$REPO_ROOT"/}")
  done < <(command grep -rnE 'os\.Getenv\(' "$backend_dir" --include='*.go' \
             | command grep -vE 'os\.Getenv\("[A-Za-z_][A-Za-z0-9_]*"\)')

  if [ "${#vars[@]}" -eq 0 ]; then
    echo "FAIL: env-coverage - no literal os.Getenv(\"...\") calls found in backend/ (extraction may be broken)"
    return 1
  fi

  # Drop excluded (OS/runtime-provided) vars.
  local -a filtered=()
  local v exvar exreason skip
  for v in "${vars[@]}"; do
    skip=0
    while IFS='|' read -r exvar exreason; do
      [ -z "$exvar" ] && continue
      if [ "$v" = "$exvar" ]; then
        skip=1
        break
      fi
    done <<< "$ENV_EXCLUDE"
    [ "$skip" -eq 0 ] && filtered+=("$v")
  done
  vars=("${filtered[@]}")

  # Add included vars (real config, invisible to the literal scan) that
  # aren't already present.
  local incvar increason already
  while IFS='|' read -r incvar increason; do
    [ -z "$incvar" ] && continue
    already=0
    for v in "${vars[@]:-}"; do
      [ "$v" = "$incvar" ] && already=1 && break
    done
    [ "$already" -eq 0 ] && vars+=("$incvar")
  done <<< "$ENV_INCLUDE"

  local nonliteral_note=""
  if [ "${#nonliteral[@]}" -gt 0 ]; then
    nonliteral_note=" [note: ${#nonliteral[@]} non-literal os.Getenv() call(s) found, checked via ENV_INCLUDE where applicable: ${nonliteral[*]}]"
  fi

  if [ ! -f "$config_doc" ]; then
    echo "FAIL: env-coverage - docs/reference/configuration.md does not exist; ${#vars[@]} var(s) to document (${vars[*]}) are undocumented${nonliteral_note}"
    return 1
  fi

  local -a missing=()
  for v in "${vars[@]}"; do
    if ! match_whole_token "$v" "$config_doc"; then
      missing+=("$v")
    fi
  done

  if [ "${#missing[@]}" -gt 0 ]; then
    echo "FAIL: env-coverage - variable(s) missing from docs/reference/configuration.md: ${missing[*]}${nonliteral_note}"
    return 1
  fi

  echo "PASS: env-coverage - all ${#vars[@]} required var(s) documented in docs/reference/configuration.md${nonliteral_note}"
  return 0
}

# ---------------------------------------------------------------------------
# dispatch
# ---------------------------------------------------------------------------

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [--check=<name>]

Valid check names:
  readme-size    README.md is under 200 lines
  contributing   CONTRIBUTING.md exists and is substantive; TESTING.md absent from root
  readme-clean   README.md has no contributor-only headings
  docs-tree      required docs/ pages exist and are non-stub
  links          relative markdown links and anchors resolve
  env-coverage   every backend os.Getenv var is documented

With no arguments, all checks run and a summary is printed.
USAGE
}

run_check() {
  case "$1" in
    readme-size)  check_readme_size ;;
    contributing) check_contributing ;;
    readme-clean) check_readme_clean ;;
    docs-tree)    check_docs_tree ;;
    links)        check_links ;;
    env-coverage) check_env_coverage ;;
    *) return 2 ;;
  esac
}

main() {
  local requested=""

  if [ "$#" -gt 0 ]; then
    case "$1" in
      --check=*) requested="${1#--check=}" ;;
      -h|--help) usage; exit 0 ;;
      *)
        echo "Unknown argument: $1" >&2
        usage
        exit 2
        ;;
    esac
  fi

  if [ -n "$requested" ]; then
    case " $CHECK_NAMES " in
      *" $requested "*) ;;
      *)
        echo "Unknown check: '$requested'" >&2
        usage
        exit 2
        ;;
    esac
    run_check "$requested"
    exit $?
  fi

  local failed=0 name
  for name in $CHECK_NAMES; do
    run_check "$name" || failed=$((failed + 1))
  done

  set -- $CHECK_NAMES
  local total="$#"

  echo "----"
  echo "Summary: $((total - failed))/$total checks passed"
  [ "$failed" -eq 0 ]
}

main "$@"
