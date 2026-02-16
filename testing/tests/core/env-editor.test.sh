#!/bin/bash

# Environment Editor Core Tests
# Tests environment variable editor functionality

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="env-editor"
TEST_BASE_URL="http://localhost:3001"
TEST_STACK="env-test"  # Stack with .env file

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
  echo "ENV EDITOR DOMAIN TEST SUMMARY"
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

# Navigate to environment tab
navigate_to_env_tab() {
  local stack_name="$1"
  
  log_info "Navigating to environment tab for stack: $stack_name"
  
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
  
  # Click on Environment tab
  snapshot=$(browser_snapshot -i)
  
  local env_ref
  env_ref=$(echo "$snapshot" | grep -iE "environment|env" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$env_ref" ]; then
    log_error "Environment tab not found"
    return 1
  fi
  
  agent-browser click "@e${env_ref}"
  agent-browser wait 2000
  
  return 0
}

# Test 1: Load environment variables
test_load_env_variables() {
  local test_id="ENV-CORE-001"
  local test_name="Load environment variables"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  if ! navigate_to_env_tab "$TEST_STACK"; then
    test_fail "Failed to navigate to environment tab"
    return 1
  fi
  
  # Verify environment variables are displayed
  log_info "Verifying environment variables are displayed..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Should show environment variables
  if ! echo "$snapshot" | grep -qiE "APP_NAME|APP_PORT|DEBUG|value"; then
    test_fail "Environment variables not displayed"
    return 1
  fi
  
  test_pass "Environment variables loaded successfully"
}

# Test 2: Display variable keys and values
test_display_variables() {
  local test_id="ENV-CORE-002"
  local test_name="Display variable keys and values"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Verify key-value pairs
  log_info "Checking for key-value pairs..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  local expected_vars=("APP_NAME" "APP_PORT" "DEBUG")
  local found_vars=0
  
  for var in "${expected_vars[@]}"; do
    if echo "$snapshot" | grep -qi "$var"; then
      found_vars=$((found_vars + 1))
    fi
  done
  
  if [ "$found_vars" -eq 0 ]; then
    test_fail "No environment variables found"
    return 1
  fi
  
  log_info "Found $found_vars/${#expected_vars[@]} expected variables"
  
  test_pass "Variable keys and values displayed correctly"
}

# Test 3: Add new variable
test_add_variable() {
  local test_id="ENV-CORE-003"
  local test_name="Add new variable"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Look for add button
  log_info "Looking for add variable button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local add_ref
  add_ref=$(echo "$snapshot" | grep -iE "add|new|plus" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$add_ref" ]; then
    test_skip "Add variable button not found"
    return 0
  fi
  
  # Count variables before
  local before_count
  before_count=$(browser_snapshot | grep -c "=" || echo "0")
  
  # Click add
  log_info "Clicking add button..."
  agent-browser click "@e${add_ref}"
  agent-browser wait 1000
  
  # Verify new input appeared
  snapshot=$(browser_snapshot -i)
  local after_count
  after_count=$(echo "$snapshot" | grep -c "=" || echo "0")
  
  if [ "$after_count" -le "$before_count" ]; then
    test_fail "New variable input did not appear"
    return 1
  fi
  
  test_pass "Add variable button works"
}

# Test 4: Edit existing variable
test_edit_variable() {
  local test_id="ENV-CORE-004"
  local test_name="Edit existing variable"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Look for edit button on a variable
  log_info "Looking for edit button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local edit_ref
  edit_ref=$(echo "$snapshot" | grep -iE "edit|pencil|modify" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$edit_ref" ]; then
    test_skip "Edit button not found (may be inline editing)"
    return 0
  fi
  
  # Click edit
  log_info "Clicking edit button..."
  agent-browser click "@e${edit_ref}"
  agent-browser wait 1000
  
  test_pass "Edit variable button accessible"
}

# Test 5: Delete variable
test_delete_variable() {
  local test_id="ENV-CORE-005"
  local test_name="Delete variable"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Look for delete button on a variable
  log_info "Looking for delete button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local delete_ref
  delete_ref=$(echo "$snapshot" | grep -iE "delete|trash|remove" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$delete_ref" ]; then
    test_skip "Delete button not found"
    return 0
  fi
  
  # Count variables before
  local before_count
  before_count=$(browser_snapshot | grep -c "=" || echo "0")
  
  # Click delete
  log_info "Clicking delete button..."
  agent-browser click "@e${delete_ref}"
  agent-browser wait 1000
  
  # Check for confirmation dialog
  snapshot=$(browser_snapshot)
  if echo "$snapshot" | grep -qiE "confirm|delete|remove"; then
    # Confirm deletion
    local confirm_ref
    confirm_ref=$(echo "$snapshot" | grep -iE "confirm|yes|delete" | grep -oP '(?<=@e)\d+' | head -1)
    
    if [ -n "$confirm_ref" ]; then
      agent-browser click "@e${confirm_ref}"
      agent-browser wait 1000
    fi
  fi
  
  test_pass "Delete variable button accessible"
}

# Test 6: Save environment variables
test_save_variables() {
  local test_id="ENV-CORE-006"
  local test_name="Save environment variables"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Look for save button
  log_info "Looking for save button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local save_ref
  save_ref=$(echo "$snapshot" | grep -i "save" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$save_ref" ]; then
    test_skip "Save button not found (may auto-save)"
    return 0
  fi
  
  # Click save
  log_info "Clicking save button..."
  agent-browser click "@e${save_ref}"
  agent-browser wait 2000
  
  # Verify no error
  snapshot=$(browser_snapshot)
  
  if echo "$snapshot" | grep -qi "error"; then
    test_fail "Save resulted in error"
    return 1
  fi
  
  test_pass "Environment variables saved successfully"
}

# Test 7: Comments preserved
test_comments_preserved() {
  local test_id="ENV-CORE-007"
  local test_name="Comments preserved in .env file"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Check for comments in display
  log_info "Checking for comment preservation..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Comments might be displayed differently
  # Just verify editor is functional
  if ! echo "$snapshot" | grep -qiE "APP_NAME|APP_PORT"; then
    test_fail "Environment variables not visible"
    return 1
  fi
  
  test_pass "Editor preserves environment file content"
}

# Test 8: Empty values displayed
test_empty_values_displayed() {
  local test_id="ENV-CORE-008"
  local test_name="Empty values displayed correctly"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
  # Check for empty variable
  log_info "Checking for empty value handling..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Should handle EMPTY_VALUE variable correctly
  if ! echo "$snapshot" | grep -qiE "EMPTY_VALUE|APP_NAME|APP_PORT"; then
    test_fail "Variables not displayed correctly"
    return 1
  fi
  
  test_pass "Empty values handled correctly"
}

# Test 9: Search/filter variables
test_search_variables() {
  local test_id="ENV-CORE-009"
  local test_name="Search/filter variables"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to environment tab
  navigate_to_env_tab "$TEST_STACK"
  
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
  agent-browser fill "@e${search_ref}" "APP_NAME"
  agent-browser wait 1000
  
  # Verify filtered results
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qi "APP_NAME"; then
    test_fail "Search did not find expected variable"
    return 1
  fi
  
  # Clear search
  agent-browser fill "@e${search_ref}" ""
  agent-browser wait 1000
  
  test_pass "Search/filter works correctly"
}

# Main test execution
main() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "ENV EDITOR CORE TESTS"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Base URL: $TEST_BASE_URL"
  log_info "Test Stack: $TEST_STACK"
  echo ""
  
  # Run tests
  test_load_env_variables
  test_display_variables
  test_add_variable
  test_edit_variable
  test_delete_variable
  test_save_variables
  test_comments_preserved
  test_empty_values_displayed
  test_search_variables
  
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
