#!/bin/bash

# Docker Manager E2E Test Orchestrator - Quick Setup
# Automates admin account creation for testing

set -euo pipefail

echo "==================================="
echo "Creating Admin Account"
echo "==================================="
echo ""

# Navigate to setup page
echo "1. Opening browser..."
agent-browser open http://localhost:3001/setup --timeout 30000
agent-browser wait 5000

# Get form elements
echo "2. Getting form elements..."
snapshot=$(agent-browser snapshot -i)

# Find username and password fields
username_ref=$(echo "$snapshot" | grep -i "username" | grep -oP '(?<=@e)\d+' | head -1)
password_ref=$(echo "$snapshot" | grep -i "password" | grep -oP '(?<=@e)\d+' | head -1)
password_confirm_ref=$(echo "$snapshot" | grep -i "confirm\|repeat" | grep -oP '(?<=@e)\d+' | head -1)

echo "Username field: @$username_ref"
echo "Password field: @$password_ref"
echo "Confirm field: @$password_confirm_ref"

# Fill form
echo "3. Filling form..."
agent-browser fill "@e${username_ref}" "testadmin"
agent-browser wait 500
agent-browser fill "@e${password_ref}" "TestPass123!"
agent-browser wait 500
agent-browser fill "@e${password_confirm_ref}" "TestPass123!"
agent-browser wait 500

# Find and click submit button
echo "4. Finding submit button..."
snapshot=$(agent-browser snapshot -i)
submit_ref=$(echo "$snapshot" | grep -iE "submit|create|register" | grep -oP '(?<=@e)\d+' | head -1)

echo "Submit button: @$submit_ref"

# Click submit
echo "5. Submitting form..."
agent-browser click "@e${submit_ref}"
agent-browser wait 3000

# Verify redirect
echo "6. Verifying setup complete..."
current_url=$(agent-browser get url)
echo "Current URL: $current_url"

if [[ "$current_url" == *"login"* ]]; then
  echo "✓ Setup complete! Redirected to login page"
  agent-browser close
  exit 0
else
  echo "✗ Setup failed or unexpected redirect"
  echo "Taking screenshot..."
  agent-browser screenshot /tmp/setup-failure.png --full
  agent-browser close
  exit 1
fi
