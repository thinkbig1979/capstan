#!/usr/bin/env bash
# scripts/check-env-hand-parse.sh
#
# ONE invariant (agent-os-1106):
#
#   No non-test Go file under backend/internal or backend/cmd looks for "=" in
#   text with a strings or bytes call, except in the allowlisted functions
#   below. A call that looks for "=" is how a hand parse of KEY=value starts.
#
# WHY. compose reads a stack's env with compose-go's dotenv parser: quotes,
# escapes, "KEY: value", multi-line values and ${VAR} references. A hand split
# of .env text at the first "=" reads a different value than compose does,
# and every such copy drifted from compose on its own (agent-os-tfef,
# agent-os-n43b). A value Capstan reads differently from compose is a secret
# redaction misses or a name host space gets wrong. New code resolves a
# stack's env through resolveComposeEnv (backend/internal/services/
# compose_env.go), which calls compose-go.
#
# WHAT IS A VIOLATION. A line, outside a // comment, calling strings.* or
# bytes.* Cut, Split*, Index*, LastIndex*, Contains*, Count or FieldsFunc*
# (the families in SPLIT_RE) with either a string-literal argument holding a
# "=" ("=", "=:", "=,:") or a '=' rune anywhere after the call (the closure
# of IndexFunc or FieldsFunc), in a function that is not on ALLOWLIST. A
# function is the nearest top-level `func` line above the call, so a closure
# counts as its enclosing function; a top-level `}` or a package-level var,
# const, type or import ends it, so code after a function is not covered by
# that function's entry.
#
# ALLOWLIST is FILE|FUNC. Each entry must still match at least one call: an
# entry whose split is gone is stale and fails the check, so the list shrinks
# as the hand parsers are removed and the instrument is shown to fire on the
# real tree every run.
#
# LIMIT. Line-based, so these are invisible to it: a call split across lines;
# a "=" held in a variable or constant (strings.Cut(s, sep)); a hand parser
# that loops over bytes; a call from a package other than strings or bytes
# (regexp, bufio.Scanner with a custom split). A literal argument is
# recognised by its position (after "(" or ",", before ")" or ","), so a
# literal holding "=" that sits elsewhere on the line, such as a comparison
# after the call, is not mistaken for one. A nested first argument
# (strings.Cut(strings.TrimSpace(s), "=")) IS seen.
#
# USAGE
#   check-env-hand-parse.sh              scan this repo
#   check-env-hand-parse.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-env-hand-parse.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation or stale allowlist entry, 2 usage/internal error
# (including a tree with no Go files under the scan roots, which must not read
# as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ALLOWLIST="
backend/internal/handlers/env.go|parseEnvFile
backend/internal/handlers/env.go|composeLineKey
backend/internal/handlers/env.go|opensMultilineValue
backend/internal/handlers/env.go|commentedSecretEnd
backend/internal/handlers/env.go|redactGlobalEnvVars
backend/internal/handlers/settings.go|parseEnvFile
backend/internal/services/redact_compose.go|sensitiveEnvValues
backend/internal/services/compose_env.go|resolveComposeEnv
backend/internal/services/exec_env.go|stripCapstanSecrets
backend/internal/services/redact_url.go|parseRcloneConnectionString
"
# Why each entry is allowed:
#   handlers/env.go parseEnvFile: the env table's own line parser; it keys rows
#     the way compose does (composeLineKey) and round-trips the file byte for
#     byte, which compose-go cannot.
#   handlers/env.go composeLineKey: where compose ends a key, the first "=" or
#     ":" (mirrors compose-go dotenv/parser.go locateKeyName). opensMultilineValue
#     and commentedSecretEnd use the same rule to find where a value starts, for
#     the locked view's masking (agent-os-yt2f); they do not read the value.
#   handlers/env.go redactGlobalEnvVars: mirrors settings.go parseEnvFile's row
#     rule (a line holding "=") to match its rows to lines for the locked view.
#     agent-os-rd1l, which replaces that parser, removes it; delete with it.
#   handlers/settings.go parseEnvFile: the global env page's parser.
#     agent-os-rd1l removes it; delete this entry with it.
#   services/redact_compose.go sensitiveEnvValues: redaction's fallback parser,
#     kept on purpose so a file compose-go rejects is still masked
#     (agent-os-tfef).
#   services/compose_env.go resolveComposeEnv, services/exec_env.go
#     stripCapstanSecrets: split a process environment's KEY=VALUE strings,
#     which hold no quotes or comments, not .env text.
#   services/redact_url.go parseRcloneConnectionString: rclone's inline
#     key=value option grammar in a remote string, not .env text.

# The awk program receives the pattern through -v, so no regex escapes are
# needed: bracket expressions only, which gawk and mawk read alike.
SPLIT_RE="(strings|bytes)[.](Cut|SplitAfterN|SplitAfterSeq|SplitAfter|SplitN|SplitSeq|Split|IndexAny|IndexByte|IndexRune|IndexFunc|Index|LastIndexAny|LastIndexByte|LastIndexFunc|LastIndex|ContainsAny|ContainsRune|ContainsFunc|Contains|Count|FieldsFuncSeq|FieldsFunc)[(].*([(,][ \t]*\"[^\"]*=[^\"]*\"[ \t]*[),]|'=')"

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

check_dir() {
  local dir="$1" root
  [ -d "$dir" ] || { echo "check-env-hand-parse: $dir is not a directory" >&2; return 2; }

  local roots=()
  for root in backend/internal backend/cmd; do
    [ -d "$dir/$root" ] && roots+=("$root")
  done
  if [ "${#roots[@]}" -eq 0 ]; then
    echo "check-env-hand-parse: neither backend/internal nor backend/cmd under $dir" >&2
    return 2
  fi

  local files
  files=$(cd "$dir" && command find "${roots[@]}" -type f -name '*.go' ! -name '*_test.go' | LC_ALL=C sort)
  if [ -z "$files" ]; then
    echo "check-env-hand-parse: no non-test Go files under ${roots[*]} in $dir" >&2
    return 2
  fi
  local nfiles
  nfiles=$(printf '%s\n' "$files" | wc -l | tr -d ' ')

  # One line per hit: FILE|FUNC<TAB>FILE:LINE: TEXT
  local hits status
  # shellcheck disable=SC2086 # $files is newline-separated paths without spaces
  hits=$(cd "$dir" && command awk -v re="$SPLIT_RE" '
    FNR == 1 { fn = "" }
    /^}/ { fn = "" }
    /^(var|const|type|import)[ \t(]/ { fn = "" }
    /^func[ \t]/ {
      s = $0
      sub(/^func[ \t]+/, "", s)
      if (s ~ /^[(]/) sub(/^[(][^)]*[)][ \t]*/, "", s)
      sub(/[[(].*/, "", s)
      fn = s
    }
    /^[ \t]*\/\// { next }
    $0 ~ re { printf "%s|%s\t%s:%d: %s\n", FILENAME, fn, FILENAME, FNR, $0 }
  ' $files 2>&1)
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "check-env-hand-parse: awk failed ($status): $hits" >&2
    return 2
  fi

  local violations="" stale="" entry
  local line key
  while IFS= read -r line; do
    [ -n "$line" ] || continue
    key=${line%%$'\t'*}
    case "$ALLOWLIST" in
      *"
$key
"*) ;;
      *) violations="$violations${line#*$'\t'}
" ;;
    esac
  done <<<"$hits"

  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    case "
$hits" in
      *"
$entry	"*) ;;
      *) stale="$stale  $entry
" ;;
    esac
  done <<<"$ALLOWLIST"

  local rc=0
  if [ -n "$violations" ]; then
    printf '%s' "$violations"
    echo "check-env-hand-parse: a strings/bytes call looking for \"=\" outside the allowlist (a hand parse of KEY=value); resolve a stack's env with resolveComposeEnv (backend/internal/services/compose_env.go), or allowlist the function with its reason"
    rc=1
  fi
  if [ -n "$stale" ]; then
    printf '%s' "$stale"
    echo "check-env-hand-parse: stale allowlist entries (no \"=\" split left in that function); remove them from ALLOWLIST"
    rc=1
  fi
  if [ "$rc" -eq 0 ]; then
    local nallowed
    nallowed=$(printf '%s\n' "$hits" | command grep -c . || true)
    echo "check-env-hand-parse: 0 violations in $nfiles files ($nallowed allowlisted splits)"
  fi
  return "$rc"
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX DIR
selftest_case() {
  local name="$1" want="$2" pat="$3" dir="$4" out status
  ST_RUN=$((ST_RUN + 1))
  out=$(check_dir "$dir" 2>&1)
  status=$?
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: env-hand-parse self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_base writes a clean tree: every allowlisted function holds one split,
# so no entry is stale, and other code touches "=" only in ways that are not
# a split (string building, a comment, a test file).
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/handlers" "$d/backend/internal/services" "$d/backend/cmd/server"
  cat > "$d/backend/internal/handlers/env.go" <<'EOF'
package handlers

func (h *EnvHandler) parseEnvFile(content string) []EnvEntry {
	eqIndex := strings.Index(trimmedLine, "=")
	return nil
}

var exportPrefix = regexp.MustCompile(`^export\s+`)

func composeLineKey(line string) (string, bool) {
	if end := strings.IndexAny(s, "=:"); end != -1 {
	}
	return "", false
}

func opensMultilineValue(line string) (byte, bool) {
	sep := strings.IndexAny(line, "=:")
	return 0, false
}

func commentedSecretEnd(value string) int {
	sep := strings.IndexAny(body, "=:")
	return -1
}

func redactGlobalEnvVars(vars []map[string]string, raw string) {
	if t == "" || !strings.Contains(t, "=") {
	}
}

func serializeEnvFile(entries []EnvEntry) string {
	buf.WriteString("=")
	if a, b, _ := strings.Cut(x, ","); y == "a=b" {
	}
	return ""
}
EOF
  cat > "$d/backend/internal/services/redact_url.go" <<'EOF'
package services

func parseRcloneConnectionString(opaque string) ([]rcloneParam, bool) {
	eq := strings.IndexAny(opaque[i:], "=,:")
	return nil, false
}
EOF
  cat > "$d/backend/internal/handlers/settings.go" <<'EOF'
package handlers

func parseEnvFile(path string) ([]map[string]string, error) {
	parts := strings.SplitN(line, "=", 2)
	return nil, nil
}
EOF
  cat > "$d/backend/internal/services/redact_compose.go" <<'EOF'
package services

func sensitiveEnvValues(content string) []string {
	key, value, ok := strings.Cut(line, "=")
	return nil
}
EOF
  cat > "$d/backend/internal/services/compose_env.go" <<'EOF'
package services

func resolveComposeEnv(st models.Stack) ([]map[string]string, error) {
	for _, kv := range dockerEnv() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			processEnv[k] = v
		}
	}
	return nil, nil
}
EOF
  cat > "$d/backend/internal/services/exec_env.go" <<'EOF'
package services

func stripCapstanSecrets(base []string) []string {
	key, _, _ := strings.Cut(kv, "=")
	return nil
}

func build() string {
	// strings.Cut(line, "=") in a comment is not a call.
	return key + "=" + val
}
EOF
  cat > "$d/backend/internal/services/exec_env_test.go" <<'EOF'
package services

func helper() { strings.Cut(line, "=") }
EOF
  printf 'package main\n\nfunc main() {}\n' > "$d/backend/cmd/server/main.go"
}

self_test() {
  local tmp
  tmp=$(mktemp -d) || { echo "env-hand-parse self-test: mktemp failed" >&2; return 2; }
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" RETURN

  local d="$tmp/clean"
  write_base "$d"
  selftest_case "clean tree" 0 '0 violations in 7 files [(]10 allowlisted splits[)]' "$d"

  d="$tmp/planted"
  write_base "$d"
  printf '\nfunc loadEnv(text string) {\n\tk, v, _ := strings.Cut(text, "=")\n}\n' >> "$d/backend/internal/services/exec_env.go"
  selftest_case "planted Cut in a new function" 1 'exec_env.go:[0-9]+: .*strings.Cut[(]text, "="[)]' "$d"

  d="$tmp/samefile"
  write_base "$d"
  printf '\nfunc (h *EnvHandler) other(line string) {\n\tparts := strings.SplitN(line, "=", 2)\n}\n' >> "$d/backend/internal/handlers/env.go"
  selftest_case "split in an allowlisted FILE, other function" 1 'handlers/env.go:[0-9]+: .*SplitN' "$d"

  d="$tmp/byte"
  write_base "$d"
  printf "\nfunc scan(b []byte) int {\n\treturn bytes.IndexByte(b, '=')\n}\n" >> "$d/backend/internal/services/exec_env.go"
  selftest_case "byte form in bytes package" 1 "bytes.IndexByte[(]b, '='[)]" "$d"

  d="$tmp/cmd"
  write_base "$d"
  printf '\nfunc flags(a string) {\n\t_ = strings.Index(a, "=")\n}\n' >> "$d/backend/cmd/server/main.go"
  selftest_case "backend/cmd is scanned" 1 'cmd/server/main.go:[0-9]+:' "$d"

  d="$tmp/closure"
  write_base "$d"
  printf '\nfunc outer() {\n\tf := func(s string) {\n\t\tstrings.Cut(s, "=")\n\t}\n\t_ = f\n}\n' >> "$d/backend/internal/services/redact_compose.go"
  selftest_case "closure counts as its enclosing function" 1 'redact_compose.go:[0-9]+:' "$d"

  d="$tmp/indexany"
  write_base "$d"
  printf '\nfunc keyEnd(line string) int {\n\treturn strings.IndexAny(line, "=:")\n}\n' >> "$d/backend/internal/services/exec_env.go"
  selftest_case "IndexAny with a set holding =" 1 'exec_env.go:[0-9]+: .*IndexAny' "$d"

  d="$tmp/fieldsfunc"
  write_base "$d"
  printf "\nfunc fields(s string) []string {\n\treturn strings.FieldsFunc(s, func(r rune) bool { return r == '=' })\n}\n" >> "$d/backend/internal/services/exec_env.go"
  selftest_case "FieldsFunc closure on '='" 1 'exec_env.go:[0-9]+: .*FieldsFunc' "$d"

  d="$tmp/nested"
  write_base "$d"
  printf '\nfunc trimmed(line string) {\n\tk, v, _ := strings.Cut(strings.TrimSpace(line), "=")\n}\n' >> "$d/backend/internal/services/exec_env.go"
  selftest_case "nested first argument" 1 'exec_env.go:[0-9]+: .*Cut[(]strings.TrimSpace' "$d"

  d="$tmp/afterfunc"
  write_base "$d"
  printf '\nvar splitKV = func(s string) (string, string, bool) { return strings.Cut(s, "=") }\n' >> "$d/backend/internal/services/redact_compose.go"
  selftest_case "package-level var after an allowlisted function" 1 'redact_compose.go:[0-9]+: var splitKV' "$d"

  d="$tmp/varblock"
  write_base "$d"
  printf '\nvar (\n\tsplitKV = func(s string) (string, string, bool) { return strings.Cut(s, "=") }\n)\n' >> "$d/backend/internal/services/redact_compose.go"
  selftest_case "var block after an allowlisted function" 1 'redact_compose.go:[0-9]+:[[:space:]]+splitKV' "$d"

  d="$tmp/stale"
  write_base "$d"
  printf 'package handlers\n\nfunc parseEnvFile(path string) ([]map[string]string, error) {\n\treturn nil, nil\n}\n' > "$d/backend/internal/handlers/settings.go"
  selftest_case "stale allowlist entry" 1 'backend/internal/handlers/settings.go[|]parseEnvFile' "$d"

  d="$tmp/empty"
  mkdir -p "$d/backend/internal/x"
  selftest_case "no Go files is an error, not clean" 2 'no non-test Go files' "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "env-hand-parse self-test: $ST_FAILS of $ST_RUN cases failed"
    return 1
  fi
  echo "env-hand-parse self-test: $ST_RUN/$ST_RUN cases passed"
  return 0
}

case "${1:-}" in
  --self-test) self_test; exit $? ;;
  -h | --help) usage; exit 0 ;;
  "") check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
