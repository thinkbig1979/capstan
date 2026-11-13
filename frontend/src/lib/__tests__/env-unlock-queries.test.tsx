/**
 * agent-os-4xsq: when the env unlock window closes, an env query nobody is
 * watching must leave the cache. Invalidating it only marks it stale and keeps
 * the unlocked payload, whole .env included, until gcTime; a stack opened inside
 * that window would render the plaintext first. A mounted query is invalidated,
 * not removed, so its editor keeps its previous data and its unsaved edits.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { QueryClient, QueryObserver } from '@tanstack/react-query'
import { renderHook, act } from '@testing-library/react'
import { invalidateEnvUnlockQueries } from '../env-unlock-queries'
import { useEnvUnlockCacheSync } from '@/hooks/useEnvUnlockCacheSync'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { queryClient as appClient } from '../query-client'
import { queryKeys } from '../query-keys'

const unlocked = { raw: 'API_KEY=real-secret\n' }
const redacted = { locked: true }

// Wait for the first fetch to land: a refetch while one is in flight joins it
// instead of calling queryFn again, which would hide whether the refetch ran.
const settled = (observer: { getCurrentResult: () => { status: string } }) =>
  vi.waitFor(() => expect(observer.getCurrentResult().status).toBe('success'))

const present = (client: QueryClient, key: readonly unknown[]) =>
  client.getQueryCache().find({ queryKey: key, exact: true }) !== undefined

describe('invalidateEnvUnlockQueries (agent-os-4xsq)', () => {
  let client: QueryClient
  const unsubscribers: Array<() => void> = []

  beforeEach(() => {
    client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } })
  })

  afterEach(() => {
    unsubscribers.splice(0).forEach((u) => u())
    client.clear()
  })

  it('4xsq: unlock, view stack A, go to B, lock: A\'s env query is absent from the cache', async () => {
    client.setQueryData(queryKeys.stack.env('A'), unlocked)
    const queryFn = vi.fn().mockResolvedValue(redacted)
    const observer = new QueryObserver(client, { queryKey: queryKeys.stack.env('B'), queryFn })
    unsubscribers.push(observer.subscribe(() => {}))
    await settled(observer)

    await invalidateEnvUnlockQueries(client)

    expect(present(client, queryKeys.stack.env('A'))).toBe(false)
    // The mounted one stays, and was refetched for the redacted payload.
    expect(present(client, queryKeys.stack.env('B'))).toBe(true)
    expect(queryFn).toHaveBeenCalledTimes(2)
    expect(client.getQueryData(queryKeys.stack.env('B'))).toEqual(redacted)
  })

  it('4xsq: an unmounted global env query is removed too, and an unrelated query is left alone', async () => {
    client.setQueryData(queryKeys.settings.globalEnv(), { vars: [{ key: 'API_KEY', value: 'real-secret' }] })
    // The unrelated query was the compose file until agent-os-w6wx put it in
    // the set; the stack row holds no secret.
    client.setQueryData(queryKeys.stack.detail('A'), { id: 'A' })

    await invalidateEnvUnlockQueries(client)

    expect(present(client, queryKeys.settings.globalEnv())).toBe(false)
    expect(present(client, queryKeys.stack.detail('A'))).toBe(true)
  })

  it('w6wx: compose and git diff queries are masked while locked, so they are in the set', async () => {
    client.setQueryData(queryKeys.stack.compose('A'), { text: 'PASSWORD: real', masked: false })
    client.setQueryData(queryKeys.git.diff('A', 'abc'), { diff: '+PASSWORD: real' })
    client.setQueryData(['git', 'A', 'changes', 'diff', 'compose.yaml'], { diff: '+PASSWORD: real' })
    const queryFn = vi.fn().mockResolvedValue({ text: 'PASSWORD: ***', masked: true })
    const observer = new QueryObserver(client, { queryKey: queryKeys.stack.compose('B'), queryFn })
    unsubscribers.push(observer.subscribe(() => {}))
    await settled(observer)

    await invalidateEnvUnlockQueries(client)

    expect(present(client, queryKeys.stack.compose('A'))).toBe(false)
    expect(present(client, queryKeys.git.diff('A', 'abc'))).toBe(false)
    expect(present(client, ['git', 'A', 'changes', 'diff', 'compose.yaml'])).toBe(false)
    // The mounted compose query stays and is refetched.
    expect(present(client, queryKeys.stack.compose('B'))).toBe(true)
    expect(queryFn).toHaveBeenCalledTimes(2)
  })

  it('4xsq: the mounted global env query is invalidated and refetched, not removed', async () => {
    const queryFn = vi.fn().mockResolvedValue(redacted)
    const observer = new QueryObserver(client, { queryKey: queryKeys.settings.globalEnv(), queryFn })
    unsubscribers.push(observer.subscribe(() => {}))
    await settled(observer)

    await invalidateEnvUnlockQueries(client)

    expect(present(client, queryKeys.settings.globalEnv())).toBe(true)
    expect(queryFn).toHaveBeenCalledTimes(2)
  })
})

describe('useEnvUnlockCacheSync on lock (agent-os-4xsq)', () => {
  beforeEach(() => {
    appClient.clear()
    act(() => useEnvUnlockStore.getState().lock())
  })

  afterEach(() => {
    act(() => useEnvUnlockStore.getState().lock())
    appClient.clear()
  })

  it('4xsq: locking after an unlock drops an unmounted stack\'s plaintext from the app query cache', () => {
    renderHook(() => useEnvUnlockCacheSync())
    act(() => useEnvUnlockStore.getState().unlock('token'))
    appClient.setQueryData(queryKeys.stack.env('A'), unlocked)

    act(() => useEnvUnlockStore.getState().lock())

    expect(present(appClient, queryKeys.stack.env('A'))).toBe(false)
  })
})
