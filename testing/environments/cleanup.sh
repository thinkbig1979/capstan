#!/bin/bash

# Capstan Test Environment Cleanup
# Removes test Docker stacks and cleans up test data

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$SCRIPT_DIR")"
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

# Stop and remove test containers
cleanup_containers() {
  log_info "Stopping test containers..."
  
  # List of test container names
  local containers=(
    "capstan-nginx-test"
    "capstan-redis-test"
    "capstan-postgres-test"
    "capstan-env-test"
    "capstan-git-test"
    "capstan-complex-nginx"
    "capstan-complex-app"
    "capstan-complex-busybox"
  )
  
  local stopped_count=0
  for container in "${containers[@]}"; do
    if docker ps -q -f name="$container" | grep -q .; then
      docker stop "$container" > /dev/null 2>&1 && stopped_count=$((stopped_count + 1)) || true
    fi
  done
  
  log_success "Stopped $stopped_count test containers"
  
  log_info "Removing test containers..."
  
  local removed_count=0
  for container in "${containers[@]}"; do
    if docker ps -a -q -f name="$container" | grep -q .; then
      docker rm "$container" > /dev/null 2>&1 && removed_count=$((removed_count + 1)) || true
    fi
  done
  
  log_success "Removed $removed_count test containers"
}

# Remove test stacks directory
cleanup_stacks_dir() {
  log_info "Cleaning up test stacks directory..."
  
  if [ -d "$TEST_STACKS_DIR" ]; then
    log_info "Removing directory: $TEST_STACKS_DIR"
    rm -rf "$TEST_STACKS_DIR"
    log_success "Test stacks directory removed"
  else
    log_warning "Test stacks directory does not exist: $TEST_STACKS_DIR"
  fi
}

# Remove test volumes
cleanup_volumes() {
  log_info "Cleaning up test volumes..."
  
  local volumes=("pgdata" "data")
  local removed_count=0
  
  for volume in "${volumes[@]}"; do
    if docker volume ls -q -f name="$volume" | grep -q .; then
      docker volume rm "$volume" > /dev/null 2>&1 && removed_count=$((removed_count + 1)) || true
    fi
  done
  
  log_success "Removed $removed_count test volumes"
}

# Remove test networks
cleanup_networks() {
  log_info "Cleaning up test networks..."
  
  local networks=("test-network")
  local removed_count=0
  
  for network in "${networks[@]}"; do
    if docker network ls -q -f name="$network" | grep -q .; then
      docker network rm "$network" > /dev/null 2>&1 && removed_count=$((removed_count + 1)) || true
    fi
  done
  
  log_success "Removed $removed_count test networks"
}

# Clean up test reports (optional, based on flags)
cleanup_reports() {
  local keep_results="${1:-false}"
  
  if [ "$keep_results" = "false" ]; then
    log_info "Cleaning up test reports..."
    
    local reports_dir="$TESTING_DIR/reports"
    
    if [ -d "$reports_dir" ]; then
      # Remove old logs (keep last 5)
      find "$reports_dir/logs" -name "*.log" -type f -mtime +7 -delete 2>/dev/null || true
      
      # Remove old screenshots (keep last 10)
      find "$reports_dir/screenshots" -name "*.png" -type f -mtime +7 -delete 2>/dev/null || true
      
      log_success "Test reports cleaned (kept recent logs and screenshots)"
    fi
  else
    log_info "Keeping test reports (--keep-results flag set)"
  fi
}

# Clean up browser sessions
cleanup_browser_sessions() {
  log_info "Cleaning up browser sessions..."
  
  # Close any open browser sessions
  agent-browser close 2>/dev/null || true
  
  log_success "Browser sessions cleaned"
}

# Prune Docker system (optional, based on flags)
prune_docker() {
  local prune_all="${1:-false}"
  
  if [ "$prune_all" = "true" ]; then
    log_info "Pruning Docker system..."
    
    # Remove dangling images
    docker image prune -f > /dev/null 2>&1 || true
    
    # Remove unused build cache
    docker builder prune -f > /dev/null 2>&1 || true
    
    log_success "Docker system pruned"
  else
    log_info "Skipping Docker system prune (use --prune-all to enable)"
  fi
}

# Print cleanup summary
print_summary() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "CLEANUP SUMMARY"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "Test containers: Stopped and removed"
  echo "Test stacks directory: Removed ($TEST_STACKS_DIR)"
  echo "Test volumes: Removed"
  echo "Test networks: Removed"
  echo "Browser sessions: Closed"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
}

# Main cleanup function
cleanup() {
  local keep_results="${1:-false}"
  local prune_all="${2:-false}"
  
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "DOCKER MANAGER TEST ENVIRONMENT CLEANUP"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Keep results: $keep_results"
  log_info "Prune Docker: $prune_all"
  echo ""
  
  # Cleanup components
  cleanup_browser_sessions
  cleanup_containers
  cleanup_volumes
  cleanup_networks
  cleanup_stacks_dir
  cleanup_reports "$keep_results"
  prune_docker "$prune_all"
  
  # Print summary
  print_summary
  
  log_success "Test environment cleanup complete!"
}

# Dry run mode
dry_run() {
  log_info "DRY RUN MODE - No actual cleanup will be performed"
  echo ""
  
  log_info "Would stop and remove containers:"
  docker ps -a --filter "name=capstan-*" --format "  - {{.Names}}"
  echo ""
  
  log_info "Would remove volumes:"
  docker volume ls --filter "name=pgdata" --format "  - {{.Name}}"
  echo ""
  
  log_info "Would remove networks:"
  docker network ls --filter "name=test-network" --format "  - {{.Name}}"
  echo ""
  
  log_info "Would remove directory: $TEST_STACKS_DIR"
  echo ""
  
  log_info "Use without --dry-run to perform actual cleanup"
}

# Parse arguments
KEEP_RESULTS=false
PRUNE_ALL=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --keep-results)
      KEEP_RESULTS=true
      shift
      ;;
    --prune-all)
      PRUNE_ALL=true
      shift
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    *)
      log_error "Unknown option: $1"
      echo "Usage: $0 [--keep-results] [--prune-all] [--dry-run]"
      exit 1
      ;;
  esac
done

# Run cleanup
if [ "$DRY_RUN" = "true" ]; then
  dry_run
else
  cleanup "$KEEP_RESULTS" "$PRUNE_ALL"
fi
