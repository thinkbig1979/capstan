import type { DashboardTab, HostView, SettingsSectionId, StackTab } from '@/lib/app-views'

/** A bundled doc (id as in HelpDoc.id) and optionally a heading anchor in it. */
export interface HelpRef {
  doc: string
  anchor?: string
}

/**
 * One key per app view. A new key family is a new union member here plus its
 * entries below.
 */
export type PageContextKey =
  | `dashboard:${DashboardTab}`
  | `host:${'overview' | HostView}`
  | `stack:${StackTab}`
  | `settings:${SettingsSectionId}`

/** Fields whose (i) hint links into the docs ("Learn more"). */
export type FieldHelpId =
  | 'backup.repository'
  | 'backup.password'
  | 'backup.retention'
  | 'backup.verify'
  | 'backup.cloud'
  | 'backup.transfers'
  | 'stack.backup'
  | 'stack.backup-stop-policy'
  | 'git.ssh-key'
  | 'git.https-token'
  | 'directories.scan-depth'
  | 'directories.default-dir'
  | 'docker-cleanup.min-age'
  | 'updates.schedule'

const DASHBOARD = 'help/dashboard'
const DOCKER_HOST = 'help/docker-host'
const STACK = 'help/stack-page'
const SETTINGS = 'help/settings'

// A Record over the union: a view with no topic is a tsc -b error, and
// helpCoverage.test.ts proves every anchor is a real heading in the bundled doc.
export const PAGE_HELP: Record<PageContextKey, HelpRef> = {
  'dashboard:stacks': { doc: DASHBOARD, anchor: 'stacks' },
  'dashboard:overview': { doc: DASHBOARD, anchor: 'metrics' },
  'dashboard:directories': { doc: DASHBOARD, anchor: 'directories' },
  'dashboard:updates': { doc: DASHBOARD, anchor: 'updates' },
  'dashboard:backups': { doc: DASHBOARD, anchor: 'backups' },

  'host:overview': { doc: DOCKER_HOST, anchor: 'overview' },
  'host:containers': { doc: DOCKER_HOST, anchor: 'containers' },
  'host:images': { doc: DOCKER_HOST, anchor: 'images' },
  'host:volumes': { doc: DOCKER_HOST, anchor: 'volumes' },
  'host:networks': { doc: DOCKER_HOST, anchor: 'networks' },
  'host:build-cache': { doc: DOCKER_HOST, anchor: 'build-cache' },

  'stack:overview': { doc: STACK, anchor: 'overview' },
  'stack:editor': { doc: STACK, anchor: 'editor' },
  'stack:logs': { doc: STACK, anchor: 'logs' },
  'stack:terminal': { doc: STACK, anchor: 'terminal' },
  'stack:metrics': { doc: STACK, anchor: 'metrics' },
  'stack:activity': { doc: STACK, anchor: 'activity' },

  'settings:account-security': { doc: SETTINGS, anchor: 'account-security' },
  'settings:appearance': { doc: SETTINGS, anchor: 'appearance' },
  'settings:directories': { doc: SETTINGS, anchor: 'directories' },
  'settings:global-env': { doc: SETTINGS, anchor: 'global-environment-variables' },
  'settings:git': { doc: SETTINGS, anchor: 'git' },
  'settings:update-schedule': { doc: SETTINGS, anchor: 'updates' },
  'settings:backup': { doc: SETTINGS, anchor: 'backup' },
  'settings:docker-cleanup': { doc: SETTINGS, anchor: 'docker-cleanup' },
  'settings:audit-log': { doc: SETTINGS, anchor: 'audit-log' },
  'settings:about': { doc: SETTINGS, anchor: 'about' },
}

export const FIELD_HELP: Record<FieldHelpId, HelpRef> = {
  'backup.repository': { doc: SETTINGS, anchor: 'repository-path' },
  'backup.password': { doc: SETTINGS, anchor: 'repository-password' },
  'backup.retention': { doc: SETTINGS, anchor: 'retention-and-pruning' },
  'backup.verify': { doc: SETTINGS, anchor: 'weekly-repository-check' },
  'backup.cloud': { doc: SETTINGS, anchor: 'cloud-sync' },
  'backup.transfers': { doc: SETTINGS, anchor: 'parallel-transfers' },
  'stack.backup': { doc: STACK, anchor: 'backup-and-stop-policy' },
  'stack.backup-stop-policy': { doc: STACK, anchor: 'backup-and-stop-policy' },
  'git.ssh-key': { doc: SETTINGS, anchor: 'ssh-private-key-path' },
  'git.https-token': { doc: SETTINGS, anchor: 'personal-access-token' },
  'directories.scan-depth': { doc: SETTINGS, anchor: 'directory-recursion-depth' },
  'directories.default-dir': { doc: SETTINGS, anchor: 'default-directory-for-new-stacks' },
  'docker-cleanup.min-age': { doc: DOCKER_HOST, anchor: 'age-floor' },
  'updates.schedule': { doc: SETTINGS, anchor: 'scan-interval-and-auto-update' },
}
