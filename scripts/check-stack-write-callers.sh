#!/usr/bin/env bash
# scripts/check-stack-write-callers.sh
#
# TWO invariants (agent-os-qags.24, the guard for agent-os-z91e.23 / PR #581):
#
#   R1. No non-test Go file under backend/internal/handlers calls (or takes the
#       method value of) UpsertStack, except StacksHandler.Create in
#       backend/internal/handlers/stack_crud.go.
#   R2. Every DeleteStack in those files sits inside the func literal of a
#       `.WithLock(func() {` call (the scanner's lock).
#
# WHY. A handler that writes a whole stacks row from a stale read lets a
# concurrent scan's column come back reverted (EnvHandler.Create did, until
# z91e.23 moved it to the one-column DB.SetStackEnvFileIfUnset), and a handler
# that deletes a row outside the scanner's lock lets a scan that globbed the
# compose file earlier write the row back as a permanent ghost (StacksHandler.
# Delete, fixed with ScannerService.WithLock). Create is the one allowed whole-
# row write: it is followed by a synchronous rescan of its own new directory.
# services/ is out of scope on purpose: the scanner owns that lock.
#
# HOW A LINE IS ATTRIBUTED. The enclosing function is the last `func` line
# above it, keyed Receiver.Name (EnvHandler.Create and StacksHandler.Create
# are different functions), until that function's closing `}` at column 0
# (gofmt layout); a line after it, such as a package-level var, is in no
# function. A lock region opens on a line ending `.WithLock(func() {` and
# closes on the first later line that is the same indentation plus `})`.
#
# LIMIT. It cannot see:
#   - WithLock given a func value built elsewhere (WithLock(fn)), or a
#     one-line literal (WithLock(func() { ... }));
#   - a call reached through a helper: a function outside the lock that
#     calls DeleteStack is reported, a helper called from inside one is not
#     proven to run under it (reported too, conservatively);
#   - an alias assigned on an earlier line and called later (the method
#     value itself is reported where it is taken);
#   - a closing `})` that is not at the opening line's indentation (not gofmt);
#   - a call in a nested func literal inside the lock region (accepted as
#     inside);
#   - a trailing // comment that names the method after code on the line (that
#     line is reported, a false positive); whole-line comments are skipped.
# There is no allow marker, on purpose: a new legitimate writer changes the
# allowed function here, in review.
#
# USAGE
#   check-stack-write-callers.sh              scan this repo
#   check-stack-write-callers.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-stack-write-callers.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree with
# no Go files under the scan root, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

ALLOWED_FILE='backend/internal/handlers/stack_crud.go'
ALLOWED_FUNC='StacksHandler.Create'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# scan_files prints path:line:[rule] text for every violation in the files
# named as arguments (paths relative to the current directory). Exit 1 means
# a violation, anything above 1 is awk's own failure.
scan_files() {
  awk -v allowed_file="$ALLOWED_FILE" -v allowed_func="$ALLOWED_FUNC" '
    FNR == 1 { fn = ""; inlock = 0; lockindent = "" }
    /^func[ \t]/ {
      name = $0
      sub(/^func[ \t]+/, "", name)
      recv = ""
      if (name ~ /^\(/) {
        r = name
        sub(/^\(/, "", r)
        sub(/\).*/, "", r)
        n = split(r, parts, /[ \t]+/)
        t = parts[n]
        gsub(/\*/, "", t)
        sub(/\[.*/, "", t)
        recv = t "."
        sub(/^\([^)]*\)[ \t]*/, "", name)
      }
      sub(/[[(].*/, "", name)
      fn = recv name
    }
    inlock && $0 == lockindent "})" { inlock = 0 }
    /^[ \t]*\/\// { next }
    /\.WithLock\(func\(\)[ \t]*\{[ \t]*$/ {
      match($0, /^[ \t]*/)
      lockindent = substr($0, 1, RLENGTH)
      inlock = 1
      next
    }
    /\.UpsertStack([^A-Za-z0-9_]|$)/ {
      if (!(FILENAME == allowed_file && fn == allowed_func)) {
        print FILENAME ":" FNR ":[UpsertStack outside " allowed_func "] " $0
        bad = 1
      }
    }
    /\.DeleteStack([^A-Za-z0-9_]|$)/ {
      if (!inlock) {
        print FILENAME ":" FNR ":[DeleteStack outside a WithLock func literal] " $0
        bad = 1
      }
    }
    # gofmt puts a top-level func'"'"'s closing brace at column 0. Ending the
    # attribution there keeps a package-level var or const after the allowed
    # function from inheriting its name.
    /^}/ { fn = ""; inlock = 0 }
    END { exit bad ? 1 : 0 }
  ' "$@"
}

check_dir() {
  local dir="$1" root='backend/internal/handlers'
  [ -d "$dir" ] || { echo "check-stack-write-callers: $dir is not a directory" >&2; return 2; }
  if [ ! -d "$dir/$root" ]; then
    echo "check-stack-write-callers: $root not found under $dir" >&2
    return 2
  fi

  local files=() nfiles
  mapfile -t files < <(cd "$dir" && command find "$root" -type f -name '*.go' ! -name '*_test.go' | LC_ALL=C sort)
  nfiles=${#files[@]}
  if [ "$nfiles" -eq 0 ]; then
    echo "check-stack-write-callers: no non-test Go files under $root in $dir" >&2
    return 2
  fi

  local out status
  out=$(cd "$dir" && scan_files "${files[@]}" 2>&1)
  status=$?
  case "$status" in
    0) echo "check-stack-write-callers: 0 violations in $nfiles files"; return 0 ;;
    1)
      if [ -z "$out" ]; then
        echo "check-stack-write-callers: awk failed with no output" >&2
        return 2
      fi
      echo "$out"
      echo "check-stack-write-callers: a handler writes a whole stacks row or deletes one outside the scanner lock (use a one-column UPDATE such as DB.SetStackEnvFileIfUnset, and run DeleteStack inside ScannerService.WithLock)"
      return 1
      ;;
    *) echo "check-stack-write-callers: scan failed ($status): $out" >&2; return 2 ;;
  esac
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
    echo "FAIL: stack-write-callers self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

# write_base writes a clean tree: the allowed Create, a Delete inside a lock
# region, an EnvHandler.Create with no row write, the interface declaration
# (no leading dot), and a comment naming the shape.
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/handlers" "$d/backend/internal/services"
  cat > "$d/backend/internal/handlers/stack_crud.go" <<'EOF'
package handlers

// h.db.UpsertStack(stack) and h.db.DeleteStack(id) in a comment are skipped.
func (h *StacksHandler) Create(c *gin.Context) {
	if err := h.db.UpsertStack(stack); err != nil {
		return
	}
}

func (h *StacksHandler) Delete(c *gin.Context) {
	h.scanner.WithLock(func() {
		if dbErr = h.db.DeleteStack(id); dbErr != nil {
			return
		}
	})
}
EOF
  cat > "$d/backend/internal/handlers/env.go" <<'EOF'
package handlers

func (h *EnvHandler) Create(c *gin.Context) {
	_, _ = h.db.SetStackEnvFileIfUnset(id, name)
}
EOF
  cat > "$d/backend/internal/handlers/stacks.go" <<'EOF'
package handlers

type stackStore interface {
	UpsertStack(stack models.Stack) error
	DeleteStack(id string) error
}
EOF
  printf 'package services\n\nfunc (s *ScannerService) scan() { _ = s.db.UpsertStack(x); _ = s.db.DeleteStack(y) }\n' > "$d/backend/internal/services/scanner.go"
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

# plant DIR FILE TEXT appends a function holding TEXT to DIR/FILE.
plant() {
  printf '\nfunc (h *OtherHandler) planted() {\n%s\n}\n' "$3" >> "$1/$2"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: stack-write-callers self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d H=backend/internal/handlers

  # GREEN: Create's UpsertStack, Delete's locked DeleteStack, an interface
  # declaration, comments, and the scanner in services/ (out of scope).
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED R1: a second handler writing a whole row.
  d=$(fresh); plant "$d" $H/stacks.go '	_ = h.db.UpsertStack(stack)'
  selftest_case "R1: UpsertStack in another handler" 1 "$H/stacks.go:[0-9]+:\[UpsertStack outside StacksHandler.Create\].*h.db.UpsertStack" "$d"

  # RED R1: EnvHandler.Create is a different function from StacksHandler.Create.
  d=$(fresh); printf 'package handlers\n\nfunc (h *EnvHandler) Create(c *gin.Context) {\n\t_ = h.db.UpsertStack(stack)\n}\n' > "$d/$H/env.go"
  selftest_case "R1: EnvHandler.Create UpsertStack" 1 "$H/env.go:4:\[UpsertStack outside" "$d"

  # RED R1: another function in the allowed file.
  d=$(fresh); printf '\nfunc (h *StacksHandler) Update(c *gin.Context) {\n\t_ = h.db.UpsertStack(stack)\n}\n' >> "$d/$H/stack_crud.go"
  selftest_case "R1: another function in stack_crud.go" 1 "$H/stack_crud.go:[0-9]+:\[UpsertStack outside" "$d"

  # RED R1: a package-level declaration after Create is not inside it (the
  # attribution must end at Create's closing brace).
  d=$(fresh); printf '\nvar upsert = h.db.UpsertStack\n' >> "$d/$H/stack_crud.go"
  selftest_case "R1: top-level var after Create" 1 "$H/stack_crud.go:[0-9]+:\[UpsertStack outside.*var upsert" "$d"

  # RED R1: a method value without a call.
  d=$(fresh); plant "$d" $H/stacks.go '	f := h.db.UpsertStack
	_ = f'
  selftest_case "R1: method value" 1 "$H/stacks.go:[0-9]+:\[UpsertStack outside.*f := h.db.UpsertStack" "$d"

  # RED R2: a DeleteStack with no lock.
  d=$(fresh); plant "$d" $H/stacks.go '	_ = h.db.DeleteStack(id)'
  selftest_case "R2: DeleteStack outside any lock" 1 "$H/stacks.go:[0-9]+:\[DeleteStack outside a WithLock func literal\]" "$d"

  # RED R2: the real Delete with its lock region removed.
  d=$(fresh); printf 'package handlers\n\nfunc (h *StacksHandler) Delete(c *gin.Context) {\n\t_ = h.db.DeleteStack(id)\n}\n' > "$d/$H/stack_crud.go"
  selftest_case "R2: Delete without WithLock" 1 "$H/stack_crud.go:4:\[DeleteStack outside" "$d"

  # RED R2: a DeleteStack AFTER a closed WithLock region in the same function.
  d=$(fresh); cat > "$d/$H/stack_crud.go" <<'EOF'
package handlers

func (h *StacksHandler) Delete(c *gin.Context) {
	h.scanner.WithLock(func() {
		_ = h.db.DeleteStack(a)
	})
	_ = h.db.DeleteStack(b)
}
EOF
  selftest_case "R2: DeleteStack after a closed region" 1 "$H/stack_crud.go:7:\[DeleteStack outside" "$d"

  # RED R2: a region does not leak into the next function.
  d=$(fresh); cat > "$d/$H/stack_crud.go" <<'EOF'
package handlers

func (h *StacksHandler) Delete(c *gin.Context) {
	h.scanner.WithLock(func() {
		_ = h.db.DeleteStack(a)
EOF
  printf '}\n\nfunc (h *StacksHandler) Other(c *gin.Context) {\n\t_ = h.db.DeleteStack(b)\n}\n' >> "$d/$H/stack_crud.go"
  selftest_case "R2: unclosed region ends at the function" 1 "$H/stack_crud.go:9:\[DeleteStack outside" "$d"

  # GREEN: a DeleteStack inside a WithLock literal in a different handler, and
  # a region at deeper indentation closed at its own level.
  d=$(fresh); plant "$d" $H/stacks.go '	if ok {
		h.scanner.WithLock(func() {
			_ = h.db.DeleteStack(id)
		})
	}'
  selftest_case "indented WithLock region" 0 "0 violations" "$d"

  # GREEN: test files are out of scope.
  d=$(fresh); printf 'package handlers\n\nfunc f() { _ = db.UpsertStack(s); _ = db.DeleteStack(i) }\n' > "$d/$H/stacks_test.go"
  selftest_case "test file" 0 "0 violations" "$d"

  # ERROR: no scan root, and a scan root with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan root" 2 "backend/internal/handlers not found" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/$H"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: stack-write-callers self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "stack-write-callers self-test: $ST_RUN control(s) passed (3 green, 9 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
