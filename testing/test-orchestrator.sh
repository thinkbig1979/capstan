#!/bin/bash

# Capstan E2E Test Orchestrator
# Coordinates parallel execution of multiple subagents for e2e browser testing

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib/browser-utils.sh"

# Configuration
TIER="${1:-all}"
DOMAIN="${2:-all}"
VERBOSE="${VERBOSE:-false}"
KEEP_SESSION="${KEEP_SESSION:-false}"
REPORTS_DIR="$SCRIPT_DIR/reports"

# Global state
declare -g TOTAL_TESTS=0
declare -g PASSED_TESTS=0
declare -g FAILED_TESTS=0
declare -g SKIPPED_TESTS=0
declare -g START_TIME
declare -g FAILED_DOMAINS=()

# Colors for output
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export BLUE='\033[0;34m'
export NC='\033[0m' # No Color

# Logging functions
log_info() {
  echo -e "${BLUE}[INFO]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $*"
}

log_success() {
  echo -e "${GREEN}[SUCCESS]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $*"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $*"
}

log_warning() {
  echo -e "${YELLOW}[WARNING]${NC} $(date '+%Y-%m-%d %H:%M:%S') - $*"
}

print_header() {
  local title="$1"
  local width=80
  echo ""
  printf "%${width}s\n" | tr ' ' '='
  printf "%*s\n" $(((${#title} + width) / 2)) "$title"
  printf "%${width}s\n" | tr ' ' '='
  echo ""
}

# Cleanup function
cleanup() {
  local exit_code=$?
  
  if [ "$exit_code" -ne 0 ] && [ -n "${FAILED_TESTS:-}" ]; then
    log_error "Test execution failed with exit code: $exit_code"
    log_info "Keeping test environment for debugging..."
  else
    if [ "$KEEP_SESSION" = "false" ]; then
      log_info "Cleaning up test environment..."
      "$SCRIPT_DIR/environments/cleanup.sh" || true
    fi
  fi
  
  return $exit_code
}

trap cleanup EXIT INT TERM

# Pre-flight checks
preflight_checks() {
  print_header "PRE-FLIGHT CHECKS"
  
  # Check if agent-browser is available
  if ! command -v agent-browser &> /dev/null; then
    log_error "agent-browser not found. Please install the browser-automating skill."
    exit 1
  fi
  log_success "agent-browser is installed"
  
  # Check if Docker is running
  if ! docker info &> /dev/null; then
    log_error "Docker daemon is not running"
    exit 1
  fi
  log_success "Docker daemon is running"
  
  # Check if port 3001 is available or app is running
  if curl -sf http://localhost:3001 > /dev/null; then
    log_success "Application is already running on http://localhost:3001"
  else
    log_warning "Application is not running on http://localhost:3001"
    log_info "Starting application..."
    cd "$SCRIPT_DIR/.."
    docker-compose up -d
    log_info "Waiting for application to be ready..."
    if ! wait_for_app "http://localhost:3001" 60; then
      log_error "Application failed to start within 60 seconds"
      exit 1
    fi
    log_success "Application is ready"
  fi
  
  # Create reports directory
  mkdir -p "$REPORTS_DIR/results" "$REPORTS_DIR/screenshots" "$REPORTS_DIR/html"
  
  log_success "All pre-flight checks passed"
}

# Wait for application to be ready
wait_for_app() {
  local url="$1"
  local timeout="$2"
  local elapsed=0
  
  while [ $elapsed -lt $timeout ]; do
    if curl -sf "$url" > /dev/null 2>&1; then
      return 0
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  
  return 1
}

# Setup test environment
setup_environment() {
  print_header "ENVIRONMENT SETUP"
  
  log_info "Setting up test environment..."
  
  # Pass tier name directly (not --tier flag)
  if [ "$TIER" != "all" ]; then
    "$SCRIPT_DIR/environments/setup.sh" "$TIER"
  else
    "$SCRIPT_DIR/environments/setup.sh"
  fi
  
  log_success "Test environment ready"
}

# Execute tests for a single domain
execute_domain() {
  local domain="$1"
  local domain_dir="$SCRIPT_DIR/tests"
  local domain_tests=()
  local exit_code=0
  
  log_info "Executing domain: $domain"
  
  # Find tests for the domain
  case "$domain" in
    auth)
      domain_tests+=("$domain_dir/smoke/auth.test.sh")
      ;;
    stack-ops)
      domain_tests+=("$domain_dir/smoke/stack-ops.test.sh")
      ;;
    editors)
      domain_tests+=("$domain_dir/core/compose-editor.test.sh")
      domain_tests+=("$domain_dir/core/env-editor.test.sh")
      ;;
    git)
      domain_tests+=("$domain_dir/core/git-integration.test.sh")
      ;;
    ui)
      domain_tests+=("$domain_dir/ui/dark-mode.test.sh")
      domain_tests+=("$domain_dir/ui/responsive.test.sh")
      ;;
    *)
      log_warning "Unknown domain: $domain"
      return 1
      ;;
  esac
  
  # Execute each test file
  for test_file in "${domain_tests[@]}"; do
    if [ -f "$test_file" ]; then
      log_info "Running: $test_file"
      
      local test_output
      local test_exit_code
      
      if [ "$VERBOSE" = "true" ]; then
        bash "$test_file" 2>&1 | tee -a "$REPORTS_DIR/results/${domain}.log"
        test_exit_code=${PIPESTATUS[0]}
      else
        test_output=$(bash "$test_file" 2>&1)
        test_exit_code=$?
        echo "$test_output" >> "$REPORTS_DIR/results/${domain}.log"
      fi
      
      if [ $test_exit_code -ne 0 ]; then
        log_error "Test file failed: $test_file (exit code: $test_exit_code)"
        exit_code=1
        FAILED_DOMAINS+=("$domain")
      fi
    else
      log_warning "Test file not found: $test_file"
    fi
  done
  
  return $exit_code
}

# Execute tests in parallel
execute_parallel() {
  print_header "PARALLEL TEST EXECUTION"
  
  local domains=()
  
  # Determine which domains to run based on tier
  case "$TIER" in
    smoke)
      domains=("auth" "stack-ops")
      ;;
    core)
      domains=("editors" "git" "ui")
      ;;
    regression)
      domains=("auth" "stack-ops" "editors" "git" "ui")
      ;;
    all)
      domains=("auth" "stack-ops" "editors" "git" "ui")
      ;;
    *)
      log_error "Unknown tier: $TIER"
      exit 1
      ;;
  esac
  
  # Override with specific domain if requested
  if [ "$DOMAIN" != "all" ]; then
    domains=("$DOMAIN")
  fi
  
  log_info "Executing tests for domains: ${domains[*]}"
  
  # Spawn parallel subagents
  local pids=()
  local domain_results=()
  
  for domain in "${domains[@]}"; do
    (
      log_info "Spawning subagent for domain: $domain"
      execute_domain "$domain"
    ) &
    pids+=($!)
  done
  
  # Wait for all subagents to complete
  local all_passed=true
  for i in "${!pids[@]}"; do
    local pid="${pids[$i]}"
    local domain="${domains[$i]}"
    
    if wait "$pid"; then
      log_success "Domain '$domain' completed successfully"
      domain_results[$i]="passed"
    else
      log_error "Domain '$domain' failed"
      domain_results[$i]="failed"
      all_passed=false
    fi
  done
  
  if [ "$all_passed" = "false" ]; then
    log_error "One or more domains failed"
    return 1
  fi
  
  log_success "All domains completed successfully"
  return 0
}

# Generate test report
generate_report() {
  print_header "GENERATING REPORT"
  
  local end_time
  local duration
  
  end_time=$(date +%s)
  duration=$((end_time - START_TIME))
  
  # Collect results from all domain logs
  local results_file="$REPORTS_DIR/results.json"
  
  echo "Generating results..."
  
  # Create JSON report
  cat > "$results_file" << EOF
{
  "test_run": {
    "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "duration_seconds": $duration,
    "tier": "$TIER",
    "total_tests": $TOTAL_TESTS,
    "passed": $PASSED_TESTS,
    "failed": $FAILED_TESTS,
    "skipped": $SKIPPED_TESTS,
    "success_rate": $(awk "BEGIN {printf \"%.2f\", $TOTAL_TESTS > 0 ? ($PASSED_TESTS / $TOTAL_TESTS) * 100 : 0}")
  },
  "domains": [
EOF

  # Add domain results
  local first=true
  for log_file in "$REPORTS_DIR/results"/*.log; do
    if [ -f "$log_file" ]; then
      [ "$first" = "true" ] && first=false || echo "," >> "$results_file"
      
      local domain
      domain=$(basename "$log_file" .log)
      
      # Parse log file for test results
      local passed
      local failed
      passed=$(grep -c "PASSED" "$log_file" || echo "0")
      failed=$(grep -c "FAILED" "$log_file" || echo "0")
      
      cat >> "$results_file" << EOF
    {
      "name": "$domain",
      "passed": $passed,
      "failed": $failed,
      "log_file": "$(basename "$log_file")"
    }
EOF
    fi
  done
  
  echo "" >> "$results_file"
  echo "  ]," >> "$results_file"
  
  # Add failed domains list
  echo "  \"failed_domains\": [" >> "$results_file"
  first=true
  for domain in "${FAILED_DOMAINS[@]}"; do
    [ "$first" = "true" ] && first=false || echo "," >> "$results_file"
    echo "    \"$domain\"" >> "$results_file"
  done
  echo "  ]" >> "$results_file"
  
  echo "}" >> "$results_file"
  
  log_success "Report generated: $results_file"
  
  # Generate HTML report
  if [ -f "$SCRIPT_DIR/scripts/generate-html-report.sh" ]; then
    log_info "Generating HTML report..."
    bash "$SCRIPT_DIR/scripts/generate-html-report.sh" || log_warning "HTML report generation failed"
  fi
  
  # Print summary
  print_summary
}

# Print test summary
print_summary() {
  print_header "TEST SUMMARY"
  
  echo "Tier: $TIER"
  echo "Total Tests: $TOTAL_TESTS"
  echo "Passed: $PASSED_TESTS"
  echo "Failed: $FAILED_TESTS"
  echo "Skipped: $SKIPPED_TESTS"
  echo ""
  
  if [ $FAILED_TESTS -gt 0 ]; then
    log_error "Failed Domains: ${FAILED_DOMAINS[*]}"
    echo ""
    echo "Failure Details:"
    echo "----------------"
    for log_file in "$REPORTS_DIR/results"/*.log; do
      if [ -f "$log_file" ]; then
        local failures
        failures=$(grep "FAILED" "$log_file" || true)
        if [ -n "$failures" ]; then
          echo ""
          echo "File: $(basename "$log_file")"
          echo "$failures"
        fi
      fi
    done
  else
    log_success "All tests passed!"
  fi
  
  echo ""
  echo "Screenshots: $REPORTS_DIR/screenshots/"
  echo "Logs: $REPORTS_DIR/results/"
  echo "Report: $REPORTS_DIR/results.json"
}

# Main execution
main() {
  START_TIME=$(date +%s)
  
  print_header "DOCKER MANAGER E2E TEST ORCHESTRATOR"
  
  log_info "Tier: $TIER"
  log_info "Domain: $DOMAIN"
  log_info "Verbose: $VERBOSE"
  log_info "Keep Session: $KEEP_SESSION"
  
  # Pre-flight checks
  preflight_checks
  
  # Setup environment
  setup_environment
  
  # Execute tests
  if execute_parallel; then
    log_success "Test execution completed"
  else
    log_error "Test execution failed"
  fi
  
  # Generate report
  generate_report
  
  # Exit with appropriate code
  if [ "$FAILED_TESTS" -gt 0 ]; then
    exit 1
  fi
  
  exit 0
}

# Print usage
usage() {
  cat << EOF
Usage: $0 [OPTIONS]

Options:
  TIER            Test tier to run (smoke|core|regression|all) [default: all]
  DOMAIN          Specific domain to run (auth|stack-ops|editors|git|ui) [default: all]
  --verbose       Enable verbose output
  --keep-session  Keep test environment after run for debugging
  --help          Show this help message

Examples:
  $0                          # Run all tests
  $0 smoke                    # Run smoke tests only
  $0 core auth                # Run auth tests from core tier
  $0 --verbose all            # Run all tests with verbose output
  $0 --keep-session smoke     # Run smoke tests and keep environment

Environment Variables:
  VERBOSE                      Enable verbose output [default: false]
  KEEP_SESSION                 Keep test environment [default: false]

EOF
  exit 0
}

# Parse arguments
if [[ "${1:-}" == "--help" ]] || [[ "${1:-}" == "-h" ]]; then
  usage
fi

if [[ "${1:-}" == "--verbose" ]]; then
  VERBOSE=true
  shift
fi

if [[ "${1:-}" == "--keep-session" ]]; then
  KEEP_SESSION=true
  shift
fi

# Run main
main "$@"
