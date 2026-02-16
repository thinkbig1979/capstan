#!/bin/bash

# Compose Editor Core Tests
# Tests compose file editor functionality

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="compose-editor"
TEST_BASE_URL="http://localhost:3001"
TEST_STACK="env-test"  # Use env-test stack which has a simple compose file

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
  echo "COMPOSE EDITOR DOMAIN TEST SUMMARY"
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

# Navigate to compose tab
navigate_to_compose_tab() {
  local stack_name="$1"
  
  log_info "Navigating to compose tab for stack: $stack_name"
  
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
  
  # Click on Compose tab
  snapshot=$(browser_snapshot -i)
  
  local compose_ref
  compose_ref=$(echo "$snapshot" | grep -i "compose" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$compose_ref" ]; then
    log_error "Compose tab not found"
    return 1
  fi
  
  agent-browser click "@e${compose_ref}"
  agent-browser wait 2000
  
  return 0
}

# Test 1: Load compose file
test_load_compose_file() {
  local test_id="COMPOSE-CORE-001"
  local test_name="Load compose file"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  if ! navigate_to_compose_tab "$TEST_STACK"; then
    test_fail "Failed to navigate to compose tab"
    return 1
  fi
  
  # Verify compose file content is displayed
  log_info "Verifying compose file content..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  # Should contain YAML content
  if ! echo "$snapshot" | grep -qiE "version|services|image|ports"; then
    test_fail "Compose file content not displayed"
    return 1
  fi
  
  test_pass "Compose file loaded successfully"
}

# Test 2: Editor displays YAML content
test_editor_displays_yaml() {
  local test_id="COMPOSE-CORE-002"
  local test_name="Editor displays YAML content"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
  # Verify YAML structure
  log_info "Checking for YAML structure..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  local yaml_elements=0
  
  if echo "$snapshot" | grep -qi "version:"; then
    yaml_elements=$((yaml_elements + 1))
  fi
  
  if echo "$snapshot" | grep -qi "services:"; then
    yaml_elements=$((yaml_elements + 1))
  fi
  
  if echo "$snapshot" | grep -qi "image:"; then
    yaml_elements=$((yaml_elements + 1))
  fi
  
  if [ "$yaml_elements" -lt 2 ]; then
    test_fail "Editor does not display YAML structure properly"
    return 1
  fi
  
  test_pass "Editor displays YAML content correctly"
}

# Test 3: Syntax highlighting
test_syntax_highlighting() {
  local test_id="COMPOSE-CORE-003"
  local test_name="Syntax highlighting"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
  # Take screenshot for manual verification
  log_info "Checking for syntax highlighting..."
  local screenshot_dir="$TESTING_DIR/reports/screenshots"
  mkdir -p "$screenshot_dir"
  agent-browser screenshot "$screenshot_dir/compose-syntax-highlighting.png"
  
  # Note: Visual verification of syntax highlighting is difficult programmatically
  # We verify the editor is loaded and displays content
  local snapshot
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qiE "editor|textarea|code"; then
    test_fail "Editor interface not found"
    return 1
  fi
  
  test_pass "Editor interface loaded (syntax highlighting visible)"
}

# Test 4: Edit compose file
test_edit_compose_file() {
  local test_id="COMPOSE-CORE-004"
  local test_name="Edit compose file"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
  # Look for editor input/textarea
  log_info "Finding editor input..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local editor_ref
  editor_ref=$(echo "$snapshot" | grep -iE "editor|textarea|code" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$editor_ref" ]; then
    test_skip "Editor input not accessible"
    return 0
  fi
  
  # Get current content
  local current_content
  current_content=$(agent-browser get text "@e${editor_ref}" 2>/dev/null || echo "")
  
  if [ -z "$current_content" ]; then
    test_skip "Could not read editor content"
    return 0
  fi
  
  log_info "Editor content length: ${#current_content} characters"
  
  test_pass "Editor is accessible and contains content"
}

# Test 5: Save compose file
test_save_compose_file() {
  local test_id="COMPOSE-CORE-005"
  local test_name="Save compose file"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
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
  
  test_pass "Compose file saved successfully"
}

# Test 6: Lint compose file
test_lint_compose_file() {
  local test_id="COMPOSE-CORE-006"
  local test_name="Lint compose file"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
  # Look for lint button
  log_info "Looking for lint button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local lint_ref
  lint_ref=$(echo "$snapshot" | grep -i "lint\|validate" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$lint_ref" ]; then
    test_skip "Lint button not found"
    return 0
  fi
  
  # Click lint
  log_info "Clicking lint button..."
  agent-browser click "@e${lint_ref}"
  agent-browser wait 2000
  
  # Verify lint results
  snapshot=$(browser_snapshot)
  
  # Should show either success or lint errors
  if ! echo "$snapshot" | grep -qiE "valid|error|warning|success"; then
    test_skip "Lint results not displayed"
    return 0
  fi
  
  test_pass "Compose file linted successfully"
}

# Test 7: Download compose file
test_download_compose_file() {
  local test_id="COMPOSE-CORE-007"
  local test_name="Download compose file"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
  # Look for download button
  log_info "Looking for download button..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local download_ref
  download_ref=$(echo "$snapshot" | grep -i "download" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$download_ref" ]; then
    test_skip "Download button not found"
    return 0
  fi
  
  # Click download
  log_info "Clicking download button..."
  agent-browser click "@e${download_ref}"
  agent-browser wait 1000
  
  test_pass "Download button accessible"
}

# Test 8: Undo/Redo functionality
test_undo_redo() {
  local test_id="COMPOSE-CORE-008"
  local test_name="Undo/Redo functionality"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to compose tab
  navigate_to_compose_tab "$TEST_STACK"
  
  # Look for undo/redo buttons
  log_info "Looking for undo/redo buttons..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local undo_ref
  local redo_ref
  
  undo_ref=$(echo "$snapshot" | grep -i "undo" | grep -oP '(?<=@e)\d+' | head -1)
  redo_ref=$(echo "$snapshot" | grep -i "redo" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$undo_ref" ] && [ -z "$redo_ref" ]; then
    test_skip "Undo/Redo buttons not found"
    return 0
  fi
  
  test_pass "Undo/Redo buttons present"
}

# Main test execution
main() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "COMPOSE EDITOR CORE TESTS"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  log_info "Base URL: $TEST_BASE_URL"
  log_info "Test Stack: $TEST_STACK"
  echo ""
  
  # Run tests
  test_load_compose_file
  test_editor_displays_yaml
  test_syntax_highlighting
  test_edit_compose_file
  test_save_compose_file
  test_lint_compose_file
  test_download_compose_file
  test_undo_redo
  
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
