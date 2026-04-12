# Frontend build stage
FROM node:20-alpine AS frontend-build

WORKDIR /app/frontend

COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

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
FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-compose \
    git \
    openssh-client \
    tzdata

COPY --from=backend-build /app/server /app/
COPY --from=frontend-build /app/frontend/dist /app/frontend/

RUN chmod +x /app/server

RUN addgroup -g 1000 -S appuser && \
    adduser -u 1000 -S -G appuser appuser && \
    chown -R appuser:appuser /app

USER appuser

EXPOSE 5001

HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:5001/health || exit 1

CMD ["/app/server"]
