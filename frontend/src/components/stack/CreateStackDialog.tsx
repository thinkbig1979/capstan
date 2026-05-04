import { useState, useEffect, useRef, useCallback } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toast } from 'sonner'
import { useCreateStack } from '@/hooks/useCreateStack'
import { useQuery } from '@tanstack/react-query'
import { settingsApi, stacksApi } from '@/lib/api'
import { convertDockerRun, isDockerRunCommand } from '@/lib/docker-run-parser'
import { useNavigate } from 'react-router-dom'
import { FileCheck, AlertCircle, Plus, Terminal } from 'lucide-react'
import type { LintResult } from '@/types'
import { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { LintResultsPanel } from '@/components/stack/LintResultsPanel'

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
  const { data: config } = useQuery({
    queryKey: ['config'],
    queryFn: settingsApi.getConfig,
    staleTime: Infinity,
  })

  const [name, setName] = useState('')
  const [selectedDir, setSelectedDir] = useState<string>('')
  const [composeContent, setComposeContent] = useState(DEFAULT_COMPOSE)
  const [envContent, setEnvContent] = useState('')
  const [deploy, setDeploy] = useState(false)
  const [showEnv, setShowEnv] = useState(false)
  const [lintResults, setLintResults] = useState<LintResult[]>([])
  const [nameError, setNameError] = useState('')
  const [composeTab, setComposeTab] = useState<'editor' | 'docker-run'>('editor')
  const [dockerRunInput, setDockerRunInput] = useState('')
  const [conversionError, setConversionError] = useState('')
  const [pendingCompose, setPendingCompose] = useState<string | null>(null)
  const editorRef = useRef<HTMLDivElement>(null)
  const nameInputRef = useRef<HTMLInputElement>(null)
  const composeRef = useRef<HTMLDivElement>(null)
  const isUpdatingFromEditor = useRef(false)

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

  const handleLint = useCallback(async () => {
    try {
      const data = await stacksApi.lint(composeContent)
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

  const resetForm = useCallback(() => {
    setName('')
    setSelectedDir('')
    setComposeContent(DEFAULT_COMPOSE)
    setEnvContent('')
    setShowEnv(false)
    setLintResults([])
    setDockerRunInput('')
    setConversionError('')
    setPendingCompose(null)
    setComposeTab('editor')
  }, [])

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
      { name, directory: selectedDir || undefined, composeContent, envContent: showEnv ? envContent : undefined, deploy },
      {
        onSuccess: (data) => {
          toast.success(
            `Stack "${name}" created successfully${deploy ? ' and deployed' : ''}`,
          )
          onOpenChange(false)
          navigate(`/stacks/${data.stack.id}`)
          resetForm()
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
  }, [name, selectedDir, composeContent, envContent, showEnv, deploy, createMutation, onOpenChange, navigate, validateName, resetForm])

  const handleConvertDockerRun = useCallback(() => {
    const trimmed = dockerRunInput.trim()
    if (!trimmed) {
      toast.error('Please paste a docker run command')
      return
    }

    if (!isDockerRunCommand(trimmed)) {
      toast.error('Input does not appear to be a docker run command')
      return
    }

    try {
      const compose = convertDockerRun(trimmed)
      if (!compose) {
        toast.error('Could not parse the docker run command')
        return
      }

      setPendingCompose(compose)
      setConversionError('')
      toast.success('Docker run command converted to Compose')
      setComposeTab('editor')
    } catch {
      setConversionError('Failed to parse the docker run command. Check the syntax and try again.')
      toast.error('Failed to parse the docker run command')
    }
  }, [dockerRunInput])

  useCodeMirrorEditor(editorRef, {
    doc: pendingCompose ?? composeContent,
    onChange: (newContent) => {
      if (!isUpdatingFromEditor.current) {
        isUpdatingFromEditor.current = true
        setComposeContent(newContent)
        requestAnimationFrame(() => {
          isUpdatingFromEditor.current = false
        })
      }
    },
    deps: [open, composeTab],
  })

  useEffect(() => {
    if (pendingCompose) {
      setComposeContent(pendingCompose)
      setPendingCompose(null)
    }
  }, [pendingCompose])

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
    <Dialog open={open} onOpenChange={(isOpen) => {
      if (!isOpen) resetForm()
      onOpenChange(isOpen)
    }}>
      <DialogContent className="w-[900px] sm:max-w-[900px] max-h-[90vh] overflow-y-auto">
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
                Directory will be {selectedDir && selectedDir !== (config?.stacksDir ?? '') ? selectedDir : (config?.stacksDir ?? '...')}/{name}
              </p>
            )}
          </div>

          {(config?.stacksDirectories?.length ?? 0) > 1 && (
            <div className="space-y-2">
              <Label htmlFor="directory">Target Directory</Label>
              <Select value={selectedDir || config?.stacksDir || ''} onValueChange={setSelectedDir}>
                <SelectTrigger id="directory">
                  <SelectValue placeholder="Select directory" />
                </SelectTrigger>
                <SelectContent>
                  {config?.stacksDirectories?.map((dir: string) => (
                    <SelectItem key={dir} value={dir}>
                      <span className="flex items-center gap-2">
                        {dir === config?.stacksDir && (
                          <Badge variant="secondary" className="text-[10px] px-1 py-0">Default</Badge>
                        )}
                        {dir}
                      </span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-xs text-muted-foreground">
                Choose which monitored directory to create the stack in.
              </p>
            </div>
          )}

          <div className="space-y-2" ref={composeRef}>
            <div className="flex items-center justify-between">
              <Label>Docker Compose</Label>
              <Button variant="outline" size="sm" onClick={handleLint}>
                <FileCheck className="mr-2 h-4 w-4" />
                Lint
              </Button>
            </div>

            <div className="inline-flex h-9 items-center justify-center rounded-lg bg-muted p-1 text-muted-foreground w-full">
              <button
                onClick={() => setComposeTab('editor')}
                className={`inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all flex-1 ${
                  composeTab === 'editor'
                    ? 'bg-background text-foreground shadow'
                    : 'hover:bg-background/50'
                }`}
              >
                Compose Editor
              </button>
              <button
                onClick={() => setComposeTab('docker-run')}
                className={`inline-flex items-center justify-center whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium ring-offset-background transition-all flex-1 ${
                  composeTab === 'docker-run'
                    ? 'bg-background text-foreground shadow'
                    : 'hover:bg-background/50'
                }`}
              >
                <Terminal className="mr-2 h-3 w-3" />
                Convert docker run
              </button>
            </div>

            {composeTab === 'editor' && (
              <div className="space-y-2">
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
            )}

            {composeTab === 'docker-run' && (
              <div className="space-y-3">
                <Textarea
                  value={dockerRunInput}
                  onChange={(e) => {
                    setDockerRunInput(e.target.value)
                    setConversionError('')
                  }}
                  placeholder={`docker run -d \\\n  --name webapp \\\n  -p 8080:80 \\\n  -e NODE_ENV=production \\\n  nginx:alpine`}
                  className="font-mono min-h-[200px] text-sm"
                />
                {conversionError && (
                  <p className="text-sm text-red-500">{conversionError}</p>
                )}
                <div className="flex items-center justify-between">
                  <p className="text-xs text-muted-foreground">
                    Paste a <code className="bg-muted px-1 rounded">docker run</code> command to convert it to Compose format
                  </p>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={handleConvertDockerRun}
                    disabled={!dockerRunInput.trim()}
                  >
                    <Terminal className="mr-2 h-4 w-4" />
                    Convert
                  </Button>
                </div>
              </div>
            )}
          </div>

          <LintResultsPanel results={lintResults} maxHeight="10rem" />

          <div className="space-y-2">
            <Button
              variant="ghost"
              onClick={() => setShowEnv(!showEnv)}
              className="w-full justify-start"
            >
              <Plus className="mr-2 h-4 w-4" />
              {showEnv ? 'Hide' : 'Add'} .env File
            </Button>

            {showEnv && (
              <div className="space-y-2 pl-4 border-l-2">
                <Label htmlFor="env">.env File</Label>
                <Textarea
                  id="env"
                  value={envContent}
                  onChange={(e) => setEnvContent(e.target.value)}
                  placeholder="KEY=value&#10;ANOTHER_KEY=value"
                  className="font-mono min-h-[150px]"
                />
                <p className="text-xs text-muted-foreground">
                  Creates a .env file alongside compose.yaml. Variables are available via {'${VAR}'} in the compose file. You can also extract hardcoded values from the Compose tab after creation.
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
            <Button variant="outline" onClick={() => {
              resetForm()
              onOpenChange(false)
            }}>
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
