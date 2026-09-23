import { describe, it, expect, vi } from 'vitest'
import type { GitCommit } from '@/types'

// The git-diff endpoint (handlers/git.go GetDiff) sends
// { commit: GitCommit, diff: string, files: string[] }. api.ts used to declare
// { commit: string; diff: string }: the wrong primitive for commit and no
// files at all, and nothing noticed because nothing read either (agent-os-kmpi).
//
// The reads below are the detector. Against the old declaration `commit.hash`
// is TS2339 on a string and `files` is TS2339 on the object, so `tsc -b` goes
// red; the runtime assertions only prove the value passes through unchanged.
// RECORDED_PAYLOAD matches what backend/internal/services
// TestGetDiff_CommitIsAlwaysOnTheWire and TestWireNullability_DiffResultFiles
// assert the Go side marshals.
const RECORDED_PAYLOAD = {
  commit: {
    hash: '0123456789abcdef0123456789abcdef01234567',
    short: '0123456',
    author: 'Test',
    email: 'test@test.invalid',
    message: 'add a',
    date: '2026-09-23T12:00:00+02:00',
  },
  diff: 'diff --git a/a.txt b/a.txt\n',
  files: ['a.txt'],
}

const instance = vi.hoisted(() => ({
  get: vi.fn(),
  interceptors: {
    request: { use: vi.fn() },
    response: { use: vi.fn() },
  },
}))

vi.mock('axios', () => ({
  default: { create: () => instance },
  AxiosError: class AxiosError extends Error {},
}))

import { gitApi } from '@/lib/api'

describe('gitApi.diff response type (agent-os-kmpi)', () => {
  it('declares commit as a GitCommit object and carries files', async () => {
    instance.get.mockResolvedValueOnce({ data: RECORDED_PAYLOAD })

    const result = await gitApi.diff('stack-1', RECORDED_PAYLOAD.commit.hash)

    const commit: GitCommit = result.commit
    const files: string[] = result.files
    expect(commit.hash).toBe(RECORDED_PAYLOAD.commit.hash)
    expect(files).toEqual(['a.txt'])
    expect(result.diff).toBe(RECORDED_PAYLOAD.diff)
  })
})
