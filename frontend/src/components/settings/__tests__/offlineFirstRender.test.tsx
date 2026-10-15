/**
 * agent-os-cg26. A settings page that FIRST renders while the browser is
 * offline has a query whose fetchStatus is 'paused': TanStack's
 * isLoading = isPending && isFetching, so isLoading is false while data is still
 * undefined, and every `if (isLoading) spinner` branch fell through to the next
 * branch with nothing to show — an error sentence naming a failed read that
 * never ran, or (git, update schedule, global env, audit log, directories) a
 * form/table built from undefined data. The first-load branch is keyed on
 * isPending instead.
 *
 * Two arms per page, on the same component:
 *  - offline: a spinner, and not the error sentence (nothing was attempted);
 *  - online with a REJECTING read: the error sentence, and no spinner, so the
 *    fix did not swallow a real first-load failure.
 */
import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ReactElement } from 'react'
import { screen, cleanup } from '@testing-library/react'
import { onlineManager } from '@tanstack/react-query'
import { renderWithProviders } from '@/test/utils'
import { HistoryRetentionSection } from '../HistoryRetentionSection'
import { AboutContent } from '../AboutContent'
import { BackupSettingsContent } from '../BackupSettingsContent'
import { GitSettingsContent } from '../GitSettingsContent'
import { UpdateScheduleContent } from '../UpdateScheduleContent'
import { GlobalEnvSettingsContent } from '../GlobalEnvSettingsContent'
import { AuditLogContent } from '../AuditLogContent'
import { DirectoriesSettingsContent } from '../DirectoriesSettingsContent'

// Every API method the pages under test can reach rejects. Offline, none of them
// is ever called (the queries are paused), which is the point of the offline arm.
vi.mock('@/lib/api', async (importOriginal) => {
  const failing = () =>
    new Proxy({}, { get: () => () => Promise.reject(new Error('boom')) })
  return {
    ...(await importOriginal<typeof import('@/lib/api')>()),
    resourcesApi: failing(),
    settingsApi: failing(),
    autoUpdateApi: failing(),
    directoryConfigApi: failing(),
    backupApi: failing(),
    versionApi: failing(),
  }
})

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const hasSpinner = (container: HTMLElement, selector = '.animate-spin') =>
  container.querySelector(selector) !== null

interface Page {
  name: string
  element: ReactElement
  /** What the page says when its first read really failed. */
  failure: RegExp
  /** The page's own spinner, when it embeds another page's (the audit log
   *  embeds HistoryRetentionSection, whose small spinner must not count). */
  spinner?: string
}

const PAGES: Page[] = [
  { name: 'HistoryRetentionSection', element: <HistoryRetentionSection />, failure: /could not be read/ },
  { name: 'AboutContent', element: <AboutContent />, failure: /Could not read the build identity/ },
  { name: 'BackupSettingsContent', element: <BackupSettingsContent />, failure: /Failed to load backup settings/ },
  { name: 'GitSettingsContent', element: <GitSettingsContent />, failure: /Could not load the git settings/ },
  { name: 'UpdateScheduleContent', element: <UpdateScheduleContent />, failure: /Could not load update settings/ },
  { name: 'GlobalEnvSettingsContent', element: <GlobalEnvSettingsContent />, failure: /Failed to load global environment variables/ },
  { name: 'AuditLogContent', element: <AuditLogContent />, failure: /Failed to load audit log/, spinner: '.animate-spin.w-8' },
  { name: 'DirectoriesSettingsContent', element: <DirectoriesSettingsContent />, failure: /Could not load the directory configuration/ },
]

afterEach(() => {
  cleanup()
  onlineManager.setOnline(true)
})

describe.each(PAGES)('$name — first render', ({ element, failure, spinner }) => {
  it('offline: shows a spinner, not the failure sentence', () => {
    onlineManager.setOnline(false)
    const { container } = renderWithProviders(element)

    expect(hasSpinner(container, spinner)).toBe(true)
    expect(screen.queryByText(failure)).not.toBeInTheDocument()
  })

  it('online with a rejecting read: still shows the failure sentence and no spinner', async () => {
    const { container } = renderWithProviders(element)

    expect(await screen.findByText(failure)).toBeInTheDocument()
    expect(hasSpinner(container, spinner)).toBe(false)
  })
})
