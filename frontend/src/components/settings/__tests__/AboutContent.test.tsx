import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { AboutContent } from '../AboutContent'

// "What is actually running on this host?" had no answer in the UI at all
// before this (agent-os-r7e).

const mockGetVersion = vi.fn()

vi.mock('@/lib/api', () => ({
  versionApi: {
    get: (...args: unknown[]) => mockGetVersion(...args),
  },
}))

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
}

function renderAbout() {
  return render(<AboutContent />, { wrapper: createWrapper() })
}

describe('AboutContent', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows the stamped version, commit and build date', async () => {
    mockGetVersion.mockResolvedValue({
      version: '1.4.0',
      commit: 'a1b2c3d4e5f6',
      buildDate: '2026-07-31T09:00:00Z',
    })

    renderAbout()

    expect(await screen.findByTestId('about-version')).toHaveTextContent('1.4.0')
    expect(screen.getByTestId('about-commit')).toHaveTextContent('a1b2c3d4e5f6')
    // Rendered through the locale formatter, so assert on the year rather than
    // on a fixed string that moves with the runner's timezone.
    expect(screen.getByTestId('about-build-date')).toHaveTextContent('2026')
  })

  it('links to the release notes for a semver build', async () => {
    mockGetVersion.mockResolvedValue({
      version: '1.4.0',
      commit: 'a1b2c3d4e5f6',
      buildDate: '2026-07-31T09:00:00Z',
    })

    renderAbout()

    const link = await screen.findByRole('link', { name: /release notes/i })
    expect(link).toHaveAttribute(
      'href',
      'https://github.com/thinkbig1979/capstan/releases/tag/v1.4.0',
    )
  })

  it('reports an unstamped local build as dev with no release link', async () => {
    mockGetVersion.mockResolvedValue({
      version: 'dev',
      commit: 'unknown',
      buildDate: 'unknown',
    })

    renderAbout()

    expect(await screen.findByTestId('about-version')).toHaveTextContent('dev')
    // "unknown" is a real answer from the server, but showing it verbatim reads
    // as an error; it becomes a dash.
    expect(screen.getByTestId('about-commit')).toHaveTextContent('—')
    expect(screen.getByTestId('about-build-date')).toHaveTextContent('—')
    expect(screen.queryByRole('link', { name: /release notes/i })).toBeNull()
  })

  it('offers no release link for a manual dispatch build', async () => {
    mockGetVersion.mockResolvedValue({
      version: 'manual-9632a74',
      commit: '9632a74abcdef',
      buildDate: '2026-07-31T09:00:00Z',
    })

    renderAbout()

    expect(await screen.findByTestId('about-version')).toHaveTextContent('manual-9632a74')
    expect(screen.queryByRole('link', { name: /release notes/i })).toBeNull()
  })

  it('says so when the endpoint fails rather than rendering blanks', async () => {
    mockGetVersion.mockRejectedValue(new Error('network down'))

    renderAbout()

    // useVersion retries once, so allow for react-query's ~1s backoff before
    // the query settles into isError.
    expect(
      await screen.findByText(/could not read the build identity/i, undefined, { timeout: 5000 }),
    ).toBeInTheDocument()
    expect(screen.queryByTestId('about-version')).toBeNull()
  })
})
