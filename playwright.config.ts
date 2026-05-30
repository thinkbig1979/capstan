import { defineConfig, devices } from 'playwright/test'

/**
 * Playwright configuration for Capstan E2E tests.
 *
 * Specs live under testing/tests/playwright/ to sit alongside the existing
 * bash-harness tests in testing/tests/.
 *
 * Run:
 *   npx playwright test                          # all specs
 *   npx playwright test backup-flow              # just backup flow
 *   npx playwright test --reporter=line          # compact output
 *
 * Environment variables:
 *   CAPSTAN_BASE_URL       Frontend URL  (default: http://localhost:3001)
 *   CAPSTAN_API_URL        Backend URL   (default: http://localhost:5001)
 *   CAPSTAN_TEST_USER      Email         (default: testadmin@example.com)
 *   CAPSTAN_TEST_PASSWORD  Password      (default: TestPass123!)
 *   AUTH_DISABLED          Skip login    (default: false)
 *   CAPSTAN_TEST_STACK     Stack name    (default: test-app)
 *   CAPSTAN_BACKUP_REPO    Restic repo   (default: /tmp/capstan-e2e-restic-repo-playwright)
 *   CAPSTAN_BACKUP_PASSPHRASE            (default: capstan-e2e-playwright-passphrase)
 */
export default defineConfig({
  testDir: './testing/tests/playwright',
  testMatch: '**/*.spec.ts',

  // Each test gets a generous timeout because backup/restore involve real
  // restic I/O and WS streaming.
  timeout: 120_000,
  expect: { timeout: 15_000 },

  // No watch mode in CI.
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,

  // Run backup-flow tests sequentially — they share state (stack ID, snapshot
  // ID) and must execute in order within the describe block.
  fullyParallel: false,
  workers: 1,

  reporter: [
    ['list'],
    ['html', { outputFolder: 'testing/reports/playwright-html', open: 'never' }],
    ['json', { outputFile: 'testing/reports/playwright-results.json' }],
  ],

  use: {
    baseURL: process.env.CAPSTAN_BASE_URL ?? 'http://localhost:3001',
    headless: true,
    screenshot: 'only-on-failure',
    screenshotsPath: './testing/reports/screenshots',
    video: 'off',
    // Give real backup operations room to breathe.
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
  },

  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
