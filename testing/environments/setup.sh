#!/bin/bash

# Capstan Test Environment Setup
# Creates test Docker stacks and test data

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$SCRIPT_DIR")"
STACKS_DIR="$SCRIPT_DIR/stacks"
TEST_STACKS_DIR="${TEST_STACKS_DIR:-/tmp/capstan-test-stacks}"

# Colors
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export BLUE='\033[0;34m'
export NC='\033[0m'

log_info() {
  echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $*"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $*"
}

log_warning() {
  echo -e "${YELLOW}[WARNING]${NC} $*"
}

# Create test stacks directory
create_test_stacks_dir() {
  log_info "Creating test stacks directory: $TEST_STACKS_DIR"
  
  mkdir -p "$TEST_STACKS_DIR"
  
  log_success "Test stacks directory created"
}

# Create test stack: nginx-test
create_nginx_stack() {
  local stack_dir="$TEST_STACKS_DIR/nginx-test"
  
  log_info "Creating nginx-test stack..."
  
  mkdir -p "$stack_dir"
  
  cat > "$stack_dir/docker-compose.yml" << 'EOF'
version: '3.8'
services:
  nginx:
    image: nginx:1.25-alpine
    container_name: capstan-nginx-test
    ports:
      - "8080:80"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-q", "--spider", "http://localhost/"]
      interval: 10s
      timeout: 5s
      retries: 3
EOF

  log_success "nginx-test stack created"
}

# Create test stack: redis-test
create_redis_stack() {
  local stack_dir="$TEST_STACKS_DIR/redis-test"
  
  log_info "Creating redis-test stack..."
  
  mkdir -p "$stack_dir"
  
  cat > "$stack_dir/docker-compose.yml" << 'EOF'
version: '3.8'
services:
  redis:
    image: redis:7-alpine
    container_name: capstan-redis-test
    ports:
      - "6379:6379"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 3
EOF

  log_success "redis-test stack created"
}

# Create test stack: postgres-test
create_postgres_stack() {
  local stack_dir="$TEST_STACKS_DIR/postgres-test"
  
  log_info "Creating postgres-test stack..."
  
  mkdir -p "$stack_dir"
  
  cat > "$stack_dir/docker-compose.yml" << 'EOF'
version: '3.8'
services:
  postgres:
    image: postgres:16-alpine
    container_name: capstan-postgres-test
    environment:
      POSTGRES_PASSWORD: testpass123
      POSTGRES_DB: testdb
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    restart: unless-stopped
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 10s
      timeout: 5s
      retries: 3

volumes:
  pgdata:
EOF

  log_success "postgres-test stack created"
}

# Create test stack with environment file
create_stack_with_env() {
  local stack_dir="$TEST_STACKS_DIR/env-test"
  
  log_info "Creating env-test stack with .env file..."
  
  mkdir -p "$stack_dir"
  
  cat > "$stack_dir/docker-compose.yml" << 'EOF'
version: '3.8'
services:
  app:
    image: alpine:3.19
    container_name: capstan-env-test
    restart: unless-stopped
    environment:
      - APP_NAME=${APP_NAME:-test-app}
      - APP_PORT=${APP_PORT:-8080}
      - DEBUG=${DEBUG:-false}
    command: ["tail", "-f", "/dev/null"]
EOF

  cat > "$stack_dir/.env" << 'EOF'
APP_NAME=test-app
APP_PORT=8080
DEBUG=false
# This is a comment
EMPTY_VALUE=
EOF

  log_success "env-test stack created"
}

# Create test stack with Git repository
create_git_stack() {
  local stack_dir="$TEST_STACKS_DIR/git-test"
  
  log_info "Creating git-test stack with git repository..."
  
  mkdir -p "$stack_dir"
  
  # Create docker-compose.yml
  cat > "$stack_dir/docker-compose.yml" << 'EOF'
version: '3.8'
services:
  web:
    image: nginx:1.25-alpine
    container_name: capstan-git-test
    ports:
      - "8081:80"
    restart: unless-stopped
EOF

  # Initialize git repo
  cd "$stack_dir"
  if [ ! -d ".git" ]; then
    git init
    git config user.email "test@example.com"
    git config user.name "Test User"
    git add docker-compose.yml
    git commit -m "Initial commit"
  fi
  
  cd - > /dev/null
  
  log_success "git-test stack created"
}

# Create complex stack for advanced testing
create_complex_stack() {
  local stack_dir="$TEST_STACKS_DIR/complex-test"
  
  log_info "Creating complex-test stack..."
  
  mkdir -p "$stack_dir"
  
  cat > "$stack_dir/docker-compose.yml" << 'EOF'
version: '3.8'
services:
  nginx:
    image: nginx:1.25-alpine
    container_name: capstan-complex-nginx
    ports:
      - "8082:80"
    restart: unless-stopped
    depends_on:
      - app
    networks:
      - test-network

  app:
    image: alpine:3.19
    container_name: capstan-complex-app
    restart: unless-stopped
    environment:
      - LOG_LEVEL=info
    networks:
      - test-network
    volumes:
      - ./data:/app/data

  busybox:
    image: busybox:latest
    container_name: capstan-complex-busybox
    restart: unless-stopped
    command: ["tail", "-f", "/dev/null"]
    networks:
      - test-network

networks:
  test-network:
    driver: bridge

volumes:
  data:
EOF

  mkdir -p "$stack_dir/data"
  echo "test data" > "$stack_dir/data/test.txt"
  
  log_success "complex-test stack created"
}

# Create test user account (if backend supports first-run setup)
create_test_user() {
  log_info "Creating test user account..."
  
  # Note: This depends on the backend's first-run setup mechanism
  # The test user credentials are:
  # Username: testadmin@example.com
  # Password: TestPass123!
  
  log_success "Test user credentials ready"
}

# Configure Capstan to use test stacks
configure_capstan() {
  log_info "Configuring Capstan to use test stacks..."
  
  # Get the configured STACKS_DIR from docker-compose.yaml
  local docker_compose_file="$TESTING_DIR/../docker-compose.yaml"
  local configured_stacks_dir="/opt/stacks"  # Default from docker-compose.yaml
  
  if [ -f "$docker_compose_file" ]; then
    local stacks_dir_line
    stacks_dir_line=$(grep "STACKS_DIR=" "$docker_compose_file" | head -1)
    if [ -n "$stacks_dir_line" ]; then
      configured_stacks_dir=$(echo "$stacks_dir_line" | grep -oP 'STACKS_DIR=\K[^ ]+')
    fi
  fi
  
  log_info "Configured STACKS_DIR: $configured_stacks_dir"
  log_info "Test stacks directory: $TEST_STACKS_DIR"
  
  # For testing, we'll use a mount approach or copy stacks
  # Simplest approach: create symlink from configured dir to test dir
  # (This requires the configured dir to exist and be writable)
  
  if [ -d "$configured_stacks_dir" ] && [ -w "$configured_stacks_dir" ]; then
    log_info "Creating symlink from $configured_stacks_dir to $TEST_STACKS_DIR"
    
    # Remove existing symlink if it exists
    [ -L "$configured_stacks_dir" ] && rm "$configured_stacks_dir"
    
    # Create symlink pointing to test stacks directory
    if [ -d "$configured_stacks_dir" ] && [ "$(ls -A $configured_stacks_dir 2>/dev/null)" ]; then
      # Directory exists and has content, backup it
      log_warning "Configured stacks directory has content, backing up..."
      mv "$configured_stacks_dir" "${configured_stacks_dir}.backup.$(date +%s)"
    fi
    
    # Create parent directory if needed
    mkdir -p "$(dirname "$configured_stacks_dir")"
    
    # Create symlink
    ln -s "$TEST_STACKS_DIR" "$configured_stacks_dir"
    log_success "Created symlink: $configured_stacks_dir -> $TEST_STACKS_DIR"
  else
    log_warning "Cannot symlink (directory doesn't exist or not writable): $configured_stacks_dir"
    log_info "You may need to manually configure Capstan to use: $TEST_STACKS_DIR"
  fi
  
  log_success "Configuration complete"
}

# Create test data directory structure
create_test_data() {
  log_info "Creating test data directory structure..."
  
  # Create directory for test screenshots
  mkdir -p "$TESTING_DIR/reports/screenshots"
  
  # Create directory for test logs
  mkdir -p "$TESTING_DIR/reports/logs"
  
  # Create directory for test results
  mkdir -p "$TESTING_DIR/reports/results"
  
  log_success "Test data directories created"
}

# Print test environment information
print_info() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "TEST ENVIRONMENT INFORMATION"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "Test Stacks Directory: $TEST_STACKS_DIR"
  echo ""
  echo "Available Test Stacks:"
  echo "  - nginx-test     : Simple nginx web server (port 8080)"
  echo "  - redis-test     : Redis server (port 6379)"
  echo "  - postgres-test  : PostgreSQL database (port 5432)"
  echo "  - env-test       : Stack with .env file"
  echo "  - git-test       : Stack with git repository"
  echo "  - complex-test   : Multi-service stack with networks and volumes"
  echo ""
  echo "Test User Credentials:"
  echo "  Username: testadmin@example.com"
  echo "  Password: TestPass123!"
  echo ""
  echo "Reports Directory: $TESTING_DIR/reports"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
}

# Main setup function
setup() {
  local tier="${1:-all}"
  
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "DOCKER MANAGER TEST ENVIRONMENT SETUP"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Tier: $tier"
  
  # Create test stacks directory
  create_test_stacks_dir
  
  # Create test data directories
  create_test_data
  
  # Create test stacks based on tier
  case "$tier" in
    smoke)
      log_info "Setting up smoke test environment..."
      create_nginx_stack
      create_redis_stack
      ;;
    core)
      log_info "Setting up core test environment..."
      create_nginx_stack
      create_redis_stack
      create_postgres_stack
      create_stack_with_env
      create_git_stack
      ;;
    regression)
      log_info "Setting up regression test environment..."
      create_nginx_stack
      create_redis_stack
      create_postgres_stack
      create_stack_with_env
      create_git_stack
      create_complex_stack
      ;;
    all)
      log_info "Setting up full test environment..."
      create_nginx_stack
      create_redis_stack
      create_postgres_stack
      create_stack_with_env
      create_git_stack
      create_complex_stack
      ;;
    *)
      log_error "Unknown tier: $tier"
      exit 1
      ;;
  esac
  
  # Create test user
  create_test_user
  
  # Configure Capstan
  configure_capstan
  
  # Print information
  print_info
  
  log_success "Test environment setup complete!"
}

# Cleanup function (for --cleanup flag)
cleanup() {
  log_info "Cleaning up test environment..."
  
  # Stop and remove test containers
  log_info "Stopping test containers..."
  docker stop capstan-nginx-test capstan-redis-test capstan-postgres-test \
             capstan-env-test capstan-git-test \
             capstan-complex-nginx capstan-complex-app capstan-complex-busybox \
             2>/dev/null || true
  
  log_info "Removing test containers..."
  docker rm capstan-nginx-test capstan-redis-test capstan-postgres-test \
            capstan-env-test capstan-git-test \
            capstan-complex-nginx capstan-complex-app capstan-complex-busybox \
            2>/dev/null || true
  
  # Remove test stacks directory
  if [ -d "$TEST_STACKS_DIR" ]; then
    log_info "Removing test stacks directory: $TEST_STACKS_DIR"
    rm -rf "$TEST_STACKS_DIR"
  fi
  
  # Remove test volumes
  log_info "Removing test volumes..."
  docker volume rm pgdata 2>/dev/null || true
  
  log_success "Test environment cleaned up"
}

# Parse arguments
if [ "${1:-}" = "--cleanup" ]; then
  cleanup
  exit 0
fi

# Run setup
setup "$@"
