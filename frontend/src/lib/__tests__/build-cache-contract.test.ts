import { describe, it, expect } from 'vitest'
import type { BuildCacheEntry } from '@/types'

/**
 * The build-cache endpoint used to serialize Docker's build.CacheRecord
 * straight to the wire, which carried an upstream tag typo —
 * `json:" Parents,omitempty"`, with a leading space — so the field arrived as
 * " Parents" and `entry.Parents` was permanently undefined. It was optional,
 * so nothing threw; the data just never showed up (agent-os-iuby).
 *
 * RECORDED_PAYLOAD below is byte-for-byte the payload asserted by
 * backend/internal/handlers/build_cache_test.go (TestToBuildCacheEntries_WireShape
 * and TestToBuildCacheEntries_OmitsParentsWhenEmptyAndNullsLastUsed). If either
 * side changes the contract unilaterally, one of the two suites fails.
 */
const RECORDED_PAYLOAD = `{
  "entries": [
    {
      "id": "cache-1",
      "parents": ["p1", "p2"],
      "type": "regular",
      "description": "RUN go build",
      "inUse": true,
      "shared": false,
      "size": 4096,
      "createdAt": "2026-08-08T10:00:00Z",
      "lastUsedAt": "2026-08-08T11:30:00Z",
      "usageCount": 3
    },
    {
      "id": "cache-2",
      "type": "regular",
      "description": "",
      "inUse": false,
      "shared": false,
      "size": 0,
      "createdAt": "0001-01-01T00:00:00Z",
      "lastUsedAt": null,
      "usageCount": 0
    }
  ]
}`

describe('build-cache wire contract', () => {
  const { entries } = JSON.parse(RECORDED_PAYLOAD) as { entries: BuildCacheEntry[] }

  it('populates parents from the recorded backend response', () => {
    // The whole point of the bead: this was undefined in production.
    expect(entries[0].parents).toEqual(['p1', 'p2'])
  })

  it('reads every declared field off the recorded response', () => {
    const entry = entries[0]
    expect(entry.id).toBe('cache-1')
    expect(entry.type).toBe('regular')
    expect(entry.description).toBe('RUN go build')
    expect(entry.inUse).toBe(true)
    expect(entry.shared).toBe(false)
    expect(entry.size).toBe(4096)
    expect(entry.createdAt).toBe('2026-08-08T10:00:00Z')
    expect(entry.lastUsedAt).toBe('2026-08-08T11:30:00Z')
    expect(entry.usageCount).toBe(3)
  })

  it('tolerates an entry with no parents and a null lastUsedAt', () => {
    expect(entries[1].parents).toBeUndefined()
    expect(entries[1].lastUsedAt).toBeNull()
  })

  it('carries no PascalCase or space-prefixed key', () => {
    for (const entry of entries) {
      for (const key of Object.keys(entry)) {
        expect(key).not.toMatch(/^[\sA-Z]/)
      }
    }
  })
})
