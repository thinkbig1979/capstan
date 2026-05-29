# Frontend build stage
FROM node:22-alpine AS frontend-build

WORKDIR /app/frontend

RUN npm install -g pnpm

COPY frontend/package.json frontend/pnpm-lock.yaml ./

RUN pnpm install --frozen-lockfile --ignore-scripts && \
    pnpm rebuild esbuild

COPY frontend/ ./

RUN npm run build

# Backend build stage
FROM golang:1.24-alpine AS backend-build

WORKDIR /app

RUN apk add --no-cache git

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -o server ./cmd/server

# Runtime stage
FROM alpine:3.21

WORKDIR /app

RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
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

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:5001/health || exit 1

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/app/server"]
