#!/usr/bin/env bash
# scripts/check-dockerhub-pulls.sh
#
# TWO invariants (agent-os-an6d):
#
#   1. CI and the release build pull no image from Docker Hub, outside a
#      reasoned allowlist. GitHub-hosted runners share an IP pool whose
#      anonymous Docker Hub allowance runs out: on 2026-10-09 the Trivy image
#      scan (a required check), the integration tests and the Playwright
#      terminal-flow stack start all failed 429 with nothing under test wrong.
#      Images come from the AWS ECR Public mirror of the Docker Official
#      Images instead: public.ecr.aws/docker/library/<image>.
#
#   2. Every pull from that mirror is paced or retried, because it allows one
#      anonymous pull per second per IP:
#      a. a job that runs the integration tests pre-pulls, through
#         scripts/pull-with-retry.sh, every public.ecr.aws ref those tests
#         name;
#      b. a job that names a public.ecr.aws ref (a compose fixture it starts)
#         pre-pulls it through scripts/pull-with-retry.sh;
#      c. a step that pulls inside BuildKit or a setup action
#         (docker/build-push-action, docker/setup-buildx-action unless
#         `driver: docker`, docker/setup-qemu-action) has an `id`,
#         `continue-on-error: true`, and a later step in the same job running
#         the same action `if: steps.<id>.outcome == 'failure'`: one bounded
#         retry, since those pulls cannot be pre-paced;
#      d. a job whose setup-buildx pulls docker.io/moby/buildkit first runs
#         scripts/docker-registry-mirror.sh, so the daemon fetches it from
#         mirror.gcr.io under its verified docker.io name (D58).
#
# WHAT IS A REFERENCE (producer arms):
#   docker/Dockerfile*      FROM <ref> (not a stage name, not scratch) and
#                           COPY --from=<ref> (not a stage, not a number)
#   .github/workflows/*.yml `image: <ref>`, `docker pull|run <ref>`,
#   docker-compose*.yaml    `uses: docker://<ref>`, the args of
#                           pull-with-retry.sh, and the IMPLICIT pulls of
#                           setup-buildx (moby/buildkit:buildx-stable-1 unless
#                           `driver: docker` or a `driver-opts: image=`) and
#                           setup-qemu (tonistiigi/binfmt:latest unless
#                           `image:`). Both defaults OBSERVED 2026-10-10 in
#                           buildx v0.37.1 driver/bkimage/bkimage.go and
#                           setup-qemu-action v4 action.yml.
#   backend Go files tagged //go:build integration (found by tag, so a new
#                           one cannot be missed): `image: <ref>` and
#                           `FROM <ref>` inside string literals, and any
#                           string literal that is wholly an image ref with a
#                           tag or digest ("alpine:3.21").
# The CONSUMER is the runner's docker daemon or BuildKit, which pulls whatever
# the ref names. Lines whose first non-blank characters are `#` or `//` are
# comments and skipped.
#
# DOCKER HUB means: no registry host in the first path component (a host has a
# `.` or `:`, or is `localhost`), or a host of docker.io, index.docker.io,
# registry-1.docker.io or registry.hub.docker.com. A ref whose first component
# is a variable ($X, ${{ }}) cannot be judged and fails too.
#
# USAGE
#   check-dockerhub-pulls.sh              scan this repo
#   check-dockerhub-pulls.sh DIR          scan DIR (a repo-root-shaped tree)
#   check-dockerhub-pulls.sh --self-test  prove the check fires both ways
#
# Exit: 0 clean, 1 violation, 2 usage/internal error (including a tree where
# no reference was found at all, which must not read as clean).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Docker Hub refs CI still pulls, and refs pulled through mirror.gcr.io
# (Google's Docker Hub cache, allowed only by name: D57), keyed <file>::<ref>,
# each with its reason.
# An entry that matches nothing fails the check, so the list cannot go stale.
ALLOWLIST='.github/workflows/security.yml::moby/buildkit:buildx-stable-1::BuildKit image setup-buildx pulls; kept as docker.io, the only name buildx verifies; the daemon fetches it through its mirror.gcr.io registry-mirror (D58); retried once
.github/workflows/docker-publish.yml::moby/buildkit:buildx-stable-1::BuildKit image setup-buildx pulls; kept as docker.io, the only name buildx verifies; the daemon fetches it through its mirror.gcr.io registry-mirror (D58); retried once
.github/workflows/docker-publish.yml::mirror.gcr.io/tonistiigi/binfmt:latest::binfmt image setup-qemu pulls; not on ECR Public (404 on public.ecr.aws and ghcr.io, checked 2026-10-10), so the Google Docker Hub cache, by decision D57; retried once
backend/internal/integrationtest/update_detection_test.go::docker.io/library/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa::RepoDigests fixture for LocalRepoDigest; never pulled'

usage() {
  echo "Usage: $(basename "$0") [DIR | --self-test]" >&2
}

# The awk program reads every scanned file (FILENAME relative to DIR) and
# prints records:
#   REF   <file> <line> <ref> <job>       a pulled image reference
#   PRE   <file> <job> <ref>              a ref pre-pulled via pull-with-retry.sh
#   INTEG <file> <job>                    the job runs -tags=integration
#   BAD   <file> <line> <message>         a step that breaks rule 2c
# Kept to POSIX awk: CI's awk is mawk (agent-os-55re).
read -r -d '' AWK_PROG <<'AWK'
function kind(f) {
  if (f ~ /(^|\/)Dockerfile[^\/]*$/) return "dockerfile"
  if (f ~ /\.go$/) return "go"
  return "yaml"
}
function emit(ref, ln) {
  gsub(/^["']|["',]$/, "", ref)
  if (ref == "") return
  print "REF\t" cur "\t" ln "\t" ref "\t" job
}
function step_end(   a, i, base, want, found) {
  if (s_uses == "") { s_reset(); return }
  base = s_uses; sub(/@.*/, "", base)
  if (base == "docker/setup-buildx-action") {
    if (s_driver == "docker") { s_reset(); return }
    if (s_drvimg != "") emit(s_drvimg, s_line); else { emit("moby/buildkit:buildx-stable-1", s_line); implicit_bk = 1 }
  } else if (base == "docker/setup-qemu-action") {
    if (s_image != "") emit(s_image, s_line); else emit("tonistiigi/binfmt:latest", s_line)
  } else if (base != "docker/build-push-action") { s_reset(); return }
  ns++
  st_base[ns] = base; st_id[ns] = s_id; st_coe[ns] = s_coe; st_if[ns] = s_if; st_line[ns] = s_line
  st_bk[ns] = implicit_bk; implicit_bk = 0
  s_reset()
}
function s_reset() { s_uses = ""; s_id = ""; s_coe = 0; s_if = ""; s_driver = ""; s_drvimg = ""; s_image = ""; s_line = 0 }
function job_end(   i, j, ok, pat) {
  step_end()
  # A step that pulls inside BuildKit or a setup action must be a guarded
  # first attempt with a retry of the same action after it.
  for (i = 1; i <= ns; i++) {
    # The docker.io BuildKit image is pulled through the daemon's
    # mirror.gcr.io registry-mirror, set up earlier in the same job (D58).
    if (st_bk[i] && !(mirror_line > 0 && mirror_line < st_line[i]))
      print "BAD\t" cur "\t" st_line[i] "\t" st_base[i] " pulls docker.io/moby/buildkit with no earlier scripts/docker-registry-mirror.sh step in the job (D58)"
    if (st_if[i] ~ /outcome == .failure./) continue
    if (st_id[i] == "" || !st_coe[i]) {
      print "BAD\t" cur "\t" st_line[i] "\t" st_base[i] " step has no id + continue-on-error: true, so a throttled pull fails the job (one bounded retry required)"
      continue
    }
    ok = 0
    for (j = i + 1; j <= ns; j++) {
      pat = "steps\\." st_id[i] "\\.outcome == .failure."
      if (st_base[j] == st_base[i] && st_if[j] ~ pat) ok = 1
    }
    if (!ok) print "BAD\t" cur "\t" st_line[i] "\t" st_base[i] " step id " st_id[i] " has no retry step (same action, if: steps." st_id[i] ".outcome == 'failure')"
  }
  ns = 0; mirror_line = 0
}
FNR == 1 {
  if (prev != "" && kind(prev) == "yaml") job_end()
  # job_end above reports on the PREVIOUS file, so records use cur, which
  # still names it there, not FILENAME, which has already moved on.
  prev = FILENAME; cur = FILENAME; k = kind(FILENAME); job = "-"; injobs = 0; stepind = -1; split("", stages); cont = ""; s_reset(); ns = 0
}
{
  line = $0
  # Join backslash-continued lines (shell `\` in run blocks, Dockerfile RUN).
  if (cont != "") { line = cont " " line; ln = cline } else ln = FNR
  if (line ~ /\\[[:space:]]*$/) { cont = line; sub(/\\[[:space:]]*$/, "", cont); cline = ln; next }
  cont = ""
  t = line; sub(/^[[:space:]]+/, "", t)
  if (t == "" ) next
  if (k == "go" && t ~ /^\/\//) next
  if (k != "go" && t ~ /^#/) next

  if (k == "dockerfile") {
    if (toupper(substr(t, 1, 5)) == "FROM ") {
      n = split(t, w, /[[:space:]]+/)
      for (i = 2; i <= n && w[i] ~ /^--/; i++) ;
      ref = w[i]
      if (tolower(ref) != "scratch" && !(tolower(ref) in stages)) emit(ref, ln)
      if (i + 2 <= n && toupper(w[i + 1]) == "AS") stages[tolower(w[i + 2])] = 1
    } else if (toupper(substr(t, 1, 5)) == "COPY " && t ~ /--from=/) {
      r = t; sub(/.*--from=/, "", r); sub(/[[:space:]].*/, "", r)
      if (r !~ /^[0-9]+$/ && !(tolower(r) in stages)) emit(r, ln)
    }
    next
  }

  if (k == "go") {
    r = line
    while (match(r, /(image:|FROM)[[:space:]]+[A-Za-z0-9._\/:@$-]+/)) {
      m = substr(r, RSTART, RLENGTH); sub(/^(image:|FROM)[[:space:]]+/, "", m)
      emit(m, ln); r = substr(r, RSTART + RLENGTH)
    }
    r = line
    while (match(r, /"[a-z0-9][a-z0-9._\/-]*(:[A-Za-z0-9][A-Za-z0-9._-]*|@sha256:[0-9a-f]+)"/)) {
      m = substr(r, RSTART + 1, RLENGTH - 2); r = substr(r, RSTART + RLENGTH)
      if (m ~ /^sha256:/) continue
      if (m ~ /:/ && m !~ /@/) { tag = m; sub(/.*:/, "", tag); if (tag ~ /\//) continue }
      if (line ~ /(image:|FROM)[[:space:]]+/ && index(line, "image: " m) + index(line, "FROM " m) > 0) continue
      emit(m, ln)
    }
    next
  }

  # YAML: workflows and compose files.
  ind = match(line, /[^ ]/) - 1
  if (line ~ /^jobs:[[:space:]]*$/) { injobs = 1; next }
  if (ind == 0) { if (injobs) job_end(); injobs = 0; job = "-" }
  if (injobs && ind == 2 && t ~ /^[A-Za-z0-9_-]+:[[:space:]]*$/) {
    job_end(); job = t; sub(/:.*/, "", job); stepind = -1; next
  }
  if (injobs && t ~ /^steps:/) { stepind = ind + 2; next }
  if (stepind >= 0 && ind == stepind && t ~ /^- /) { step_end(); s_line = ln; t = substr(t, 3); sub(/^[[:space:]]+/, "", t) }
  if (stepind >= 0 && ind < stepind && t !~ /^- /) { step_end(); stepind = -1 }

  if (t ~ /^uses:/) { u = t; sub(/^uses:[[:space:]]*/, "", u); sub(/[[:space:]].*/, "", u); s_uses = u
    if (u ~ /^docker:\/\//) { sub(/^docker:\/\//, "", u); emit(u, ln) } }
  if (t ~ /docker-registry-mirror\.sh/ && !mirror_line) mirror_line = ln
  if (t ~ /^id:/) { v = t; sub(/^id:[[:space:]]*/, "", v); sub(/[[:space:]].*/, "", v); s_id = v }
  if (t ~ /^continue-on-error:[[:space:]]*true/) s_coe = 1
  if (t ~ /^if:/) s_if = t
  if (t ~ /^driver:[[:space:]]*docker[[:space:]]*$/) s_driver = "docker"
  if (t ~ /^driver-opts:.*image=/) { v = t; sub(/.*image=/, "", v); sub(/[[:space:],"'].*/, "", v); s_drvimg = v }
  if (t ~ /^(- )?image:[[:space:]]*[^[:space:]]/) {
    v = t; sub(/^(- )?image:[[:space:]]*/, "", v); sub(/[[:space:]].*/, "", v)
    if (s_uses ~ /^docker\/setup-qemu-action/) s_image = v
    else emit(v, ln)
  }
  if (t ~ /-tags[= ]integration/) print "INTEG\t" cur "\t" job
  r = t
  while (match(r, /docker[[:space:]]+(pull|run)[[:space:]]+[^|;&]*/)) {
    m = substr(r, RSTART, RLENGTH); r = substr(r, RSTART + RLENGTH)
    sub(/^docker[[:space:]]+(pull|run)[[:space:]]+/, "", m)
    n = split(m, w, /[[:space:]]+/)
    for (i = 1; i <= n; i++) {
      if (w[i] ~ /^-/) { if (w[i] !~ /=/ && w[i] ~ /^(--name|--network|--entrypoint|-e|--env|-v|--volume|-p|--publish|--platform|-u|--user|-w|--workdir|--mount|--label|-l)$/) i++; continue }
      if (w[i] !~ /^["']?\$/) emit(w[i], ln)
      break
    }
  }
  if (t ~ /pull-with-retry\.sh/) {
    m = t; sub(/.*pull-with-retry\.sh[[:space:]]*/, "", m)
    n = split(m, w, /[[:space:]]+/)
    for (i = 1; i <= n; i++) if (w[i] != "") print "PRE\t" cur "\t" job "\t" w[i]
  }
}
END { if (prev != "" && kind(prev) == "yaml") job_end() }
AWK

is_hub() {
  local ref="$1" name first
  name="${ref%%@*}"
  case "$name" in */*) first="${name%%/*}" ;; *) return 0 ;; esac
  case "$first" in
    *'$'*) return 0 ;;
    docker.io|index.docker.io|registry-1.docker.io|registry.hub.docker.com) return 0 ;;
    localhost|*.*|*:*) return 1 ;;
    *) return 0 ;;
  esac
}

# is_gcr_mirror: mirror.gcr.io is Google's Docker Hub cache. It is allowed for
# the images ECR Public does not carry, one allowlisted ref at a time (D57).
is_gcr_mirror() {
  case "$1" in mirror.gcr.io/*) return 0 ;; *) return 1 ;; esac
}

check_dir() {
  local dir="$1"
  [ -d "$dir" ] || { echo "check-dockerhub-pulls: $dir is not a directory" >&2; return 2; }

  local files=() f
  for f in "$dir"/docker/Dockerfile* "$dir"/.github/workflows/*.yml "$dir"/.github/workflows/*.yaml "$dir"/docker-compose*.yaml; do
    [ -f "$f" ] && files+=("${f#"$dir"/}")
  done
  if [ -d "$dir/backend" ]; then
    while IFS= read -r f; do
      [ -n "$f" ] && files+=("$f")
    done < <(cd "$dir" && command grep -rlE '^//go:build .*integration' --include='*.go' backend | sort)
  fi
  if [ "${#files[@]}" -eq 0 ]; then
    echo "check-dockerhub-pulls: no scoped files under $dir" >&2
    return 2
  fi

  local recs
  recs=$(cd "$dir" && awk "$AWK_PROG" "${files[@]}") || { echo "check-dockerhub-pulls: awk failed" >&2; return 2; }

  local nref
  nref=$(command grep -c '^REF' <<<"$recs")
  if [ "$nref" -eq 0 ]; then
    echo "check-dockerhub-pulls: 0 image references in ${#files[@]} files; the extractor is blind, not the tree clean" >&2
    return 2
  fi

  local viol=0 allowed=0 used="" key file line ref job reason entry
  # Rule 1: Docker Hub refs.
  while IFS=$'\t' read -r _ file line ref job; do
    is_hub "$ref" || is_gcr_mirror "$ref" || continue
    key="$file::$ref"
    entry=$(command grep -F -- "$key::" <<<"$ALLOWLIST" | head -n 1)
    if [ -n "$entry" ]; then
      allowed=$((allowed + 1)); used="$used"$'\n'"$key"
      continue
    fi
    if is_gcr_mirror "$ref"; then
      echo "$file:$line: mirror.gcr.io image $ref is not allowlisted (that mirror is allowed only by name, for images ECR Public lacks: D57)"
    else
      echo "$file:$line: Docker Hub image $ref (pull it from public.ecr.aws/docker/library/, or allowlist it with a reason)"
    fi
    viol=$((viol + 1))
  done < <(command grep '^REF' <<<"$recs")
  while IFS= read -r entry; do
    [ -n "$entry" ] || continue
    key="${entry%::*}"
    if ! command grep -qxF -- "$key" <<<"$used"; then
      echo "allowlist entry matches nothing (remove it): $key"
      viol=$((viol + 1))
    fi
  done <<<"$ALLOWLIST"

  # Rule 2a: integration jobs pre-pull every mirror ref the integration Go
  # files name. Only public.ecr.aws refs: the pacing is for its limit, and a
  # ghcr.io string in a test is a fixture, not a pull.
  local goref ijob ifile
  while IFS=$'\t' read -r _ ifile ijob; do
    while IFS=$'\t' read -r _ file line ref job; do
      case "$ref" in public.ecr.aws/*) ;; *) continue ;; esac
      if ! command grep -qxF -- "PRE"$'\t'"$ifile"$'\t'"$ijob"$'\t'"$ref" <<<"$recs"; then
        echo "$ifile: job $ijob runs the integration tests but does not pre-pull $ref ($file:$line) with scripts/pull-with-retry.sh"
        viol=$((viol + 1))
      fi
    done < <(command grep -E '^REF'$'\t''backend/' <<<"$recs" | sort -t$'\t' -k4,4 -u)
  done < <(command grep '^INTEG' <<<"$recs" | sort -u)

  # Rule 2b: a mirror image a workflow job names is pre-pulled in that job.
  while IFS=$'\t' read -r _ file line ref job; do
    [ "$job" = "-" ] && continue
    case "$ref" in public.ecr.aws/*) ;; *) continue ;; esac
    if ! command grep -qxF -- "PRE"$'\t'"$file"$'\t'"$job"$'\t'"$ref" <<<"$recs"; then
      echo "$file:$line: job $job names image $ref but does not pre-pull it with scripts/pull-with-retry.sh"
      viol=$((viol + 1))
    fi
  done < <(command grep -E '^REF'$'\t''\.github/workflows/' <<<"$recs")

  # Rule 2c: guarded BuildKit / setup-action steps.
  while IFS=$'\t' read -r _ file line msg; do
    echo "$file:$line: $msg"
    viol=$((viol + 1))
  done < <(command grep '^BAD' <<<"$recs")

  if [ "$viol" -gt 0 ]; then
    echo "check-dockerhub-pulls: $viol violation(s) (images come from public.ecr.aws/docker/library/, paced by scripts/pull-with-retry.sh or one retry step; agent-os-an6d)"
    return 1
  fi
  local npre
  npre=$(command grep -c '^PRE' <<<"$recs")
  echo "check-dockerhub-pulls: 0 violations; $nref image refs in ${#files[@]} files, $allowed allowlisted (Docker Hub or mirror.gcr.io), $npre pre-pulled"
  return 0
}

ST_RUN=0
ST_FAILS=0

# selftest_case NAME WANT_EXIT WANT_OUTPUT_REGEX DIR
selftest_case() {
  local name="$1" want="$2" pat="$3" dir="$4" out status
  ST_RUN=$((ST_RUN + 1))
  out=$(ALLOWLIST="$ST_ALLOW" check_dir "$dir" 2>&1)
  status=$?
  if [ "$status" -ne "$want" ] || ! command grep -qE -- "$pat" <<<"$out"; then
    ST_FAILS=$((ST_FAILS + 1))
    echo "FAIL: dockerhub-pulls self-test case \"$name\": want exit $want matching /$pat/, got exit $status:"
    echo "$out"
  fi
}

write_base() {
  local d="$1"
  mkdir -p "$d/docker" "$d/.github/workflows" "$d/backend/it"
  cat > "$d/docker/Dockerfile" <<'EOF'
# FROM node:22 in a comment is not a pull
FROM --platform=$BUILDPLATFORM public.ecr.aws/docker/library/golang:1.27.2-trixie AS build
FROM build AS again
COPY --from=build /x /y
FROM public.ecr.aws/docker/library/debian:trixie-slim
COPY --from=again /y /z
EOF
  cat > "$d/.github/workflows/ci.yml" <<'EOF'
name: ci
jobs:
  it:
    runs-on: ubuntu-latest
    steps:
      - uses: docker/setup-buildx-action@v4
        with:
          driver: docker
      - name: Pre-pull
        run: |
          bash scripts/pull-with-retry.sh \
            public.ecr.aws/docker/library/alpine:3.21 \
            public.ecr.aws/docker/library/nginx:1
      - run: go test -tags=integration ./...
  e2e:
    runs-on: ubuntu-latest
    steps:
      - run: |
          cat > compose.yaml <<'X'
          services:
            web:
              image: public.ecr.aws/docker/library/nginx:1
          X
      - run: bash scripts/pull-with-retry.sh public.ecr.aws/docker/library/nginx:1
  scan:
    runs-on: ubuntu-latest
    steps:
      - name: Build
        id: build
        continue-on-error: true
        uses: docker/build-push-action@v7
      - if: steps.build.outcome == 'failure'
        run: sleep 30
      - name: Build (retry)
        if: steps.build.outcome == 'failure'
        uses: docker/build-push-action@v7
EOF
  # A second workflow after ci.yml, so a record from ci.yml's last job (which
  # is only closed when the next file starts) must still name ci.yml.
  printf 'name: z\njobs:\n  z:\n    runs-on: ubuntu-latest\n    steps:\n      - run: true\n' > "$d/.github/workflows/z.yml"
  cat > "$d/backend/it/a_test.go" <<'EOF'
//go:build integration

package it

// "alpine:3.21" in a comment is not a pull
const ref = "public.ecr.aws/docker/library/alpine:3.21"
const yaml = "services:\n  web:\n    image: public.ecr.aws/docker/library/nginx:1\n"
var notImages = []string{"sha256:abc", "http://x:1/y", "a:b/c"}
EOF
  cat > "$d/backend/it/unit_test.go" <<'EOF'
package it

const notScanned = "nginx:1.21"
EOF
}

fresh() {
  local d
  d=$(mktemp -d "$ST_TMP/case.XXXXXX") || return 1
  write_base "$d"
  echo "$d"
}

selftest() {
  ST_TMP=$(mktemp -d) || { echo "FAIL: dockerhub-pulls self-test - could not create a temp directory"; return 2; }
  trap 'rm -rf "$ST_TMP"' RETURN
  ST_ALLOW=''
  local d

  # GREEN: mirror refs, stage names, comments, an untagged Go file, paced pulls.
  d=$(fresh); selftest_case "clean base tree" 0 "0 violations" "$d"

  # RED, rule 1, one per producer arm.
  d=$(fresh); sed -i 's|public.ecr.aws/docker/library/debian:trixie-slim|debian:trixie-slim|' "$d/docker/Dockerfile"
  selftest_case "bare FROM" 1 "docker/Dockerfile:5: Docker Hub image debian:trixie-slim" "$d"
  d=$(fresh); echo 'COPY --from=docker:29-cli /usr/local/bin/docker /usr/bin/' >> "$d/docker/Dockerfile"
  selftest_case "COPY --from image" 1 "Docker Hub image docker:29-cli" "$d"
  d=$(fresh); sed -i 's|image: public.ecr.aws/docker/library/nginx:1$|image: nginx:1.25-alpine|' "$d/.github/workflows/ci.yml"
  selftest_case "workflow image:" 1 "ci.yml:22: Docker Hub image nginx:1.25-alpine" "$d"
  d=$(fresh); printf '      - run: docker run --rm -e A=1 busybox:1 true\n' >> "$d/.github/workflows/ci.yml"
  selftest_case "docker run" 1 "Docker Hub image busybox:1" "$d"
  d=$(fresh); printf '      - uses: docker://alpine:3\n' >> "$d/.github/workflows/ci.yml"
  selftest_case "uses docker://" 1 "Docker Hub image alpine:3" "$d"
  d=$(fresh); sed -i 's|          driver: docker|          install: true|' "$d/.github/workflows/ci.yml"
  selftest_case "implicit buildkit" 1 "Docker Hub image moby/buildkit:buildx-stable-1" "$d"
  selftest_case "implicit buildkit, no registry-mirror step" 1 "ci.yml:6: docker/setup-buildx-action pulls docker.io/moby/buildkit with no earlier scripts/docker-registry-mirror.sh" "$d"
  d=$(fresh); sed -i 's|public.ecr.aws/docker/library/alpine:3.21"|alpine:3.21"|' "$d/backend/it/a_test.go"
  selftest_case "Go literal" 1 "a_test.go:6: Docker Hub image alpine:3.21" "$d"
  d=$(fresh); sed -i 's|image: public.ecr.aws/docker/library/nginx:1\\n|image: docker.io/library/nginx:1\\n|' "$d/backend/it/a_test.go"
  selftest_case "Go image: in a string, explicit docker.io" 1 "a_test.go:7: Docker Hub image docker.io/library/nginx:1" "$d"
  d=$(fresh); sed -i 's|^FROM build AS again|FROM ${BASE} AS again|' "$d/docker/Dockerfile"
  selftest_case "variable ref" 1 'Docker Hub image \$\{BASE\}' "$d"

  d=$(fresh); printf '      - uses: docker/setup-qemu-action@v4\n        with:\n          image: mirror.gcr.io/tonistiigi/binfmt:latest\n' >> "$d/.github/workflows/ci.yml"
  selftest_case "mirror.gcr.io not allowlisted" 1 "mirror.gcr.io image mirror.gcr.io/tonistiigi/binfmt:latest is not allowlisted" "$d"
  d=$(fresh); printf '      - uses: docker/setup-qemu-action@v4\n' >> "$d/.github/workflows/ci.yml"
  selftest_case "implicit binfmt" 1 "Docker Hub image tonistiigi/binfmt:latest" "$d"

  # Allowlist: a matching entry passes, a stale one fails.
  d=$(fresh); printf '      - run: docker run --rm busybox:1 true\n' >> "$d/.github/workflows/ci.yml"
  ST_ALLOW='.github/workflows/ci.yml::busybox:1::reason'
  selftest_case "allowlisted" 0 "1 allowlisted" "$d"
  d=$(fresh); cat >> "$d/.github/workflows/ci.yml" <<'EOF'
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: bash scripts/docker-registry-mirror.sh
      - id: bx
        continue-on-error: true
        uses: docker/setup-buildx-action@v4
      - if: steps.bx.outcome == 'failure'
        uses: docker/setup-buildx-action@v4
EOF
  ST_ALLOW='.github/workflows/ci.yml::moby/buildkit:buildx-stable-1::reason'
  selftest_case "buildkit behind registry-mirror, guarded, allowlisted" 0 "2 allowlisted" "$d"
  ST_ALLOW='.github/workflows/ci.yml::busybox:1::reason'
  d=$(fresh); selftest_case "stale allowlist entry" 1 "matches nothing" "$d"
  ST_ALLOW=''

  # RED, rule 2.
  d=$(fresh); sed -i 's|            public.ecr.aws/docker/library/alpine:3.21 \\|            \\|' "$d/.github/workflows/ci.yml"
  selftest_case "integration ref not pre-pulled" 1 "job it runs the integration tests but does not pre-pull public.ecr.aws/docker/library/alpine:3.21" "$d"
  d=$(fresh); sed -i '/pull-with-retry.sh public.ecr.aws/d' "$d/.github/workflows/ci.yml"
  selftest_case "fixture image not pre-pulled" 1 "job e2e names image public.ecr.aws/docker/library/nginx:1 but does not pre-pull" "$d"
  d=$(fresh); printf 'const notPulled = "ghcr.io/x/app:latest"\n' >> "$d/backend/it/a_test.go"
  selftest_case "non-mirror Go fixture string needs no pre-pull" 0 "0 violations" "$d"
  d=$(fresh); sed -i '/continue-on-error: true/d' "$d/.github/workflows/ci.yml"
  selftest_case "build step not guarded, last job of a file" 1 "ci.yml:[0-9]+: docker/build-push-action step has no id" "$d"
  d=$(fresh); sed -i '/name: Build (retry)/,$d' "$d/.github/workflows/ci.yml"
  selftest_case "build step has no retry" 1 "ci.yml:[0-9]+: docker/build-push-action step id build has no retry step" "$d"

  # Blind extractor must not read as clean.
  d=$(mktemp -d "$ST_TMP/case.XXXXXX"); mkdir -p "$d/docker"; echo 'RUN true' > "$d/docker/Dockerfile"
  selftest_case "no references" 2 "extractor is blind" "$d"

  if [ "$ST_FAILS" -gt 0 ]; then
    echo "dockerhub-pulls self-test: $ST_FAILS of $ST_RUN cases failed"
    return 1
  fi
  echo "dockerhub-pulls self-test: $ST_RUN cases passed"
  return 0
}

case "${1:-}" in
  --self-test) selftest; exit $? ;;
  -h|--help) usage; exit 0 ;;
  "") check_dir "$REPO_ROOT"; exit $? ;;
  -*) usage; exit 2 ;;
  *) check_dir "$1"; exit $? ;;
esac
