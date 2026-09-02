# Frontend build stage — runs on the build host's native arch ($BUILDPLATFORM),
# not the target arch, because the compiled output (static JS/CSS in dist/) is
# architecture-neutral. This avoids slow QEMU emulation of the Node build.
FROM --platform=$BUILDPLATFORM node:22-trixie-slim AS frontend-build

WORKDIR /app/frontend

# Pin pnpm for reproducible builds. A floating "npm install -g pnpm" silently
# tracks the latest pnpm, whose behavior changes (e.g. overrides moved to
# pnpm-workspace.yaml, default minimumReleaseAge supply-chain checks).
RUN npm install -g pnpm@11.5.1

# pnpm-workspace.yaml carries the dependency overrides for pnpm 10.16+/11.
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./

RUN pnpm install --frozen-lockfile --ignore-scripts && \
    pnpm rebuild esbuild

COPY frontend/ ./

RUN npm run build

# Backend build stage — also pinned to $BUILDPLATFORM. Go cross-compiles
# natively (CGO disabled), so we build on the fast host arch and emit a binary
# for the requested $TARGETARCH. TARGETOS/TARGETARCH are provided by Buildx.
FROM --platform=$BUILDPLATFORM golang:1.26.6-trixie AS backend-build

WORKDIR /app

# No package install here: the Debian golang images are buildpack-deps based and
# already carry git (2.47.3), which `go mod download` needs for VCS-resolved
# modules. The Alpine variant did not, hence the `apk add git` this replaces.

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
ARG TARGETOS
ARG TARGETARCH

# Build identity, stamped into internal/version at link time. The defaults keep
# a plain `docker build` (no --build-arg) honest: it reports "dev", not an empty
# string that reads as a bug. docker-publish.yml supplies the real values from
# the same docker/metadata-action output that names the image tag.
#
# A typo in an -X symbol path is accepted silently by the linker and leaves the
# default in place, so backend/internal/version/ldflags_test.go asserts these
# paths against the symbols actually declared in that package. Keep them in step.
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# The ${VAR:-default} rewrites cover the case the ARG defaults do not: an ARG
# default only applies when the arg is *absent*, so a --build-arg carrying an
# empty value (a CI expression that resolved to nothing) would stamp an empty
# string — the exact silent failure this feature exists to prevent.
RUN set -eu; \
    VERSION="${VERSION:-dev}"; \
    COMMIT="${COMMIT:-unknown}"; \
    BUILD_DATE="${BUILD_DATE:-unknown}"; \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a \
      -ldflags "-X github.com/thinkbig1979/capstan/backend/internal/version.Version=${VERSION} -X github.com/thinkbig1979/capstan/backend/internal/version.Commit=${COMMIT} -X github.com/thinkbig1979/capstan/backend/internal/version.BuildDate=${BUILD_DATE}" \
      -o server ./cmd/server

# Backup-tools fetch stage — downloads restic and rclone static binaries for the
# target architecture on the build host. Pinned to exact versions for
# reproducible builds.
#
# $BUILDPLATFORM is load-bearing, and was missing before the Debian move: every
# step here is arch-neutral (curl, sha256sum, bunzip2, unzip) on an archive
# chosen by $TARGETARCH, so nothing needs emulating. Left unpinned, the arm64 leg
# ran the package install under QEMU — 324s measured on 2026-08-07, against
# seconds for the apk it replaced. The consequence is that the fetched binaries
# cannot be executed in this stage, so the `restic version` / `rclone version`
# smoke checks moved to the runtime stage, where they exercise the exact
# binaries that ship rather than a copy in a throwaway stage.
FROM --platform=$BUILDPLATFORM debian:trixie-slim AS backup-tools

# Pinned versions — bump here when upgrading.
# Note: use RESTIC_VER / RCLONE_VER (not RESTIC_VERSION / RCLONE_VERSION) to
# avoid accidental collision with restic/rclone's own env-var flags.
ARG RESTIC_VER=0.19.1
ARG RCLONE_VER=1.74.4

# SHA256 checksums of the per-arch release archives, from the projects' official
# SHA256SUMS (restic GitHub release; rclone downloads.rclone.org). Pin these in
# lockstep with the versions above so a tampered or corrupted download fails the
# build. Restic ships .bz2, rclone ships .zip — the sums are of those archives.
ARG RESTIC_SHA256_AMD64=f415415624dcc452f2a02b8c33641791a8c6d6d3b65bbb3543fcf9a25151585c
ARG RESTIC_SHA256_ARM64=a5f64aaab53d51e311fa3829124c5b703f2d14cf187d8640b6be3b2b49376465
ARG RCLONE_SHA256_AMD64=fe435e0c36228e7c2f116a8701f01127bb1f694005fc11d1f27186c8bca4115d
ARG RCLONE_SHA256_ARM64=97685285c9ad6a0cf17d5844115d2a67245af6444db672187074bd9c358de419

ARG TARGETARCH

# ca-certificates is not optional here: with --no-install-recommends, curl does
# not pull it in, and both downloads below are HTTPS.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      bzip2 \
      ca-certificates \
      curl \
      unzip && \
    rm -rf /var/lib/apt/lists/*

# restic — official static binary from GitHub releases.
# Mapping: TARGETARCH (amd64 | arm64) -> restic arch string (amd64 | arm64)
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) RESTIC_SHA256="${RESTIC_SHA256_AMD64}" ;; \
      arm64) RESTIC_SHA256="${RESTIC_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL \
      "https://github.com/restic/restic/releases/download/v${RESTIC_VER}/restic_${RESTIC_VER}_linux_${TARGETARCH}.bz2" \
      -o /tmp/restic.bz2; \
    echo "${RESTIC_SHA256}  /tmp/restic.bz2" | sha256sum -c -; \
    bunzip2 /tmp/restic.bz2; \
    mv /tmp/restic /usr/local/bin/restic; \
    chmod +x /usr/local/bin/restic

# rclone — official static binary from downloads.rclone.org.
# Mapping: TARGETARCH (amd64 | arm64) -> rclone arch string (amd64 | arm64)
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) RCLONE_SHA256="${RCLONE_SHA256_AMD64}" ;; \
      arm64) RCLONE_SHA256="${RCLONE_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH=${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL \
      "https://downloads.rclone.org/v${RCLONE_VER}/rclone-v${RCLONE_VER}-linux-${TARGETARCH}.zip" \
      -o /tmp/rclone.zip; \
    echo "${RCLONE_SHA256}  /tmp/rclone.zip" | sha256sum -c -; \
    unzip -j /tmp/rclone.zip "rclone-v${RCLONE_VER}-linux-${TARGETARCH}/rclone" -d /tmp; \
    mv /tmp/rclone /usr/local/bin/rclone; \
    chmod +x /usr/local/bin/rclone; \
    rm /tmp/rclone.zip

# Docker CLI source stage. Debian has no docker-cli/buildx/compose packages
# outside Docker's own apt repo, and `docker.io` from Debian proper drags in the
# daemon — which this image must never contain, it talks to the host's. The
# official docker:*-cli image ships exactly the three static binaries we want,
# built for both target arches, so we lift them out of it.
#
# Pinned by index digest, not just by tag: the tag moves and a silently swapped
# CLI is the kind of change that only shows up in production. Dependabot tracks
# this FROM line like any other.
FROM docker:29-cli@sha256:3f4743208d2338c934d7b8bcfbe1bb54c0b2355c510ad5e0f31c0c4a54bd704e AS docker-cli

# Runtime stage
FROM debian:trixie-slim

WORKDIR /app

# `passwd` (groupadd/usermod/groupmod) and `tzdata` are already in trixie-slim;
# they are named anyway because entrypoint.sh's UID/GID remap depends on them
# and a future trim of the base should fail loudly here, not at container start.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      git \
      gosu \
      openssh-client \
      passwd \
      tzdata \
      wget && \
    rm -rf /var/lib/apt/lists/*

# The Docker CLI and its two plugins. /usr/local/libexec/... is one of the CLI's
# default plugin search paths, so `docker buildx` / `docker compose` resolve
# with no extra config — keep the destination in step with the source path.
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-buildx /usr/local/libexec/docker/cli-plugins/docker-buildx
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose

# Non-root runtime user. The Docker socket's group GID differs from host to
# host, so it is deliberately NOT hardcoded here — entrypoint.sh discovers the
# mounted socket's group at runtime and joins it. UID/GID default to 1000 and
# can be remapped to the host owner via the PUID/PGID env vars.
RUN groupadd -g 1000 appuser && \
    useradd -u 1000 -g appuser -m -d /home/appuser -s /bin/sh appuser

# Backup tools — copied from the dedicated fetch stage so the runtime layer
# stays lean (no curl/unzip/bzip2 installed).
COPY --from=backup-tools /usr/local/bin/restic /usr/local/bin/restic
COPY --from=backup-tools /usr/local/bin/rclone /usr/local/bin/rclone

# Smoke test the shipped binaries, in the image that ships them and for the arch
# it targets. A wrong-arch or truncated download is otherwise only discovered by
# a user's first backup. Adds no bytes: the commands write nothing.
RUN restic version && rclone version

# --chmod/--chown on the COPY rather than a following RUN. A `chmod +x` or a
# `chown -R` over /app forces overlayfs to copy the 50 MB server binary up into
# a second layer — 59.4 MB of pure duplicate in the 0.16.1 image. Setting the
# bits as the file lands costs nothing.
COPY --chown=appuser:appuser --chmod=755 --from=backend-build /app/server /app/
COPY --chown=appuser:appuser --from=frontend-build /app/frontend/dist /app/frontend/

# /opt/stacks is deliberately left root-owned, as before: PUID/PGID are matched
# to the host owner of the mounted stacks instead of rewriting its ownership.
RUN mkdir -p /opt/stacks /app/data && \
    chown appuser:appuser /app /app/data

COPY docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENV STACKS_DIR=/opt/stacks
ENV DATA_DIR=/app/data
ENV PORT=5001
# Remap the runtime user to match the host owner of your stacks/data volumes.
# Defaults to 1000 (the typical first Linux user); no host changes required.
ENV PUID=1000
ENV PGID=1000

EXPOSE 5001

# Liveness, not readiness: /health makes no Docker call, so a Docker daemon
# outage cannot mark this container unhealthy and get it restarted for an outage
# a restart would not fix (agent-os-69a).
#
# A real GET, not --spider. --spider sends HEAD, no HEAD route is registered, so
# every HEAD falls through to the SPA catch-all and returns 200 — the check
# passed even when GET /health returned 503, making it a bare port check.
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 -O /dev/null http://localhost:${PORT:-5001}/health || exit 1

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/app/server"]
