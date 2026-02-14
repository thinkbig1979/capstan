#!/bin/bash

# Docker Manager - Startup Script

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== Docker Manager Local Setup ===${NC}"

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
mkdir -p /tmp/stacks /tmp/docker-manager-data
echo -e "${GREEN}Created local directories${NC}"

# Build and start services
echo -e "${YELLOW}Building and starting services...${NC}"

# Check if docker compose (v2) or docker-compose (v1) is available
if command -v docker compose &> /dev/null; then
    docker compose up -d --build
elif command -v docker-compose &> /dev/null; then
    docker-compose up -d --build
else
    echo -e "${RED}Error: Neither 'docker compose' nor 'docker-compose' found${NC}"
    echo "Please install Docker Compose: https://docs.docker.com/compose/install/"
    exit 1
fi

# Wait for backend to be healthy
echo -e "${YELLOW}Waiting for backend to start...${NC}"
max_attempts=30
attempt=0

while [ $attempt -lt $max_attempts ]; do
    if curl -sf http://localhost:5001/health > /dev/null 2>&1; then
        echo -e "${GREEN}Backend is healthy!${NC}"
        break
    fi
    attempt=$((attempt + 1))
    echo -n "."
    sleep 2
done

if [ $attempt -eq $max_attempts ]; then
    echo -e "${RED}Backend failed to start${NC}"
    echo "Check logs with: docker compose logs backend (or docker-compose logs backend)"
    exit 1
fi

# Display service URLs
echo ""
echo -e "${GREEN}=== Docker Manager is running! ===${NC}"
echo ""
echo "Services:"
echo "  - Backend:  http://localhost:5001"
echo "  - Frontend: http://localhost:3000"
echo "  - Health:   http://localhost:5001/health"
echo ""
echo "Useful commands:"
echo "  - View logs:     docker-compose logs -f"
echo "  - Stop services: docker-compose down"
echo "  - Restart:       docker-compose restart"
echo "  - Backend only:  cd backend && make run"
echo ""
echo -e "${YELLOW}Note: Authentication is disabled for local testing (AUTH_DISABLED=true)${NC}"
echo -e "${YELLOW}Stacks are stored in: /tmp/stacks${NC}"
echo ""
