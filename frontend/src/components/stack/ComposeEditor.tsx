import { useEffect, useRef, useState, useCallback } from 'react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Save, FileCheck, AlertCircle, Variable } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient, stacksApi } from '@/lib/api'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'
import { isActionResult } from '@/lib/action-result'
import type { LintResult } from '@/types'
import { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { LintResultsPanel } from '@/components/stack/LintResultsPanel'

interface ComposeEditorProps {
  stackId: string
}

export function ComposeEditor({ stackId }: ComposeEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const handleSaveRef = useRef<(forceSave?: boolean) => void>(() => {})
  const [content, setContent] = useState('')
  const [lastSaved, setLastSaved] = useState('')
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [showSaveConfirm, setShowSaveConfirm] = useState(false)
  const [isLintingBeforeSave, setIsLintingBeforeSave] = useState(false)

  const queryClient = useQueryClient()

  const { isLoading, data } = useQuery({
    queryKey: ['stack', stackId, 'compose'],
    queryFn: async () => {
      const response = await apiClient.get(`/stacks/${stackId}/compose`)
      return (response.data as { content: string }).content
    },
  })

  const [selectedText, setSelectedText] = useState('')
  const [extractVarName, setExtractVarName] = useState('')
  const [showExtractDialog, setShowExtractDialog] = useState(false)
  const [isExtracting, setIsExtracting] = useState(false)
  const selectedTextRef = useRef('')

  const { viewRef } = useCodeMirrorEditor(editorRef, {
    doc: data || content,
    onSave: () => {
      handleSaveRef.current()
      return true
    },
    onSelect: (text) => {
      selectedTextRef.current = text
      setSelectedText(text)
    },
    deps: [stackId],
  })

  useEffect(() => {
    if (data) {
      setContent(data)
      setLastSaved(data)
    }
  }, [data])

  const saveMutation = useMutation({
    mutationFn: async (content: string) => {
      const response = await apiClient.put(`/stacks/${stackId}/compose`, { content })
      return response.data
    },
    onSuccess: (data, variables) => {
      setLastSaved(variables)
      setLintResults(data.lintResults || [])
      toast.success('Compose file saved successfully')
      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toast.error('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      }
      queryClient.invalidateQueries({ queryKey: ['stack', stackId] })
    },
    onError: (error: { response?: { data?: { lintResults?: LintResult[] } } }) => {
      const appError = classifyError(error)
      if (error.response?.data?.lintResults) {
        setLintResults(error.response.data.lintResults)
        toast.error('Lint errors detected')
      } else {
        toast.error(appError.message)
      }
    },
  })

  const handleSave = useCallback(
    async (forceSave = false) => {
      if (!viewRef.current) return
      const currentContent = viewRef.current.state.doc.toString()

      if (forceSave) {
        saveMutation.mutate(currentContent)
        setShowSaveConfirm(false)
        return
      }

      setIsLintingBeforeSave(true)
      try {
        const response = await apiClient.post(`/stacks/${stackId}/compose/lint`, { content: currentContent })
        const results = response.data.lintResults || []
        setLintResults(results)

        if (results.some((r: LintResult) => r.level === 'error')) {
          setShowSaveConfirm(true)
        } else {
          saveMutation.mutate(currentContent)
        }
      } catch {
        saveMutation.mutate(currentContent)
      } finally {
        setIsLintingBeforeSave(false)
      }
    },
    [saveMutation, stackId],
  )
  handleSaveRef.current = handleSave

  const lintMutation = useMutation({
    mutationFn: async (content: string) => {
      const response = await apiClient.post(`/stacks/${stackId}/compose/lint`, { content })
      return response.data
    },
    onSuccess: (data) => {
      setLintResults(data.lintResults || [])
      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toast.error('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      } else {
        toast.success('No lint issues found')
      }
      queryClient.invalidateQueries({ queryKey: ['stack', stackId, 'compose'] })
    },
    onError: () => {
      toast.error('Failed to lint compose file')
    },
  })

  const handleLint = useCallback(() => {
    if (!viewRef.current) return
    const currentContent = viewRef.current.state.doc.toString()
    lintMutation.mutate(currentContent)
  }, [lintMutation])

  const hasUnsavedChanges = content !== lastSaved
  const errorCount = lintResults.filter((r) => r.level === 'error').length

  const inferVarName = (yamlContent: string, cursorPos: number): string => {
    const lines = yamlContent.split('\n')
    let charIdx = 0
    let currentLineIdx = 0

    for (let i = 0; i < lines.length; i++) {
      const lineEnd = charIdx + lines[i].length
      if (charIdx <= cursorPos && cursorPos <= lineEnd) {
        currentLineIdx = i
        break
      }
      charIdx = lineEnd + 1
    }

    const currentLine = lines[currentLineIdx]
    const colonMatch = currentLine.match(/^\s*([a-zA-Z0-9_-]+)\s*:/)
    if (colonMatch) {
      const key = colonMatch[1].replace(/-/g, '_').toUpperCase()
      return key
    }

    const listMatch = currentLine.match(/^\s*-\s*([A-Za-z0-9_]+)\s*=/)
    if (listMatch) {
      return listMatch[1].replace(/-/g, '_').toUpperCase()
    }

    for (let i = currentLineIdx - 1; i >= Math.max(0, currentLineIdx - 5); i--) {
      const line = lines[i]
      const keyMatch = line.match(/^\s*([a-zA-Z0-9_-]+)\s*:/)
      if (keyMatch && !keyMatch[1].match(/^(services|environment|ports|volumes|networks|depends_on|deploy|build|image|restart|container_name|hostname|labels|command|entrypoint|env_file|extra_hosts|healthcheck|logging|cap_add|cap_drop|devices|dns|tmpfs|ulimits|security_opt|shm_size|stdin_open|tty|user|working_dir|domainname|mac_address|privileged|read_only|pid|cgroup_parent|network_mode|stop_signal|stop_grace_period|isolation|configs|secrets|links|external_links|sysctls|named|anonymous)$/i)) {
        return keyMatch[1].replace(/-/g, '_').toUpperCase()
      }
    }

    return 'ENV_VAR'
  }

  const handleExtractToEnv = useCallback(() => {
    if (!viewRef.current || !selectedText) return
    const view = viewRef.current
    const sel = view.state.selection.main
    const inferred = inferVarName(view.state.doc.toString(), sel.from)
    setExtractVarName(inferred)
    setShowExtractDialog(true)
  }, [selectedText])

  /**
   * Atomic extract-to-env (audit finding #11).
   *
   * Preferred path: PUT /stacks/:id/compose-env writes compose + env in one
   * transaction — no partial-write window where compose references a missing var.
   *
   * Fallback path: if the atomic endpoint returns 404 (backend not yet migrated),
   * we fall back to the env-first sequential write. Writing env first means the
   * compose reference is only persisted after the var exists in .env.
   */
  const confirmExtract = useCallback(async () => {
    if (!viewRef.current || !selectedText || !extractVarName.trim()) return

    const view = viewRef.current
    const sel = view.state.selection.main
    const varName = extractVarName.trim().toUpperCase().replace(/[^A-Z0-9_]/g, '_')

    setIsExtracting(true)
    try {
      const currentCompose = view.state.doc.toString()
      const before = currentCompose.slice(0, sel.from)
      const after = currentCompose.slice(sel.to)
      const updatedCompose = before + `\${${varName}}` + after

      // Build the updated .env content
      let currentEnv = ''
      try {
        const envData = await stacksApi.getEnv(stackId)
        if (envData?.raw) {
          currentEnv = envData.raw
        }
      } catch {
        // No .env file yet — the atomic endpoint will create it.
      }

      const newEnvLine = `${varName}=${selectedText}`
      const updatedEnv = currentEnv ? `${currentEnv.trimEnd()}\n${newEnvLine}` : newEnvLine

      // Attempt atomic write — body: { composeContent, envRaw } per ComposeEnvRequest
      let atomicSuccess = false
      try {
        const result = await stacksApi.updateComposeAndEnv(stackId, updatedCompose, updatedEnv)
        if (isActionResult(result)) {
          if (result.outcome === 'success' || result.outcome === 'no_change') {
            atomicSuccess = true
          } else {
            toast.error(result.reason || 'Failed to extract variable to .env')
            return
          }
        } else {
          // The endpoint doesn't exist yet (pre-B4 backend) — fall through to sequential.
          atomicSuccess = false
        }
      } catch (e: unknown) {
        const err = e as { status?: number; response?: { status?: number } }
        const status = err.status ?? err.response?.status
        if (status === 404) {
          // Backend not yet migrated; use env-first sequential fallback.
          atomicSuccess = false
        } else {
          toast.error('Failed to extract variable to .env')
          return
        }
      }

      if (!atomicSuccess) {
        // Sequential fallback — write env FIRST so the compose reference is
        // never persisted without the variable being available.
        await apiClient.put(`/stacks/${stackId}/env`, { raw: updatedEnv })
        await apiClient.put(`/stacks/${stackId}/compose`, { content: updatedCompose })
      }

      // Update editor state
      view.dispatch({
        changes: { from: sel.from, to: sel.to, insert: `\${${varName}}` },
      })

      setContent(updatedCompose)
      setLastSaved(updatedCompose)
      queryClient.invalidateQueries({ queryKey: ['stack', stackId] })
      toast.success(`Extracted ${varName} to .env`)
      setShowExtractDialog(false)
      setSelectedText('')
    } catch {
      toast.error('Failed to extract variable to .env')
    } finally {
      setIsExtracting(false)
    }
  }, [selectedText, extractVarName, stackId, queryClient])

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 flex-wrap">
          <Button
            onClick={() => handleSave()}
            disabled={isLoading || saveMutation.isPending || !hasUnsavedChanges || isLintingBeforeSave}
          >
            <Save className="mr-2 h-4 w-4" />
            {isLintingBeforeSave ? 'Validating...' : saveMutation.isPending ? 'Saving...' : 'Save'}
          </Button>
          <Button variant="outline" onClick={handleLint} disabled={isLoading || lintMutation.isPending}>
            <FileCheck className="mr-2 h-4 w-4" />
            {lintMutation.isPending ? 'Linting...' : 'Lint'}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={handleExtractToEnv}
            disabled={!selectedText || isLoading || saveMutation.isPending}
            title={selectedText ? 'Extract selected value to .env file' : 'Select a value in the editor to extract'}
          >
            <Variable className="mr-2 h-4 w-4" />
            Extract to .env
          </Button>
          {hasUnsavedChanges && (
            <Badge variant="secondary" className="text-xs">
              {errorCount > 0 ? `Unsaved changes (${errorCount} errors)` : 'Unsaved changes'}
            </Badge>
          )}
        </div>
        <div className="text-sm text-muted-foreground hidden sm:block">
          Ctrl+S to save
        </div>
      </div>

      <div className="relative">
        {isLoading && (
          <div className="absolute inset-0 z-10 flex items-center justify-center bg-background/80 rounded-md border">
            <div className="flex items-center gap-2">
              <LoadingSpinner size="default" />
              <span className="text-muted-foreground">Loading compose file...</span>
            </div>
          </div>
        )}
        <div ref={editorRef} className="rounded-md border overflow-hidden" style={{ minHeight: '400px' }} />
      </div>

      <LintResultsPanel results={lintResults} />

      <Dialog open={showExtractDialog} onOpenChange={setShowExtractDialog}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Variable className="h-5 w-5" />
              Extract to .env
            </DialogTitle>
            <DialogDescription>
              Replace the selected value with a variable reference and add it to the .env file.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <div className="text-sm text-muted-foreground">Selected value:</div>
              <code className="block rounded-md bg-muted p-2 text-sm font-mono break-all">
                {selectedText.length > 100 ? `${selectedText.slice(0, 100)}...` : selectedText}
              </code>
            </div>
            <div className="space-y-2">
              <Label htmlFor="extract-var-name">Variable name</Label>
              <Input
                id="extract-var-name"
                value={extractVarName}
                onChange={(e) => setExtractVarName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'))}
                placeholder="VARIABLE_NAME"
                className="font-mono"
              />
            </div>
            <div className="text-sm text-muted-foreground">
              Will become: <code className="font-mono">${'{'}{extractVarName || 'VARIABLE_NAME'}{'}'}</code> in compose, <code className="font-mono">{extractVarName || 'VARIABLE_NAME'}={selectedText}</code> in .env
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setShowExtractDialog(false)}>
              Cancel
            </Button>
            <Button onClick={confirmExtract} disabled={!extractVarName.trim() || isExtracting}>
              {isExtracting ? 'Extracting...' : 'Extract'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={showSaveConfirm} onOpenChange={setShowSaveConfirm}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertCircle className="h-5 w-5 text-warning" />
              Save with Lint Errors?
            </DialogTitle>
            <DialogDescription>
              Your compose file has <span className="font-semibold text-destructive">{errorCount} error(s)</span>.
              Would you like to save anyway or fix the errors first?
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-60 overflow-y-auto rounded-md border bg-muted/50 p-3">
            <div className="space-y-2">
              <div className="text-sm font-medium">Errors found:</div>
              {lintResults
                .filter((r) => r.level === 'error')
                .map((result, index) => (
                  <div key={`err-${result.line || index}-${result.message}`} className="flex items-start gap-2 text-sm">
                    <AlertCircle className="h-4 w-4 text-destructive mt-0.5 shrink-0" />
                    <div className="flex-1 min-w-0">
                      <div className="font-medium">{result.message}</div>
                      {result.rule && <div className="text-xs text-muted-foreground">{result.rule}</div>}
                    </div>
                  </div>
                ))}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setShowSaveConfirm(false)}>
              Fix errors first
            </Button>
            <Button onClick={() => handleSave(true)}>
              Save anyway
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
