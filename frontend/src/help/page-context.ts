import { useLocation } from 'react-router'
import { DASHBOARD_TABS, HOST_VIEWS, SETTINGS_SECTION_IDS, STACK_TABS } from '@/lib/app-views'
import type { PageContextKey } from './registry'

// The old stack tab names StackPage() redirects to the merged tab set.
const LEGACY_STACK_TABS: Record<string, string> = {
  compose: 'editor',
  environment: 'editor',
  split: 'editor',
  history: 'activity',
  updates: 'activity',
  backups: 'activity',
}

function isOneOf<T extends string>(values: readonly T[], value: string): value is T {
  return (values as readonly string[]).includes(value)
}

/**
 * The help topic key for a location, resolved the way the pages resolve it:
 * the dashboard defaults to stacks, settings to account-security, /host to its
 * overview. An unknown dashboard tab maps to stacks, an unknown /host/<view> to
 * the overview (HostPage redirects it there), and the legacy /?tab=<hostview>
 * to that host view (DashboardPage redirects it to /host/<view>); an unknown stack tab renders no tab content,
 * so it has no topic (null, the panel shows the guide index).
 */
/** @knipignore exported for help/__tests__/page-context.test.ts, which tests the mapping without a router; used in-file by usePageHelpContext */
export function pageContextFor(pathname: string, search: string): PageContextKey | null {
  const segments = pathname.split('/').filter(Boolean)

  if (segments.length === 0) {
    const tab = new URLSearchParams(search).get('tab') || 'stacks'
    if (isOneOf(HOST_VIEWS, tab)) return `host:${tab}`
    return `dashboard:${isOneOf(DASHBOARD_TABS, tab) ? tab : 'stacks'}`
  }

  if (segments[0] === 'host') {
    const view = segments[1] ?? ''
    return `host:${isOneOf(HOST_VIEWS, view) ? view : 'overview'}`
  }

  if (segments[0] === 'stacks' && segments.length >= 2) {
    const raw = segments[2] ?? 'overview'
    const tab = LEGACY_STACK_TABS[raw] ?? raw
    return isOneOf(STACK_TABS, tab) ? `stack:${tab}` : null
  }

  if (segments[0] === 'settings') {
    const section = segments[1] ?? ''
    return `settings:${isOneOf(SETTINGS_SECTION_IDS, section) ? section : 'account-security'}`
  }

  return null
}

export function usePageHelpContext(): PageContextKey | null {
  const { pathname, search } = useLocation()
  return pageContextFor(pathname, search)
}
