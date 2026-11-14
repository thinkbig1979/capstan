import { createElement, type ReactNode } from 'react'
import { renderHook } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { pageContextFor, usePageHelpContext } from '../page-context'
import { PAGE_HELP, type PageContextKey } from '../registry'

function contextAt(url: string): PageContextKey | null {
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(MemoryRouter, { initialEntries: [url] }, children)
  return renderHook(() => usePageHelpContext(), { wrapper }).result.current
}

// Every app view, as the URL a user lands on. Written out rather than derived
// from the exported arrays so a renamed tab fails here, not silently passes.
const ALL_VIEWS: [string, PageContextKey][] = [
  ['/', 'dashboard:stacks'],
  ['/?tab=directories', 'dashboard:directories'],
  ['/?tab=updates', 'dashboard:updates'],
  ['/?tab=backups', 'dashboard:backups'],
  ['/?tab=host&view=containers', 'host:containers'],
  ['/?tab=host&view=images', 'host:images'],
  ['/?tab=host&view=volumes', 'host:volumes'],
  ['/?tab=host&view=networks', 'host:networks'],
  ['/?tab=host&view=build-cache', 'host:build-cache'],
  ['/?tab=host', 'host:overview'],
  ['/stacks/web', 'stack:overview'],
  ['/stacks/web/editor', 'stack:editor'],
  ['/stacks/web/logs', 'stack:logs'],
  ['/stacks/web/terminal', 'stack:terminal'],
  ['/stacks/web/metrics', 'stack:metrics'],
  ['/stacks/web/activity', 'stack:activity'],
  ['/settings', 'settings:account-security'],
  ['/settings/appearance', 'settings:appearance'],
  ['/settings/directories', 'settings:directories'],
  ['/settings/global-env', 'settings:global-env'],
  ['/settings/git', 'settings:git'],
  ['/settings/update-schedule', 'settings:update-schedule'],
  ['/settings/backup', 'settings:backup'],
  ['/settings/docker-cleanup', 'settings:docker-cleanup'],
  ['/settings/audit-log', 'settings:audit-log'],
  ['/settings/about', 'settings:about'],
]

describe('usePageHelpContext', () => {
  it('covers every PAGE_HELP key exactly once', () => {
    expect(ALL_VIEWS).toHaveLength(26)
    expect(ALL_VIEWS.map(([, key]) => key).sort()).toEqual(Object.keys(PAGE_HELP).sort())
  })

  it.each(ALL_VIEWS)('%s -> %s', (url, key) => {
    expect(contextAt(url)).toBe(key)
  })

  it.each([
    ['/?tab=stacks', 'dashboard:stacks'],
    ['/?tab=nope', 'dashboard:stacks'],
    ['/?tab=', 'dashboard:stacks'],
    ['/?tab=host&view=nope', 'host:overview'],
    ['/?tab=host&view=', 'host:overview'],
    ['/?tab=host&free=1', 'host:overview'],
    ['/?tab=host&view=images&free=1', 'host:images'],
    ['/?tab=host#automatic-cleanup', 'host:overview'],
    // Removed with the Metrics tab (agent-os-mi4r): DashboardPage sends it to /.
    ['/?tab=overview', 'dashboard:stacks'],
    // A list view is not a dashboard tab, and view= means nothing off the Host tab.
    ['/?tab=volumes', 'dashboard:stacks'],
    ['/?tab=updates&view=images', 'dashboard:updates'],
    ['/settings/nope', 'settings:account-security'],
    ['/stacks/web/activity?view=backups', 'stack:activity'],
    ['/stacks/web/logs?container=web-1', 'stack:logs'],
    ['/stacks/web/overview', 'stack:overview'],
  ])('%s -> %s', (url, key) => {
    expect(contextAt(url)).toBe(key)
  })

  // The redirect map in StackPage(): help resolves to the tab it lands on.
  it.each([
    ['/stacks/web/compose', 'stack:editor'],
    ['/stacks/web/environment', 'stack:editor'],
    ['/stacks/web/split', 'stack:editor'],
    ['/stacks/web/history', 'stack:activity'],
    ['/stacks/web/updates', 'stack:activity'],
    ['/stacks/web/backups', 'stack:activity'],
  ])('legacy %s -> %s', (url, key) => {
    expect(contextAt(url)).toBe(key)
  })

  // agent-os-mi4r: the /host routes are gone, so they have no topic.
  it.each(['/stacks/web/nope', '/login', '/setup', '/stacks', '/host', '/host/images'])('%s has no page topic', (url) => {
    expect(contextAt(url)).toBeNull()
  })

  it('pageContextFor reads the tab from the query string, not the path', () => {
    expect(pageContextFor('/', '?tab=updates')).toBe('dashboard:updates')
    expect(pageContextFor('/', '?tab=host&view=volumes')).toBe('host:volumes')
    expect(pageContextFor('/', '')).toBe('dashboard:stacks')
  })
})
