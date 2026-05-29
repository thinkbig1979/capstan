# Capstan E2E Testing - Quick Reference

## Essential Commands

### Run Tests
```bash
# Run all tests
./testing/test-orchestrator.sh

# Run smoke tests only
./testing/test-orchestrator.sh smoke

# Run core tests only
./testing/test-orchestrator.sh core

# Run specific domain
./testing/test-orchestrator.sh all auth

# Verbose output
./testing/test-orchestrator.sh --verbose

# Keep environment for debugging
./testing/test-orchestrator.sh --keep-session
```

### Environment Management
```bash
# Set up test environment
./testing/environments/setup.sh

# Clean up test environment
./testing/environments/cleanup.sh

# Clean up but keep results
./testing/environments/cleanup.sh --keep-results

# Dry run (show what would be removed)
./testing/environments/cleanup.sh --dry-run
```

## Test Tiers

| Tier | Tests | Time | Fail Action |
|------|-------|------|-------------|
| **smoke** | 10-15 | ~3 min | Block deployment |
| **core** | 20-30 | ~15 min | Fix before release |
| **regression** | 30-40 | ~45 min | Schedule fix |

## Test Domains

- **auth**: Login, logout, protected routes
- **stack-ops**: Start, stop, restart, pull
- **editors**: Compose editor, env editor
- **git**: Status, pull, history, diff
- **ui**: Dark mode, responsive, accessibility

## Key Files

| File | Purpose |
|------|---------|
| `test-orchestrator.sh` | Master test coordinator |
| `lib/browser-utils.sh` | Browser automation helpers |
| `lib/assert.sh` | Test assertions |
| `environments/setup.sh` | Create test environment |
| `environments/cleanup.sh` | Remove test environment |
| `tests/smoke/auth.test.sh` | Example test file |

## Browser Automation Helpers

```bash
navigate_to "http://localhost:3001/login"
click_by_text "Login"
fill_by_label "Email" "user@example.com"
get_current_url
wait_for_text "Welcome"
take_screenshot "/path/to/screenshot.png"
browser_close
```

## Assertion Helpers

```bash
assert_equals "$actual" "$expected"
assert_contains "$haystack" "$needle"
assert_url_contains "/dashboard"
assert_text_visible "Welcome"
assert_element_visible "@e1"
```

## Test Structure

```bash
#!/bin/bash
set -euo pipefail

source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

test_my_feature() {
  test_start "ID" "Name"
  
  # Test code here
  navigate_to "http://localhost:3001"
  assert_text_visible "Expected" "Text should be visible"
  
  test_pass "Test passed"
}

main() {
  test_my_feature
  test_summary
  browser_close
}

main "$@"
```

## Viewing Results

```bash
# Console output (real-time)
./testing/test-orchestrator.sh smoke

# JSON report
cat testing/reports/results.json

# Screenshots
ls testing/reports/screenshots/

# Logs
cat testing/reports/results/auth.log
```

## Test User Credentials

- Username: `testadmin@example.com`
- Password: `TestPass123!`

## Test Stacks Location

- Directory: `/tmp/capstan-test-stacks`
- Configure: `export STACKS_DIR=/tmp/capstan-test-stacks`

## Troubleshooting

```bash
# Verbose mode
./testing/test-orchestrator.sh --verbose

# Keep environment
./testing/test-orchestrator.sh --keep-session

# Check browser
agent-browser --version

# Check Docker
docker ps
docker-compose ps

# View logs
docker-compose logs -f
cat testing/reports/results/*.log
```

## Quick Checklist

- [ ] `agent-browser` installed and working
- [ ] Docker running and accessible
- [ ] Application running on `http://localhost:3001`
- [ ] Test environment set up
- [ ] Scripts are executable (`chmod +x *.sh`)
- [ ] Test stacks directory created

## File Permissions

Make scripts executable:
```bash
chmod +x testing/test-orchestrator.sh
chmod +x testing/environments/*.sh
chmod +x testing/tests/**/*.sh
chmod +x testing/lib/*.sh
```

## Test Execution Flow

1. **Pre-flight**: Check tools and app status
2. **Setup**: Create test stacks and data
3. **Execute**: Run tests in parallel (5 domains)
4. **Report**: Generate JSON and HTML reports
5. **Cleanup**: Remove test artifacts (unless --keep-session)

## Integration Points

- **Base URL**: `http://localhost:3001`
- **Stacks Dir**: `/tmp/capstan-test-stacks`
- **Reports Dir**: `testing/reports/`
- **Test User**: `testadmin@example.com` / `TestPass123!`

## Common Patterns

### Navigate and Verify
```bash
navigate_to "http://localhost:3001/login"
assert_url_contains "/login"
assert_text_visible "Login"
```

### Fill Form and Submit
```bash
fill_by_label "Email" "user@example.com"
fill_by_label "Password" "password123"
click_by_text "Login"
```

### Wait for State Change
```bash
click_by_text "Start"
wait_for_text "Running"
assert_text_visible "Running" "Stack should be running"
```

## Tips

1. **Start with smoke tests** - Quick validation
2. **Use verbose mode** for debugging
3. **Keep session** when debugging failures
4. **Check screenshots** for visual issues
5. **Review logs** for error details
6. **Run locally** before CI/CD

## Further Reading

- Full documentation: `testing/README.md`
- Detailed plan: `testing/plan.md`
- Browser skill docs: `~/.claude/skills/agent-browser/`
- Agent OS standards: `.agent-os/standards/e2e-ui-testing-standards.md`
