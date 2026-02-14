#!/bin/bash

# Backend-only quick start script

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=== Docker Manager Backend - Quick Start ===${NC}"

# Check if we're in the right directory
if [ ! -f "go.mod" ]; then
    echo -e "Error: go.mod not found. Please run from backend directory"
    exit 1
fi

# Check if .env exists
if [ ! -f ".env" ]; then
    echo -e "${YELLOW}Creating .env from .env.example...${NC}"
    cp .env.example .env
fi

# Load environment
export $(cat .env | grep -v '^#' | xargs)

# Create directories
echo -e "${YELLOW}Creating directories...${NC}"
mkdir -p "$STACKS_DIR" "$DATA_DIR"

# Install dependencies if needed
if [ ! -d "vendor" ]; then
    echo -e "${YELLOW}Downloading Go dependencies...${NC}"
    go mod download
fi

# Build and run
echo -e "${YELLOW}Starting backend server...${NC}"
echo ""
echo "Backend will be available at: http://localhost:$PORT"
echo "Health check: http://localhost:$PORT/health"
echo ""
echo "Press Ctrl+C to stop"
echo ""

make run
