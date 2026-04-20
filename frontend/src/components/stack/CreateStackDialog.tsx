import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toast } from 'sonner'
import { useCreateStack } from '@/hooks/useCreateStack'
import { useQuery } from '@tanstack/react-query'
import { authApi, apiClient } from '@/lib/api'
import { useNavigate } from 'react-router-dom'
import { FileCheck, AlertCircle, AlertTriangle, Info, Plus } from 'lucide-react'
import type { LintResult } from '@/types'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { yaml } from '@codemirror/lang-yaml'
import { keymap } from '@codemirror/view'
import { search } from '@codemirror/search'
import { autocompletion } from '@codemirror/autocomplete'
import { oneDark } from '@codemirror/theme-one-dark'
import { useUIStore } from '@/stores/uiStore'

interface CreateStackDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const DEFAULT_COMPOSE = `services:
  app:
    image: nginx:latest
    restart: unless-stopped
    ports:
      - "8080:80"
`

export function CreateStackDialog({ open, onOpenChange }: CreateStackDialogProps) {
  const navigate = useNavigate()
  const createMutation = useCreateStack()
  const { theme } = useUIStore()
  const { data: config } = useQuery({
    queryKey: ['config'],
    queryFn: authApi.getConfig,
    staleTime: Infinity,
  })

  const [name, setName] = useState('')
  const [composeContent, setComposeContent] = useState(DEFAULT_COMPOSE)
  const [envContent, setEnvContent] = useState('')
  const [deploy, setDeploy] = useState(true)
  const [showEnv, setShowEnv] = useState(false)
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [nameError, setNameError] = useState('')
  const editorViewRef = useRef<EditorView | null>(null)
  const editorRef = useRef<HTMLDivElement>(null)
  const nameInputRef = useRef<HTMLInputElement>(null)
  const composeRef = useRef<HTMLDivElement>(null)
  const isUpdatingFromEditor = useRef(false)

  const isDarkTheme = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  const validateName = useCallback((value: string) => {
    if (!value.trim()) {
      return 'Stack name is required'
    }
    if (!/^[a-zA-Z0-9._-]+$/.test(value)) {
      return 'Only letters, numbers, dots, hyphens, and underscores are allowed'
    }
    if (value.length < 1 || value.length > 50) {
      return 'Stack name must be between 1 and 50 characters'
    }
    return ''
  }, [])

  const handleNameChange = useCallback(
    (value: string) => {
      setName(value)
      setNameError(validateName(value))
    },
    [validateName],
  )

  const getLintIcon = useCallback((level: string) => {
    switch (level) {
      case 'error':
        return <AlertCircle className="h-4 w-4 text-red-500" />
      case 'warning':
        return <AlertTriangle className="h-4 w-4 text-yellow-500" />
      default:
        return <Info className="h-4 w-4 text-blue-500" />
    }
  }, [])

  const handleLint = useCallback(async () => {
    try {
      const response = await apiClient.post<{ lintResults: LintResult[] }>('/stacks/lint', { compose: composeContent })
      const data = response.data
      setLintResults(data.lintResults || [])

      if (data.lintResults?.some((r: LintResult) => r.level === 'error')) {
        toast.error('Lint errors detected')
      } else if (data.lintResults?.some((r: LintResult) => r.level === 'warning')) {
        toast.warning('Lint warnings detected')
      } else {
        toast.success('No lint issues found')
      }
    } catch {
      toast.error('Failed to lint compose file')
    }
  }, [composeContent])

  const handleCreate = useCallback(() => {
    const error = validateName(name)
    if (error) {
      setNameError(error)
      toast.error(error)
      return
    }

    if (!composeContent.trim()) {
      toast.error('Compose content cannot be empty')
      return
    }

    createMutation.mutate(
      { name, composeContent, envContent: showEnv ? envContent : undefined, deploy },
      {
        onSuccess: (data) => {
          toast.success(
            `Stack "${name}" created successfully${deploy ? ' and deployed' : ''}`,
          )
          onOpenChange(false)
          navigate(`/stacks/${data.stack.id}`)
          setName('')
          setComposeContent(DEFAULT_COMPOSE)
          setEnvContent('')
          setShowEnv(false)
          setLintResults([])
        },
        onError: (error: unknown) => {
          const err = error as { error?: string; lintResults?: LintResult[] }
          if (err.lintResults && err.lintResults.length > 0) {
            setLintResults(err.lintResults)
            toast.error('Lint errors detected')
          } else if (err.error?.includes('already exists')) {
            toast.error('Stack name already exists')
          } else {
            toast.error(err.error || 'Failed to create stack')
          }
        },
      },
    )
  }, [name, composeContent, envContent, showEnv, deploy, createMutation, onOpenChange, navigate, validateName])

  const handleSave = useCallback(() => {
    if (!editorViewRef.current) return
    const currentContent = editorViewRef.current.state.doc.toString()
    isUpdatingFromEditor.current = true
    setComposeContent(currentContent)
    requestAnimationFrame(() => {
      isUpdatingFromEditor.current = false
    })
  }, [])

  // Initialize CodeMirror editor
  useEffect(() => {
    if (!editorRef.current || !open) return

    if (editorViewRef.current) {
      editorViewRef.current.destroy()
    }

    const extensions = [
      basicSetup,
      yaml(),
      keymap.of([
        {
          key: 'Mod-s',
          run: () => {
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

    if (isDarkTheme) {
      extensions.push(oneDark)
    }

    const state = EditorState.create({
      doc: composeContent,
      extensions: [
        ...extensions,
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            handleSave()
          }
        }),
      ],
    })

    const view = new EditorView({
      state,
      parent: editorRef.current,
    })

    editorViewRef.current = view

    return () => {
      view.destroy()
      editorViewRef.current = null
    }
  }, [open, isDarkTheme, handleSave])

  // Update editor content when composeContent changes externally
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (editorViewRef.current && !isUpdatingFromEditor.current) {
      const transaction = editorViewRef.current.state.update({
        changes: { from: 0, to: editorViewRef.current.state.doc.length, insert: composeContent },
      })
      editorViewRef.current.dispatch(transaction)
    }
  }, [composeContent]) // eslint-disable-line react-hooks/exhaustive-deps

  const hasLintErrors = lintResults.some((r) => r.level === 'error')

  const getValidationErrors = useCallback(() => {
    const errors: { field: string; message: string }[] = []
    if (nameError) {
      errors.push({ field: 'name', message: nameError })
    }
    if (hasLintErrors) {
      errors.push({
        field: 'compose',
        message: `${lintResults.filter((r) => r.level === 'error').length} lint error(s)`,
      })
    }
    return errors
  }, [nameError, hasLintErrors, lintResults])


  const validationErrors = getValidationErrors()
  const isCreateDisabled = createMutation.isPending || validationErrors.length > 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[600px] sm:max-w-[600px] max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Create New Stack</DialogTitle>
        </DialogHeader>

        <div className="mt-6 space-y-6">
          <div className="space-y-2">
            <Label htmlFor="name">Stack Name</Label>
            <Input
              id="name"
              ref={nameInputRef}
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              placeholder="my-stack"
              className={nameError ? 'border-red-500' : ''}
            />
            {nameError && <p className="text-sm text-red-500">{nameError}</p>}
            {name && !nameError && (
              <p className="text-xs text-muted-foreground">
                Directory will be: {config?.stacksDir ?? '...'}/{name}
              </p>
            )}
          </div>

          <div className="space-y-2" ref={composeRef}>
            <div className="flex items-center justify-between">
              <Label>Docker Compose</Label>
              <Button variant="outline" size="sm" onClick={handleLint}>
                <FileCheck className="mr-2 h-4 w-4" />
                Lint
              </Button>
            </div>
            <div
              ref={editorRef}
              className={`rounded-md border overflow-hidden ${hasLintErrors ? 'border-red-500' : ''}`}
              style={{ minHeight: '300px' }}
            />
            {hasLintErrors && (
              <p className="text-sm text-red-500">
                {lintResults.filter((r) => r.level === 'error').length} error(s) found - fix before creating
              </p>
            )}
          </div>

          {lintResults.length > 0 && (
            <div className="rounded-md border bg-card">
              <div className="p-3 border-b">
                <h4 className="font-semibold text-sm">Lint Results</h4>
              </div>
              <div className="divide-y max-h-40 overflow-y-auto">
                {lintResults.map((result, index) => (
                  <div key={`${result.level}-${result.line || index}-${result.message}`} className="p-3 flex items-start gap-3 hover:bg-muted/50">
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

          <div className="space-y-2">
            <Button
              variant="ghost"
              onClick={() => setShowEnv(!showEnv)}
              className="w-full justify-start"
            >
              <Plus className="mr-2 h-4 w-4" />
              {showEnv ? 'Hide' : 'Add'} Environment Variables
            </Button>

            {showEnv && (
              <div className="space-y-2 pl-4 border-l-2">
                <Label htmlFor="env">Environment Variables</Label>
                <Textarea
                  id="env"
                  value={envContent}
                  onChange={(e) => setEnvContent(e.target.value)}
                  placeholder="KEY=value&#10;ANOTHER_KEY=value"
                  className="font-mono min-h-[150px]"
                />
                <p className="text-xs text-muted-foreground">
                  Add environment variables in KEY=value format (one per line)
                </p>
              </div>
            )}
          </div>

          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="deploy"
              checked={deploy}
              onChange={(e) => setDeploy(e.target.checked)}
              className="h-4 w-4"
            />
            <Label htmlFor="deploy" className="cursor-pointer">
              Deploy after creation
            </Label>
          </div>
        </div>

        <DialogFooter className="mt-6 flex-col items-stretch gap-2">
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end sm:space-x-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button onClick={handleCreate} disabled={isCreateDisabled}>
                    {createMutation.isPending ? 'Creating...' : 'Create Stack'}
                  </Button>
                </TooltipTrigger>
                {isCreateDisabled && !createMutation.isPending && (
                  <TooltipContent>
                    <p>Fix validation errors to create stack</p>
                  </TooltipContent>
                )}
              </Tooltip>
            </TooltipProvider>
          </div>
          {validationErrors.length > 0 && (
            <div className="rounded-md border border-red-200 bg-red-50 dark:bg-red-950 dark:border-red-900 p-3">
              <div className="text-sm font-medium text-red-900 dark:text-red-100 mb-2">
                Please fix the following errors:
              </div>
              <ul className="space-y-1">
                 {validationErrors.map((error) => (
                   <li key={error.field}>
                     <button
                       type="button"
                       className="text-sm text-red-700 dark:text-red-300 hover:underline flex items-center gap-1"
                     >
                       <AlertCircle className="h-3 w-3" />
                       {error.message}
                     </button>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
