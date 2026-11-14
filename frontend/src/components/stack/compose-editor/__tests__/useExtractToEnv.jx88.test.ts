import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

// agent-os-jx88: Extract to .env wrote `${varName}=${selectedText}` unquoted,
// so a selection holding " #" lost its tail to a comment and whitespace was
// trimmed. The selection sits in the compose YAML, where compose already
// substitutes $VAR, ${VAR} and $$, and dotenv does the same in an unquoted or
// double-quoted value, so "$" is left as it is. A value holding whitespace,
// #, ", ' or \ is double-quoted with " and \ escaped. The table is shared with
// the backend test that checks each line with compose-go itself
// (settings_globalenv_jx88_test.go).

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), info: vi.fn(), warning: vi.fn(), error: vi.fn() },
}))

const stacksApi = vi.hoisted(() => ({
  getEnv: vi.fn(),
  updateComposeAndEnv: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ stacksApi }))

vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}))

import cases from './extractToEnvCases.json'
import { useExtractToEnv } from '../useExtractToEnv'

function setup(selected: string) {
  const compose = `services:\n  web:\n    environment:\n      X: ${selected}\n`
  const from = compose.indexOf(selected)
  const view = {
    state: {
      doc: { toString: () => compose },
      selection: { main: { from, to: from + selected.length } },
    },
    dispatch: vi.fn(),
  }
  const { result } = renderHook(() =>
    useExtractToEnv({
      stackId: 'stack-1',
      viewRef: { current: view } as never,
      selectedText: selected,
      setSelectedText: vi.fn(),
      setContent: vi.fn(),
      setLastSaved: vi.fn(),
    }),
  )
  act(() => {
    result.current.setExtractVarName('MY_VAR')
  })
  return { result }
}

async function extractedEnv(selected: string): Promise<string | undefined> {
  const { result } = setup(selected)
  await act(async () => {
    await result.current.confirmExtract()
  })
  const call = stacksApi.updateComposeAndEnv.mock.calls[0]
  return call?.[2] as string | undefined
}

beforeEach(() => {
  vi.clearAllMocks()
  stacksApi.getEnv.mockResolvedValue({ hasEnvFile: true, raw: 'A=1\n' })
  stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success' })
})

describe('useExtractToEnv value quoting (jx88)', () => {
  it.each(cases.cases.map((c) => [c.selected, c.line]))(
    'jx88: selection %j is written as %j',
    async (selected, line) => {
      expect(await extractedEnv(selected)).toBe(`A=1\n${line}`)
    },
  )
})

describe('useExtractToEnv empty selection (jx88 review F3)', () => {
  it('jx88: an empty selection writes nothing', async () => {
    const { result } = setup('')
    await act(async () => {
      await result.current.confirmExtract()
    })
    expect(stacksApi.getEnv).not.toHaveBeenCalled()
    expect(stacksApi.updateComposeAndEnv).not.toHaveBeenCalled()
  })
})
