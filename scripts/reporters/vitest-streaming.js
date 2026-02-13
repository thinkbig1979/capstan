/**
 * Vitest Streaming Reporter v3.0.0
 *
 * Real-time test progress reporting for Agent OS v3.3.0+
 * Compatible with Vitest 3.x reporter API
 *
 * @see https://vitest.dev/api/advanced/reporters
 */

const EVENT_PREFIX = "[AGENT-OS-TEST]";

class VitestStreamingReporter {
  constructor() {
    this.testStarts = new Map();
    this.totalStats = { passed: 0, failed: 0, skipped: 0, total: 0 };
  }

  onInit(ctx) {
    this.emit({ type: "run_start", timestamp: Date.now() });
  }

  onTestRunStart(testModules) {
    const count = testModules?.length || 0;
    this.emit({ type: "files_collected", count, timestamp: Date.now() });
    console.log(`${EVENT_PREFIX} ◉ Collected ${count} test file(s)`);
  }

  onTestModuleStart(module) {
    if (!module) return;
    this.emit({
      type: "file_start",
      file: module.moduleId,
      timestamp: Date.now(),
    });
  }

  onTestCaseReady(testCase) {
    if (!testCase) return;
    const testId = testCase.id || testCase.name;
    this.testStarts.set(testId, Date.now());
    this.totalStats.total++;

    this.emit({
      type: "test_start",
      name: testCase.name,
      fullName: testCase.fullName,
      file: testCase.module?.moduleId,
      timestamp: Date.now(),
    });
    console.log(`  ${EVENT_PREFIX} ▶ ${testCase.name}`);
  }

  onTestCaseResult(testCase) {
    if (!testCase) return;
    const testId = testCase.id || testCase.name;
    const startTime = this.testStarts.get(testId) || Date.now();
    const duration = Date.now() - startTime;
    this.testStarts.delete(testId);

    let status = "unknown",
      error = null;
    try {
      const result =
        typeof testCase.result === "function"
          ? testCase.result()
          : testCase.result;
      status = this.normalizeStatus(result?.state);
      error = result?.errors?.[0];
    } catch {
      /* ignore */
    }

    if (status === "passed") this.totalStats.passed++;
    else if (status === "failed") this.totalStats.failed++;
    else if (status === "skipped") this.totalStats.skipped++;

    this.emit({
      type: "test_end",
      name: testCase.name,
      status,
      duration,
      error: error ? { message: error.message } : null,
      timestamp: Date.now(),
    });

    const icon = status === "passed" ? "✓" : status === "failed" ? "✗" : "○";
    const color =
      status === "passed"
        ? "\x1b[32m"
        : status === "failed"
          ? "\x1b[31m"
          : "\x1b[33m";
    console.log(
      `  ${EVENT_PREFIX} ${color}${icon}\x1b[0m ${testCase.name} (${duration}ms)`,
    );
    if (error)
      console.log(`  ${EVENT_PREFIX}   \x1b[31mError: ${error.message}\x1b[0m`);
  }

  onTestModuleEnd(module) {
    if (!module) return;
    this.emit({
      type: "file_end",
      file: module.moduleId,
      timestamp: Date.now(),
    });
  }

  onTestRunEnd(testModules, unhandledErrors) {
    if (unhandledErrors?.length) {
      for (const err of unhandledErrors) {
        console.error(
          `${EVENT_PREFIX} \x1b[31mError: ${err.message || err}\x1b[0m`,
        );
      }
    }

    this.emit({
      type: "run_end",
      passed: this.totalStats.passed,
      failed: this.totalStats.failed,
      skipped: this.totalStats.skipped,
      total: this.totalStats.total,
      timestamp: Date.now(),
    });

    const icon = this.totalStats.failed > 0 ? "✗" : "✓";
    const color = this.totalStats.failed > 0 ? "\x1b[31m" : "\x1b[32m";
    console.log(
      `\n${EVENT_PREFIX} ${color}${icon}\x1b[0m Summary: ${this.totalStats.passed}/${this.totalStats.total} passed`,
    );
  }

  normalizeStatus(status) {
    return (
      { pass: "passed", fail: "failed", skip: "skipped", todo: "skipped" }[
        status
      ] || status
    );
  }

  emit(event) {
    console.log(JSON.stringify(event));
  }
}

export default VitestStreamingReporter;
