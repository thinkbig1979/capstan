/**
 * Single source of truth for every react-query cache key in the app.
 *
 * Keys used to be hand-built array literals at each call site, so a reader and a
 * writer could disagree by one string and nothing would fail loudly — the
 * invalidation just silently missed and the UI served stale data until a
 * refetchInterval or page reload covered for it. (That is exactly what happened
 * to the sidebar backup-status footer: it read ['backup-status'] while every
 * writer invalidated ['backup','status'].)
 *
 * Rules for using this module:
 *  - Never inline a key array at a call site; add an entry here instead.
 *  - Keys are hierarchical, and react-query matches by prefix: invalidating
 *    `stack.all` reaches every ['stack', id, …] entry, and `resources.all`
 *    reaches every ['resources', kind]. Order entries so the broad prefix is a
 *    real prefix of the narrow ones.
 *  - Parameterised entries take their params as arguments so the variable part
 *    cannot drift between the useQuery and the invalidate.
 */

import type { UpdateHistoryFilters } from '@/types'

/** Filters accepted by the paginated audit-log query. */
export interface AuditLogFilters {
  page: number
  pageSize: number
  action: string
  search: string
  dateFrom: string
  dateTo: string
}

export const queryKeys = {
  /** The stack list. */
  stacks: () => ['stacks'] as const,

  /** A single stack and its per-stack sub-resources. */
  stack: {
    /** Prefix for every stack-scoped entry — invalidate to refresh all of them. */
    all: () => ['stack'] as const,
    detail: (stackId: string) => ['stack', stackId] as const,
    compose: (stackId: string) => ['stack', stackId, 'compose'] as const,
    env: (stackId: string) => ['stack', stackId, 'env'] as const,
  },

  /** Aggregate dashboard counters. */
  dashboardStats: () => ['dashboard-stats'] as const,

  /** Server config (stacks directories etc.). */
  config: () => ['config'] as const,

  /** Discovered stack directories. */
  directories: () => ['directories'] as const,

  /** Configured directory scan depth. */
  scanDepth: () => ['scan-depth'] as const,

  /** Docker resources, one entry per kind. */
  resources: {
    /** Prefix for every resource kind. */
    all: () => ['resources'] as const,
    images: () => ['resources', 'images'] as const,
    volumes: () => ['resources', 'volumes'] as const,
    networks: () => ['resources', 'networks'] as const,
    buildCache: () => ['resources', 'build-cache'] as const,
    updates: () => ['resources', 'updates'] as const,
    updateJobs: () => ['resources', 'update-jobs'] as const,
  },

  /** Applied-update history. `all` prefix-matches every filtered list. */
  updateHistory: {
    all: () => ['update-history'] as const,
    list: (filters?: UpdateHistoryFilters) => ['update-history', filters] as const,
  },

  /** Per-stack auto-update policies. */
  autoUpdatePolicies: () => ['auto-update-policies'] as const,

  /** Settings panes. */
  settings: {
    all: () => ['settings'] as const,
    updates: () => ['settings', 'updates'] as const,
    git: () => ['settings', 'git'] as const,
    globalEnv: () => ['settings', 'global-env'] as const,
  },

  /** Git state for a stack. `all` prefix-matches log and diff. */
  git: {
    all: (stackId: string) => ['git', stackId] as const,
    log: (stackId: string, limit?: number, offset?: number, file?: string) =>
      ['git', stackId, 'log', limit, offset, file] as const,
    diff: (stackId: string, hash: string) => ['git', stackId, 'diff', hash] as const,
  },

  /** Paginated, filtered audit log. */
  auditLog: (filters: AuditLogFilters) =>
    [
      'audit-log',
      filters.page,
      filters.pageSize,
      filters.action,
      filters.search,
      filters.dateFrom,
      filters.dateTo,
    ] as const,

  /** Backup/restore. */
  backup: {
    /** Prefix for every backup entry. */
    all: () => ['backup'] as const,
    settings: () => ['backup', 'settings'] as const,
    policies: () => ['backup', 'policies'] as const,
    status: () => ['backup', 'status'] as const,
    history: (limit?: number) => ['backup', 'history', { limit }] as const,
    snapshots: (stackId: string) => ['backup', 'snapshots', stackId] as const,
    run: (runId: string) => ['backup', 'runs', runId] as const,
    snapshotPreview: (snapshotId: string) => ['backup', 'snapshot-preview', snapshotId] as const,
    stackHistory: (limit: number | undefined, stackId: string) =>
      ['backup', 'history', { limit }, 'stack', stackId] as const,
  },
} as const
