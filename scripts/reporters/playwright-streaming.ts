/**
 * Playwright Streaming Reporter v1.0.0
 *
 * Real-time test progress reporting for Agent OS v3.3.0+
 *
 * Features:
 * - Emits structured JSON events for each test start/end
 * - Reports step-level progress for fine-grained monitoring
 * - Tracks per-test timing for hung test detection
 * - Compatible with Agent OS test monitor utility
 *
 * Usage in playwright.config.ts:
 *   reporter: [['./scripts/reporters/playwright-streaming.ts'], ['html']],
 *
 * Or via CLI:
 *   playwright test --reporter=./scripts/reporters/playwright-streaming.ts
 *
 * Output Format (JSON lines to stdout):
 *   {"type":"test_start","name":"...","file":"...","timestamp":...}
 *   {"type":"step_start","testName":"...","stepName":"..."}
 *   {"type":"step_end","testName":"...","stepName":"...","status":"passed|failed"}
 *   {"type":"test_end","name":"...","status":"passed|failed|skipped|timedOut","duration":123}
 *
 * @see https://playwright.dev/docs/api/class-reporter
 */

import type {
  FullConfig,
  FullResult,
  Reporter,
  Suite,
  TestCase,
  TestResult,
  TestStep
} from '@playwright/test/reporter';

const EVENT_PREFIX = '[AGENT-OS-TEST]';

interface TestTiming {
  startTime: number;
  file: string;
  line: number;
}

interface SuiteStats {
  total: number;
  passed: number;
  failed: number;
  skipped: number;
  timedOut: number;
  startTime: number;
}

class PlaywrightStreamingReporter implements Reporter {
  private testTimings: Map<string, TestTiming> = new Map();
  private suiteStats: Map<string, SuiteStats> = new Map();
  private config: FullConfig | null = null;

  /**
   * Called once before running tests
   */
  onBegin(config: FullConfig, suite: Suite): void {
    this.config = config;
    const totalTests = suite.allTests().length;

    this.emit({
      type: 'run_start',
      totalTests,
      workers: config.workers,
      projects: config.projects.map(p => p.name),
      timestamp: Date.now()
    });

    console.log(`${EVENT_PREFIX} Starting test run with ${totalTests} tests across ${config.workers} worker(s)`);

    // Initialize per-file stats
    for (const test of suite.allTests()) {
      const filepath = test.location.file;
      if (!this.suiteStats.has(filepath)) {
        this.suiteStats.set(filepath, {
          total: 0,
          passed: 0,
          failed: 0,
          skipped: 0,
          timedOut: 0,
          startTime: Date.now()
        });
      }
      const stats = this.suiteStats.get(filepath)!;
      stats.total++;
    }
  }

  /**
   * Called when a test begins
   */
  onTestBegin(test: TestCase, result: TestResult): void {
    const testId = this.getTestId(test);

    this.testTimings.set(testId, {
      startTime: Date.now(),
      file: test.location.file,
      line: test.location.line
    });

    this.emit({
      type: 'test_start',
      name: test.title,
      fullName: this.getFullTestName(test),
      file: test.location.file,
      line: test.location.line,
      project: test.parent.project()?.name,
      retry: result.retry,
      timestamp: Date.now()
    });

    console.log(`  ${EVENT_PREFIX} START: ${test.title}`);
  }

  /**
   * Called when a test step begins
   */
  onStepBegin(test: TestCase, result: TestResult, step: TestStep): void {
    // Only report test.step() calls, not internal Playwright steps
    if (step.category !== 'test.step') return;

    this.emit({
      type: 'step_start',
      testName: test.title,
      stepName: step.title,
      file: test.location.file,
      timestamp: Date.now()
    });

    console.log(`    ${EVENT_PREFIX} STEP: ${step.title}`);
  }

  /**
   * Called when a test step ends
   */
  onStepEnd(test: TestCase, result: TestResult, step: TestStep): void {
    if (step.category !== 'test.step') return;

    const duration = step.duration;
    const status = step.error ? 'failed' : 'passed';

    this.emit({
      type: 'step_end',
      testName: test.title,
      stepName: step.title,
      status,
      duration,
      error: step.error ? {
        message: step.error.message,
        stack: step.error.stack?.split('\n').slice(0, 5).join('\n')
      } : null,
      timestamp: Date.now()
    });

    const icon = status === 'passed' ? '✓' : '✗';
    const color = status === 'passed' ? '\x1b[32m' : '\x1b[31m';
    console.log(`    ${EVENT_PREFIX} ${color}${icon}\x1b[0m ${step.title} (${duration}ms)`);
  }

  /**
   * Called when a test ends
   */
  onTestEnd(test: TestCase, result: TestResult): void {
    const testId = this.getTestId(test);
    const timing = this.testTimings.get(testId);
    const duration = timing ? Date.now() - timing.startTime : result.duration;

    this.testTimings.delete(testId);

    // Update suite stats
    const filepath = test.location.file;
    if (this.suiteStats.has(filepath)) {
      const stats = this.suiteStats.get(filepath)!;
      switch (result.status) {
        case 'passed':
          stats.passed++;
          break;
        case 'failed':
          stats.failed++;
          break;
        case 'skipped':
          stats.skipped++;
          break;
        case 'timedOut':
          stats.timedOut++;
          break;
      }
    }

    const error = result.errors?.[0];

    this.emit({
      type: 'test_end',
      name: test.title,
      fullName: this.getFullTestName(test),
      status: result.status,
      duration,
      file: test.location.file,
      line: test.location.line,
      project: test.parent.project()?.name,
      retry: result.retry,
      attachments: result.attachments.map(a => ({
        name: a.name,
        contentType: a.contentType,
        path: a.path
      })),
      error: error ? {
        message: error.message,
        stack: error.stack?.split('\n').slice(0, 5).join('\n'),
        snippet: error.snippet
      } : null,
      timestamp: Date.now()
    });

    // Human-readable output
    const icons: Record<string, string> = {
      passed: '✓',
      failed: '✗',
      skipped: '○',
      timedOut: '⏱',
      interrupted: '!'
    };
    const colors: Record<string, string> = {
      passed: '\x1b[32m',
      failed: '\x1b[31m',
      skipped: '\x1b[33m',
      timedOut: '\x1b[31m',
      interrupted: '\x1b[31m'
    };

    const icon = icons[result.status] || '?';
    const color = colors[result.status] || '';
    console.log(`  ${EVENT_PREFIX} ${color}${icon}\x1b[0m ${test.title} (${duration}ms)`);

    if (error) {
      console.log(`  ${EVENT_PREFIX}   Error: ${error.message?.split('\n')[0]}`);
    }

    if (result.status === 'timedOut') {
      console.log(`  ${EVENT_PREFIX}   \x1b[31mTIMEOUT: Test exceeded timeout\x1b[0m`);
    }
  }

  /**
   * Called when the entire test run ends
   */
  onEnd(result: FullResult): void | Promise<void> {
    // Emit per-file summaries
    for (const [filepath, stats] of this.suiteStats.entries()) {
      const duration = Date.now() - stats.startTime;
      const filename = filepath.split('/').pop() || filepath;

      this.emit({
        type: 'file_end',
        file: filepath,
        name: filename,
        duration,
        total: stats.total,
        passed: stats.passed,
        failed: stats.failed,
        skipped: stats.skipped,
        timedOut: stats.timedOut,
        timestamp: Date.now()
      });

      const icon = (stats.failed > 0 || stats.timedOut > 0) ? '✗' : '✓';
      const color = (stats.failed > 0 || stats.timedOut > 0) ? '\x1b[31m' : '\x1b[32m';
      console.log(`${EVENT_PREFIX} ${color}${icon}\x1b[0m File: ${filename} (${stats.passed}/${stats.total} passed, ${duration}ms)`);
    }

    // Overall summary
    this.emit({
      type: 'run_end',
      status: result.status,
      duration: result.duration,
      timestamp: Date.now()
    });

    const statusColor = result.status === 'passed' ? '\x1b[32m' : '\x1b[31m';
    console.log(`\n${EVENT_PREFIX} ${statusColor}Run ${result.status}\x1b[0m in ${result.duration}ms`);

    // Check for stuck tests (shouldn't happen, but safety net)
    if (this.testTimings.size > 0) {
      console.error(`${EVENT_PREFIX} \x1b[33mWarning: ${this.testTimings.size} test(s) did not properly complete\x1b[0m`);
      for (const [testId, timing] of this.testTimings.entries()) {
        const runningFor = Date.now() - timing.startTime;
        console.error(`${EVENT_PREFIX}   - ${testId} (running for ${runningFor}ms)`);
      }
    }
  }

  /**
   * Called on stdout from test
   */
  onStdOut(chunk: string | Buffer, test?: TestCase, result?: TestResult): void {
    // Pass through stdout but don't emit JSON event for it (too noisy)
    const text = chunk.toString();
    if (!text.startsWith('{') && !text.startsWith(EVENT_PREFIX)) {
      process.stdout.write(chunk);
    }
  }

  /**
   * Called on stderr from test
   */
  onStdErr(chunk: string | Buffer, test?: TestCase, result?: TestResult): void {
    process.stderr.write(chunk);
  }

  /**
   * Called when an error occurs outside of test execution
   */
  onError(error: { message: string; stack?: string }): void {
    this.emit({
      type: 'error',
      message: error.message,
      stack: error.stack,
      timestamp: Date.now()
    });

    console.error(`${EVENT_PREFIX} \x1b[31mError:\x1b[0m ${error.message}`);
  }

  /**
   * Get unique identifier for a test
   */
  private getTestId(test: TestCase): string {
    return `${test.location.file}:${test.location.line}::${test.title}`;
  }

  /**
   * Get full test name including parent suites
   */
  private getFullTestName(test: TestCase): string {
    const parts: string[] = [];
    let current: Suite | undefined = test.parent;

    while (current) {
      if (current.title) {
        parts.unshift(current.title);
      }
      current = current.parent;
    }

    parts.push(test.title);
    return parts.join(' > ');
  }

  /**
   * Emit structured JSON event
   */
  private emit(event: Record<string, unknown>): void {
    console.log(JSON.stringify(event));
  }
}

export default PlaywrightStreamingReporter;
