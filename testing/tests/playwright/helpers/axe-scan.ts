/**
 * Shared axe-core scan helpers (WCAG 2.0 A + AA) for the a11y specs.
 *
 * Used by a11y.spec.ts (AUTH_DISABLED backend) and by auth-session.spec.ts (the
 * login and setup pages, which only exist on an auth-on backend). One copy, so
 * the two specs cannot drift apart on what counts as a blocking violation.
 *
 * `critical` and `serious` violations block. `moderate` and `minor` findings are
 * returned in `other` for the caller to attach as annotations.
 */

import type { Page } from 'playwright/test'
import AxeBuilder from '@axe-core/playwright'

// axe-core is a transitive dependency of @axe-core/playwright, so its types are
// derived from the builder rather than imported from a package the root does not list.
export type Result = Awaited<ReturnType<AxeBuilder['analyze']>>['violations'][number]
export type NodeResult = Result['nodes'][number]

export type Scheme = 'light' | 'dark'

export interface AllowedViolation {
  rule: string
  /** Target name from the caller's target list. */
  page: string
  /** Restrict to one scheme; omit for both. */
  scheme?: Scheme
  /** Narrows the entry to specific nodes of that rule. */
  matches: (node: NodeResult) => boolean
  reason: string
}

export const nodeData = (node: NodeResult): Record<string, unknown> =>
  (node.any[0]?.data ?? {}) as Record<string, unknown>

const BLOCKING = new Set(['critical', 'serious'])

export interface Finding {
  rule: string
  impact: string
  target: string
  html: string
  detail: string
  helpUrl: string
}

export async function runAxe(page: Page): Promise<Result[]> {
  const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze()
  return result.violations
}

/**
 * Splits blocking violations into allowlisted and not, and collects the rest.
 * `allowlist` defaults to empty: a caller with no known third-party violations
 * passes nothing and every blocking violation fails.
 */
export function classify(
  violations: Result[],
  pageName: string,
  scheme: Scheme,
  allowlist: AllowedViolation[] = [],
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
      const entry = allowlist.find(
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

export function describeFindings(findings: Finding[]): string {
  return findings
    .map((f) => `  [${f.impact}] ${f.rule}: ${f.target}\n    ${f.detail}\n    ${f.html}\n    ${f.helpUrl}`)
    .join('\n')
}
