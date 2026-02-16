#!/bin/bash

# Assertion Utilities for E2E Tests
# Provides helper functions for test assertions

# Colors for output
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export NC='\033[0m' # No Color

# Test tracking
declare -g CURRENT_TEST=""
declare -g TEST_COUNT=0
declare -g PASSED_COUNT=0
declare -g FAILED_COUNT=0
declare -g SKIPPED_COUNT=0

# Logging functions
log_info() {
  echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
  echo -e "${GREEN}[PASS]${NC} $*"
}

log_error() {
  echo -e "${RED}[FAIL]${NC} $*"
}

log_warning() {
  echo -e "${YELLOW}[SKIP]${NC} $*"
}

# Start a test
test_start() {
  local test_id="$1"
  local test_name="$2"
  
  CURRENT_TEST="$test_id"
  TEST_COUNT=$((TEST_COUNT + 1))
  
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "TEST: $test_id - $test_name"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

# Mark test as passed
test_pass() {
  local message="${1:-}"
  
  PASSED_COUNT=$((PASSED_COUNT + 1))
  
  if [ -n "$message" ]; then
    log_success "$CURRENT_TEST: $message"
  else
    log_success "$CURRENT_TEST: Assertion passed"
  fi
}

# Mark test as failed
test_fail() {
  local message="${1:-}"
  
  FAILED_COUNT=$((FAILED_COUNT + 1))
  
  if [ -n "$message" ]; then
    log_error "$CURRENT_TEST: $message"
  else
    log_error "$CURRENT_TEST: Assertion failed"
  fi
  
  # Take screenshot on failure
  if [ -n "${SCREENSHOT_ON_FAILURE:-}" ]; then
    local screenshot_dir="${SCREENSHOT_DIR:-./testing/reports/screenshots}"
    mkdir -p "$screenshot_dir"
    take_screenshot "$screenshot_dir/${CURRENT_TEST}-failure.png" --full
    log_info "Screenshot saved: $screenshot_dir/${CURRENT_TEST}-failure.png"
  fi
}

# Mark test as skipped
test_skip() {
  local message="${1:-}"
  
  SKIPPED_COUNT=$((SKIPPED_COUNT + 1))
  
  if [ -n "$message" ]; then
    log_warning "$CURRENT_TEST: $message"
  else
    log_warning "$CURRENT_TEST: Test skipped"
  fi
}

# Print test summary
test_summary() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "TEST SUMMARY"
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

# Assert that two values are equal
assert_equals() {
  local actual="$1"
  local expected="$2"
  local message="${3:-Values should be equal}"
  
  if [ "$actual" = "$expected" ]; then
    test_pass "$message (actual: '$actual', expected: '$expected')"
    return 0
  else
    test_fail "$message - Actual: '$actual', Expected: '$expected'"
    return 1
  fi
}

# Assert that two values are not equal
assert_not_equals() {
  local actual="$1"
  local not_expected="$2"
  local message="${3:-Values should not be equal}"
  
  if [ "$actual" != "$not_expected" ]; then
    test_pass "$message (actual: '$actual', not expected: '$not_expected')"
    return 0
  else
    test_fail "$message - Value should not be: '$actual'"
    return 1
  fi
}

# Assert that a string contains a substring
assert_contains() {
  local haystack="$1"
  local needle="$2"
  local message="${3:-String should contain substring}"
  
  if [[ "$haystack" == *"$needle"* ]]; then
    test_pass "$message (found: '$needle')"
    return 0
  else
    test_fail "$message - String does not contain: '$needle'"
    return 1
  fi
}

# Assert that a string does not contain a substring
assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  local message="${3:-String should not contain substring}"
  
  if [[ "$haystack" != *"$needle"* ]]; then
    test_pass "$message (does not contain: '$needle')"
    return 0
  else
    test_fail "$message - String should not contain: '$needle'"
    return 1
  fi
}

# Assert that a string matches a regex pattern
assert_matches() {
  local string="$1"
  local pattern="$2"
  local message="${3:-String should match pattern}"
  
  if [[ "$string" =~ $pattern ]]; then
    test_pass "$message (matches: '$pattern')"
    return 0
  else
    test_fail "$message - String does not match pattern: '$pattern'"
    return 1
  fi
}

# Assert that a value is true
assert_true() {
  local value="$1"
  local message="${2:-Value should be true}"
  
  if [ "$value" = "true" ] || [ "$value" = "1" ] || [ "$value" = "yes" ]; then
    test_pass "$message (value: '$value')"
    return 0
  else
    test_fail "$message - Expected true, got: '$value'"
    return 1
  fi
}

# Assert that a value is false
assert_false() {
  local value="$1"
  local message="${2:-Value should be false}"
  
  if [ "$value" = "false" ] || [ "$value" = "0" ] || [ "$value" = "no" ] || [ -z "$value" ]; then
    test_pass "$message (value: '$value')"
    return 0
  else
    test_fail "$message - Expected false, got: '$value'"
    return 1
  fi
}

# Assert that a value is empty
assert_empty() {
  local value="$1"
  local message="${2:-Value should be empty}"
  
  if [ -z "$value" ]; then
    test_pass "$message (value is empty)"
    return 0
  else
    test_fail "$message - Expected empty, got: '$value'"
    return 1
  fi
}

# Assert that a value is not empty
assert_not_empty() {
  local value="$1"
  local message="${2:-Value should not be empty}"
  
  if [ -n "$value" ]; then
    test_pass "$message (value: '$value')"
    return 0
  else
    test_fail "$message - Expected non-empty, got: '$value'"
    return 1
  fi
}

# Assert that a file exists
assert_file_exists() {
  local filepath="$1"
  local message="${2:-File should exist}"
  
  if [ -f "$filepath" ]; then
    test_pass "$message (file: '$filepath')"
    return 0
  else
    test_fail "$message - File not found: '$filepath'"
    return 1
  fi
}

# Assert that a file does not exist
assert_file_not_exists() {
  local filepath="$1"
  local message="${2:-File should not exist}"
  
  if [ ! -f "$filepath" ]; then
    test_pass "$message (file: '$filepath')"
    return 0
  else
    test_fail "$message - File should not exist: '$filepath'"
    return 1
  fi
}

# Assert that a directory exists
assert_dir_exists() {
  local dirpath="$1"
  local message="${2:-Directory should exist}"
  
  if [ -d "$dirpath" ]; then
    test_pass "$message (directory: '$dirpath')"
    return 0
  else
    test_fail "$message - Directory not found: '$dirpath'"
    return 1
  fi
}

# Assert that a URL contains a path
assert_url_contains() {
  local expected_path="$1"
  local message="${2:-URL should contain path}"
  
  local current_url
  current_url=$(get_current_url)
  
  if [[ "$current_url" == *"$expected_path"* ]]; then
    test_pass "$message (url: '$current_url', contains: '$expected_path')"
    return 0
  else
    test_fail "$message - URL does not contain path: '$expected_path', actual: '$current_url'"
    return 1
  fi
}

# Assert that current URL equals expected URL
assert_url_equals() {
  local expected_url="$1"
  local message="${2:-URL should match}"
  
  local current_url
  current_url=$(get_current_url)
  
  if [ "$current_url" = "$expected_url" ]; then
    test_pass "$message (url: '$current_url')"
    return 0
  else
    test_fail "$message - Expected URL: '$expected_url', actual: '$current_url'"
    return 1
  fi
}

# Assert that element is visible on page
assert_element_visible() {
  local element_ref="$1"
  local message="${2:-Element should be visible}"
  
  local snapshot
  snapshot=$(agent-browser snapshot -i)
  
  if echo "$snapshot" | grep -q "@e$element_ref"; then
    test_pass "$message (element: @e$element_ref)"
    return 0
  else
    test_fail "$message - Element not found: @e$element_ref"
    return 1
  fi
}

# Assert that element is not visible on page
assert_element_not_visible() {
  local element_ref="$1"
  local message="${2:-Element should not be visible}"
  
  local snapshot
  snapshot=$(agent-browser snapshot -i)
  
  if ! echo "$snapshot" | grep -q "@e$element_ref"; then
    test_pass "$message (element: @e$element_ref)"
    return 0
  else
    test_fail "$message - Element should not be visible: @e$element_ref"
    return 1
  fi
}

# Assert that text is visible on page
assert_text_visible() {
  local text="$1"
  local message="${2:-Text should be visible}"
  
  local snapshot
  snapshot=$(agent-browser snapshot)
  
  if echo "$snapshot" | grep -q "$text"; then
    test_pass "$message (text: '$text')"
    return 0
  else
    test_fail "$message - Text not found: '$text'"
    return 1
  fi
}

# Assert that text is not visible on page
assert_text_not_visible() {
  local text="$1"
  local message="${2:-Text should not be visible}"
  
  local snapshot
  snapshot=$(agent-browser snapshot)
  
  if ! echo "$snapshot" | grep -q "$text"; then
    test_pass "$message (text: '$text')"
    return 0
  else
    test_fail "$message - Text should not be visible: '$text'"
    return 1
  fi
}

# Assert that page title matches expected
assert_title_equals() {
  local expected_title="$1"
  local message="${2:-Page title should match}"
  
  local actual_title
  actual_title=$(get_page_title)
  
  if [ "$actual_title" = "$expected_title" ]; then
    test_pass "$message (title: '$actual_title')"
    return 0
  else
    test_fail "$message - Expected title: '$expected_title', actual: '$actual_title'"
    return 1
  fi
}

# Assert that a number is greater than another
assert_greater_than() {
  local actual="$1"
  local expected="$2"
  local message="${3:-Value should be greater}"
  
  if [ "$actual" -gt "$expected" ]; then
    test_pass "$message ($actual > $expected)"
    return 0
  else
    test_fail "$message - Expected $actual > $expected"
    return 1
  fi
}

# Assert that a number is less than another
assert_less_than() {
  local actual="$1"
  local expected="$2"
  local message="${3:-Value should be less}"
  
  if [ "$actual" -lt "$expected" ]; then
    test_pass "$message ($actual < $expected)"
    return 0
  else
    test_fail "$message - Expected $actual < $expected"
    return 1
  fi
}

# Assert that an HTTP request succeeds
assert_http_success() {
  local url="$1"
  local message="${2:-HTTP request should succeed}"
  
  local response_code
  response_code=$(curl -s -o /dev/null -w "%{http_code}" "$url")
  
  if [ "$response_code" -ge 200 ] && [ "$response_code" -lt 300 ]; then
    test_pass "$message (url: '$url', code: $response_code)"
    return 0
  else
    test_fail "$message - HTTP request failed with code: $response_code"
    return 1
  fi
}

# Assert that an HTTP request fails
assert_http_failure() {
  local url="$1"
  local message="${2:-HTTP request should fail}"
  
  local response_code
  response_code=$(curl -s -o /dev/null -w "%{http_code}" "$url")
  
  if [ "$response_code" -ge 400 ]; then
    test_pass "$message (url: '$url', code: $response_code)"
    return 0
  else
    test_fail "$message - HTTP request succeeded when it should fail (code: $response_code)"
    return 1
  fi
}

# Assert that Docker container is running
assert_container_running() {
  local container_name="$1"
  local message="${2:-Container should be running}"
  
  if docker ps --format '{{.Names}}' | grep -q "^${container_name}$"; then
    test_pass "$message (container: '$container_name')"
    return 0
  else
    test_fail "$message - Container not running: '$container_name'"
    return 1
  fi
}

# Assert that Docker container is stopped
assert_container_stopped() {
  local container_name="$1"
  local message="${2:-Container should be stopped}"
  
  if docker ps -a --format '{{.Names}}' | grep -q "^${container_name}$"; then
    if ! docker ps --format '{{.Names}}' | grep -q "^${container_name}$"; then
      test_pass "$message (container: '$container_name')"
      return 0
    fi
  fi
  
  test_fail "$message - Container is running or does not exist: '$container_name'"
  return 1
}

# Export functions
export -f log_info
export -f log_success
export -f log_error
export -f log_warning
export -f test_start
export -f test_pass
export -f test_fail
export -f test_skip
export -f test_summary
export -f assert_equals
export -f assert_not_equals
export -f assert_contains
export -f assert_not_contains
export -f assert_matches
export -f assert_true
export -f assert_false
export -f assert_empty
export -f assert_not_empty
export -f assert_file_exists
export -f assert_file_not_exists
export -f assert_dir_exists
export -f assert_url_contains
export -f assert_url_equals
export -f assert_element_visible
export -f assert_element_not_visible
export -f assert_text_visible
export -f assert_text_not_visible
export -f assert_title_equals
export -f assert_greater_than
export -f assert_less_than
export -f assert_http_success
export -f assert_http_failure
export -f assert_container_running
export -f assert_container_stopped
