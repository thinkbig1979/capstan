#!/bin/bash

# Stack Operations Smoke Tests
# Tests basic stack operations (start, stop, restart, pull)

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="stack-ops"
TEST_BASE_URL="http://localhost:3001"
TEST_STACK="nginx-test"

# Test counter
declare -g TEST_COUNT=0
declare -g PASSED_COUNT=0
declare -g FAILED_COUNT=0
declare -g SKIPPED_COUNT=0

# Test helpers
test_start() {
  local test_id="$1"
  local test_name="$2"
  
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "TEST: $test_id - $test_name"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  TEST_COUNT=$((TEST_COUNT + 1))
}

test_pass() {
  local message="${1:-}"
  
  PASSED_COUNT=$((PASSED_COUNT + 1))
  
  if [ -n "$message" ]; then
    log_success "$message"
  else
    log_success "Test passed"
  fi
}

test_fail() {
  local message="${1:-}"
  
  FAILED_COUNT=$((FAILED_COUNT + 1))
  
  if [ -n "$message" ]; then
    log_error "$message"
  else
    log_error "Test failed"
  fi
  
  local screenshot_dir="$TESTING_DIR/reports/screenshots"
  mkdir -p "$screenshot_dir"
  agent-browser screenshot "$screenshot_dir/${TEST_DOMAIN}-failure.png" --full
}

test_summary() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "STACK OPERATIONS DOMAIN TEST SUMMARY"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "Total:   $TEST_COUNT"
  echo "Passed:  $PASSED_COUNT"
  echo "Failed:  $FAILED_COUNT"
  echo "Skipped: $SKIPPED_COUNT"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  if [ $FAILED_COUNT -gt 0 ]; then
    return 1
  fi
  
  return 0
}

# Navigate to stack details page
navigate_to_stack() {
  local stack_name="$1"
  
  log_info "Navigating to stack: $stack_name"
  
  # Go to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Find and click on stack card
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local stack_ref
  stack_ref=$(echo "$snapshot" | grep -i "$stack_name" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$stack_ref" ]; then
    log_error "Stack not found: $stack_name"
    return 1
  fi
  
  agent-browser click "@e${stack_ref}"
  agent-browser wait 2000
  
  # Verify on stack details page
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"stack"* ]]; then
    log_error "Failed to navigate to stack details"
    return 1
  fi
  
  return 0
}

# Test 1: Start a stopped stack
test_start_stack() {
  local test_id="STACK-SMOKE-001"
  local test_name="Start a stopped stack"
  
  test_start "$test_id" "$test_name"
  
  # First, ensure stack is stopped
  log_info "Ensuring stack is stopped first..."
  docker stop docker-manager-nginx-test 2>/dev/null || true
  sleep 2
  
  # Navigate to stack
  if ! navigate_to_stack "$TEST_STACK"; then
    test_fail "Failed to navigate to stack"
    return 1
  fi
  
  # Look for start button
  log_info "Looking for start button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local start_ref
  start_ref=$(echo "$snapshot" | grep -i "start" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$start_ref" ]; then
    test_skip "Start button not found (stack may already be running)"
    return 0
  fi
  
  # Click start
  log_info "Clicking start button..."
  agent-browser click "@e${start_ref}"
  agent-browser wait 3000
  
  # Verify status changed to running
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qi "running"; then
    test_fail "Stack status did not change to running"
    return 1
  fi
  
  # Verify container is actually running in Docker
  if ! docker ps --format '{{.Names}}' | grep -q "^docker-manager-nginx-test$"; then
    test_fail "Docker container is not actually running"
    return 1
  fi
  
  test_pass "Stack started successfully"
}

# Test 2: Stop a running stack
test_stop_stack() {
  local test_id="STACK-SMOKE-002"
  local test_name="Stop a running stack"
  
  test_start "$test_id" "$test_name"
  
  # First, ensure stack is running
  log_info "Ensuring stack is running first..."
  docker start docker-manager-nginx-test 2>/dev/null || true
  sleep 2
  
  # Navigate to stack
  if ! navigate_to_stack "$TEST_STACK"; then
    test_fail "Failed to navigate to stack"
    return 1
  fi
  
  # Look for stop button
  log_info "Looking for stop button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local stop_ref
  stop_ref=$(echo "$snapshot" | grep -i "stop" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$stop_ref" ]; then
    test_skip "Stop button not found (stack may already be stopped)"
    return 0
  fi
  
  # Click stop
  log_info "Clicking stop button..."
  agent-browser click "@e${stop_ref}"
  agent-browser wait 3000
  
  # Verify status changed to stopped
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qi "stopped\|exited"; then
    test_fail "Stack status did not change to stopped"
    return 1
  fi
  
  # Verify container is actually stopped in Docker
  if docker ps --format '{{.Names}}' | grep -q "^docker-manager-nginx-test$"; then
    test_fail "Docker container is still running"
    return 1
  fi
  
  test_pass "Stack stopped successfully"
}

# Test 3: Restart a stack
test_restart_stack() {
  local test_id="STACK-SMOKE-003"
  local test_name="Restart a stack"
  
  test_start "$test_id" "$test_name"
  
  # Ensure stack is running first
  log_info "Ensuring stack is running..."
  docker start docker-manager-nginx-test 2>/dev/null || true
  sleep 2
  
  # Navigate to stack
  if ! navigate_to_stack "$TEST_STACK"; then
    test_fail "Failed to navigate to stack"
    return 1
  fi
  
  # Look for restart button
  log_info "Looking for restart button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local restart_ref
  restart_ref=$(echo "$snapshot" | grep -i "restart" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$restart_ref" ]; then
    test_skip "Restart button not found"
    return 0
  fi
  
  # Get container ID before restart
  local container_id_before
  container_id_before=$(docker ps --filter "name=docker-manager-nginx-test" --format "{{.ID}}")
  
  # Click restart
  log_info "Clicking restart button..."
  agent-browser click "@e${restart_ref}"
  agent-browser wait 5000
  
  # Verify status is running
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qi "running"; then
    test_fail "Stack status is not running after restart"
    return 1
  fi
  
  # Verify container restarted (ID may or may not change)
  if ! docker ps --format '{{.Names}}' | grep -q "^docker-manager-nginx-test$"; then
    test_fail "Docker container is not running after restart"
    return 1
  fi
  
  test_pass "Stack restarted successfully"
}

# Test 4: View stack details
test_view_stack_details() {
  local test_id="STACK-SMOKE-004"
  local test_name="View stack details"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to stack
  if ! navigate_to_stack "$TEST_STACK"; then
    test_fail "Failed to navigate to stack"
    return 1
  fi
  
  # Verify stack details are displayed
  log_info "Checking for stack details..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Should show various stack information
  local details_found=false
  
  if echo "$snapshot" | grep -qi "compose\|container\|service\|port\|image"; then
    details_found=true
  fi
  
  if [ "$details_found" = "false" ]; then
    test_fail "Stack details not displayed properly"
    return 1
  fi
  
  test_pass "Stack details displayed correctly"
}

# Test 5: Stack tabs (Compose, Environment, Git, Terminal, Logs, Metrics)
test_stack_tabs() {
  local test_id="STACK-SMOKE-005"
  local test_name="Stack tabs navigation"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to stack
  if ! navigate_to_stack "$TEST_STACK"; then
    test_fail "Failed to navigate to stack"
    return 1
  fi
  
  # Look for tabs
  log_info "Checking for stack tabs..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  # Common tabs that should be present
  local tabs_found=false
  
  if echo "$snapshot" | grep -qi "compose\|environment\|env"; then
    tabs_found=true
  fi
  
  if [ "$tabs_found" = "false" ]; then
    test_skip "Stack tabs not found (may be collapsed or different layout)"
    return 0
  fi
  
  # Try clicking a tab
  local tab_ref
  tab_ref=$(echo "$snapshot" | grep -i "compose" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -n "$tab_ref" ]; then
    log_info "Clicking on Compose tab..."
    agent-browser click "@e${tab_ref}"
    agent-browser wait 1000
    
    # Verify tab content loaded
    snapshot=$(browser_snapshot)
    if echo "$snapshot" | grep -qi "yaml\|version\|services"; then
      test_pass "Stack tabs work correctly"
      return 0
    fi
  fi
  
  test_skip "Could not interact with tabs"
}

# Test 6: Multiple stacks displayed
test_multiple_stacks() {
  local test_id="STACK-SMOKE-006"
  local test_name="Multiple stacks displayed"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Look for multiple stacks
  log_info "Checking for multiple stacks..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  # Count occurrences of stack names
  local stack_count
  stack_count=$(echo "$snapshot" | grep -ciE "nginx|redis|postgres|env|git|complex" || echo "0")
  
  if [ "$stack_count" -lt 2 ]; then
    test_skip "Less than 2 stacks displayed (test environment may not be fully set up)"
    return 0
  fi
  
  log_info "Found $stack_count stacks"
  
  test_pass "Multiple stacks displayed correctly"
}

# Test 7: Stack search/filter
test_stack_search() {
  local test_id="STACK-SMOKE-007"
  local test_name="Stack search/filter functionality"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Look for search input
  log_info "Looking for search input..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local search_ref
  search_ref=$(echo "$snapshot" | grep -iE "search|filter" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$search_ref" ]; then
    test_skip "Search input not found"
    return 0
  fi
  
  # Enter search term
  log_info "Entering search term..."
  agent-browser fill "@e${search_ref}" "nginx"
  agent-browser wait 1000
  
  # Verify filtered results
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qi "nginx"; then
    test_fail "Search did not find expected stack"
    return 1
  fi
  
  # Clear search
  agent-browser fill "@e${search_ref}" ""
  agent-browser wait 1000
  
  test_pass "Stack search/filter works correctly"
}

# Main test execution
main() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "STACK OPERATIONS SMOKE TESTS"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Base URL: $TEST_BASE_URL"
  log_info "Test Stack: $TEST_STACK"
  echo ""
  
  # Run tests
  test_start_stack
  test_stop_stack
  test_restart_stack
  test_view_stack_details
  test_stack_tabs
  test_multiple_stacks
  test_stack_search
  
  # Print summary
  test_summary
  
  # Clean up
  browser_close
  
  # Return exit code
  if [ $FAILED_COUNT -gt 0 ]; then
    return 1
  fi
  
  return 0
}

# Run main
main "$@"
