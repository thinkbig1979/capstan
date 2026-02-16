#!/bin/bash

# Browser Automation Utilities for E2E Tests
# Provides helper functions for using agent-browser

# Global browser session
declare -g BROWSER_SESSION_ID=""

# Colors for output
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export BLUE='\033[0;34m'
export NC='\033[0m' # No Color

# Logging functions
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

# Initialize browser session
browser_init() {
  local url="${1:-http://localhost:3001}"
  local timeout="${2:-30000}"
  
  log_info "Initializing browser session for: $url"
  
  local output
  output=$(agent-browser open "$url" --timeout "$timeout" 2>&1)
  
  if [ $? -ne 0 ]; then
    log_error "Failed to open browser: $output"
    return 1
  fi
  
  # Wait for React hydration
  log_info "Waiting for page hydration..."
  agent-browser wait 2000
  
  log_success "Browser session initialized"
  return 0
}

# Close browser session
browser_close() {
  if [ -n "$BROWSER_SESSION_ID" ]; then
    log_info "Closing browser session: $BROWSER_SESSION_ID"
    agent-browser close
    BROWSER_SESSION_ID=""
  fi
}

# Take snapshot of page
browser_snapshot() {
  local filter="${1:-}"
  
  if [ -n "$filter" ]; then
    agent-browser snapshot -i | grep -i "$filter"
  else
    agent-browser snapshot -i
  fi
}

# Get interactive elements with refs
get_interactive_elements() {
  local filter="${1:-}"
  
  browser_snapshot "$filter"
}

# Find element ref by text or attribute
find_element_ref() {
  local pattern="$1"
  local snapshot
  
  snapshot=$(browser_snapshot)
  
  # Search for pattern and extract ref number
  echo "$snapshot" | grep -i "$pattern" | grep -oP '(?<=@e)\d+' | head -1
}

# Find button ref by text
get_button_ref() {
  local button_text="$1"
  
  find_element_ref "$button_text.*button" || find_element_ref "$button_text"
}

# Find input ref by label or placeholder
get_input_ref() {
  local label_text="$1"
  
  find_element_ref "$label_text.*input" || find_element_ref "$label_text"
}

# Click an element by text
click_by_text() {
  local text="$1"
  local ref
  
  ref=$(find_element_ref "$text")
  
  if [ -z "$ref" ]; then
    log_error "Element not found with text: $text"
    return 1
  fi
  
  log_info "Clicking element: @e$ref ($text)"
  agent-browser click "@e$ref"
  
  # Wait for any page changes
  agent-browser wait 1000
  
  return 0
}

# Fill an input field by label
fill_by_label() {
  local label="$1"
  local value="$2"
  local ref
  
  ref=$(get_input_ref "$label")
  
  if [ -z "$ref" ]; then
    log_error "Input not found with label: $label"
    return 1
  fi
  
  log_info "Filling input: @e$ref with value: $value"
  agent-browser fill "@e$ref" "$value"
  
  # Wait for any validation
  agent-browser wait 500
  
  return 0
}

# Type text into element (doesn't clear first)
type_by_label() {
  local label="$1"
  local text="$2"
  local ref
  
  ref=$(get_input_ref "$label")
  
  if [ -z "$ref" ]; then
    log_error "Input not found with label: $label"
    return 1
  fi
  
  log_info "Typing into input: @e$ref"
  agent-browser type "@e$ref" "$text"
  
  return 0
}

# Press a key
press_key() {
  local key="$1"
  
  log_info "Pressing key: $key"
  agent-browser press "$key"
  
  agent-browser wait 500
}

# Navigate to URL
navigate_to() {
  local url="$1"
  local timeout="${2:-30000}"
  
  log_info "Navigating to: $url"
  agent-browser open "$url" --timeout "$timeout"
  agent-browser wait 2000
  
  return 0
}

# Get current URL
get_current_url() {
  agent-browser get url
}

# Get page title
get_page_title() {
  agent-browser get title
}

# Wait for element to appear
wait_for_element() {
  local selector="$1"
  local timeout="${2:-10000}"
  
  local start
  local elapsed
  
  start=$(date +%s)
  
  while true; do
    if agent-browser snapshot -i | grep -q "$selector"; then
      return 0
    fi
    
    elapsed=$(($(date +%s) - start))
    if [ $elapsed -gt $((timeout / 1000)) ]; then
      log_error "Element not found within timeout: $selector"
      return 1
    fi
    
    sleep 0.5
  done
}

# Wait for text to appear
wait_for_text() {
  local text="$1"
  local timeout="${2:-10000}"
  
  local start
  local elapsed
  
  start=$(date +%s)
  
  while true; do
    if agent-browser snapshot | grep -q "$text"; then
      return 0
    fi
    
    elapsed=$(($(date +%s) - start))
    if [ $elapsed -gt $((timeout / 1000)) ]; then
      log_error "Text not found within timeout: $text"
      return 1
    fi
    
    sleep 0.5
  done
}

# Take screenshot
take_screenshot() {
  local filename="$1"
  local full_page="${2:-false}"
  
  log_info "Taking screenshot: $filename"
  
  if [ "$full_page" = "true" ]; then
    agent-browser screenshot "$filename" --full
  else
    agent-browser screenshot "$filename"
  fi
}

# Scroll page
scroll_page() {
  local direction="${1:-down}"
  local amount="${2:-500}"
  
  log_info "Scrolling $direction by $amount pixels"
  
  if [ "$direction" = "down" ]; then
    agent-browser scroll down "$amount"
  elif [ "$direction" = "up" ]; then
    agent-browser scroll up "$amount"
  fi
  
  agent-browser wait 500
}

# Get text from element
get_element_text() {
  local ref="$1"
  
  agent-browser get text "@e$ref"
}

# Check if element is visible
is_element_visible() {
  local ref="$1"
  
  local snapshot
  snapshot=$(agent-browser snapshot -i)
  
  echo "$snapshot" | grep -q "@e$ref"
}

# Check if text is visible
is_text_visible() {
  local text="$1"
  
  local snapshot
  snapshot=$(agent-browser snapshot)
  
  echo "$snapshot" | grep -q "$text"
}

# Select option from dropdown (click-based workaround)
select_option() {
  local dropdown_label="$1"
  local option_text="$2"
  
  log_info "Selecting option: $option_text from dropdown: $dropdown_label"
  
  # Click dropdown to open
  click_by_text "$dropdown_label"
  agent-browser wait 500
  
  # Click option
  click_by_text "$option_text"
  agent-browser wait 500
  
  return 0
}

# Check/uncheck checkbox
toggle_checkbox() {
  local label="$1"
  local checked="${2:-true}"
  local ref
  
  ref=$(find_element_ref "$label.*checkbox" || find_element_ref "$label")
  
  if [ -z "$ref" ]; then
    log_error "Checkbox not found with label: $label"
    return 1
  fi
  
  local current_state
  current_state=$(agent-browser get text "@e$ref" || echo "")
  
  # Check if we need to toggle
  if [[ "$current_state" =~ Checked ]]; then
    if [ "$checked" = "false" ]; then
      agent-browser click "@e$ref"
    fi
  else
    if [ "$checked" = "true" ]; then
      agent-browser click "@e$ref"
    fi
  fi
  
  agent-browser wait 500
  return 0
}

# Submit form (press Enter or click submit button)
submit_form() {
  local submit_button_text="${1:-Submit}"
  
  log_info "Submitting form"
  
  # Try to find and click submit button
  if click_by_text "$submit_button_text"; then
    return 0
  fi
  
  # Fall back to pressing Enter
  press_key Enter
  
  return 0
}

# Wait for navigation to complete
wait_for_navigation() {
  local expected_url="${1:-}"
  local timeout="${2:-10000}"
  
  log_info "Waiting for navigation..."
  agent-browser wait 2000
  
  if [ -n "$expected_url" ]; then
    local current_url
    current_url=$(get_current_url)
    
    if [[ "$current_url" != *"$expected_url"* ]]; then
      log_error "Navigation failed. Expected: $expected_url, Got: $current_url"
      return 1
    fi
  fi
  
  return 0
}

# Get all links with specific text
get_links_by_text() {
  local text="$1"
  
  browser_snapshot | grep -i "link.*$text" || true
}

# Navigate by clicking a link
navigate_by_link() {
  local link_text="$1"
  
  log_info "Navigating via link: $link_text"
  click_by_text "$link_text"
  wait_for_navigation
}

# Switch to iframe (if needed)
switch_to_frame() {
  local frame_selector="$1"
  
  log_info "Switching to frame: $frame_selector"
  # agent-browser doesn't have direct frame support yet
  # This is a placeholder for future implementation
  log_warning "Frame switching not yet supported"
}

# Switch back to main content
switch_to_main_frame() {
  log_info "Switching to main frame"
  # Placeholder for future implementation
}

# Execute JavaScript in browser
execute_script() {
  local script="$1"
  
  log_info "Executing JavaScript: $script"
  # agent-browser doesn't have direct script execution yet
  # This is a placeholder for future implementation
  log_warning "Script execution not yet supported"
}

# Get browser console logs
get_console_logs() {
  # agent-browser doesn't expose console logs yet
  # This is a placeholder for future implementation
  log_warning "Console log access not yet supported"
}

# Clear browser cookies
clear_cookies() {
  log_info "Clearing cookies..."
  agent-browser cookies clear
  log_success "Cookies cleared"
}

# Clear browser storage
clear_storage() {
  log_info "Clearing browser storage..."
  
  # Try to clear storage, but don't fail if we get security errors
  # This can happen when the page has restricted access
  agent-browser storage local clear 2>/dev/null || log_warning "Local storage not accessible"
  log_success "Local storage cleared"
  
  agent-browser storage session clear 2>/dev/null || log_warning "Session storage not accessible"
  log_success "Session storage cleared"
  
  # Navigate to root to clear any client-side state
  navigate_to "http://localhost:3001"
  agent-browser wait 1000
}

# Set browser viewport size
set_viewport() {
  local width="$1"
  local height="$2"
  
  log_info "Setting viewport to ${width}x${height}"
  # agent-browser doesn't have direct viewport setting yet
  # This is a placeholder for future implementation
  log_warning "Viewport setting not yet supported"
}

# Get page source
get_page_source() {
  log_info "Getting page source"
  # agent-browser doesn't have direct source access yet
  # This is a placeholder for future implementation
  log_warning "Page source access not yet supported"
}

# Export functions for use in other scripts
export -f log_info
export -f log_success
export -f log_error
export -f log_warning
export -f browser_init
export -f browser_close
export -f browser_snapshot
export -f get_interactive_elements
export -f find_element_ref
export -f get_button_ref
export -f get_input_ref
export -f click_by_text
export -f fill_by_label
export -f type_by_label
export -f press_key
export -f navigate_to
export -f get_current_url
export -f get_page_title
export -f wait_for_element
export -f wait_for_text
export -f take_screenshot
export -f scroll_page
export -f get_element_text
export -f is_element_visible
export -f is_text_visible
export -f select_option
export -f toggle_checkbox
export -f submit_form
export -f wait_for_navigation
export -f get_links_by_text
export -f navigate_by_link
