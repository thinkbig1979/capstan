import { useCallback } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { toast } from 'sonner'
import { toastInvalid } from '@/lib/error-handler'
import { convertDockerRun, isDockerRunCommand } from '@/lib/docker-run-parser'

interface UseDockerRunConversionArgs {
  dockerRunInput: string
  setConversionError: Dispatch<SetStateAction<string>>
  setPendingCompose: Dispatch<SetStateAction<string | null>>
  setComposeTab: Dispatch<SetStateAction<'editor' | 'docker-run'>>
}

export function useDockerRunConversion({
  dockerRunInput,
  setConversionError,
  setPendingCompose,
  setComposeTab,
}: UseDockerRunConversionArgs) {
  const handleConvertDockerRun = useCallback(() => {
    const trimmed = dockerRunInput.trim()
    if (!trimmed) {
      toastInvalid('Please paste a docker run command')
      return
    }

    if (!isDockerRunCommand(trimmed)) {
      toastInvalid('Input does not appear to be a docker run command')
      return
    }

    try {
      const compose = convertDockerRun(trimmed)
      if (!compose) {
        toastInvalid('Could not parse the docker run command')
        return
      }

      setPendingCompose(compose)
      setConversionError('')
      toast.success('Docker run command converted to Compose')
      setComposeTab('editor')
    } catch {
      setConversionError('Failed to parse the docker run command. Check the syntax and try again.')
      toastInvalid('Failed to parse the docker run command')
    }
  }, [dockerRunInput, setConversionError, setPendingCompose, setComposeTab])

  return { handleConvertDockerRun }
}
