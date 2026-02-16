# Multi-stage build for Docker Manager Backend
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o server ./cmd/server

# Final stage
FROM alpine:latest

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache \
    docker-cli \
    ca-certificates \
    wget

# Copy binary from builder
COPY --from=builder /build/server .

# Create directories
RUN mkdir -p /opt/stacks /app/data

# Set permissions
RUN chmod 755 /app/server

# Set default environment
ENV STACKS_DIR=/opt/stacks
ENV DATA_DIR=/app/data
ENV PORT=5001

# Expose port
EXPOSE 5001

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 -O /dev/null http://127.0.0.1:5001/health || exit 1

# Run the application
CMD ["./server"]
