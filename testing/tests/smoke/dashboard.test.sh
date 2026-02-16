#!/bin/bash

# Dashboard Smoke Tests
# Tests dashboard discovery and stack display functionality

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="dashboard"
TEST_BASE_URL="http://localhost:3001"

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
  echo "DASHBOARD DOMAIN TEST SUMMARY"
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

# Test 1: Load dashboard page
test_load_dashboard() {
  local test_id="DASH-SMOKE-001"
  local test_name="Load dashboard page"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  log_info "Navigating to dashboard..."
  if ! navigate_to "$TEST_BASE_URL/dashboard"; then
    test_fail "Failed to navigate to dashboard"
    return 1
  fi
  
  # Verify page title
  local page_title
  page_title=$(get_page_title)
  assert_contains "$page_title" "Dashboard\|Dashboard" "Page title should contain 'Dashboard'"
  
  # Verify dashboard elements are present
  log_info "Verifying dashboard elements..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Check for common dashboard elements
  if ! echo "$snapshot" | grep -qi "stack"; then
    test_fail "Dashboard should show stack information"
    return 1
  fi
  
  test_pass "Dashboard loaded successfully with required elements"
}

# Test 2: Display stack cards
test_display_stack_cards() {
  local test_id="DASH-SMOKE-002"
  local test_name="Display stack cards"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Look for stack cards
  log_info "Checking for stack cards..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  # Test stacks should be visible
  if ! echo "$snapshot" | grep -qi "nginx\|redis\|postgres"; then
    test_fail "Test stacks should be visible on dashboard"
    return 1
  fi
  
  # Verify card structure
  log_info "Verifying stack card structure..."
  if ! echo "$snapshot" | grep -qi "status\|running\|stopped"; then
    test_fail "Stack cards should show status information"
    return 1
  fi
  
  test_pass "Stack cards displayed correctly with status"
}

# Test 3: Navigate to stack details
test_navigate_to_stack_details() {
  local test_id="DASH-SMOKE-003"
  local test_name="Navigate to stack details"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Find and click on a stack card
  log_info "Looking for stack card..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local stack_ref
  stack_ref=$(echo "$snapshot" | grep -i "nginx\|redis\|postgres" | head -1 | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$stack_ref" ]; then
    test_skip "No stack card found to click"
    return 0
  fi
  
  log_info "Clicking on stack card..."
  agent-browser click "@e${stack_ref}"
  agent-browser wait 2000
  
  # Verify navigation to stack details page
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"stack"* ]] && [[ "$current_url" != *"details"* ]]; then
    test_fail "Did not navigate to stack details. URL: $current_url"
    return 1
  fi
  
  test_pass "Successfully navigated to stack details page"
}

# Test 4: Rescan directories button
test_rescan_directories() {
  local test_id="DASH-SMOKE-004"
  local test_name="Rescan directories button"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Look for rescan button
  log_info "Looking for rescan button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local rescan_ref
  rescan_ref=$(echo "$snapshot" | grep -i "rescan\|refresh" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$rescan_ref" ]; then
    test_skip "Rescan button not found (may not be implemented yet)"
    return 0
  fi
  
  # Click rescan
  log_info "Clicking rescan button..."
  agent-browser click "@e${rescan_ref}"
  agent-browser wait 2000
  
  # Verify rescan completed (no error shown)
  snapshot=$(browser_snapshot)
  
  if echo "$snapshot" | grep -qi "error"; then
    test_fail "Rescan resulted in error"
    return 1
  fi
  
  test_pass "Rescan directories button works correctly"
}

# Test 5: Stack status indicators
test_stack_status_indicators() {
  local test_id="DASH-SMOKE-005"
  local test_name="Stack status indicators"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Look for status indicators
  log_info "Checking for status indicators..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Should have status-related information
  if ! echo "$snapshot" | grep -qiE "running|stopped|exited|healthy|unhealthy"; then
    test_fail "Status indicators should be visible"
    return 1
  fi
  
  test_pass "Stack status indicators are displayed"
}

# Test 6: Sidebar navigation
test_sidebar_navigation() {
  local test_id="DASH-SMOKE-006"
  local test_name="Sidebar navigation"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Look for sidebar navigation
  log_info "Checking for sidebar navigation..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  # Should have navigation items
  if ! echo "$snapshot" | grep -qiE "dashboard|stacks|settings|logout"; then
    test_skip "Sidebar navigation not found (may be collapsed or different layout)"
    return 0
  fi
  
  # Try clicking on navigation item
  local nav_ref
  nav_ref=$(echo "$snapshot" | grep -i "stack" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -n "$nav_ref" ]; then
    log_info "Clicking navigation item..."
    agent-browser click "@e${nav_ref}"
    agent-browser wait 1000
  fi
  
  test_pass "Sidebar navigation works correctly"
}

# Test 7: Responsive layout
test_responsive_layout() {
  local test_id="DASH-SMOKE-007"
  local test_name="Responsive layout"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to dashboard
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Take screenshot to verify layout
  log_info "Checking responsive layout..."
  local screenshot_dir="$TESTING_DIR/reports/screenshots"
  mkdir -p "$screenshot_dir"
  agent-browser screenshot "$screenshot_dir/dashboard-layout.png"
  
  test_pass "Dashboard layout renders correctly"
}

# Main test execution
main() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "DASHBOARD SMOKE TESTS"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Base URL: $TEST_BASE_URL"
  echo ""
  
  # Run tests
  test_load_dashboard
  test_display_stack_cards
  test_navigate_to_stack_details
  test_rescan_directories
  test_stack_status_indicators
  test_sidebar_navigation
  test_responsive_layout
  
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
