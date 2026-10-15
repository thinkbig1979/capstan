import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { expectHintOpensHelp } from '@/test/helpHint'
import { CleanupPolicyForm } from '../CleanupPolicyForm'

// Moved from settings/__tests__/DockerCleanupCard.test.tsx with the form
// (agent-os-6qfz), plus the D98 switches, warnings and threshold.

const mockGetCleanupPolicy = vi.fn()
const mockUpdateCleanupPolicy = vi.fn()

vi.mock('@/lib/api', () => ({
  resourcesApi: {
    getCleanupPolicy: (...args: unknown[]) => mockGetCleanupPolicy(...args),
    updateCleanupPolicy: (...args: unknown[]) => mockUpdateCleanupPolicy(...args),
  },
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn(), info: vi.fn(), warning: vi.fn() },
}))

const GIB = 1024 ** 3

// The floors are deliberately NOT the server's current 1/1: they come off the
// wire, so a form that hardcoded the real floor would still pass against 1 and
// tell us nothing.
const POLICY = {
  enabled: false,
  minAgeHours: 168,
  intervalHours: 24,
  categories: {
    danglingImages: true,
    unusedImages: false,
    buildCache: true,
    containers: false,
    volumes: false,
    networks: false,
  },
  alertThresholdBytes: 5 * GIB,
  minAllowedAgeHours: 6,
  minAllowedIntervalHours: 2,
}

const SWITCH_LABELS = [
  'Dangling images',
  'Unused tagged images',
  'Build cache',
  'Stopped containers outside any stack',
  'Unattached volumes',
  'Unused networks',
]

const UNUSED_IMAGES_WARNING =
  'Removes images no container uses, including the previous version you might roll back to. They can be pulled again if they are in a registry.'
const VOLUMES_WARNING =
  'Volumes can hold databases or uploads. Automatic removal cannot be undone. Stack volumes are never removed.'

function renderForm(props: Parameters<typeof CleanupPolicyForm>[0] = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  )
  return { ...render(<CleanupPolicyForm {...props} />, { wrapper }), queryClient }
}

const save = () => screen.getByRole('button', { name: 'Save cleanup schedule' })

beforeEach(() => {
  vi.clearAllMocks()
  mockGetCleanupPolicy.mockResolvedValue(POLICY)
  mockUpdateCleanupPolicy.mockResolvedValue(POLICY)
})

describe('CleanupPolicyForm: the age floor and interval', () => {
  it('disables Save below the floor and enables it at the floor', async () => {
    renderForm()
    const age = await screen.findByLabelText('Age floor (hours)')

    // Below the floor the server would 400, so the form refuses first.
    fireEvent.change(age, { target: { value: '3' } })
    expect(save()).toBeDisabled()
    expect(screen.getByText('The age floor must be at least 6 hours.')).toBeInTheDocument()

    // Same instrument, other side: exactly at the floor is a legal change.
    fireEvent.change(age, { target: { value: '6' } })
    expect(save()).toBeEnabled()
    expect(screen.queryByText('The age floor must be at least 6 hours.')).not.toBeInTheDocument()
  })

  it('keeps Save disabled while nothing has changed', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')
    expect(save()).toBeDisabled()
  })

  it('blocks Save while the interval is below its floor', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText('Run every (hours)'), { target: { value: '1' } })

    expect(save()).toBeDisabled()
    expect(screen.getByText('The interval must be at least 2 hours.')).toBeInTheDocument()
  })

  it('sends only the fields that changed', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText('Run every (hours)'), { target: { value: '12' } })
    fireEvent.click(save())

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ intervalHours: 12 })
  })

  it('sends the enabled switch when it is the only change', async () => {
    renderForm()
    fireEvent.click(await screen.findByRole('switch', { name: 'Run cleanup on a schedule' }))
    fireEvent.click(save())

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ enabled: true })
  })

  it('opened from "Turn on", starts with the schedule switched on and unsaved', async () => {
    renderForm({ startEnabled: true })
    const sw = await screen.findByRole('switch', { name: 'Run cleanup on a schedule' })
    expect(sw).toBeChecked()
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()

    fireEvent.click(save())
    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ enabled: true }))
  })

  it('refuses to render the form when the policy cannot be read', async () => {
    mockGetCleanupPolicy.mockRejectedValue(new Error('unreadable'))
    renderForm()

    expect(await screen.findByText(/could not be read/)).toBeInTheDocument()
    // No invented values, and no Save to persist them over the real policy.
    expect(screen.queryByLabelText('Age floor (hours)')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Save cleanup schedule' })).not.toBeInTheDocument()
  })

  it('docker-cleanup.min-age: the age floor hint opens help at its topic', async () => {
    const user = userEvent.setup()
    renderForm()
    await expectHintOpensHelp(user, 'Age floor', 'docker-cleanup.min-age')
  })
})

describe('CleanupPolicyForm: a failed REFETCH must not discard data (agent-os-wczm)', () => {
  // Resolves first, then rejects a REFETCH. A first-fetch-rejects fixture
  // leaves `data` undefined and stays green under either guard.
  it('keeps the populated form and the unsaved edit when a REFETCH fails', async () => {
    const { queryClient } = renderForm()

    const ageFloor = await screen.findByLabelText('Age floor (hours)')
    fireEvent.change(ageFloor, { target: { value: '240' } })
    expect(ageFloor).toHaveValue(240)

    mockGetCleanupPolicy.mockRejectedValue(new Error('boom'))
    await queryClient.refetchQueries({ queryKey: ['settings', 'docker-cleanup'] })

    await waitFor(() =>
      expect(queryClient.getQueryState(['settings', 'docker-cleanup'])?.status).toBe('error'),
    )
    expect(screen.getByLabelText('Age floor (hours)')).toHaveValue(240)
    expect(screen.getByLabelText('Run every (hours)')).toHaveValue(24)
    expect(screen.queryByText(/The cleanup schedule could not be read/)).not.toBeInTheDocument()
    expect(screen.getByText(/Could not refresh the cleanup schedule/)).toBeInTheDocument()
  })
})

/**
 * agent-os-qags.4. `parseInt('') || 0` turned a cleared box into 0. The draft is
 * number | null: undefined = untouched, null = cleared. Each arm asserts the box
 * STAYS empty as well as that Save is off.
 */
describe('CleanupPolicyForm: a cleared numeric field is empty, not 0 (agent-os-qags.4)', () => {
  it.each([
    ['Age floor (hours)', '240'],
    ['Run every (hours)', '48'],
    ['Warn on the dashboard when more than this many GB can be freed', '10'],
  ])('%s: clearing keeps the box empty and blocks Save', async (label, typed) => {
    renderForm()
    const input = await screen.findByLabelText(label)

    fireEvent.change(input, { target: { value: typed } })
    fireEvent.change(input, { target: { value: '' } })

    expect(input).toHaveValue(null)
    expect(screen.getByText('Enter a number')).toBeInTheDocument()
    expect(save()).toBeDisabled()
    fireEvent.click(save())
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()
  })

  it('refilling the field re-enables Save and sends the typed value', async () => {
    renderForm()
    const input = await screen.findByLabelText('Run every (hours)')

    fireEvent.change(input, { target: { value: '' } })
    fireEvent.change(input, { target: { value: '48' } })

    expect(screen.queryByText('Enter a number')).not.toBeInTheDocument()
    fireEvent.click(save())
    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ intervalHours: 48 }))
  })
})

describe('CleanupPolicyForm: categories (D98-A)', () => {
  it('shows the six switches, set from the stored policy', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')

    const states = SWITCH_LABELS.map((name) => [name, (screen.getByRole('switch', { name }) as HTMLElement).getAttribute('aria-checked')])
    expect(states).toEqual([
      ['Dangling images', 'true'],
      ['Unused tagged images', 'false'],
      ['Build cache', 'true'],
      ['Stopped containers outside any stack', 'false'],
      ['Unattached volumes', 'false'],
      ['Unused networks', 'false'],
    ])
  })

  it('shows the unused-images warning only while that switch is on', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')
    expect(screen.queryByText(UNUSED_IMAGES_WARNING)).toBeNull()

    fireEvent.click(screen.getByRole('switch', { name: 'Unused tagged images' }))
    expect(screen.getByTestId('cleanup-warning-unusedImages')).toHaveTextContent(UNUSED_IMAGES_WARNING)

    fireEvent.click(screen.getByRole('switch', { name: 'Unused tagged images' }))
    expect(screen.queryByText(UNUSED_IMAGES_WARNING)).toBeNull()
  })

  it('shows the volumes warning only while that switch is on', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')
    expect(screen.queryByText(VOLUMES_WARNING)).toBeNull()

    fireEvent.click(screen.getByRole('switch', { name: 'Unattached volumes' }))
    expect(screen.getByTestId('cleanup-warning-volumes')).toHaveTextContent(VOLUMES_WARNING)
  })

  it('sends only the switches that changed', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')
    fireEvent.click(screen.getByRole('switch', { name: 'Stopped containers outside any stack' }))
    fireEvent.click(screen.getByRole('switch', { name: 'Build cache' }))
    fireEvent.click(save())

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledTimes(1))
    expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({
      categories: { buildCache: false, containers: true },
    })
  })

  it('refuses an enabled schedule with every category off', async () => {
    renderForm({ startEnabled: true })
    await screen.findByLabelText('Age floor (hours)')
    fireEvent.click(screen.getByRole('switch', { name: 'Dangling images' }))
    fireEvent.click(screen.getByRole('switch', { name: 'Build cache' }))

    expect(screen.getByText('Turn on at least one category, or turn automatic cleanup off.')).toBeInTheDocument()
    expect(save()).toBeDisabled()

    // Other side: one category back on is a legal save.
    fireEvent.click(screen.getByRole('switch', { name: 'Build cache' }))
    expect(save()).toBeEnabled()
  })

  it('asks before saving with volumes newly on, and saves nothing until confirmed', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')
    fireEvent.click(screen.getByRole('switch', { name: 'Unattached volumes' }))
    fireEvent.click(save())

    const ask = await screen.findByTestId('cleanup-volumes-confirm')
    expect(ask).toHaveTextContent('remove unattached volumes older than 168 h, every 24 h')
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Turn on volume removal' }))
    await waitFor(() =>
      expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ categories: { volumes: true } }),
    )
  })

  it('saves nothing when the volumes confirmation is declined', async () => {
    renderForm()
    await screen.findByLabelText('Age floor (hours)')
    fireEvent.click(screen.getByRole('switch', { name: 'Unattached volumes' }))
    fireEvent.click(save())
    fireEvent.click(await screen.findByRole('button', { name: 'Go back' }))

    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(mockUpdateCleanupPolicy).not.toHaveBeenCalled()
    expect(screen.queryByTestId('cleanup-volumes-confirm')).toBeNull()
    expect(save()).toBeEnabled()
  })

  it('does not ask again when volumes were already on', async () => {
    mockGetCleanupPolicy.mockResolvedValue({ ...POLICY, categories: { ...POLICY.categories, volumes: true } })
    renderForm()
    fireEvent.change(await screen.findByLabelText('Run every (hours)'), { target: { value: '12' } })
    fireEvent.click(save())

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ intervalHours: 12 }))
    expect(screen.queryByTestId('cleanup-volumes-confirm')).toBeNull()
  })
})

describe('CleanupPolicyForm: the dashboard warning threshold (D98-C)', () => {
  const label = 'Warn on the dashboard when more than this many GB can be freed'

  it('shows the stored bytes in GB', async () => {
    renderForm()
    expect(await screen.findByLabelText(label)).toHaveValue(5)
  })

  it('sends bytes on the wire', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText(label), { target: { value: '12' } })
    fireEvent.click(save())

    await waitFor(() =>
      expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ alertThresholdBytes: 12 * GIB }),
    )
  })

  it('sends 0 to turn the warning off', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText(label), { target: { value: '0' } })
    fireEvent.click(save())

    await waitFor(() => expect(mockUpdateCleanupPolicy).toHaveBeenCalledWith({ alertThresholdBytes: 0 }))
  })

  it('refuses more than the server allows', async () => {
    renderForm()
    fireEvent.change(await screen.findByLabelText(label), { target: { value: '1025' } })
    expect(save()).toBeDisabled()
    expect(screen.getByText('The limit is 1024 GB.')).toBeInTheDocument()
  })
})
