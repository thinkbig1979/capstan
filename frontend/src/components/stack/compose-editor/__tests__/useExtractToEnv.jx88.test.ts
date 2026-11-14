import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

// agent-os-jx88: Extract to .env wrote `${varName}=${selectedText}` unquoted,
// so a selection holding " #" lost its tail to a comment, "$" was
// interpolated, and whitespace was trimmed: the variable compose read was
// not the text that was selected. Such a value is now single-quoted (compose
// reads a single-quoted value literally); one that also holds a single quote
// is refused, with nothing written.

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

import { toast } from 'sonner'
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
  it.each([
    ['a value with a space', 'two words', "MY_VAR='two words'"],
    ['a value with $', 'pa$word', "MY_VAR='pa$word'"],
    ['a value with " #"', 'abc #def', "MY_VAR='abc #def'"],
    ['a value with #', 'x#y', "MY_VAR='x#y'"],
    ['a value with a double quote', 'say "hi"', `MY_VAR='say "hi"'`],
    ['a value with a backslash', 'C:\\dir', "MY_VAR='C:\\dir'"],
  ])('jx88: %s is single-quoted', async (_name, selected, line) => {
    expect(await extractedEnv(selected)).toBe(`A=1\n${line}`)
  })

  it('jx88: a plain value stays unquoted', async () => {
    expect(await extractedEnv('nginx:1.2.3')).toBe('A=1\nMY_VAR=nginx:1.2.3')
  })

  it("jx88: a value with a single quote and nothing to quote stays unquoted", async () => {
    expect(await extractedEnv("it's")).toBe("A=1\nMY_VAR=it's")
  })

  it.each([
    ['a quote and a space', "it's here"],
    ['a leading quote', "'x"],
  ])('jx88: %s is refused, nothing written', async (_name, selected) => {
    expect(await extractedEnv(selected)).toBeUndefined()
    expect(stacksApi.updateComposeAndEnv).not.toHaveBeenCalled()
    const shown = [
      ...vi.mocked(toast.error).mock.calls,
      ...vi.mocked(toast.warning).mock.calls,
    ].flat().map((x) => JSON.stringify(x)).join(' ')
    expect(shown).toMatch(/single quote/i)
  })

  it('jx88: a value to quote that ends in a backslash is refused, nothing written', async () => {
    expect(await extractedEnv('C:\\my dir\\')).toBeUndefined()
    expect(stacksApi.updateComposeAndEnv).not.toHaveBeenCalled()
  })
})
