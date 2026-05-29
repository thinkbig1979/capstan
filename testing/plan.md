# Capstan E2E Browser Testing Plan

**Version**: 1.0.0  
**Date**: 2025-02-15  
**Testing Framework**: agent-browser (Playwright)  
**Test Strategy**: Orchestrated Parallel Execution with Multiple Subagents  

---

## Executive Summary

This plan outlines a comprehensive end-to-end (E2E) browser testing strategy for the Capstan application using the `browser-automating` skill. The approach uses multiple parallel subagents to execute tests efficiently, with all test artifacts contained within the `testing/` directory to avoid polluting the system.

### Key Objectives

1. **Full Feature Coverage**: Test all major user flows from the smoke test checklist
2. **Parallel Execution**: Use multiple subagents for 60-80% faster test execution
3. **Isolated Environment**: All test data, scripts, and outputs in `testing/` folder
4. **Headless Automation**: Use `agent-browser` for reliable, automated testing
5. **Continuous Integration Ready**: Tests can run in CI/CD pipelines

---

## Architecture Overview

### Test Execution Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Test Orchestrator (Master Agent)              │
│                                                                  │
│  1. Coordinates test execution across multiple subagents        │
│  2. Manages test dependencies and execution order                │
│  3. Aggregates results and generates reports                    │
│  4. Handles failures and retry logic                            │
└─────────────────────────────────────────────────────────────────┘
                                │
                                ├─┬─┬─┬─┬─┬─┬─┬─┐
                                │ │ │ │ │ │ │ │ │
                                ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼ ▼
┌─────────────┬─────────────┬─────────────┬─────────────┬─────────────┐
│   Subagent  │   Subagent  │   Subagent  │   Subagent  │   Subagent  │
│   1: Auth   │   2: Stack  │   3: Editor │   4: Git   │   5: UI     │
│             │  Operations │             │ Integration │  Features   │
└─────────────┴─────────────┴─────────────┴─────────────┴─────────────┘
        │                │                │               │           │
        ▼                ▼                ▼               ▼           ▼
  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐  ┌──────────┐
  │  Login   │    │  Start   │    │  Compose │    │  Status  │  │  Dark   │
  │  Setup   │    │  Stop    │    │  Editor  │    │  Pull    │  │  Mode   │
  │  Logout  │    │  Restart │    │  Env Var │    │  History │  │  Theme  │
  └──────────┘    └──────────┘    └──────────┘    └──────────┘  └──────────┘
        │                │                │               │           │
        └────────────────┴────────────────┴───────────────┴───────────┘
                                │
                                ▼
                    ┌───────────────────┐
                    │  Test Environment  │
                    │  - Docker stacks   │
                    │  - Test data       │
                    │  - Mock git repos  │
                    └───────────────────┘
```

### Directory Structure

```
testing/
├── README.md                          # This file
├── plan.md                            # Detailed test plan
├── test-orchestrator.sh              # Master script to coordinate tests
├── environments/
│   ├── setup.sh                      # Creates test environment
│   ├── stacks/                       # Docker Compose stacks for testing
│   │   ├── nginx-test/
│   │   ├── redis-test/
│   │   └── postgres-test/
│   └── cleanup.sh                    # Tears down test environment
├── tests/
│   ├── smoke/                        # Smoke tests (critical path)
│   │   ├── auth.test.sh              # Authentication tests
│   │   ├── dashboard.test.sh         # Dashboard discovery tests
│   │   └── stack-ops.test.sh         # Stack operation tests
│   ├── core/                         # Core feature tests
│   │   ├── compose-editor.test.sh    # Compose file editor tests
│   │   ├── env-editor.test.sh        # Environment variable tests
│   │   └── git-integration.test.sh   # Git integration tests
│   ├── ui/                           # UI/UX tests
│   │   ├── dark-mode.test.sh         # Dark mode tests
│   │   ├── responsive.test.sh        # Responsive design tests
│   │   └── accessibility.test.sh     # Accessibility tests
│   └── regression/                   # Edge case and regression tests
│       ├── terminal.test.sh          # Terminal feature tests
│       ├── logs.test.sh              # Log viewer tests
│       └── metrics.test.sh           # Metrics dashboard tests
├── reports/
│   ├── results.json                  # Test results summary
│   ├── screenshots/                  # Failure screenshots
│   └── coverage.json                 # Test coverage metrics
├── lib/
│   ├── browser-utils.sh              # Common browser automation functions
│   ├── assert.sh                     # Assertion utilities
│   └── setup-stack.sh                # Test stack setup utilities
└── config/
    ├── test-config.yml               # Global test configuration
    └── agents.yml                    # Subagent configuration
```

---

## Test Strategy

### Test Tiers

Based on Agent OS e2e standards, tests are organized into tiers:

| Tier | Purpose | Tests | Execution Time | Failure Impact |
|------|---------|-------|----------------|----------------|
| **Smoke** | Critical path validation | 10-15 tests | ~3 minutes | **Stop all** - Block deployment |
| **Core** | Main feature validation | 20-30 tests | ~15 minutes | Fix before release |
| **Regression** | Edge cases & bugs | 30-40 tests | ~45 minutes | Schedule for fix |

### Parallel Execution Model

Tests are grouped into **5 functional domains** that can run in parallel:

```
┌─────────────────────────────────────────────────────────────┐
│                PARALLEL EXECUTION MATRIX                     │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  Domain 1: Authentication (3 tests)                         │
│    └── Tests: login, logout, protected routes               │
│                                                             │
│  Domain 2: Stack Operations (4 tests)                       │
│    └── Tests: start, stop, restart, pull                    │
│                                                             │
│  Domain 3: Editors (5 tests)                                │
│    └── Tests: compose editor, env editor, linting, save    │
│                                                             │
│  Domain 4: Git Integration (3 tests)                         │
│    └── Tests: status, pull, history, diff                  │
│                                                             │
│  Domain 5: UI Features (5 tests)                            │
│    └── Tests: dark mode, responsive, accessibility, logs     │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Execution Timeline**:
```
Time:  0min    3min    6min    9min   12min   15min
       |-------|-------|-------|-------|-------|
Domain1: [====]       
Domain2: [============]  
Domain3: [==============] 
Domain4: [=====]       
Domain5: [=============] 
                              ^ Full suite complete
```

### Browser Agent Usage

All tests use the `browser-automating` skill with the `agent-browser` CLI:

```bash
# Open page with extended timeout for React apps
agent-browser open http://localhost:3001 --timeout 30000

# Wait for hydration
agent-browser wait 2000

# Get interactive elements with refs
agent-browser snapshot -i

# Interact using refs
agent-browser click @e1
agent-browser fill @e2 "username"

# Screenshot for debugging
agent-browser screenshot testing/reports/screenshots/auth-login-fail.png

# Close session
agent-browser close
```

---

## Subagent Coordination

### Master Orchestrator

The `test-orchestrator.sh` script coordinates all subagents:

```bash
#!/bin/bash
set -euo pipefail

# Configuration
TESTING_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPORTS_DIR="$TESTING_DIR/reports"
CONFIG_FILE="$TESTING_DIR/config/agents.yml"

# Load test configuration
source "$TESTING_DIR/lib/browser-utils.sh"

# Phase 1: Environment Setup
log_info "Setting up test environment..."
"$TESTING_DIR/environments/setup.sh"

# Phase 2: Start Application (if not running)
if ! curl -sf http://localhost:3001 > /dev/null; then
  log_info "Starting Capstan..."
  docker-compose up -d
  wait_for_app "http://localhost:3001" 60
fi

# Phase 3: Execute Tests in Parallel
log_info "Starting parallel test execution..."

# Spawn subagents for each domain
execute_parallel_tests() {
  local domains=("auth" "stack-ops" "editors" "git" "ui")
  local pids=()
  
  for domain in "${domains[@]}"; do
    log_info "Spawning subagent for domain: $domain"
    (
      set -euo pipefail
      log_domain_start "$domain"
      run_domain_tests "$domain"
      log_domain_complete "$domain"
    ) &
    pids+=($!)
  done
  
  # Wait for all subagents to complete
  for pid in "${pids[@]}"; do
    wait "$pid" || log_error "Subagent failed with pid: $pid"
  done
}

# Phase 4: Aggregated Results
log_info "Aggregating test results..."
generate_report

# Phase 5: Cleanup
log_info "Cleaning up test environment..."
"$TESTING_DIR/environments/cleanup.sh" --keep-results

# Exit with appropriate code
if [ "$FAILED_TESTS" -gt 0 ]; then
  log_error "Test suite failed: $FAILED_TESTS/$TOTAL_TESTS tests failed"
  exit 1
else
  log_success "All $TOTAL_TESTS tests passed!"
  exit 0
fi
```

### Subagent Implementation

Each subagent domain is a self-contained test suite:

**Example: `tests/smoke/auth.test.sh`**
```bash
#!/bin/bash
set -euo pipefail

TEST_NAME="Authentication Tests"
DOMAIN="auth"
RESULTS_DIR="$TESTING_DIR/reports/results/auth"

source "$TESTING_DIR/lib/browser-utils.sh"
source "$TESTING_DIR/lib/assert.sh"

test_login_success() {
  local test_id="${DOMAIN}-001"
  log_test_start "$test_id" "Login with valid credentials"
  
  # Navigate to login page
  agent-browser open "http://localhost:3001/login" --timeout 30000
  agent-browser wait 2000
  
  # Get form elements
  local snapshot
  snapshot=$(agent-browser snapshot -i)
  
  # Extract refs for username and password fields
  local email_ref
  local password_ref
  email_ref=$(echo "$snapshot" | grep -oP '(?<=@e)\d+(?=.+email)' | head -1)
  password_ref=$(echo "$snapshot" | grep -oP '(?<=@e)\d+(?=.+password)' | head -1)
  
  # Fill form
  agent-browser fill "@e${email_ref}" "testadmin@example.com"
  agent-browser fill "@e${password_ref}" "TestPass123!"
  
  # Submit
  agent-browser click $(get_button_ref "Login")
  agent-browser wait 2000
  
  # Verify redirect to dashboard
  local url
  url=$(agent-browser get url)
  assert_contains "$url" "/dashboard" "Redirect to dashboard after login"
  
  log_test_pass "$test_id"
}

test_login_invalid_credentials() {
  local test_id="${DOMAIN}-002"
  log_test_start "$test_id" "Login with invalid credentials shows error"
  
  agent-browser open "http://localhost:3001/login" --timeout 30000
  agent-browser wait 2000
  
  local snapshot
  snapshot=$(agent-browser snapshot -i)
  
  local email_ref
  local password_ref
  email_ref=$(echo "$snapshot" | grep -oP '(?<=@e)\d+(?=.+email)' | head -1)
  password_ref=$(echo "$snapshot" | grep -oP '(?<=@e)\d+(?=.+password)' | head -1)
  
  agent-browser fill "@e${email_ref}" "invalid@example.com"
  agent-browser fill "@e${password_ref}" "wrongpassword"
  
  agent-browser click $(get_button_ref "Login")
  agent-browser wait 2000
  
  # Verify error message
  snapshot=$(agent-browser snapshot)
  assert_contains "$snapshot" "Invalid" "Error message displayed"
  
  log_test_pass "$test_id"
}

# Run all tests in domain
main() {
  log_domain_start "$DOMAIN"
  
  test_login_success
  test_login_invalid_credentials
  # ... more tests
  
  log_domain_complete "$DOMAIN"
}

main "$@"
```

---

## Test Coverage Matrix

### Smoke Tests (Critical Path)

| Test ID | Scenario | Domain | Priority |
|---------|----------|--------|----------|
| SMOKE-001 | Load application homepage | UI | P0 |
| SMOKE-002 | Login with valid credentials | Auth | P0 |
| SMOKE-003 | Logout and redirect to login | Auth | P0 |
| SMOKE-004 | Access protected route redirects to login | Auth | P0 |
| SMOKE-005 | Dashboard displays stacks | UI | P0 |
| SMOKE-006 | Start a stopped stack | Stack-Ops | P0 |
| SMOKE-007 | Stop a running stack | Stack-Ops | P0 |
| SMOKE-008 | Restart a stack | Stack-Ops | P0 |
| SMOKE-009 | Open stack details | UI | P0 |
| SMOKE-010 | Navigate between pages | UI | P0 |

### Core Tests (Main Features)

| Test ID | Scenario | Domain | Priority |
|---------|----------|--------|----------|
| CORE-001 | Compose editor loads YAML | Editors | P1 |
| CORE-002 | Compose editor saves changes | Editors | P1 |
| CORE-003 | Compose editor highlights syntax errors | Editors | P1 |
| CORE-004 | Environment editor displays variables | Editors | P1 |
| CORE-005 | Environment editor adds variable | Editors | P1 |
| CORE-006 | Environment editor removes variable | Editors | P1 |
| CORE-007 | Git status displays correctly | Git | P1 |
| CORE-008 | Git pull updates files | Git | P1 |
| CORE-009 | Git history shows commits | Git | P1 |
| CORE-010 | Git diff shows changes | Git | P1 |
| CORE-011 | Terminal connects to container | UI | P1 |
| CORE-012 | Terminal executes commands | UI | P1 |
| CORE-013 | Logs stream in real-time | UI | P1 |
| CORE-014 | Metrics display CPU and memory | UI | P1 |
| CORE-015 | Settings page opens | UI | P1 |

### Regression Tests (Edge Cases)

| Test ID | Scenario | Domain | Priority |
|---------|----------|--------|----------|
| REG-001 | Empty compose file validation | Editors | P2 |
| REG-002 | Invalid YAML syntax handling | Editors | P2 |
| REG-003 | Large compose file performance | Editors | P2 |
| REG-004 | Special characters in env values | Editors | P2 |
| REG-005 | Empty env file handling | Editors | P2 |
| REG-006 | Git merge conflict handling | Git | P2 |
| REG-007 | Git with no commits | Git | P2 |
| REG-008 | Stopped container terminal access | UI | P2 |
| REG-009 | Logs with high throughput | UI | P2 |
| REG-010 | Mobile viewport navigation | UI | P2 |

---

## Implementation Steps

### Phase 1: Infrastructure Setup (Day 1)

1. **Create directory structure**
   ```bash
   mkdir -p testing/{environments,tests/{smoke,core,ui,regression},reports,lib,config}
   ```

2. **Create test environment setup**
   - `testing/environments/setup.sh` - Creates test Docker stacks
   - `testing/environments/cleanup.sh` - Removes test stacks
   - `testing/environments/stacks/*.yml` - Test compose files

3. **Create utility libraries**
   - `testing/lib/browser-utils.sh` - Browser automation helpers
   - `testing/lib/assert.sh` - Test assertions
   - `testing/lib/setup-stack.sh` - Stack creation helpers

4. **Create configuration files**
   - `testing/config/test-config.yml` - Global test settings
   - `testing/config/agents.yml` - Subagent configuration

### Phase 2: Smoke Tests (Days 2-3)

Implement critical path tests:
- Authentication (login, logout, protected routes)
- Dashboard (stack discovery)
- Stack operations (start, stop, restart)

### Phase 3: Core Tests (Days 4-5)

Implement main feature tests:
- Compose editor
- Environment editor
- Git integration
- Terminal, logs, metrics

### Phase 4: UI/UX Tests (Day 6)

Implement UI tests:
- Dark mode
- Responsive design
- Accessibility

### Phase 5: Regression Tests (Days 7-8)

Implement edge case tests:
- Error handling
- Performance scenarios
- Edge cases

### Phase 6: Integration & Reporting (Day 9)

- Create master orchestrator
- Implement result aggregation
- Generate HTML reports
- Integrate with CI/CD

---

## Test Execution

### Run Full Test Suite

```bash
# Run all tests
./testing/test-orchestrator.sh

# Run specific tier
./testing/test-orchestrator.sh --tier smoke
./testing/test-orchestrator.sh --tier core
./testing/test-orchestrator.sh --tier regression

# Run specific domain
./testing/test-orchestrator.sh --domain auth
./testing/test-orchestrator.sh --domain stack-ops
```

### Run Individual Test

```bash
# Run single test suite
./testing/tests/smoke/auth.test.sh

# Run with verbose output
./testing/tests/smoke/auth.test.sh --verbose

# Run with screenshots on failure
./testing/tests/smoke/auth.test.sh --screenshots
```

### View Results

```bash
# View test results
cat testing/reports/results.json

# View detailed report
open testing/reports/html/report.html

# View failure screenshots
ls testing/reports/screenshots/
```

---

## Result Reporting

### JSON Output Structure

```json
{
  "test_run": {
    "timestamp": "2025-02-15T10:00:00Z",
    "duration_seconds": 847,
    "total_tests": 35,
    "passed": 33,
    "failed": 2,
    "skipped": 0
  },
  "domains": [
    {
      "name": "auth",
      "status": "passed",
      "tests": [
        {
          "id": "auth-001",
          "name": "Login with valid credentials",
          "status": "passed",
          "duration_seconds": 4.2
        }
      ]
    }
  ],
  "failures": [
    {
      "test_id": "core-007",
      "test_name": "Git pull updates files",
      "error": "Expected to find 'Pull successful' in page",
      "screenshot": "screenshots/core-007-failure.png",
      "stack_trace": "..."
    }
  ]
}
```

### HTML Report

Generate a visual HTML report with:
- Test execution timeline
- Pass/fail breakdown by domain
- Failure details with screenshots
- Coverage metrics
- Historical trends

---

## CI/CD Integration

### GitHub Actions Example

```yaml
name: E2E Tests

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main]

jobs:
  e2e-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Node.js
        uses: actions/setup-node@v3
        with:
          node-version: '22'
      
      - name: Install dependencies
        run: |
          cd frontend
          npm ci
      
      - name: Setup test environment
        run: ./testing/environments/setup.sh
      
      - name: Run smoke tests
        run: ./testing/test-orchestrator.sh --tier smoke
      
      - name: Upload test results
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: test-results
          path: testing/reports/
      
      - name: Upload failure screenshots
        if: failure()
        uses: actions/upload-artifact@v3
        with:
          name: failure-screenshots
          path: testing/reports/screenshots/
```

---

## Maintenance & Troubleshooting

### Common Issues

1. **Test flakiness due to timing**
   - Solution: Increase wait times, use explicit waits instead of timeouts

2. **Docker socket permissions**
   - Solution: Ensure test user has docker group access

3. **Stale test data**
   - Solution: Run `testing/environments/cleanup.sh` before each run

4. **Browser version incompatibility**
   - Solution: Update `agent-browser` to latest version

### Debugging

```bash
# Run tests with verbose output
./testing/test-orchestrator.sh --verbose

# Run tests with browser visible (headed mode)
export AGENT_BROWSER_MODE=headed
./testing/tests/smoke/auth.test.sh

# Run single test with debugging
./testing/tests/smoke/auth.test.sh --debug --keep-session

# View browser session logs
cat testing/reports/browser-sessions/
```

---

## Success Criteria

### Phase 1 (Week 1)
- [ ] Complete infrastructure setup
- [ ] All smoke tests passing (10 tests)
- [ ] Test execution time < 5 minutes

### Phase 2 (Week 2)
- [ ] All core tests passing (15 tests)
- [ ] Full suite execution time < 20 minutes
- [ ] HTML report generation working

### Phase 3 (Week 3)
- [ ] All regression tests passing (10 tests)
- [ ] CI/CD integration complete
- [ ] 0% flaky tests in 10 consecutive runs

---

## Next Steps

1. Review and approve this plan
2. Set up infrastructure (Phase 1)
3. Implement smoke tests (Phase 2)
4. Expand to core and regression tests (Phases 3-5)
5. Integrate with CI/CD (Phase 6)

---

## Appendix

### Test Data Requirements

- Test stacks: 3 simple Docker Compose stacks (nginx, redis, postgres)
- Test user credentials: `testadmin@example.com` / `TestPass123!`
- Mock git repository: Small test repo with sample commits

### Browser Agent Commands Reference

See the browser-automating skill documentation for complete command reference:
- Navigation: `open`, `back`, `forward`, `close`
- Page analysis: `snapshot`, `get url`, `get title`
- Interactions: `click`, `fill`, `type`, `press`
- Information: `get text`, `screenshot`
- Waiting: `wait <ms>`, `wait @element`

### Related Documentation

- [Agent OS E2E Standards](.agent-os/standards/e2e-ui-testing-standards.md)
- [E2E Testing Protocol](.agent-os/instructions/core/e2e.md)
- [Smoke Test Checklist](Supporting-Docs/testing/smoke-test-checklist.md)
- [browser-automating Skill](~/.claude/skills/agent-browser/SKILL.md)
