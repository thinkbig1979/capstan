import { useState, useEffect, useRef, useMemo, useCallback } from 'react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetFooter } from '@/components/ui/sheet'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { toast } from 'sonner'
import { useCreateStack } from '@/hooks/useCreateStack'
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

  const [name, setName] = useState('')
  const [composeContent, setComposeContent] = useState(DEFAULT_COMPOSE)
  const [envContent, setEnvContent] = useState('')
  const [deploy, setDeploy] = useState(true)
  const [showEnv, setShowEnv] = useState(false)
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [nameError, setNameError] = useState('')
  const editorViewRef = useRef<EditorView | null>(null)
  const editorRef = useRef<HTMLDivElement>(null)

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
      const response = await fetch('/api/v1/stacks/lint', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ compose: composeContent }),
      })
      const data = await response.json()
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
        onError: (error: { response?: { data?: { lintResults?: LintResult[]; error?: string } } }) => {
          if (error.response?.data?.lintResults) {
            setLintResults(error.response.data.lintResults)
            toast.error('Lint errors detected')
          } else if (error.response?.data?.error?.includes('already exists')) {
            toast.error('Stack name already exists')
          } else {
            toast.error('Failed to create stack')
          }
        },
      },
    )
  }, [name, composeContent, envContent, showEnv, deploy, createMutation, onOpenChange, navigate, validateName])

  const handleSave = useCallback(() => {
    if (!editorViewRef.current) return
    const currentContent = editorViewRef.current.state.doc.toString()
    setComposeContent(currentContent)
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

  // Update editor content when composeContent changes
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (editorViewRef.current) {
      const transaction = editorViewRef.current.state.update({
        changes: { from: 0, to: editorViewRef.current.state.doc.length, insert: composeContent },
      })
      editorViewRef.current.dispatch(transaction)
    }
  }, [composeContent]) // eslint-disable-line react-hooks/exhaustive-deps

  const hasLintErrors = lintResults.some((r) => r.level === 'error')

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent className="w-[600px] sm:max-w-[600px] overflow-y-auto">
        <SheetHeader>
          <SheetTitle>Create New Stack</SheetTitle>
        </SheetHeader>

        <div className="mt-6 space-y-6">
          <div className="space-y-2">
            <Label htmlFor="name">Stack Name</Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => handleNameChange(e.target.value)}
              placeholder="my-stack"
              className={nameError ? 'border-red-500' : ''}
            />
            {nameError && <p className="text-sm text-red-500">{nameError}</p>}
            {name && !nameError && (
              <p className="text-xs text-muted-foreground">
                Directory will be: /docker-stacks/{name}
              </p>
            )}
          </div>

          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label>Docker Compose</Label>
              <Button variant="outline" size="sm" onClick={handleLint}>
                <FileCheck className="mr-2 h-4 w-4" />
                Lint
              </Button>
            </div>
            <div ref={editorRef} className="rounded-md border overflow-hidden" style={{ minHeight: '300px' }} />
          </div>

          {lintResults.length > 0 && (
            <div className="rounded-md border bg-card">
              <div className="p-3 border-b">
                <h4 className="font-semibold text-sm">Lint Results</h4>
              </div>
              <div className="divide-y max-h-40 overflow-y-auto">
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

        <SheetFooter className="mt-6">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={handleCreate}
            disabled={createMutation.isPending || !!nameError || hasLintErrors}
          >
            {createMutation.isPending ? 'Creating...' : 'Create Stack'}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
