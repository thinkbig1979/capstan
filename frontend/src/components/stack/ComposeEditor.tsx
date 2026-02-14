import { useEffect, useRef, useState, useCallback, useMemo } from 'react'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { linter, lintGutter } from '@codemirror/lint'
import { oneDark } from '@codemirror/theme-one-dark'
import { keymap } from '@codemirror/view'
import { search } from '@codemirror/search'
import { autocompletion } from '@codemirror/autocomplete'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Save, FileCheck, AlertCircle, AlertTriangle, Info } from 'lucide-react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
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

  const isDark = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' &&
        typeof window !== 'undefined' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  const queryClient = useQueryClient()

  const { isLoading } = useQuery({
    queryKey: ['stack', stackId, 'compose'],
    queryFn: async () => {
      const response = await apiClient.get(`/stacks/${stackId}/compose`)
      return response.data as string
    },
    onSuccess: (data) => {
      setContent(data)
      setLastSaved(data)
      if (viewRef.current) {
        viewRef.current.dispatch({
          changes: { from: 0, to: viewRef.current.state.doc.length, insert: data },
        })
      }
    },
  })

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
      if (error.response?.data?.lintResults) {
        setLintResults(error.response.data.lintResults)
        toast.error('Lint errors detected')
      } else {
        toast.error('Failed to save compose file')
      }
    },
  })

  const handleSave = useCallback(() => {
    if (!viewRef.current) return
    const currentContent = viewRef.current.state.doc.toString()
    saveMutation.mutate(currentContent)
  }, [saveMutation])

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
          const diagnostics = lintResults.map((result) => ({
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
        parent: editorRef.current,
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
    return <div className="flex items-center justify-center py-8">Loading...</div>
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Button onClick={handleSave} disabled={saveMutation.isPending || !hasUnsavedChanges}>
            <Save className="mr-2 h-4 w-4" />
            {saveMutation.isPending ? 'Saving...' : 'Save'}
          </Button>
          <Button variant="outline" onClick={handleLint} disabled={lintMutation.isPending}>
            <FileCheck className="mr-2 h-4 w-4" />
            {lintMutation.isPending ? 'Linting...' : 'Lint'}
          </Button>
          {hasUnsavedChanges && (
            <Badge variant="secondary" className="text-xs">
              Unsaved changes
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
    </div>
  )
}
