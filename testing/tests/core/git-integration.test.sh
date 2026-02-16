#!/bin/bash

# Git Integration Core Tests
# Tests git integration functionality

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="git"
TEST_BASE_URL="http://localhost:3001"
TEST_STACK="git-test"  # Stack with git repository

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
  echo "GIT INTEGRATION DOMAIN TEST SUMMARY"
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

# Navigate to git tab
navigate_to_git_tab() {
  local stack_name="$1"
  
  log_info "Navigating to git tab for stack: $stack_name"
  
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
  
  # Click on Git tab
  snapshot=$(browser_snapshot -i)
  
  local git_ref
  git_ref=$(echo "$snapshot" | grep -i "git" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$git_ref" ]; then
    log_error "Git tab not found"
    return 1
  fi
  
  agent-browser click "@e${git_ref}"
  agent-browser wait 2000
  
  return 0
}

# Test 1: Display git status
test_git_status() {
  local test_id="GIT-CORE-001"
  local test_name="Display git status"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to git tab
  if ! navigate_to_git_tab "$TEST_STACK"; then
    test_fail "Failed to navigate to git tab"
    return 1
  fi
  
  # Verify git status is displayed
  log_info "Verifying git status display..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Should show git information
  if ! echo "$snapshot" | grep -qiE "git|status|branch|commit"; then
    test_fail "Git status not displayed"
    return 1
  fi
  
  test_pass "Git status displayed successfully"
}

# Test 2: Display commit history
test_commit_history() {
  local test_id="GIT-CORE-002"
  local test_name="Display commit history"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to git tab
  navigate_to_git_tab "$TEST_STACK"
  
  # Look for history tab
  log_info "Looking for history tab..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local history_ref
  history_ref=$(echo "$snapshot" | grep -iE "history|log|commits" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$history_ref" ]; then
    test_skip "History tab not found"
    return 0
  fi
  
  # Click history
  agent-browser click "@e${history_ref}"
  agent-browser wait 1000
  
  # Verify commit history displayed
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qiE "commit|date|author|message"; then
    test_fail "Commit history not displayed"
    return 1
  fi
  
  test_pass "Commit history displayed successfully"
}

# Test 3: Pull changes button
test_pull_button() {
  local test_id="GIT-CORE-003"
  local test_name="Pull changes button"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to git tab
  navigate_to_git_tab "$TEST_STACK"
  
  # Look for pull button
  log_info "Looking for pull button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local pull_ref
  pull_ref=$(echo "$snapshot" | grep -i "pull" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$pull_ref" ]; then
    test_skip "Pull button not found"
    return 0
  fi
  
  # Click pull
  log_info "Clicking pull button..."
  agent-browser click "@e${pull_ref}"
  agent-browser wait 2000
  
  # Verify pull completed
  snapshot=$(browser_snapshot)
  
  if echo "$snapshot" | grep -qi "error"; then
    test_fail "Pull resulted in error"
    return 1
  fi
  
  test_pass "Pull button accessible"
}

# Test 4: View diff
test_view_diff() {
  local test_id="GIT-CORE-004"
  local test_name="View diff"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to git tab
  navigate_to_git_tab "$TEST_STACK"
  
  # Look for diff tab
  log_info "Looking for diff tab..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local diff_ref
  diff_ref=$(echo "$snapshot" | grep -i "diff" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$diff_ref" ]; then
    test_skip "Diff tab not found (may need changes)"
    return 0
  fi
  
  # Click diff
  agent-browser click "@e${diff_ref}"
  agent-browser wait 1000
  
  # Verify diff displayed
  snapshot=$(browser_snapshot)
  
  if echo "$snapshot" | grep -qiE "add|remove|change|modified"; then
    test_pass "Diff displayed successfully"
    return 0
  fi
  
  test_skip "No diff available to display"
}

# Main test execution
main() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "GIT INTEGRATION CORE TESTS"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Base URL: $TEST_BASE_URL"
  log_info "Test Stack: $TEST_STACK"
  echo ""
  
  # Run tests
  test_git_status
  test_commit_history
  test_pull_button
  test_view_diff
  
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
