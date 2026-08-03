# Frontend build stage — runs on the build host's native arch ($BUILDPLATFORM),
# not the target arch, because the compiled output (static JS/CSS in dist/) is
# architecture-neutral. This avoids slow QEMU emulation of the Node build.
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-build

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
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS backend-build

WORKDIR /app

RUN apk add --no-cache git

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
# reproducible builds. No QEMU emulation needed: the binaries are static and
# architecture-specific, resolved via $TARGETARCH provided by Buildx.
FROM alpine:3.24 AS backup-tools

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

RUN apk add --no-cache curl bzip2 unzip

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
    chmod +x /usr/local/bin/restic; \
    restic version

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
    rclone version; \
    rm /tmp/rclone.zip

# Runtime stage
FROM alpine:3.24

WORKDIR /app

RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-buildx \
    docker-cli-compose \
    git \
    openssh-client \
    shadow \
    su-exec \
    tzdata \
    wget

# Non-root runtime user. The Docker socket's group GID differs from host to
# host, so it is deliberately NOT hardcoded here — entrypoint.sh discovers the
# mounted socket's group at runtime and joins it. UID/GID default to 1000 and
# can be remapped to the host owner via the PUID/PGID env vars.
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# Backup tools — copied from the dedicated fetch stage so the runtime layer
# stays lean (no curl/unzip/bzip2 installed).
COPY --from=backup-tools /usr/local/bin/restic /usr/local/bin/restic
COPY --from=backup-tools /usr/local/bin/rclone /usr/local/bin/rclone

COPY --from=backend-build /app/server /app/
COPY --from=frontend-build /app/frontend/dist /app/frontend/

RUN chmod +x /app/server && \
    mkdir -p /opt/stacks /app/data && \
    chown -R appuser:appuser /app

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
