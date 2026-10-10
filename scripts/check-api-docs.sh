#!/usr/bin/env bash
# scripts/check-api-docs.sh
#
# CI guard for docs/reference/api.md (agent-os-yg1.5): extracts every route
# registered on the Gin router in backend/internal/handlers/*.go and fails if
# a route is present in neither that doc nor ROUTE_ALLOW below.
#
# There is no OpenAPI spec in backend/ (swaggo annotations or huma would be a
# reasonable follow-up if the API grows a consumer beyond the bundled web UI)
# so docs/reference/api.md is hand-written and drifts from the handlers on
# its own. This script is the cheap alternative: it doesn't validate request/
# response shapes, only that every registered METHOD+PATH is mentioned.
#
# EXTRACTION METHOD AND ITS KNOWN LIMITATION:
# Each handler file has one or more `func (h *XHandler) RegisterYRoutes(group
# *gin.RouterGroup, ...)` methods containing flat sequences of
# `group.METHOD("path", handlerFunc)` calls (verified against every handler
# file in this repo at the time this script was written -- none nest a
# route-registration call inside an `if`/closure/loop). The path each such
# method mounts under is NOT written next to the call: it comes from which
# *gin.RouterGroup variable main.go passes in (e.g. `protected.Group("/git")`
# then `gitHandler.RegisterRoutes(gitGroup)`). A dependency-free bash script
# cannot resolve that wiring in general, so PREFIX_MAP below is a maintained
# table -- file:FuncName -> mount prefix -- each row cited to the exact
# backend/cmd/server/main.go call site it reflects, the same pattern
# check-docs.sh already uses for ENV_INCLUDE/ENV_EXCLUDE. If main.go is
# changed to mount a RegisterRoutes call under a different group, this table
# goes stale silently: it will keep computing the OLD prefix and routes may
# pass or fail this check for the wrong reason. There is no static check for
# that here -- a reviewer changing a Group(...) call in main.go needs to
# check this table by hand. This mirrors check-docs.sh's slugify() limitation
# note: a known, documented gap rather than a hidden one.
#
# A handful of routes are registered directly in main.go rather than inside
# any handler's RegisterRoutes method (currently just one: the compose-lint
# route below main.go's `composeGroup := protected.Group("/compose")`). These
# are listed in ROUTE_INCLUDE, same shape as PREFIX_MAP's citations.
#
# Static file routes (/assets/*, /fonts/*, /vite.svg) and the SPA NoRoute
# fallback are registered via .Static()/.StaticFile()/.NoRoute(), not
# .GET()/.POST()/etc., so the extraction below does not see them at all --
# they need no ROUTE_ALLOW entry because there is nothing to allow.
#
# Dependency-free by design: only grep, sed, awk, find, wc, test (plus bash
# builtins) -- no markdown parser, no Go AST, no network access. Matches the
# house style of scripts/check-docs.sh, this epic's other gate script.
#
# This environment has been observed (project memory: eslint-exit-code-via-
# rtk) to rewrite commands via a shell hook that can return a bogus exit code
# for at least one tool. `command` below forces the real binary regardless of
# what may be aliased/shimmed in the calling shell.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HANDLERS_DIR="$REPO_ROOT/backend/internal/handlers"
DOC="$REPO_ROOT/docs/reference/api.md"

# ---------------------------------------------------------------------------
# PREFIX_MAP: "file:FuncName|prefix|citation" -- one row per RegisterRoutes*
# method found in backend/internal/handlers/*.go, cited to the main.go call
# site that mounts it. `prefix` is "" for routes mounted on the bare engine
# (health.go only). Verified 2026-08-07 against backend/cmd/server/main.go
# in this worktree -- see the header comment above for what "verified" means
# and does not mean here.
# ---------------------------------------------------------------------------
PREFIX_MAP="auth.go:RegisterPublicRoutes|/api/v1/auth|main.go: authGroup := api.Group(\"/auth\"); authHandler.RegisterPublicRoutes(authGroup)
auth.go:RegisterRoutes|/api/v1/auth|main.go: authHandler.RegisterRoutes(authGroup), same authGroup as RegisterPublicRoutes
auth.go:RegisterProtectedRoutes|/api/v1|main.go: authHandler.RegisterProtectedRoutes(protected)
settings.go:RegisterRoutes|/api/v1|main.go: settingsHandler.RegisterRoutes(protected)
directories.go:RegisterRoutes|/api/v1/directories|main.go: directoriesGroup := protected.Group(\"/directories\"); directoriesHandler.RegisterRoutes(directoriesGroup)
stacks.go:RegisterRoutes|/api/v1/stacks|main.go: wireStacksGroup -> stacksGroup := protected.Group(\"/stacks\"); stacksHandler.RegisterRoutes(stacksGroup)
compose.go:RegisterRoutes|/api/v1/stacks|main.go: wireStacksGroup -> envHandler/composeHandler.RegisterRoutes(stacksGroup), same stacksGroup as stacks.go
env.go:RegisterRoutes|/api/v1/stacks|main.go: wireStacksGroup -> envHandler.RegisterRoutes(stacksGroup), same stacksGroup as stacks.go
git.go:RegisterRoutes|/api/v1/git|main.go: gitGroup := protected.Group(\"/git\"); gitHandler.RegisterRoutes(gitGroup)
logs.go:RegisterRoutes|/api/v1|main.go: logsHandler.RegisterRoutes(protected)
monitoring.go:RegisterRoutes|/api/v1|main.go: monitorHandler.RegisterRoutes(protected, ...)
dashboard.go:RegisterRoutes|/api/v1|main.go: dashboardHandler.RegisterRoutes(protected, ...)
resources.go:RegisterRoutes|/api/v1|main.go: resourcesHandler.RegisterRoutes(protected)
terminal.go:RegisterRoutes|/api/v1|main.go: terminalHandler.RegisterRoutes(wsGroup, ...), wsGroup := protected.Group(\"\")
operations.go:RegisterRoutes|/api/v1|main.go: operationsHandler.RegisterRoutes(wsGroup, ...), same wsGroup as terminal.go
update_jobs_ws.go:RegisterRoutes|/api/v1|main.go: updateJobsWSHandler.RegisterRoutes(wsGroup), same wsGroup as terminal.go
version.go:RegisterVersionRoutes|/api/v1|main.go: handlers.NewVersionHandler().RegisterVersionRoutes(api)
backup.go:RegisterRoutes|/api/v1|main.go: backupHandler.RegisterRoutes(protected)
backup.go:RegisterWSRoutes|/api/v1|main.go: backupHandler.RegisterWSRoutes(wsGroup, ...), same wsGroup as terminal.go
health.go:RegisterRoutes||main.go: handlers.NewHealthHandler(...).RegisterRoutes(r) -- r is the bare *gin.Engine, no prefix"

# ---------------------------------------------------------------------------
# ROUTE_INCLUDE: routes registered directly in main.go rather than inside any
# handler's RegisterRoutes* method, so PREFIX_MAP/extraction can't see them.
# "METHOD /full/path|citation"
# ---------------------------------------------------------------------------
ROUTE_INCLUDE="POST /api/v1/compose/lint|main.go: composeGroup := protected.Group(\"/compose\"); composeGroup.POST(\"/lint\", stacksHandler.Lint)"

# ---------------------------------------------------------------------------
# ROUTE_ALLOW: registered routes deliberately absent from docs/reference/
# api.md, one "METHOD /path|reason" per line. Currently empty -- every route
# the extractor finds is documented. Kept as a named table (rather than
# omitted) so the FAIL message below has somewhere to point a future
# intentionally-undocumented route at, matching check-docs.sh's
# ENV_EXCLUDE/ENV_INCLUDE shape.
# ---------------------------------------------------------------------------
ROUTE_ALLOW=""

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

# Look up "file:FuncName" in PREFIX_MAP. Prints the prefix (may be empty
# string) and returns 0 on a match, prints nothing and returns 1 otherwise.
prefix_for() {
  local key="$1" row rowkey rowprefix rest
  while IFS='|' read -r rowkey rowprefix rest; do
    [ -z "$rowkey" ] && continue
    if [ "$rowkey" = "$key" ]; then
      printf '%s' "$rowprefix"
      return 0
    fi
  done <<< "$PREFIX_MAP"
  return 1
}

# ---------------------------------------------------------------------------
# extraction
# ---------------------------------------------------------------------------

# Emits "file:FuncName:METHOD:path" for every group.METHOD("path", ...) call
# found inside a RegisterRoutes*-style method, across every non-test handler
# file, in a single awk invocation (not one process per route or per file --
# check-docs.sh's link-checker was 5-12x slower before switching away from
# exactly that pattern).
extract_route_calls() {
  local -a files=()
  local f
  while IFS= read -r f; do
    files+=("$f")
  done < <(command find "$HANDLERS_DIR" -maxdepth 1 -type f -name '*.go' ! -name '*_test.go' | command sort)

  # awk with no file operands reads stdin, which hangs CI on an empty handlers
  # dir instead of reaching check_api_docs's "extracted zero routes" failure.
  [ "${#files[@]}" -eq 0 ] && return 0

  command awk '
    FNR == 1 { fn = "" }
    /^func \(h \*[A-Za-z]+\) Register[A-Za-z]*\(/ {
      match($0, /Register[A-Za-z]*/)
      fn = substr($0, RSTART, RLENGTH)
      next
    }
    /^}/ { fn = ""; next }
    fn != "" && /\.(GET|POST|PUT|PATCH|DELETE)\(/ {
      line = $0
      method = ""
      if (match(line, /\.(GET|POST|PUT|PATCH|DELETE)\(/)) {
        method = substr(line, RSTART + 1, RLENGTH - 2)
      }
      path = ""
      if (match(line, /\("[^"]*"/)) {
        path = substr(line, RSTART + 2, RLENGTH - 3)
      }
      n = split(FILENAME, parts, "/")
      base = parts[n]
      printf "%s:%s:%s:%s\n", base, fn, method, path
    }
  ' "${files[@]}"
}

# Builds the full extracted route list ("METHOD /path" per line, deduped) into
# the caller's `routes` array (bash dynamic scoping, same pattern check-docs.sh
# uses for check_links/resolve_link).
build_routes() {
  routes=()
  local -A seen=()
  local row file fn method path prefix full
  while IFS=: read -r file fn method path; do
    [ -z "$file" ] && continue
    prefix=$(prefix_for "$file:$fn") || {
      routes+=("UNMAPPED $file:$fn $method $path")
      continue
    }
    if [ -z "$path" ]; then
      full="$prefix"
    else
      full="$prefix$path"
    fi
    row="$method $full"
    if [ -z "${seen[$row]+set}" ]; then
      seen["$row"]=1
      routes+=("$row")
    fi
  done < <(extract_route_calls)

  local incrow incurl increason
  while IFS='|' read -r incrow increason; do
    [ -z "$incrow" ] && continue
    if [ -z "${seen[$incrow]+set}" ]; then
      seen["$incrow"]=1
      routes+=("$incrow")
    fi
  done <<< "$ROUTE_INCLUDE"
}

# ---------------------------------------------------------------------------
# check
# ---------------------------------------------------------------------------

check_api_docs() {
  if [ ! -d "$HANDLERS_DIR" ]; then
    echo "FAIL: api-docs - $HANDLERS_DIR not found"
    return 1
  fi
  if [ ! -f "$DOC" ]; then
    echo "FAIL: api-docs - docs/reference/api.md does not exist"
    return 1
  fi

  local -a routes
  build_routes

  if [ "${#routes[@]}" -eq 0 ]; then
    echo "FAIL: api-docs - extracted zero routes from $HANDLERS_DIR (extraction is broken)"
    return 1
  fi

  local -a unmapped=() missing=() allowed_missing=()
  local row method path allowurl allowreason skip

  for row in "${routes[@]}"; do
    case "$row" in
      UNMAPPED*)
        unmapped+=("$row")
        continue
        ;;
    esac

    if command grep -qF -- "\`$row\`" "$DOC"; then
      continue
    fi

    skip=0
    while IFS='|' read -r allowurl allowreason; do
      [ -z "$allowurl" ] && continue
      if [ "$allowurl" = "$row" ]; then
        skip=1
        allowed_missing+=("$row")
        break
      fi
    done <<< "$ROUTE_ALLOW"
    [ "$skip" -eq 1 ] && continue

    missing+=("$row")
  done

  if [ "${#unmapped[@]}" -gt 0 ]; then
    echo "FAIL: api-docs - route(s) whose file:func has no PREFIX_MAP entry: ${unmapped[*]}"
    return 1
  fi

  if [ "${#missing[@]}" -gt 0 ]; then
    echo "FAIL: api-docs - route(s) registered but not documented in docs/reference/api.md (add a \`METHOD /path\` entry, or a ROUTE_ALLOW row with a reason): ${missing[*]}"
    return 1
  fi

  echo "PASS: api-docs - all ${#routes[@]} extracted route(s) are documented in docs/reference/api.md (${#allowed_missing[@]} allowlisted)"
  return 0
}

# ---------------------------------------------------------------------------
# entry point
# ---------------------------------------------------------------------------

usage() {
  cat <<USAGE >&2
Usage: $(basename "$0") [--list | --self-test]

With no arguments, runs the coverage check and prints one PASS/FAIL line.
--list prints the extracted route table (one "METHOD /path" per line) and
exits 0, for debugging what the extractor sees.
--self-test runs the check over fixture handler dirs and docs (red and green
controls) and exits non-zero if any control misbehaves.
USAGE
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_STATUS WANT_OUTPUT_REGEX HANDLERS_DIR DOC [ROUTE_INCLUDE [ROUTE_ALLOW]]
# Points the check's globals at fixtures inside a subshell, so the real tables
# and paths are untouched afterwards. The awk that runs is the real extractor.
selftest_case() {
  local name="$1" want="$2" pat="$3" out status
  ST_RUN=$((ST_RUN + 1))
  out=$(
    HANDLERS_DIR="$4"
    DOC="$5"
    ROUTE_INCLUDE="${6-}"
    ROUTE_ALLOW="${7-}"
    check_api_docs 2>&1
  )
  status=$?
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: api-docs self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_handlers DIR writes a handler tree the extractor must read fully:
# every verb, a path-less call, a second Register method, and three files it
# must ignore (a _test.go, a non-Register method, a call after the closing
# brace). auth.go:RegisterRoutes and settings.go:RegisterRoutes are real
# PREFIX_MAP keys, so the fixture reuses the real mapping.
write_handlers() {
  mkdir -p "$1"
  cat > "$1/auth.go" <<'EOF'
package handlers

func (h *AuthHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me", h.Me)
	group.POST("/login", h.Login)
	group.PUT("/profile", h.Profile)
	group.PATCH("/password", h.Password)
	group.DELETE("/session", h.Logout)
	group.GET("", h.Root)
}

func (h *AuthHandler) helper(group *gin.RouterGroup) {
	group.GET("/not-a-route", h.X)
}
EOF
  cat > "$1/settings.go" <<'EOF'
package handlers

func (h *SettingsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/settings", h.Get)
}
EOF
  cat > "$1/auth_test.go" <<'EOF'
package handlers

func (h *AuthHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/from-a-test", h.X)
}
EOF
}

# write_doc FILE ROUTE... writes a doc holding each ROUTE as a backticked entry.
write_doc() {
  local f="$1" r
  shift
  : > "$f"
  for r in "$@"; do
    printf '| `%s` | documented |\n' "$r" >> "$f"
  done
}

# write_doc_except FILE EXCLUDE ROUTE... is write_doc minus the one ROUTE equal
# to EXCLUDE. Exact match on purpose: a ${arr[@]/pat} substitution would also
# rewrite the longer routes that merely start with the pattern.
write_doc_except() {
  local f="$1" skip="$2" r
  shift 2
  : > "$f"
  for r in "$@"; do
    [ "$r" = "$skip" ] || printf '| `%s` | documented |\n' "$r" >> "$f"
  done
}

selftest() {
  local tmp
  tmp=$(mktemp -d) || { echo "FAIL: api-docs self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$tmp"' RETURN
  local h="$tmp/handlers" inc="POST /api/v1/compose/lint|fixture"
  write_handlers "$h"
  local -a all=("GET /api/v1/auth/me" "POST /api/v1/auth/login" "PUT /api/v1/auth/profile"
    "PATCH /api/v1/auth/password" "DELETE /api/v1/auth/session" "GET /api/v1/auth"
    "GET /api/v1/settings" "POST /api/v1/compose/lint")

  # GREEN: every verb, the path-less call and the ROUTE_INCLUDE row are
  # documented. 7 handler routes + 1 include = 8; the helper, the _test.go
  # and the non-Register method add nothing.
  write_doc "$tmp/doc-full.md" "${all[@]}"
  selftest_case "all routes documented" 0 "PASS: api-docs - all 8 extracted route" "$h" "$tmp/doc-full.md" "$inc"

  # RED: one route missing from the doc is named, and only that one.
  write_doc_except "$tmp/doc-no-del.md" "DELETE /api/v1/auth/session" "${all[@]}"
  selftest_case "undocumented DELETE" 1 "registered but not documented.*DELETE /api/v1/auth/session" "$h" "$tmp/doc-no-del.md" "$inc"

  # RED: the ROUTE_INCLUDE route is checked like any other.
  write_doc_except "$tmp/doc-no-lint.md" "POST /api/v1/compose/lint" "${all[@]}"
  selftest_case "undocumented ROUTE_INCLUDE row" 1 "not documented.*POST /api/v1/compose/lint" "$h" "$tmp/doc-no-lint.md" "$inc"

  # RED: a documented route that merely CONTAINS the registered one as a
  # prefix does not count (doc has GET /api/v1/auth/me, not GET /api/v1/auth);
  # the doc match is on the whole backticked METHOD+PATH.
  write_doc_except "$tmp/doc-prefix.md" "GET /api/v1/auth" "${all[@]}"
  selftest_case "longer doc entry is not documentation of its prefix" 1 "not documented.*GET /api/v1/auth( |$)" "$h" "$tmp/doc-prefix.md" "$inc"

  # GREEN: an allowlisted route may be absent from the doc, and is counted.
  selftest_case "allowlisted route" 0 "PASS: api-docs - all 8 .*\(1 allowlisted\)" "$h" "$tmp/doc-no-del.md" "$inc" "DELETE /api/v1/auth/session|fixture reason"

  # RED: a Register method whose file:func has no PREFIX_MAP row fails
  # rather than guessing a prefix.
  mkdir -p "$tmp/unmapped"
  printf 'package handlers\n\nfunc (h *NewHandler) RegisterRoutes(group *gin.RouterGroup) {\n\tgroup.GET("/x", h.X)\n}\n' > "$tmp/unmapped/brandnew.go"
  selftest_case "file:func missing from PREFIX_MAP" 1 "no PREFIX_MAP entry: UNMAPPED brandnew\.go:RegisterRoutes GET /x" "$tmp/unmapped" "$tmp/doc-full.md" "$inc"

  # RED: zero extracted routes is a broken extractor, not a pass.
  mkdir -p "$tmp/empty"
  selftest_case "zero routes extracted" 1 "extracted zero routes" "$tmp/empty" "$tmp/doc-full.md" ""

  # RED: missing handlers dir and missing doc are named, not read as clean.
  selftest_case "handlers dir missing" 1 "api-docs - .*/nope not found" "$tmp/nope" "$tmp/doc-full.md" "$inc"
  selftest_case "doc missing" 1 "api\.md does not exist" "$h" "$tmp/nope.md" "$inc"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: api-docs self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "api-docs self-test: $ST_RUN control(s) passed (2 green, 7 red)"
  return 0
}

main() {
  if [ "$#" -gt 0 ]; then
    case "$1" in
      --self-test) selftest; exit $? ;;
      --list)
        local -a routes
        build_routes
        printf '%s\n' "${routes[@]}"
        exit 0
        ;;
      -h|--help) usage; exit 0 ;;
      *)
        echo "Unknown argument: $1" >&2
        usage
        exit 2
        ;;
    esac
  fi

  check_api_docs
}

main "$@"
