import { useEffect, useRef, useState, useCallback, useMemo } from 'react'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { linter, lintGutter, type Diagnostic } from '@codemirror/lint'
import { oneDark } from '@codemirror/theme-one-dark'
import { keymap } from '@codemirror/view'
import { search } from '@codemirror/search'
import { autocompletion } from '@codemirror/autocomplete'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Save, FileCheck, AlertCircle, AlertTriangle, Info } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import { classifyError } from '@/lib/error-handler'
import { toast } from 'sonner'
import type { LintResult } from '@/types'
import { useUIStore } from '@/stores/uiStore'

interface ComposeEditorProps {
  stackId: string
}

export function ComposeEditor({ stackId }: ComposeEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const { theme } = useUIStore()
  const [content, setContent] = useState('')
  const [lastSaved, setLastSaved] = useState('')
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [showSaveConfirm, setShowSaveConfirm] = useState(false)
  const [isLintingBeforeSave, setIsLintingBeforeSave] = useState(false)

  const isDark = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' &&
        typeof window !== 'undefined' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  const queryClient = useQueryClient()

  const { isLoading, data } = useQuery({
    queryKey: ['stack', stackId, 'compose'],
    queryFn: async () => {
      const response = await apiClient.get(`/stacks/${stackId}/compose`)
      return response.data as string
    },
  })

  useEffect(() => {
    if (data) {
      setContent(data)
      setLastSaved(data)
      if (viewRef.current) {
        viewRef.current.dispatch({
          changes: { from: 0, to: viewRef.current.state.doc.length, insert: data },
        })
      }
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

  // Initialize CodeMirror editor
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (!editorRef.current) return

    const createEditor = () => {
      const extensions = [
        basicSetup,
        yaml(),
        lintGutter(),
        linter(() => {
          const diagnostics: Diagnostic[] = lintResults.map((result) => ({
            from: 0,
            to: 0,
            severity: result.level === 'error' ? 'error' : result.level === 'warning' ? 'warning' : 'info',
            message: result.message,
          }))
          return diagnostics
        }),
        keymap.of([
          {
            key: 'Mod-s',
            run: () => {
              handleSave()
              return true
            },
          },
        ]),
        search({ top: true }),
        autocompletion(),
        EditorView.theme({
          '&': { fontSize: '14px' },
          '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace' },
        }),
      ]

      if (isDark) {
        extensions.push(oneDark)
      }

      const state = EditorState.create({
        doc: content,
        extensions,
      })

      const view = new EditorView({
        state,
        parent: editorRef.current || undefined,
      })

      return view
    }

    const view = createEditor()
    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
    }
  }, [stackId, isDark, handleSave])

  // Update content from editor
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (viewRef.current) {
      const currentContent = viewRef.current.state.doc.toString()
      setContent(currentContent)
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Update lint diagnostics when results change
  useEffect(() => {
    if (!viewRef.current) return

    viewRef.current.dispatch({
      effects: [],
    })
  }, [lintResults]) // eslint-disable-line react-hooks/exhaustive-deps

  const hasUnsavedChanges = content !== lastSaved
  const errorCount = lintResults.filter((r) => r.level === 'error').length

  const getLintIcon = (level: string) => {
    switch (level) {
      case 'error':
        return <AlertCircle className="h-4 w-4 text-red-500" />
      case 'warning':
        return <AlertTriangle className="h-4 w-4 text-yellow-500" />
      default:
        return <Info className="h-4 w-4 text-blue-500" />
    }
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-8">
        <div className="flex items-center gap-2">
          <LoadingSpinner size="default" />
          <span className="text-muted-foreground">Loading compose file...</span>
        </div>
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Button
            onClick={() => handleSave()}
            disabled={saveMutation.isPending || !hasUnsavedChanges || isLintingBeforeSave}
          >
            <Save className="mr-2 h-4 w-4" />
            {isLintingBeforeSave ? 'Validating...' : saveMutation.isPending ? 'Saving...' : 'Save'}
          </Button>
          <Button variant="outline" onClick={handleLint} disabled={lintMutation.isPending}>
            <FileCheck className="mr-2 h-4 w-4" />
            {lintMutation.isPending ? 'Linting...' : 'Lint'}
          </Button>
          {hasUnsavedChanges && (
            <Badge variant="secondary" className="text-xs">
              {errorCount > 0 ? `Unsaved changes (${errorCount} errors)` : 'Unsaved changes'}
            </Badge>
          )}
        </div>
        <div className="text-sm text-muted-foreground">
          Ctrl+S to save
        </div>
      </div>

      <div ref={editorRef} className="rounded-md border overflow-hidden" style={{ minHeight: '400px' }} />

      {lintResults.length > 0 && (
        <div className="rounded-md border bg-card">
          <div className="p-4 border-b">
            <h3 className="font-semibold">Lint Results</h3>
          </div>
          <div className="divide-y">
            {lintResults.map((result, index) => (
              <div key={index} className="p-3 flex items-start gap-3 hover:bg-muted/50">
                {getLintIcon(result.level)}
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    {result.line && (
                      <Badge variant="outline" className="text-xs">
                        Line {result.line}
                      </Badge>
                    )}
                    <span className="text-sm font-medium">{result.message}</span>
                  </div>
                  {result.rule && (
                    <div className="text-xs text-muted-foreground mt-1">
                      {result.rule}
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <Dialog open={showSaveConfirm} onOpenChange={setShowSaveConfirm}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertCircle className="h-5 w-5 text-yellow-500" />
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
                  <div key={index} className="flex items-start gap-2 text-sm">
                    <AlertCircle className="h-4 w-4 text-red-500 mt-0.5 flex-shrink-0" />
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
