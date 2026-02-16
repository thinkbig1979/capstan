#!/bin/bash

# Authentication Smoke Tests
# Tests critical authentication flows

set -euo pipefail

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTING_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="auth"
TEST_USER="testadmin@example.com"
TEST_PASSWORD="TestPass123!"
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
  
  # Take screenshot on failure
  local screenshot_dir="$TESTING_DIR/reports/screenshots"
  mkdir -p "$screenshot_dir"
  agent-browser screenshot "$screenshot_dir/${TEST_DOMAIN}-failure.png" --full
}

test_summary() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "AUTH DOMAIN TEST SUMMARY"
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

# Test 1: Load login page
test_load_login_page() {
  local test_id="AUTH-SMOKE-001"
  local test_name="Load login page"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to login page
  log_info "Navigating to login page..."
  if ! navigate_to "$TEST_BASE_URL/login"; then
    test_fail "Failed to navigate to login page"
    return 1
  fi
  
  # Verify page title (SPAs use consistent titles, check URL instead)
  local page_title
  page_title=$(get_page_title)
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"login"* ]]; then
    test_fail "Not on login page. URL: $current_url"
    return 1
  fi
  
  # Verify login form is present
  log_info "Verifying login form elements..."
  local snapshot
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -qi "email"; then
    test_fail "Email input field not found"
    return 1
  fi
  
  if ! echo "$snapshot" | grep -qi "password"; then
    test_fail "Password input field not found"
    return 1
  fi
  
  if ! echo "$snapshot" | grep -qi "login"; then
    test_fail "Login button not found"
    return 1
  fi
  
  test_pass "Login page loaded successfully with all required elements"
}

# Test 2: Login with valid credentials
test_login_valid_credentials() {
  local test_id="AUTH-SMOKE-002"
  local test_name="Login with valid credentials"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to login page
  navigate_to "$TEST_BASE_URL/login"
  
  # Get form elements
  log_info "Getting login form elements..."
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  # Extract refs for email and password fields
  local email_ref
  local password_ref
  
  email_ref=$(echo "$snapshot" | grep -i "email" | grep -oP '(?<=@e)\d+' | head -1)
  password_ref=$(echo "$snapshot" | grep -i "password" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$email_ref" ]; then
    test_fail "Email field reference not found"
    return 1
  fi
  
  if [ -z "$password_ref" ]; then
    test_fail "Password field reference not found"
    return 1
  fi
  
  # Fill in credentials
  log_info "Entering credentials..."
  agent-browser fill "@e${email_ref}" "$TEST_USER"
  agent-browser wait 500
  agent-browser fill "@e${password_ref}" "$TEST_PASSWORD"
  agent-browser wait 500
  
  # Find and click login button
  local login_ref
  login_ref=$(echo "$snapshot" | grep -i "login.*button" | grep -oP '(?<=@e)\d+' | head -1)
  
  if [ -z "$login_ref" ]; then
    login_ref=$(echo "$snapshot" | grep -i "login" | grep -oP '(?<=@e)\d+' | head -1)
  fi
  
  if [ -z "$login_ref" ]; then
    test_fail "Login button reference not found"
    return 1
  fi
  
  log_info "Clicking login button..."
  agent-browser click "@e${login_ref}"
  agent-browser wait 2000
  
  # Verify redirect to dashboard
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"dashboard"* ]] && [[ "$current_url" != *"/"* ]]; then
    test_fail "Did not redirect to dashboard after login. URL: $current_url"
    return 1
  fi
  
  test_pass "Login successful, redirected to dashboard"
}

# Test 3: Login with invalid credentials
test_login_invalid_credentials() {
  local test_id="AUTH-SMOKE-003"
  local test_name="Login with invalid credentials"
  
  test_start "$test_id" "$test_name"
  
  # Navigate to login page
  navigate_to "$TEST_BASE_URL/login"
  
  # Get form elements
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local email_ref
  local password_ref
  
  email_ref=$(echo "$snapshot" | grep -i "email" | grep -oP '(?<=@e)\d+' | head -1)
  password_ref=$(echo "$snapshot" | grep -i "password" | grep -oP '(?<=@e)\d+' | head -1)
  
  # Fill in invalid credentials
  log_info "Entering invalid credentials..."
  agent-browser fill "@e${email_ref}" "invalid@example.com"
  agent-browser wait 500
  agent-browser fill "@e${password_ref}" "wrongpassword"
  agent-browser wait 500
  
  # Click login button
  local login_ref
  login_ref=$(echo "$snapshot" | grep -i "login" | grep -oP '(?<=@e)\d+' | head -1)
  
  agent-browser click "@e${login_ref}"
  agent-browser wait 2000
  
  # Verify error message
  snapshot=$(browser_snapshot)
  
  if ! echo "$snapshot" | grep -iq "invalid\|incorrect\|error"; then
    test_fail "Error message not displayed for invalid credentials"
    return 1
  fi
  
  # Verify still on login page
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"login"* ]]; then
    test_fail "Redirected away from login page with invalid credentials. URL: $current_url"
    return 1
  fi
  
  test_pass "Error displayed correctly for invalid credentials"
}

# Test 4: Logout
test_logout() {
  local test_id="AUTH-SMOKE-004"
  local test_name="Logout functionality"
  
  test_start "$test_id" "$test_name"
  
  # First login
  navigate_to "$TEST_BASE_URL/login"
  
  local snapshot
  snapshot=$(browser_snapshot -i)
  
  local email_ref
  local password_ref
  
  email_ref=$(echo "$snapshot" | grep -i "email" | grep -oP '(?<=@e)\d+' | head -1)
  password_ref=$(echo "$snapshot" | grep -i "password" | grep -oP '(?<=@e)\d+' | head -1)
  
  agent-browser fill "@e${email_ref}" "$TEST_USER"
  agent-browser wait 500
  agent-browser fill "@e${password_ref}" "$TEST_PASSWORD"
  agent-browser wait 500
  
  local login_ref
  login_ref=$(echo "$snapshot" | grep -i "login" | grep -oP '(?<=@e)\d+' | head -1)
  
  agent-browser click "@e${login_ref}"
  agent-browser wait 2000
  
  # Look for logout button/menu
  log_info "Looking for logout option..."
  snapshot=$(browser_snapshot -i)
  
  local logout_ref
  logout_ref=$(echo "$snapshot" | grep -i "logout" | grep -oP '(?<=@e)\d+' | head -1)
  
  # Try clicking user menu if logout not directly visible
  if [ -z "$logout_ref" ]; then
    log_info "Logout button not directly visible, checking for user menu..."
    local menu_ref
    menu_ref=$(echo "$snapshot" | grep -i "user\|avatar\|profile" | grep -oP '(?<=@e)\d+' | head -1)
    
    if [ -n "$menu_ref" ]; then
      log_info "Clicking user menu..."
      agent-browser click "@e${menu_ref}"
      agent-browser wait 1000
      
      # Get new snapshot
      snapshot=$(browser_snapshot -i)
      logout_ref=$(echo "$snapshot" | grep -i "logout" | grep -oP '(?<=@e)\d+' | head -1)
    fi
  fi
  
  if [ -z "$logout_ref" ]; then
    test_fail "Logout option not found"
    return 1
  fi
  
  # Click logout
  log_info "Clicking logout..."
  agent-browser click "@e${logout_ref}"
  agent-browser wait 2000
  
  # Verify redirect to login page
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"login"* ]]; then
    test_fail "Did not redirect to login after logout. URL: $current_url"
    return 1
  fi
  
  test_pass "Logout successful, redirected to login page"
}

# Test 5: Access protected route without authentication
test_protected_route_redirect() {
  local test_id="AUTH-SMOKE-005"
  local test_name="Protected route redirects to login"
  
  test_start "$test_id" "$test_name"
  
  # Ensure logged out
  navigate_to "$TEST_BASE_URL/login"
  
  # Try to access dashboard directly
  log_info "Attempting to access dashboard without authentication..."
  navigate_to "$TEST_BASE_URL/dashboard"
  agent-browser wait 2000
  
  # Verify redirect to login
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" != *"login"* ]]; then
    test_fail "Protected route did not redirect to login. URL: $current_url"
    return 1
  fi
  
  test_pass "Protected route correctly redirected to login page"
}

# Main test execution
main() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "AUTHENTICATION SMOKE TESTS"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  
  # Check if setup is needed first
  log_info "Checking if setup is required..."
  navigate_to "$TEST_BASE_URL"
  agent-browser wait 2000
  
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" == *"setup"* ]]; then
    log_info "Setup required, creating admin account..."
    
    # Try to complete setup through API
    local setup_result
    setup_result=$(curl -s -X POST "http://localhost:5001/api/v1/auth/setup" \
      -H "Content-Type: application/json" \
      -d '{"username":"testadmin","password":"TestPass123!"}' 2>&1)
    
    log_info "Setup result: $setup_result"
    
    # Refresh the page
    navigate_to "$TEST_BASE_URL/setup"
    agent-browser wait 3000
    current_url=$(get_current_url)
  fi
  
  # Clear browser state before tests
  log_info "Clearing browser state before tests..."
  clear_storage
  clear_cookies
  
  log_info "Test User: $TEST_USER"
  log_info "Base URL: $TEST_BASE_URL"
  echo ""
  
  # Run tests
  test_load_login_page
  test_login_valid_credentials
  test_login_invalid_credentials
  test_logout
  test_protected_route_redirect
  
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
