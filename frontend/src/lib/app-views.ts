/**
 * The app's views, as the arrays the pages render their tabs and sections
 * from. They live in a .ts module rather than in the pages because
 * react-refresh/only-export-components rejects a .tsx file that exports a
 * component and an array. The help registry (src/help/registry.ts) keys its
 * page topics on these, and helpCoverage.test.ts fails when a value has no
 * topic, so a new tab or section added here must ship with its help section.
 */

/** The dashboard's top tab row, in display order. 'overview' is Metrics. */
export const DASHBOARD_TABS = ['stacks', 'overview', 'directories', 'updates', 'backups'] as const
export type DashboardTab = (typeof DASHBOARD_TABS)[number]

/** Dashboard views reached through the host strip, not the tab row. */
export const HOST_VIEWS = ['containers', 'images', 'volumes', 'networks', 'build-cache'] as const
export type HostView = (typeof HOST_VIEWS)[number]

export type DashboardView = DashboardTab | HostView

/** The stack page's tabs, in display order. */
export const STACK_TABS = ['overview', 'editor', 'logs', 'terminal', 'metrics', 'activity'] as const
export type StackTab = (typeof STACK_TABS)[number]

/** Every settings section id; SettingsPage's catalog is typed against these. */
export const SETTINGS_SECTION_IDS = [
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
] as const
export type SettingsSectionId = (typeof SETTINGS_SECTION_IDS)[number]
