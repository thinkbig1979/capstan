#!/usr/bin/env bash
# scripts/check-goroutine-sends.sh
#
# ONE invariant (agent-os-qags.18, safe-defaults rule 3; the class agent-os-z91e.1
# fixed in logs.go):
#
#   In non-test Go under backend/internal and backend/cmd, a channel send inside a
#   goroutine body is a `case` of a select, or it is on the ALLOWLIST below with a
#   reason.
#
# WHY. A goroutine that PRODUCES into a channel with a bare send statement
# (`ch <- v`), read by a consumer that can stop draining (ctx end, a write error),
# parks forever on the send. z91e.1 fixed one in logs.go; rule 3 says "a channel
# send in a producer selects on ctx.Done()" but nothing enforced it.
#
# MEMBERSHIP RULE. How a line is decided to be a violation:
#   "inside a goroutine body": comments, string literals, rune literals and raw
#   strings are blanked first (so a "}" or "{" inside text cannot move the brace
#   depth). A body OPENS on a line that has
#     - `go func(`                               a goroutine literal;
#     - `.Go(... func(` or `AfterFunc(... func(`  errgroup / timer goroutines;
#     - `name := func(` (or `name = func(`, `var name = func(`) when `go name(`
#       appears anywhere in the same file: a closure that is launched by name.
#   The body ENDS when the brace depth falls back to what it was before the
#   opening line, so a bare send on the line AFTER the closing `}()` is outside it
#   (the self-test pins this). A single-line `go func() { ch <- v }()` opens and
#   closes on one line and is inside. Nested literals stay inside the outer body.
#   "a send": a `<-` whose left side ends in an identifier character, `)`, `}` or
#   `]` (so `x := <-ch` and a leading `<-ch` are receives), that is not `chan<-` /
#   `<-chan` in a type, and whose statement does not start with `case` (a select
#   case). The channel is the expression just before the `<-`.
#
# ALLOWLIST by file | func | channel (never by line). Every entry must still match
# at least one send: a stale entry fails the check. THE SAME-CHANNEL EXCUSE: an
# entry excuses every send on that channel in that function, so a NEW bare send on
# `out` in an allowlisted `RunStreaming` is excused and a new send on another
# channel there fails. Adding a send to an excused channel means re-reading its
# entry's reason.
#
# LIMIT. Line-based and gofmt-shaped. Cannot see: a goroutine whose body is a
# NAMED function or method (`go w.watchLoop()`; the census on bc0bf5d found 0
# sends in those, every non-case send in the tree is one of the 15 below); a
# select whose ONLY case is the send (equivalent to a bare send); sends in
# producers that are not goroutines (docker_lifecycle.go RunStreaming's pre-goroutine
# nil-receiver send on a buffered-1 channel, update_job_manager.go Enqueue's send
# into the worker queue); two statements on one line.
#
# USAGE
#   check-goroutine-sends.sh              scan this repo
#   check-goroutine-sends.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-goroutine-sends.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation or stale allowlist, 2 usage/internal error
# (including a tree with no Go files under the scan roots).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# file | func | channel | reason
ALLOWLIST='
backend/internal/handlers/backup.go|previewSnapshotViaRestic|done|buffered-1 done channel: one send, read once after the out range loop ends (OBSERVED handlers/backup.go previewSnapshotViaRestic)
backend/internal/services/backup_restic.go|Backup|out|forwards the translated restic lines into the caller-supplied out; every caller passes the run registry drainLoop channel, which ranges until finish() closes it (OBSERVED backup_runner.go drainLoop; callers backup.go RunBackupWithRunID and the database backup). LATENT: a caller that stops reading parks the translate goroutine
backend/internal/services/backup_restic.go|Run|out|scan closure launched per pipe: sends restic output into the caller-supplied out. Callers read: runDrained (drains), drainLoop (ranges to close), Backup raw (ranged by the translate goroutine); other callers pass their own out through (INFERRED, not each traced). LATENT: a caller that stops reading parks the scanner
backend/internal/services/backup_restic.go|Run|scanDone|done channel with capacity 2 for the two scanners, each sends once, both received after cmd.Wait
backend/internal/services/docker_lifecycle.go|RunStreaming|out|buffered-100 stream returned to the caller; both callers range it to close (OBSERVED handlers/operations.go for-range RunStreaming at the down and the subcommand call). LATENT: a future consumer that stops early parks the producer
backend/internal/services/docker_lifecycle.go|RunStreaming|scanDone|done channel with capacity 2 for the two scanners, each sends once, both received after Wait
backend/internal/services/docker_update.go|fetchRemoteDigests|sem|semaphore with capacity 5: the acquire is released by the defer in the same goroutine
'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# candidates DIR ROOT...: print "relfile|func|line|channel|text" for every bare
# send inside a goroutine body.
candidates() {
  local dir="$1" f; shift
  while IFS= read -r f; do
    awk -v rel="${f#"$dir"/}" '
      # blank(l): l with comments, strings, runes and raw strings blanked out.
      function blank(l,   out, i, c, instr) {
        out = ""; instr = ""
        for (i = 1; i <= length(l); i++) {
          c = substr(l, i, 1)
          if (rawstr) { if (c == "`") rawstr = 0; continue }
          if (instr != "") {
            if (c == "\\") { i++; continue }
            if (c == instr) instr = ""
            continue
          }
          if (c == "`") { rawstr = 1; continue }
          if (c == "\"" || c == "\047") { instr = c; continue }
          if (c == "/" && substr(l, i + 1, 1) == "/") break
          out = out c
        }
        return out
      }
      # sendChan(code): the channel of the first bare send on the line, "" if none.
      function sendChan(code,   off, i, pre, post, last) {
        off = 0
        while ((i = index(substr(code, off + 1), "<-")) > 0) {
          i += off
          pre = substr(code, 1, i - 1)
          post = substr(code, i + 2)
          off = i + 1
          sub(/[ \t]+$/, "", pre)
          if (pre == "") continue
          last = substr(pre, length(pre), 1)
          if (last !~ /[A-Za-z0-9_)}\]]/) continue
          if (pre ~ /chan$/) continue
          # a keyword on the left makes it a receive: `case <-ctx.Done():`, `return <-ch`.
          if (pre ~ /(^|[^A-Za-z0-9_])(case|return|range|if|for|switch|else|go|defer|select)$/) continue
          if (post ~ /^[ \t]*chan([^A-Za-z0-9_]|$)/) continue
          if (pre ~ /(^|[^A-Za-z0-9_])case[ \t]/) return ""
          if (match(pre, /[A-Za-z_][A-Za-z0-9_.]*(\[[^]]*\])?$/)) return substr(pre, RSTART, RLENGTH)
          return "?"
        }
        return ""
      }
      # pass 1: names launched with `go name(`.
      FNR == NR {
        if ($0 ~ /^[ \t]*\/\//) next
        if (match($0, /(^|[^A-Za-z0-9_.])go[ \t]+[A-Za-z_][A-Za-z0-9_]*\(/)) {
          t = substr($0, RSTART, RLENGTH)
          sub(/^.*go[ \t]+/, "", t); sub(/\($/, "", t)
          if (t != "func") launched[t] = 1
        }
        next
      }
      FNR == 1 { depth = 0; sp = 0; rawstr = 0; fn = "" }
      /^func / {
        s = $0
        sub(/^func[ \t]+/, "", s)
        if (s ~ /^\(/) sub(/^\([^)]*\)[ \t]*/, "", s)
        sub(/[\[(].*$/, "", s)
        fn = s
      }
      {
        code = blank($0)
        opens = 0
        if (code ~ /(^|[^A-Za-z0-9_.])go[ \t]+func[ \t]*\(/) opens = 1
        else if (code ~ /\.Go\(.*func[ \t]*\(/ || code ~ /AfterFunc\(.*func[ \t]*\(/) opens = 1
        else if (match(code, /^[ \t]*(var[ \t]+)?[A-Za-z_][A-Za-z0-9_]*[ \t]*:?=[ \t]*func[ \t]*\(/)) {
          nm = substr(code, RSTART, RLENGTH)
          sub(/^[ \t]*(var[ \t]+)?/, "", nm); sub(/[ \t]*:?=.*$/, "", nm)
          if (nm in launched) opens = 1
        }
        if (opens) stack[++sp] = depth
        if (sp > 0) {
          ch = sendChan(code)
          if (ch != "") printf "%s|%s|%d|%s|%s\n", rel, (fn == "" ? "<top-level>" : fn), FNR, ch, $0
        }
        n = gsub(/\{/, "{", code); m = gsub(/\}/, "}", code)
        depth += n - m
        while (sp > 0 && depth <= stack[sp]) sp--
      }
    ' "$f" "$f"
  done < <(command grep -rl --include='*.go' '' "$@" 2>/dev/null \
             | command grep -v '_test\.go$' \
             | sort)
}

check_dir() {
  local dir="$1" root
  [ -d "$dir" ] || { echo "goroutine-sends: $dir is not a directory" >&2; return 2; }

  local roots=()
  for root in backend/internal backend/cmd; do
    [ -d "$dir/$root" ] && roots+=("$dir/$root")
  done
  if [ "${#roots[@]}" -eq 0 ]; then
    echo "goroutine-sends: neither backend/internal nor backend/cmd under $dir" >&2
    return 2
  fi

  local nfiles
  nfiles=$(command grep -rl --include='*.go' '' "${roots[@]}" | command grep -vc '_test\.go$')
  if [ "$nfiles" -eq 0 ]; then
    echo "goroutine-sends: no non-test Go files under backend/internal or backend/cmd in $dir" >&2
    return 2
  fi

  local cands
  cands=$(candidates "$dir" "${roots[@]}")

  local bad="" used="" file fn ln ch text key
  while IFS='|' read -r file fn ln ch text; do
    [ -z "$file" ] && continue
    key="$file|$fn|$ch|"
    if printf '%s\n' "$ALLOWLIST" | command grep -qF "$key"; then
      used+="$key"$'\n'
    else
      bad+="  $file:$ln ($fn, chan $ch): $(echo "$text" | sed 's/^[ \t]*//')"$'\n'
    fi
  done <<< "$cands"

  local stale="" entry
  while IFS= read -r entry; do
    [ -z "$entry" ] && continue
    key="$(echo "$entry" | cut -d'|' -f1-3)|"
    if ! printf '%s' "$used" | command grep -qF "$key"; then
      stale+="  stale allowlist entry: $(echo "$entry" | cut -d'|' -f1) ($(echo "$entry" | cut -d'|' -f2), chan $(echo "$entry" | cut -d'|' -f3))"$'\n'
    fi
  done <<< "$ALLOWLIST"

  local rc=0
  if [ -n "$bad" ]; then
    echo "goroutine-sends: a bare channel send inside a goroutine. Make it a select case beside ctx.Done() (or a stop channel), or add file|func|channel to ALLOWLIST in $(basename "$0") with the reason it cannot park:"
    printf '%s' "$bad"
    rc=1
  fi
  if [ -n "$stale" ]; then
    echo "goroutine-sends: stale ALLOWLIST entries match no send (remove them, or the scan did not reach these files):"
    printf '%s' "$stale"
    rc=1
  fi
  if [ "$rc" -eq 0 ]; then
    local n
    n=$(printf '%s' "$used" | command grep -c .)
    echo "goroutine-sends: $nfiles Go files scanned, $n allowlisted bare send(s), 0 violations"
  fi
  return "$rc"
}

# --- self-test -------------------------------------------------------------

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX DIR
selftest_case() {
  local name="$1" want="$2" pat="$3" dir="$4" out rc
  ST_RUN=$((ST_RUN + 1))
  out=$(check_dir "$dir" 2>&1)
  rc=$?
  if [ "$rc" -ne "$want" ] || ! echo "$out" | command grep -qE -- "$pat"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: goroutine-sends self-test case \"$name\": want exit $want matching /$pat/, got exit $rc:"
    echo "$out"
  fi
}

# write_base DIR: one bare send for every allowlist entry, plus the shapes that
# must NOT fire, and nothing else, so it is clean.
write_base() {
  local d="$1"
  mkdir -p "$d/backend/internal/services" "$d/backend/internal/handlers" "$d/backend/cmd/server"
  cat > "$d/backend/internal/handlers/backup.go" <<'GO'
package handlers

func (h *BackupHandler) previewSnapshotViaRestic(ctx context.Context) ([]string, error) {
	out := make(chan services.StreamLine, 256)
	done := make(chan error, 1)
	go func() {
		done <- restic.RestorePreview(ctx, out)
		close(out)
	}()
	for line := range out {
		_ = line
	}
	return nil, <-done
}
GO
  cat > "$d/backend/internal/services/backup_restic.go" <<'GO'
package services

func (r *execRunner) Run(ctx context.Context, name string, out chan<- StreamLine) error {
	scanDone := make(chan struct{}, 2)
	scan := func(rd io.Reader) {
		sc := bufio.NewScanner(rd)
		for sc.Scan() {
			out <- StreamLine{Type: "data", Line: sc.Text()}
		}
		scanDone <- struct{}{}
	}
	go scan(stdoutR)
	go scan(stderrR)
	<-scanDone
	return nil
}

func (m *ResticManager) Backup(ctx context.Context, out chan<- StreamLine) error {
	raw := make(chan StreamLine, 32)
	go func() {
		for sl := range raw {
			out <- sl
		}
	}()
	return nil
}
GO
  cat > "$d/backend/internal/services/docker_lifecycle.go" <<'GO'
package services

func (s *DockerService) RunStreaming(ctx context.Context) <-chan StreamLine {
	out := make(chan StreamLine, 100)
	go func() {
		defer close(out)
		scanDone := make(chan struct{}, 2)
		scanPipe := func(r io.Reader) {
			go func() {
				out <- StreamLine{Type: "data"}
				scanDone <- struct{}{}
			}()
		}
		scanPipe(stdoutR)
		<-scanDone
		out <- StreamLine{Type: "done"}
	}()
	return out
}
GO
  cat > "$d/backend/internal/services/docker_update.go" <<'GO'
package services

func fetchRemoteDigests(ctx context.Context, refs map[string]struct{}) {
	sem := make(chan struct{}, 5)
	for ref := range refs {
		go func(ref string) {
			sem <- struct{}{}
			defer func() { <-sem }()
		}(ref)
	}
}
GO
  # Shapes that must stay green: select cases, receives, type syntax, text with
  # braces and "<-" in strings and comments, a send AFTER a goroutine literal
  # closed, a closure that is called but never launched with go, a send in a
  # plain (non-goroutine) function.
  cat > "$d/backend/internal/handlers/shapes.go" <<'GO'
package handlers

func shapes(ctx context.Context, out chan<- string, in <-chan string) <-chan string {
	ch := make(chan string, 1)
	go func() {
		msg := "} ch <- 1 {"
		raw := `}` + "{"
		_, _ = msg, raw
		// ch <- 1 in a comment
		select {
		case <-ctx.Done():
			return
		case out <- "x":
		case v := <-in:
			_ = v
		}
		x := <-in
		_ = x
	}()
	ch <- "after the literal closed"
	return ch
}

func calledNotLaunched(out chan string) {
	push := func(v string) {
		out <- v
	}
	push("a")
}

func (q *queue) Enqueue(item int) {
	q.items <- item
}
GO
  cat > "$d/backend/internal/handlers/shapes_test.go" <<'GO'
package handlers

func fixture() {
	go func() { ch <- 1 }()
}
GO
  printf 'package main\n\nfunc main() {}\n' > "$d/backend/cmd/server/main.go"
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

# plant DIR BODY appends a function holding BODY to DIR/backend/internal/handlers/shapes.go.
plant() {
  printf '\nfunc planted(ctx context.Context, ch chan int) {\n%s\n}\n' "$2" >> "$1/backend/internal/handlers/shapes.go"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: goroutine-sends self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  local d

  # GREEN: allowlisted sends, select cases, receives, text, a send after the
  # literal closed, a closure never launched with go, a _test.go file.
  d=$(fresh); selftest_case "clean base tree" 0 "8 allowlisted bare send\(s\), 0 violations" "$d"

  # RED: the bead's reproducer, a planted bare send in a go literal.
  d=$(fresh); plant "$d" "	go func() {
		ch <- 1
	}()"
  selftest_case "planted bare send" 1 "shapes.go:[0-9]+ \(planted, chan ch\)" "$d"

  # RED: the same on one line.
  d=$(fresh); plant "$d" "	go func() { ch <- 1 }()"
  selftest_case "single-line go literal" 1 "shapes.go:[0-9]+ \(planted, chan ch\)" "$d"

  # RED: a literal with parameters, and a send two levels down (a closure inside).
  d=$(fresh); plant "$d" "	go func(n int) {
		for i := 0; i < n; i++ {
			func() {
				ch <- i
			}()
		}
	}(3)"
  selftest_case "nested closure inside a go literal" 1 "shapes.go:[0-9]+ \(planted, chan ch\)" "$d"

  # RED: a closure launched by name (backup_restic.go Run shape).
  d=$(fresh); plant "$d" "	pump := func() {
		ch <- 1
	}
	go pump()"
  selftest_case "closure launched by name" 1 "shapes.go:[0-9]+ \(planted, chan ch\)" "$d"

  # GREEN: the same closure only called, never launched with go.
  d=$(fresh); plant "$d" "	pump := func() {
		ch <- 1
	}
	pump()"
  selftest_case "closure called, not launched" 0 "0 violations" "$d"

  # RED: time.AfterFunc and an errgroup .Go literal run in their own goroutine.
  d=$(fresh); plant "$d" "	time.AfterFunc(time.Second, func() {
		ch <- 1
	})"
  selftest_case "time.AfterFunc literal" 1 "shapes.go:[0-9]+ \(planted, chan ch\)" "$d"
  d=$(fresh); plant "$d" "	g.Go(func() error {
		ch <- 1
		return nil
	})"
  selftest_case "errgroup Go literal" 1 "shapes.go:[0-9]+ \(planted, chan ch\)" "$d"

  # RED: the send on a channel the allowlist does not name, in an allowlisted func.
  d=$(fresh)
  printf '\nfunc (s *DockerService) RunStreaming(other chan int) {\n\tgo func() {\n\t\tother <- 1\n\t}()\n}\n' >> "$d/backend/internal/services/docker_lifecycle.go"
  selftest_case "other channel in an allowlisted func" 1 "docker_lifecycle.go:[0-9]+ \(RunStreaming, chan other\)" "$d"

  # RED: an allowlisted file|func|channel in the wrong file is not excused.
  d=$(fresh)
  printf '\nfunc fetchRemoteDigests() {\n\tgo func() {\n\t\tsem <- struct{}{}\n\t}()\n}\n' >> "$d/backend/internal/handlers/shapes.go"
  selftest_case "allowlisted name, wrong file" 1 "shapes.go:[0-9]+ \(fetchRemoteDigests, chan sem\)" "$d"

  # RED: the pre-fix logs.go shape (agent-os-z91e.1): a scanner goroutine sending
  # each line into a channel the reader stops draining.
  d=$(fresh)
  cat >> "$d/backend/internal/handlers/shapes.go" <<'GO'

func StreamLogs(ctx context.Context, rc io.Reader) {
	logChan := make(chan string)
	go func() {
		defer close(logChan)
		scanner := bufio.NewScanner(rc)
		for scanner.Scan() {
			logChan <- scanner.Text()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-logChan:
			_, _ = line, ok
		}
	}
}
GO
  selftest_case "pre-fix logs.go shape" 1 "shapes.go:[0-9]+ \(StreamLogs, chan logChan\)" "$d"

  # RED: a stale allowlist entry (its channel no longer has a send).
  d=$(fresh)
  printf 'package services\n\nfunc fetchRemoteDigests() {}\n' > "$d/backend/internal/services/docker_update.go"
  selftest_case "stale allowlist entry" 1 "stale allowlist entry: backend/internal/services/docker_update.go \(fetchRemoteDigests, chan sem\)" "$d"

  # ERROR: no scan roots, and scan roots with no Go file, are not clean trees.
  d=$(mktemp -d "$ST_TMP/empty.XXXXXX")
  selftest_case "no scan roots" 2 "neither backend/internal" "$d"
  d=$(mktemp -d "$ST_TMP/nogo.XXXXXX"); mkdir -p "$d/backend/internal"
  selftest_case "no Go files" 2 "no non-test Go files" "$d"

  if [ "$ST_FAILS" -ne 0 ]; then
    echo "FAIL: goroutine-sends self-test - $ST_FAILS of $ST_RUN control(s) failed"
    return 1
  fi
  echo "goroutine-sends self-test: $ST_RUN control(s) passed (2 green, 10 red, 2 error)"
  return 0
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  --self-test) selftest; exit $? ;;
  '') check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
