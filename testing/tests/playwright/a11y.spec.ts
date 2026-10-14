/**
 * Accessibility scan (axe-core, WCAG 2.0 A + AA) over the dashboard, a stack page
 * and every settings section.
 *
 * Fails on any `critical` or `serious` violation that is not on the ALLOWLIST
 * below. `moderate` and `minor` findings are attached to the test as annotations
 * and do not fail it.
 *
 * Prerequisites (same backend as terminal-flow.spec.ts):
 *   - App on CAPSTAN_BASE_URL (default http://localhost:3001), backend on
 *     CAPSTAN_API_URL (default http://localhost:5001).
 *   - AUTH_DISABLED=true. The login and setup pages are not scanned: they need
 *     the auth-session job's virgin backend.
 *   - A stack named CAPSTAN_TEST_STACK (default test-app).
 *
 * Run:
 *   AUTH_DISABLED=true npx playwright test a11y --retries=0
 *
 * MATRIX (56 scans). Every URL below in light at 1280x720 and in dark at
 * 1280x720, plus four representative URLs in light at 375x812. Contrast
 * failures are theme specific (the primary label failed in both themes with
 * different ratios), so dark is scanned in full; layout-driven failures are
 * what the narrow viewport adds, and four URLs cover the layouts.
 *
 * NO FIXED SLEEPS. A scan starts after `waitForInvalidationSettle`, because the
 * app keeps rendering after load (WS-driven invalidations, debounced 750ms; see
 * helpers/network-settle.ts).
 *
 * THE ALLOWLIST IS A RATCHET, NOT A WAIVER. Each entry is a known violation in
 * third-party code that the app cannot fix in its own stylesheet. A violation
 * that matches no entry fails the run, and an entry that no longer fires also
 * fails (the `ALLOWLIST` tests at the bottom rescan the page the entry names),
 * so fixing the cause forces the entry out in the same change.
 *
 * THE SCAN HAS A CONTROL. The `CONTROL` tests feed the same scan helper a page
 * with a known violation (an <img> with no alt, `image-alt`, critical) and the
 * same page fixed. A scan that stopped reporting would fail the first.
 */

import { test, expect, Page } from 'playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { waitForInvalidationSettle } from './helpers/network-settle'

// axe-core is a transitive dependency of @axe-core/playwright, so its types are
// derived from the builder rather than imported from a package the root does not list.
type Result = Awaited<ReturnType<AxeBuilder['analyze']>>['violations'][number]
type NodeResult = Result['nodes'][number]

const API_URL = process.env.CAPSTAN_API_URL ?? 'http://localhost:5001'
const AUTH_DISABLED = (process.env.AUTH_DISABLED ?? 'false') === 'true'
const TEST_STACK_NAME = process.env.CAPSTAN_TEST_STACK ?? 'test-app'

// ─── Matrix ──────────────────────────────────────────────────────────────────

const DASHBOARD_TABS = [
  'stacks',
  'overview',
  'containers',
  'directories',
  'updates',
  'backups',
  'images',
  'volumes',
  'networks',
  'build-cache',
]
const STACK_TABS = ['overview', 'editor', 'logs', 'terminal', 'metrics', 'activity']
const SETTINGS_SECTIONS = [
  'account-security',
  'appearance',
  'directories',
  'global-env',
  'git',
  'update-schedule',
  'backup',
  'docker-cleanup',
  'audit-log',
  'about',
]

interface Target {
  /** Stable name used in test titles and allowlist entries. */
  name: string
  /** Builds the URL once the stack id is known. */
  url: (stackId: string) => string
}

const TARGETS: Target[] = [
  // The dashboard is `/`, and the default 'stacks' tab clears ?tab= entirely
  // (DashboardPage.tsx setActiveTab), so it is scanned at `/`.
  ...DASHBOARD_TABS.map((t) => ({
    name: `dash:${t}`,
    url: () => (t === 'stacks' ? '/' : `/?tab=${t}`),
  })),
  ...STACK_TABS.map((t) => ({
    name: `stack:${t}`,
    url: (id: string) => `/stacks/${encodeURIComponent(id)}/${t}`,
  })),
  ...SETTINGS_SECTIONS.map((s) => ({ name: `settings:${s}`, url: () => `/settings/${s}` })),
]

const NARROW_TARGETS = ['dash:stacks', 'dash:containers', 'stack:overview', 'settings:appearance']

type Scheme = 'light' | 'dark'

const CONFIGS: Array<{
  label: string
  scheme: Scheme
  viewport: { width: number; height: number }
  targets: Target[]
}> = [
  { label: 'light 1280', scheme: 'light', viewport: { width: 1280, height: 720 }, targets: TARGETS },
  { label: 'dark 1280', scheme: 'dark', viewport: { width: 1280, height: 720 }, targets: TARGETS },
  {
    label: 'light 375',
    scheme: 'light',
    viewport: { width: 375, height: 812 },
    targets: TARGETS.filter((t) => NARROW_TARGETS.includes(t.name)),
  },
]

// ─── Allowlist ───────────────────────────────────────────────────────────────

interface AllowedViolation {
  rule: string
  /** Target name from TARGETS. */
  page: string
  /** Restrict to one scheme; omit for both. */
  scheme?: Scheme
  /** Narrows the entry to specific nodes of that rule. */
  matches: (node: NodeResult) => boolean
  reason: string
}

const nodeData = (node: NodeResult): Record<string, unknown> =>
  (node.any[0]?.data ?? {}) as Record<string, unknown>

const ALLOWLIST: AllowedViolation[] = [
  {
    rule: 'scrollable-region-focusable',
    page: 'stack:editor',
    matches: (n) => n.target.join(' ') === '.cm-scroller',
    reason:
      'CodeMirror 6 renders its scroller with tabindex="-1"; the focusable contenteditable ' +
      '(.cm-content) is a sibling inside it. Library markup, not ours.',
  },
  {
    rule: 'color-contrast',
    page: 'stack:editor',
    scheme: 'dark',
    matches: (n) => String(nodeData(n).fgColor).toLowerCase() === '#e06c75',
    reason:
      'YAML keys in @codemirror/theme-one-dark are #e06c75 on #282c34 (4.38:1; 4.08:1 on the ' +
      'active line). Fixing needs a custom highlight style.',
  },
]

// ─── Scan ────────────────────────────────────────────────────────────────────

const BLOCKING = new Set(['critical', 'serious'])

interface Finding {
  rule: string
  impact: string
  target: string
  html: string
  detail: string
  helpUrl: string
}

async function runAxe(page: Page): Promise<Result[]> {
  const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  return result.violations
}

/** Splits blocking violations into allowlisted and not, and collects the rest. */
function classify(
  violations: Result[],
  pageName: string,
  scheme: Scheme,
): { blocking: Finding[]; allowed: AllowedViolation[]; other: string[] } {
  const blocking: Finding[] = []
  const allowed: AllowedViolation[] = []
  const other: string[] = []
  for (const v of violations) {
    if (!BLOCKING.has(v.impact ?? '')) {
      other.push(`${v.impact} ${v.id} x${v.nodes.length}`)
      continue
    }
    for (const node of v.nodes) {
      const entry = ALLOWLIST.find(
        (a) =>
          a.rule === v.id &&
          a.page === pageName &&
          (a.scheme === undefined || a.scheme === scheme) &&
          a.matches(node),
      )
      if (entry) {
        allowed.push(entry)
        continue
      }
      const d = nodeData(node)
      blocking.push({
        rule: v.id,
        impact: v.impact ?? '',
        target: node.target.join(' '),
        html: (node.html ?? '').slice(0, 160),
        detail: d.contrastRatio
          ? `${d.fgColor} on ${d.bgColor} = ${d.contrastRatio}:1 (needs ${d.expectedContrastRatio})`
          : (node.failureSummary ?? '').split('\n').slice(0, 2).join(' '),
        helpUrl: v.helpUrl,
      })
    }
  }
  return { blocking, allowed, other }
}

function describeFindings(findings: Finding[]): string {
  return findings
    .map((f) => `  [${f.impact}] ${f.rule}: ${f.target}\n    ${f.detail}\n    ${f.html}\n    ${f.helpUrl}`)
    .join('\n')
}

async function openAndSettle(page: Page, url: string): Promise<void> {
  await page.goto(url)
  await expect(page.locator('main').first()).toBeVisible()
  await waitForInvalidationSettle(page)
}

// ─── Specs ───────────────────────────────────────────────────────────────────

let stackId = ''

test.describe('a11y: axe wcag2a/wcag2aa', () => {
  test.beforeAll(async ({ request }) => {
    expect(
      AUTH_DISABLED,
      'a11y.spec.ts needs a backend started with AUTH_DISABLED=true, and AUTH_DISABLED=true in this ' +
        "process's environment; the login and setup pages are not scanned",
    ).toBe(true)
    const resp = await request.get(`${API_URL}/api/v1/stacks`)
    expect(resp.status(), 'GET /api/v1/stacks').toBe(200)
    const body = await resp.json()
    const stacks: Array<{ id: string; name?: string; projectName?: string }> = Array.isArray(body)
      ? body
      : (body.stacks ?? [])
    const stack = stacks.find((s) => (s.projectName ?? s.name ?? s.id ?? '').includes(TEST_STACK_NAME))
    expect(stack, `Stack '${TEST_STACK_NAME}' not found`).toBeTruthy()
    stackId = stack!.id
  })

  for (const cfg of CONFIGS) {
    test.describe(cfg.label, () => {
      test.use({ colorScheme: cfg.scheme, viewport: cfg.viewport })

      for (const target of cfg.targets) {
        test(`A11Y-${cfg.label.replace(' ', '-')}-${target.name}`, async ({ page }) => {
          await openAndSettle(page, target.url(stackId))
          const { blocking, other } = classify(await runAxe(page), target.name, cfg.scheme)
          for (const o of other) test.info().annotations.push({ type: 'non-blocking', description: o })
          // Counted rather than toEqual([]): a failing toEqual would dump every node.
          expect(
            blocking.length,
            `${blocking.length} critical/serious violation(s) on ${target.name} (${cfg.label}):\n` +
              describeFindings(blocking),
          ).toBe(0)
        })
      }
    })
  }

  // ─── ALLOWLIST: an entry that stops firing must be removed ─────────────────

  for (const entry of ALLOWLIST) {
    const schemes: Scheme[] = entry.scheme ? [entry.scheme] : ['light']
    for (const scheme of schemes) {
      test.describe(`allowlist ${entry.rule} ${entry.page}`, () => {
        test.use({ colorScheme: scheme, viewport: { width: 1280, height: 720 } })

        test(`A11Y-ALLOWLIST-${entry.rule}-${entry.page}-${scheme} still fires (delete the entry if not)`, async ({
          page,
        }) => {
          const target = TARGETS.find((t) => t.name === entry.page)!
          await openAndSettle(page, target.url(stackId))
          const { allowed } = classify(await runAxe(page), entry.page, scheme)
          expect(
            allowed.filter((a) => a === entry).length,
            `ALLOWLIST entry no longer fires on ${entry.page} (${scheme}); remove it from a11y.spec.ts.\n` +
              `Entry: ${entry.reason}`,
          ).toBeGreaterThan(0)
        })
      })
    }
  }
})

// ─── CONTROL: the scan is shown to fire, and to stay quiet on a fixed page ────

test.describe('a11y: CONTROL (needs no backend)', () => {
  const doc = (img: string) =>
    `<!doctype html><html lang="en"><head><title>control</title></head><body><main><h1>Control</h1>${img}</main></body></html>`

  test('A11Y-CONTROL-001: an <img> with no alt is reported as critical image-alt', async ({ page: p }) => {
    await p.setContent(doc('<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">'))
    const { blocking } = classify(await runAxe(p), 'control', 'light')
    expect(blocking.map((b) => `${b.impact} ${b.rule}`)).toEqual(['critical image-alt'])
  })

  test('A11Y-CONTROL-002: the same page with alt="" is clean', async ({ page: p }) => {
    await p.setContent(doc('<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="">'))
    const { blocking } = classify(await runAxe(p), 'control', 'light')
    expect(blocking.map((b) => `${b.impact} ${b.rule}`)).toEqual([])
  })
})
