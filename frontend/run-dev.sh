#!/bin/bash

# Frontend dev server quick start script

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=== Docker Manager Frontend - Quick Start ===${NC}"

# Check if we're in the right directory
if [ ! -f "package.json" ]; then
    echo -e "Error: package.json not found. Please run from frontend directory"
    exit 1
fi

# Check if .env exists
if [ ! -f ".env" ]; then
    echo -e "${YELLOW}Creating .env...${NC}"
    cat > .env << 'EOF'
VITE_API_BASE_URL=http://localhost:5001
EOF
fi

# Install dependencies if needed
if [ ! -d "node_modules" ]; then
    echo -e "${YELLOW}Installing dependencies...${NC}"
    npm install
fi

# Start dev server
echo -e "${YELLOW}Starting frontend dev server...${NC}"
echo ""
echo "Frontend will be available at: http://localhost:5173"
echo ""
echo "Press Ctrl+C to stop"
echo ""

npm run dev
