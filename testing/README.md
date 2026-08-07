# Capstan E2E Testing

This directory contains end-to-end (E2E) browser tests for the Capstan application using the `agent-browser` skill and orchestrated parallel execution with multiple subagents.

## Quick Start

### Run All Tests

```bash
cd testing
./test-orchestrator.sh
```

### Run Specific Test Tier

```bash
# Run only smoke tests (critical path)
./test-orchestrator.sh smoke

# Run only core tests (main features)
./test-orchestrator.sh core

# Run only regression tests (edge cases)
./test-orchestrator.sh regression
```

### Run Specific Domain

```bash
# Run only authentication tests
./test-orchestrator.sh all auth

# Run only stack operations tests
./test-orchestrator.sh all stack-ops

# Run only editor tests
./test-orchestrator.sh all editors
```

### Run with Verbose Output

```bash
./test-orchestrator.sh --verbose smoke
```

### Keep Test Environment for Debugging

```bash
./test-orchestrator.sh --keep-session
```

## Directory Structure

```
testing/
├── README.md                          # This file
├── test-orchestrator.sh              # Master test coordinator
├── environments/
│   ├── setup.sh                      # Creates test environment
│   ├── cleanup.sh                    # Tears down test environment
│   └── stacks/                       # Test Docker Compose stacks
├── tests/
│   ├── smoke/                        # Smoke tests (critical path)
│   │   └── auth.test.sh             # Authentication tests
│   ├── core/                         # Core feature tests
│   ├── ui/                           # UI/UX tests
│   └── regression/                   # Edge case tests
├── reports/
│   ├── results.json                  # Test results summary
│   ├── screenshots/                  # Failure screenshots
│   └── logs/                         # Test execution logs
└── lib/
    ├── browser-utils.sh              # Browser automation helpers
    └── assert.sh                     # Test assertions
```

## Test Tiers

### Smoke Tests (Critical Path)
- **Purpose**: Validate critical functionality that must work
- **Execution Time**: ~3 minutes
- **Failure Impact**: **Stop deployment** - Blocks releases
- **Tests**: Authentication, dashboard, stack operations

### Core Tests (Main Features)
- **Purpose**: Validate main features and user flows
- **Execution Time**: ~15 minutes
- **Failure Impact**: Fix before release
- **Tests**: Editors, Git integration, terminal, logs, metrics

### Regression Tests (Edge Cases)
- **Purpose**: Catch edge cases and prevent regressions
- **Execution Time**: ~45 minutes
- **Failure Impact**: Schedule for fix
- **Tests**: Error handling, performance scenarios, edge cases

## Test Domains

Tests are organized into 5 functional domains that run in parallel:

| Domain | Description | Tests |
|--------|-------------|-------|
| **auth** | Authentication flows | Login, logout, protected routes |
| **stack-ops** | Stack operations | Start, stop, restart, pull |
| **editors** | File editors | Compose editor, environment editor |
| **git** | Git integration | Status, pull, history, diff |
| **ui** | UI features | Dark mode, responsive design, accessibility |

## Prerequisites

1. **agent-browser**: The browser automation CLI tool
   - Part of the browser-automating skill
   - Should be available in your PATH

2. **Docker**: For running test stacks
   - Test stacks use Docker Compose
   - Docker socket access required

3. **Capstan**: The application under test
   - Should be running on `http://localhost:3001`
   - Or will be started by the test orchestrator

4. **Bash**: All test scripts are Bash shell scripts
   - Requires Bash 4.0+

## Environment Setup

The test environment includes:

- **Test Docker Stacks**: 6 pre-configured stacks for testing
  - `nginx-test`: Simple nginx web server
  - `redis-test`: Redis server
  - `postgres-test`: PostgreSQL database
  - `env-test`: Stack with `.env` file
  - `git-test`: Stack with git repository
  - `complex-test`: Multi-service stack with networks

- **Test User**: Pre-configured credentials
  - Username: `testadmin@example.com`
  - Password: `TestPass123!`

- **Test Data**: Isolated directory structure
  - Test stacks: `/tmp/capstan-test-stacks`
  - Test reports: `testing/reports/`

### Manual Setup

```bash
# Set up test environment
./testing/environments/setup.sh

# Configure Capstan to use test stacks
export STACKS_DIR=/tmp/capstan-test-stacks

# Start Capstan (if not running)
cd ..
docker-compose up -d
```

### Manual Cleanup

```bash
# Clean up test environment
./testing/environments/cleanup.sh

# Clean up and keep test results
./testing/environments/cleanup.sh --keep-results

# Clean up and prune Docker
./testing/environments/cleanup.sh --prune-all

# Dry run (show what would be removed)
./testing/environments/cleanup.sh --dry-run
```

## Writing Tests

### Test Structure

Each test file should follow this structure:

```bash
#!/bin/bash
set -euo pipefail

# Source utilities
source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

# Test configuration
TEST_DOMAIN="my-domain"
TEST_BASE_URL="http://localhost:3001"

# Define test functions
test_my_feature() {
  local test_id="MY-DOMAIN-001"
  local test_name="My feature test"
  
  test_start "$test_id" "$test_name"
  
  # Test implementation
  navigate_to "$TEST_BASE_URL/my-page"
  assert_text_visible "Expected Text" "Text should be visible"
  
  test_pass "Test passed"
}

# Main execution
main() {
  test_my_feature
  # ... more tests
  
  test_summary
  browser_close
}

main "$@"
```

### Browser Automation Helpers

See `lib/browser-utils.sh` for available functions:

- `navigate_to <url>` - Navigate to a URL
- `click_by_text <text>` - Click element by text
- `fill_by_label <label> <value>` - Fill input by label
- `get_current_url` - Get current URL
- `get_page_title` - Get page title
- `wait_for_text <text>` - Wait for text to appear
- `take_screenshot <path>` - Take screenshot
- `browser_close` - Close browser session

### Assertion Helpers

See `lib/assert.sh` for available assertions:

- `assert_equals <actual> <expected>` - Assert values are equal
- `assert_contains <haystack> <needle>` - Assert string contains substring
- `assert_url_contains <path>` - Assert URL contains path
- `assert_text_visible <text>` - Assert text is visible
- `assert_element_visible <ref>` - Assert element is visible

## Test Execution Flow

```
1. Pre-flight Checks
   ├── Verify agent-browser installed
   ├── Verify Docker running
   └── Verify application running (or start it)

2. Environment Setup
   ├── Create test stacks directory
   ├── Create test Docker stacks
   └── Create test data directories

3. Parallel Test Execution
   ├── Spawn subagent for auth domain
   ├── Spawn subagent for stack-ops domain
   ├── Spawn subagent for editors domain
   ├── Spawn subagent for git domain
   └── Spawn subagent for ui domain

4. Result Aggregation
   ├── Collect results from all domains
   ├── Generate JSON report
   ├── Generate HTML report
   └── Print summary

5. Cleanup
   ├── Stop test containers
   ├── Remove test stacks
   └── Close browser sessions
```

## Viewing Results

### Console Output

Test results are printed to the console in real-time:

```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
TEST: AUTH-SMOKE-001 - Load login page
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
[PASS] Login page loaded successfully with all required elements
```

### JSON Report

```bash
# View JSON report
cat testing/reports/results.json

# Format JSON for readability
cat testing/reports/results.json | jq
```

### Screenshots

```bash
# List failure screenshots
ls testing/reports/screenshots/

# View specific screenshot
open testing/reports/screenshots/AUTH-SMOKE-001-failure.png
```

### Logs

```bash
# View test logs for specific domain
cat testing/reports/results/auth.log

# View all logs
ls testing/reports/logs/
```

## Troubleshooting

### Browser Not Starting

```bash
# Check agent-browser is installed
agent-browser --version

# Test agent-browser manually
agent-browser open http://example.com
```

### Docker Permission Errors

```bash
# Add user to docker group
sudo usermod -aG docker $USER

# Logout and login again
```

### Test Flakiness

```bash
# Run with verbose output
./test-orchestrator.sh --verbose

# Keep session for debugging
./test-orchestrator.sh --keep-session

# View screenshots
ls testing/reports/screenshots/
```

### Application Not Starting

```bash
# Check Docker Compose status
docker-compose ps

# View logs
docker-compose logs -f

# Check port availability
lsof -i :3001
```

## CI/CD Integration

### GitHub Actions

```yaml
name: E2E Tests

on: [push, pull_request]

jobs:
  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup environment
        run: ./testing/environments/setup.sh
      
      - name: Run smoke tests
        run: ./testing/test-orchestrator.sh smoke
      
      - name: Upload results
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: test-results
          path: testing/reports/
```

### GitLab CI

```yaml
e2e-tests:
  stage: test
  script:
    - ./testing/environments/setup.sh
    - ./testing/test-orchestrator.sh smoke
  artifacts:
    when: always
    paths:
      - testing/reports/
```

## Best Practices

1. **Keep Tests Isolated**: Each test should be independent
2. **Use Descriptive Names**: Make test IDs and names clear
3. **Clean Up After Tests**: Close browser sessions, remove test data
4. **Use Assertions**: Always use assertion helpers, don't rely on manual checks
5. **Take Screenshots on Failure**: Automatic screenshots help debugging
6. **Test Happy Path First**: Start with successful scenarios, then edge cases
7. **Keep Tests Fast**: Avoid unnecessary waits, use explicit waits
8. **Use Page Object Pattern**: Group related actions by page/feature

## Contributing

When adding new tests:

1. Determine the appropriate tier (smoke/core/regression)
2. Choose the appropriate domain (auth/stack-ops/editors/git/ui)
3. Follow the test structure template
4. Use browser-utils.sh and assert.sh helpers
5. Test locally before committing
6. Update this README if needed

## Support

For issues or questions:

- Review existing tests for examples
- Check browser-automating skill documentation
- Review Agent OS e2e standards

## License

These tests are part of the Capstan project and follow the same license.
