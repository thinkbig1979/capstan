import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

// agent-os-yre8. confirmExtract() covered every failure of a multi-step write
// with ONE fixed sentence, and its outer catch took no binding at all — so the
// cause was not merely discarded, it was never observed.
//
// Two write paths reach that catch and they fail DIFFERENTLY:
//
//   ATOMIC       PUT /stacks/:id/compose-env, handlers/compose.go
//                PutComposeAndEnv, which answers a truth.ActionResult whose
//                reason names WHICH write failed and whether a rollback
//                happened: "failed to write env file; compose unchanged" vs
//                "failed to write compose file; env rolled back".
//   SEQUENTIAL   two bare apiClient.put calls, /env then /compose. If env
//                succeeds and compose then fails, the variable exists in .env
//                while the compose file still holds the literal — a real
//                half-applied state that was behind the same one sentence.
//
// These arms assert the DESCRIPTION, because the fixed title is not the defect:
// the title is the only place the ACTION lives and it is kept deliberately.

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), info: vi.fn(), warning: vi.fn(), error: vi.fn() },
}))

const stacksApi = vi.hoisted(() => ({
  getEnv: vi.fn(),
  updateComposeAndEnv: vi.fn(),
}))
const apiClient = vi.hoisted(() => ({ put: vi.fn() }))

vi.mock('@/lib/api', () => ({ stacksApi, apiClient }))

const invalidateQueries = vi.hoisted(() => vi.fn())
vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries }),
}))

import { toast } from 'sonner'
import { useExtractToEnv } from '../useExtractToEnv'

const COMPOSE = 'services:\n  web:\n    image: nginx:1.2.3\n'
// The selection is `nginx:1.2.3`, i.e. the image tag literal.
const SEL_FROM = COMPOSE.indexOf('nginx:1.2.3')
const SEL_TO = SEL_FROM + 'nginx:1.2.3'.length

function makeView() {
  return {
    state: {
      doc: { toString: () => COMPOSE },
      selection: { main: { from: SEL_FROM, to: SEL_TO } },
    },
    dispatch: vi.fn(),
  }
}

function setup() {
  const view = makeView()
  const args = {
    stackId: 'stack-1',
    viewRef: { current: view } as never,
    selectedText: 'nginx:1.2.3',
    setSelectedText: vi.fn(),
    setContent: vi.fn(),
    setLastSaved: vi.fn(),
  }
  const { result } = renderHook(() => useExtractToEnv(args))
  act(() => {
    result.current.setExtractVarName('WEB_IMAGE')
  })
  return { result, view }
}

/** The rejection an axios call makes when the backend answers 5xx. */
function rejectedActionResult(reason: string, status = 500) {
  return { outcome: 'failed', reason, status }
}

beforeEach(() => {
  vi.clearAllMocks()
  stacksApi.getEnv.mockResolvedValue({ hasEnvFile: false, raw: '' })
})

describe('useExtractToEnv — the atomic compose-env write', () => {
  it('carries the ActionResult reason in the toast DESCRIPTION when the write is rejected', async () => {
    stacksApi.updateComposeAndEnv.mockRejectedValue(
      rejectedActionResult('failed to write compose file; env rolled back'),
    )

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'failed to write compose file; env rolled back',
    })
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('distinguishes the rollback outcomes rather than covering both with one sentence', async () => {
    // The whole point of the reason: "was my env file left modified?" is
    // exactly what these two strings answer and the fixed sentence did not.
    stacksApi.updateComposeAndEnv.mockRejectedValue(
      rejectedActionResult('failed to write env file; compose unchanged'),
    )

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'failed to write env file; compose unchanged',
    })
  })

  it('still shows the classified cause when the rejection is not an ActionResult', async () => {
    stacksApi.updateComposeAndEnv.mockRejectedValue({
      status: 403,
      message: 'Re-enter your password to edit environment files',
    })

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'Re-enter your password to edit environment files',
    })
  })

  it('keeps the 404 fall-through to the sequential path, and does not toast on it', async () => {
    // Two-sided: the guard that routes a 404 to the fallback must NOT be
    // reclassified as a failure by the cause-rendering change.
    stacksApi.updateComposeAndEnv.mockRejectedValue({ status: 404 })
    apiClient.put.mockResolvedValue({ data: {} })

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(toast.error).not.toHaveBeenCalled()
    expect(apiClient.put).toHaveBeenCalledTimes(2)
    expect(toast.success).toHaveBeenCalledWith('Extracted WEB_IMAGE to .env')
  })
})

describe('useExtractToEnv — the sequential fallback', () => {
  it('carries the env-write reason in the DESCRIPTION when the env PUT fails', async () => {
    stacksApi.updateComposeAndEnv.mockRejectedValue({ status: 404 })
    apiClient.put.mockRejectedValueOnce(
      rejectedActionResult('env file created but DB not updated'),
    )

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'env file created but DB not updated',
    })
    expect(toast.error).toHaveBeenCalledTimes(1)
  })

  it('names the half-applied state when env succeeds and compose then fails', async () => {
    // The state the one fixed sentence hid: the variable is now in .env while
    // the compose file still holds the literal.
    stacksApi.updateComposeAndEnv.mockRejectedValue({ status: 404 })
    apiClient.put
      .mockResolvedValueOnce({ data: {} })
      .mockRejectedValueOnce(rejectedActionResult('failed to write compose file'))

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(apiClient.put).toHaveBeenCalledTimes(2)
    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'failed to write compose file',
    })
  })
})

describe('useExtractToEnv — the success control', () => {
  // Without this arm every assertion above would also pass against a hook that
  // failed unconditionally.
  it('writes atomically and reports success', async () => {
    stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'written' })

    const { result, view } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(toast.error).not.toHaveBeenCalled()
    expect(apiClient.put).not.toHaveBeenCalled()
    expect(view.dispatch).toHaveBeenCalled()
    expect(toast.success).toHaveBeenCalledWith('Extracted WEB_IMAGE to .env')
  })
})
