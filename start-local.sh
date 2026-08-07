#!/bin/bash

# Capstan - Startup Script

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== Capstan Local Setup ===${NC}"

# Check if we're in the right directory
if [ ! -f "docker-compose.yaml" ]; then
    echo -e "${RED}Error: docker-compose.yaml not found${NC}"
    echo "Please run this script from the project root directory"
    exit 1
fi

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo -e "${YELLOW}Warning: Docker is not running. Please start Docker first.${NC}"
    exit 1
fi

# Check if .env exists in backend
if [ ! -f "backend/.env" ]; then
    echo -e "${YELLOW}Creating backend/.env from .env.example...${NC}"
    cp backend/.env.example backend/.env
    echo -e "${GREEN}Created backend/.env${NC}"
fi

# Check if frontend .env exists
if [ ! -f "frontend/.env" ]; then
    echo -e "${YELLOW}Creating frontend/.env...${NC}"
    cat > frontend/.env << 'EOF'
VITE_API_BASE_URL=http://localhost:5001
EOF
    echo -e "${GREEN}Created frontend/.env${NC}"
fi

# Create local directories
mkdir -p /tmp/stacks /tmp/capstan-data
echo -e "${GREEN}Created local directories${NC}"

# Build and start services
echo -e "${YELLOW}Building and starting services...${NC}"

# Check if docker compose (v2) or docker-compose (v1) is available
if docker compose version > /dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
elif command -v docker-compose > /dev/null 2>&1; then
    COMPOSE_CMD="docker-compose"
else
    echo -e "${RED}Error: Neither 'docker compose' nor 'docker-compose' found${NC}"
    echo "Please install Docker Compose: https://docs.docker.com/compose/install/"
    exit 1
fi

$COMPOSE_CMD up -d --build

# Wait for the app to be ready.
#
# Read the container's own healthcheck rather than curling
# http://localhost:5001/health from the host. /health enforces a network
# allowlist (HEALTH_ALLOWED_NETWORKS) that is empty by default, and a host
# request to a published port arrives from the Docker bridge gateway, not
# loopback, so it is correctly rejected with 403. The container's HEALTHCHECK
# runs on loopback and is the signal that policy is designed around.
echo -e "${YELLOW}Waiting for backend to start...${NC}"
max_attempts=30
attempt=0
ready=0

container_id="$($COMPOSE_CMD ps -q app 2>/dev/null || true)"
if [ -z "$container_id" ]; then
    echo -e "${RED}No 'app' container found — the start above did not create one${NC}"
    echo "Check logs with: $COMPOSE_CMD logs app"
    exit 1
fi

state=""
health=""
while [ $attempt -lt $max_attempts ]; do
    state="$(docker inspect -f '{{.State.Status}}' "$container_id" 2>/dev/null || true)"
    health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id" 2>/dev/null || true)"

    case "${state}:${health}" in
        running:healthy)
            ready=1
            break
            ;;
        running:none)
            # Image carries no HEALTHCHECK — fall back to a host-reachable
            # endpoint. /api/v1/version is public and not network-restricted.
            if curl -sf http://localhost:5001/api/v1/version > /dev/null 2>&1; then
                ready=1
                break
            fi
            ;;
        running:unhealthy)
            echo ""
            echo -e "${RED}Backend started but its healthcheck is failing${NC}"
            echo "Check logs with: $COMPOSE_CMD logs app"
            exit 1
            ;;
        exited:*|dead:*)
            echo ""
            echo -e "${RED}Backend container stopped (state: ${state})${NC}"
            echo "Check logs with: $COMPOSE_CMD logs app"
            exit 1
            ;;
    esac

    attempt=$((attempt + 1))
    echo -n "."
    sleep 2
done

if [ $ready -ne 1 ]; then
    echo ""
    echo -e "${RED}Backend failed to start (last state: ${state:-unknown}/${health:-unknown})${NC}"
    echo "Check logs with: $COMPOSE_CMD logs app"
    exit 1
fi

# The healthcheck above only proves the app is healthy from inside the
# container. Confirm the published port answers from the host too, so
# "running but unreachable" reports as itself instead of as a startup timeout.
if ! curl -sf http://localhost:5001/api/v1/version > /dev/null 2>&1; then
    echo ""
    echo -e "${RED}Backend is healthy inside its container but http://localhost:5001 does not answer from this host${NC}"
    echo "The app is running — the published port or a network restriction is the problem, not startup."
    echo "Check logs with: $COMPOSE_CMD logs app"
    exit 1
fi

echo -e "${GREEN}Backend is healthy!${NC}"

# Display service URLs
echo ""
echo -e "${GREEN}=== Capstan is running! ===${NC}"
echo ""
echo "Services:"
echo "  - Backend:  http://localhost:5001"
echo "  - Frontend: http://localhost:3000"
echo "  - Health:   $COMPOSE_CMD ps  (/health is loopback-only by default, so it 403s from the host)"
echo ""
echo "Useful commands:"
echo "  - View logs:     $COMPOSE_CMD logs -f"
echo "  - Stop services: $COMPOSE_CMD down"
echo "  - Restart:       $COMPOSE_CMD restart"
echo "  - Backend only:  cd backend && make run"
echo ""
echo -e "${YELLOW}Note: Authentication is disabled for local testing (AUTH_DISABLED=true)${NC}"
echo -e "${YELLOW}Stacks are stored in: /tmp/stacks${NC}"
echo ""
