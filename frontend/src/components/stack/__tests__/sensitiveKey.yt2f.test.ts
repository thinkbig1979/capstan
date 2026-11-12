import { describe, it, expect } from 'vitest'
import { isSensitiveKey } from '../env-editor/sensitiveKey'

// agent-os-yt2f: the frontend had two copies of the secret-name rule and both
// differed from the server's IsSensitiveEnvKey. This is now the one copy, and
// it is tested against the same vector as the Go rule
// (TestSensitiveKey_Yt2f_SharedVector), so the two cannot drift apart. The
// vector has two copies because vitest's fs.allow keeps tests out of backend/:
// this one and backend/internal/services/testdata/sensitive_env_keys.json,
// which TestSensitiveKey_Yt2f_VectorCopiesMatch keeps byte-equal.
const files = import.meta.glob<string>('./sensitive_env_keys.json', {
  query: '?raw',
  import: 'default',
  eager: true,
})

describe('isSensitiveKey yt2f', () => {
  const raw = Object.values(files)
  const vector = JSON.parse(raw[0] ?? '{}') as { sensitive?: string[]; notSensitive?: string[] }

  it('yt2f reads the shared vector', () => {
    // A glob that matched nothing would make the cases below vacuous.
    expect(raw).toHaveLength(1)
    expect(vector.sensitive?.length).toBeGreaterThan(0)
    expect(vector.notSensitive?.length).toBeGreaterThan(0)
  })

  it('yt2f masks every key the server masks', () => {
    const missed = (vector.sensitive ?? []).filter((k) => !isSensitiveKey(k))
    expect(missed).toEqual([])
  })

  it('yt2f shows every key the server shows', () => {
    const masked = (vector.notSensitive ?? []).filter((k) => isSensitiveKey(k))
    expect(masked).toEqual([])
  })
})
