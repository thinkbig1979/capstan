import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithProviders } from '@/test/utils'
import { UpdatesTable } from '@/components/dashboard/updates-tab/UpdatesTable'
import type { AutoUpdatePolicy, ContainerUpdateInfo } from '@/types'

/**
 * agent-os-ygyv. The Updates tab saved a container's auto-update policy under
 * the container's ID. An update recreates the container under a new ID, so the
 * policy stopped matching after the first update and auto-update stopped for
 * that container without a word. The policy's target is now the container's
 * name. These tests render the table with the REAL AutoUpdateToggle and read
 * what it sends to the API, because the toggle saves whatever target it is
 * handed.
 */

const { setPolicy } = vi.hoisted(() => ({ setPolicy: vi.fn() }))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, autoUpdateApi: { ...actual.autoUpdateApi, setPolicy } }
})
vi.mock('@/hooks/useBackup', () => ({
  useBackupPolicies: () => ({ data: [], isError: false, refetch: vi.fn() }),
}))
vi.mock('@/components/dashboard/BackupToggle', () => ({ BackupToggle: () => null }))
vi.mock('@/components/dashboard/UpdateJobStatusCell', () => ({ UpdateJobStatusCell: () => null }))

// A standalone container, recreated by its last update under a new id.
const container: ContainerUpdateInfo = {
  containerId: 'f00dfeed0000',
  containerName: 'web',
  image: 'nginx',
  imageRef: 'nginx:latest',
  state: 'running',
  stackId: '',
  projectName: '',
  serviceName: '',
  isCompose: false,
  stackLookupFailed: false,
}

function policy(targetId: string): AutoUpdatePolicy {
  return {
    id: 'p1',
    targetType: 'container',
    targetId,
    enabled: true,
    consecutiveFailures: 0,
    paused: false,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  }
}

function renderTable(policies: AutoUpdatePolicy[]) {
  const map = new Map(policies.map((p) => [`${p.targetType}:${p.targetId}`, p]))
  return renderWithProviders(
    <UpdatesTable
      sortedUpdates={[container]}
      totalCount={1}
      sortBy="name"
      onSortChange={() => {}}
      query=""
      onQueryChange={() => {}}
      scannedAt={undefined}
      isRefreshing={false}
      onCheck={() => {}}
      policies={map}
      globalAutoUpdateState="enabled"
      jobForContainer={() => undefined}
      expandedIds={new Set()}
      onToggleExpand={() => {}}
      onUpdate={() => {}}
      updatePending={false}
    />,
  )
}

describe('UpdatesTable — a container policy is keyed on the container name (agent-os-ygyv)', () => {
  beforeEach(() => {
    setPolicy.mockReset()
    setPolicy.mockResolvedValue(policy('web'))
  })

  it('ygyv: switching auto-update on saves the container name, not its id', async () => {
    renderTable([])

    fireEvent.click(screen.getByRole('switch', { name: /^Auto-update container/ }))

    await waitFor(() => expect(setPolicy).toHaveBeenCalledTimes(1))
    expect(setPolicy).toHaveBeenCalledWith('container', 'web', { enabled: true })
  })

  it('ygyv: a policy saved under the name is found for the recreated container', () => {
    renderTable([policy('web')])

    expect(screen.getByRole('switch', { name: 'Auto-update container web' })).toBeChecked()
  })

  it('ygyv: a policy keyed on a container id does not match the container', () => {
    renderTable([policy(container.containerId)])

    expect(screen.getByRole('switch', { name: 'Auto-update container web' })).not.toBeChecked()
  })
})
