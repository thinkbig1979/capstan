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

// agent-os-erfc. A failed READ of an existing .env left currentEnv as '' and
// the very next statement built the new file content from it — so a 500 on a
// file we could not read replaced that file with a single line, and the
// operator was told the extraction succeeded.
//
// The discrimination that was missing: `hasEnvFile: false` is the legitimate
// no-file answer and arrives as a 200 (agent-os-bt5y, env.go:105), while a read
// fault is a REJECTION (env.go:131). The first must still write; the second
// must not write at all.
//
// The two rejection fixtures are what the api.ts interceptor actually produces,
// not bare Errors. It rejects with a FLAT OBJECT: the response body spread, with
// the status injected (api.ts:170-171). For a read fault the body is the
// serialised AppError, `{code, message}` (models/errors.go:98-99, emitted via
// handleError at respond.go:177).

/** GET /stacks/:id/env answering 500 with the backend's own sanitised message. */
const READ_FAULT_WITH_BODY = {
  code: 'READ_ERROR',
  message: 'Failed to read env file',
  status: 500,
}

/**
 * The same fault with an EMPTY response body — a proxy 500, where
 * spreadableBody() contributes nothing and only the status survives. Kept as a
 * separate arm because it proves the abort is keyed on the REJECTION, not on a
 * cause being readable: classifyError's 5xx arm substitutes its own sentence
 * here, so a guard that only fired when a message existed would pass the arm
 * above and fail this one.
 */
const READ_FAULT_BODYLESS = { status: 500 }

/**
 * The DB says this stack HAS an env file and the file is not on disk
 * (env.go:122-130). This is the arm a future reader is most likely to doubt,
 * because a missing file LOOKS like the no-file case — and it is not: env.go:98-100
 * states that the DB and the filesystem disagreeing is something that went
 * wrong and must not be fused with the no-file state, which has its own 200
 * answer at env.go:105. So it aborts like any other rejection.
 */
const READ_FAULT_DISK_MISSING = {
  code: 'NOT_FOUND',
  message: 'Env file not found on disk',
  status: 404,
}

describe('useExtractToEnv — a failed .env READ must not reach the write', () => {
  it('makes NO write call and presents the backend cause when the env GET is rejected', async () => {
    stacksApi.getEnv.mockRejectedValue(READ_FAULT_WITH_BODY)
    // Both write paths are armed to SUCCEED, so "no error toast from a write"
    // is not an alternative explanation for anything asserted below.
    stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'written' })
    apiClient.put.mockResolvedValue({ data: {} })

    const { result, view } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    // BOTH write routes, because the sequential fallback is a second way to
    // reach the same file.
    expect(stacksApi.updateComposeAndEnv).toHaveBeenCalledTimes(0)
    expect(apiClient.put).toHaveBeenCalledTimes(0)
    expect(view.dispatch).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: '500: Failed to read env file',
    })
  })

  it('makes NO write call when the rejection carries no body at all', async () => {
    stacksApi.getEnv.mockRejectedValue(READ_FAULT_BODYLESS)
    stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'written' })
    apiClient.put.mockResolvedValue({ data: {} })

    const { result, view } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(stacksApi.updateComposeAndEnv).toHaveBeenCalledTimes(0)
    expect(apiClient.put).toHaveBeenCalledTimes(0)
    expect(view.dispatch).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: '500: Something went wrong on the server',
    })
  })

  // The other side, and the reason this is not simply "abort on anything that
  // is not a populated file". These two arms pass BEFORE the fix and must keep
  // passing after it: they are the controls that stop the abort from eating the
  // legitimate paths, so their evidence is a mutation of the FIXED hook, not a
  // failure against the broken one.
  it('still writes the single new line when the stack has no env file (a 200)', async () => {
    stacksApi.getEnv.mockResolvedValue({ hasEnvFile: false })
    stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'written' })

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(stacksApi.updateComposeAndEnv).toHaveBeenCalledTimes(1)
    expect(stacksApi.updateComposeAndEnv).toHaveBeenCalledWith(
      'stack-1',
      expect.any(String),
      'WEB_IMAGE=nginx:1.2.3',
    )
    expect(toast.error).not.toHaveBeenCalled()
    expect(toast.success).toHaveBeenCalledWith('Extracted WEB_IMAGE to .env')
  })

  it('makes NO write call when the env file is recorded but missing from disk (404)', async () => {
    stacksApi.getEnv.mockRejectedValue(READ_FAULT_DISK_MISSING)
    stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'written' })
    apiClient.put.mockResolvedValue({ data: {} })

    const { result, view } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(stacksApi.updateComposeAndEnv).toHaveBeenCalledTimes(0)
    expect(apiClient.put).toHaveBeenCalledTimes(0)
    expect(view.dispatch).not.toHaveBeenCalled()
    expect(toast.success).not.toHaveBeenCalled()
    expect(toast.error).toHaveBeenCalledWith('Failed to extract variable to .env', {
      description: 'Env file not found on disk',
    })
  })

  it('appends to the existing file when the env file was read successfully', async () => {
    // The happy-path regression control: the whole point of reading the file
    // first is that its contents survive the write.
    stacksApi.getEnv.mockResolvedValue({ hasEnvFile: true, raw: 'FOO=bar\nBAZ=qux\n' })
    stacksApi.updateComposeAndEnv.mockResolvedValue({ outcome: 'success', reason: 'written' })

    const { result } = setup()
    await act(async () => {
      await result.current.confirmExtract()
    })

    expect(stacksApi.updateComposeAndEnv).toHaveBeenCalledWith(
      'stack-1',
      expect.any(String),
      'FOO=bar\nBAZ=qux\nWEB_IMAGE=nginx:1.2.3',
    )
    expect(toast.error).not.toHaveBeenCalled()
  })
})
