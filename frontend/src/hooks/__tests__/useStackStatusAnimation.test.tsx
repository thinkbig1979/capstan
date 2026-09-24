import { describe, it, expect } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useStackStatusAnimation } from '../useStackStatusAnimation'
import { queryKeys } from '@/lib/query-keys'
import type { Stack } from '@/types'

/**
 * agent-os-eqif: a stack flagged statusStale carries its last STORED status,
 * not a live one. A stored status that differs from the last live one is not a
 * status change, so it must not pulse.
 */

const stack = (overrides: Partial<Stack> = {}): Stack => ({
  envFile: '',
  gitBranch: '',
  gitCommit: '',
  id: 's1',
  directory: '/srv/stacks/web',
  composeFile: 'docker-compose.yaml',
  projectName: 'web',
  status: 'running',
  isGitRepo: false,
  gitDirty: false,
  gitAhead: 0,
  gitBehind: 0,
  containers: [],
  ...overrides,
})

function setup() {
  const client = new QueryClient()
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  const { result } = renderHook(() => useStackStatusAnimation(), { wrapper })
  const set = (stacks: Stack[]) => act(() => { client.setQueryData(queryKeys.stacks(), stacks) })
  return { result, set }
}

describe('useStackStatusAnimation', () => {
  // Control: a live change pulses.
  it('animates a live status change', () => {
    const { result, set } = setup()
    set([stack({ status: 'running' })])
    set([stack({ status: 'stopped' })])

    expect(result.current.isAnimating('s1')).toBe(true)
  })

  it('does not animate when the new status is a stale stored one', () => {
    const { result, set } = setup()
    set([stack({ status: 'running' })])
    set([stack({ status: 'stopped', statusStale: true })])

    expect(result.current.isAnimating('s1')).toBe(false)
  })

  it('compares the next live status with the last live one, not the stale one', () => {
    const { result, set } = setup()
    set([stack({ status: 'running' })])
    set([stack({ status: 'stopped', statusStale: true })])
    set([stack({ status: 'running' })])

    expect(result.current.isAnimating('s1')).toBe(false)
  })
})
