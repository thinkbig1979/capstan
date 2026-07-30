import { useRef, useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { useQuery } from '@tanstack/react-query'
import { settingsApi } from '@/lib/api'
import type { LintResult } from '@/types'
import { LintResultsPanel } from '@/components/stack/LintResultsPanel'
import { DEFAULT_COMPOSE } from './create-stack/constants'
import { useComposeLint } from './create-stack/useComposeLint'
import { useComposeEditorMount } from './create-stack/useComposeEditorMount'
import { useDockerRunConversion } from './create-stack/useDockerRunConversion'
import { useCreateStackSubmit } from './create-stack/useCreateStackSubmit'
import { StackNameField } from './create-stack/StackNameField'
import { DirectorySelect } from './create-stack/DirectorySelect'
import { ComposeTabsPanel } from './create-stack/ComposeTabsPanel'
import { EnvFileSection } from './create-stack/EnvFileSection'
import { CreateStackFooter } from './create-stack/CreateStackFooter'
import { queryKeys } from '@/lib/query-keys'

interface CreateStackDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function CreateStackDialog({ open, onOpenChange }: CreateStackDialogProps) {
  const { data: config } = useQuery({
    queryKey: queryKeys.config(),
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
  // The editor lives inside a Radix Dialog portal; on the first open the
  // container ref isn't attached yet when the CodeMirror mount effect fires,
  // leaving a blank editor until the user toggles tabs. Bump this after the
  // dialog has laid out so the mount effect re-runs with the ref present.
  const [editorEpoch, setEditorEpoch] = useState(0)
  const nameInputRef = useRef<HTMLInputElement>(null)
  const composeRef = useRef<HTMLDivElement>(null)

  const { handleLint, hasLintErrors } = useComposeLint({ composeContent, lintResults, setLintResults })

  const { handleConvertDockerRun } = useDockerRunConversion({
    dockerRunInput,
    setConversionError,
    setPendingCompose,
    setComposeTab,
  })

  const { editorRef } = useComposeEditorMount({
    open,
    composeTab,
    editorEpoch,
    setEditorEpoch,
    composeContent,
    setComposeContent,
    pendingCompose,
    setPendingCompose,
  })

  const { handleNameChange, resetForm, handleCreate, validationErrors, isCreateDisabled, isPending } =
    useCreateStackSubmit({
      name,
      setName,
      setNameError,
      selectedDir,
      setSelectedDir,
      composeContent,
      setComposeContent,
      envContent,
      setEnvContent,
      showEnv,
      setShowEnv,
      deploy,
      nameError,
      hasLintErrors,
      lintResults,
      setLintResults,
      setDockerRunInput,
      setConversionError,
      setPendingCompose,
      setComposeTab,
      onOpenChange,
    })

  const directoryPreview =
    selectedDir && selectedDir !== (config?.stacksDir ?? '') ? selectedDir : (config?.stacksDir ?? '...')

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
          <StackNameField
            name={name}
            nameError={nameError}
            onNameChange={handleNameChange}
            directoryPreview={directoryPreview}
            nameInputRef={nameInputRef}
          />

          {(config?.stacksDirectories?.length ?? 0) > 1 && (
            <DirectorySelect
              stacksDir={config?.stacksDir}
              stacksDirectories={config?.stacksDirectories ?? []}
              selectedDir={selectedDir}
              onSelectedDirChange={setSelectedDir}
            />
          )}

          <ComposeTabsPanel
            composeRef={composeRef}
            composeTab={composeTab}
            onComposeTabChange={setComposeTab}
            onLint={handleLint}
            editorRef={editorRef}
            hasLintErrors={hasLintErrors}
            lintResults={lintResults}
            dockerRunInput={dockerRunInput}
            onDockerRunInputChange={(value) => {
              setDockerRunInput(value)
              setConversionError('')
            }}
            conversionError={conversionError}
            onConvert={handleConvertDockerRun}
          />

          <LintResultsPanel results={lintResults} maxHeight="10rem" />

          <EnvFileSection
            showEnv={showEnv}
            onToggle={() => setShowEnv(!showEnv)}
            envContent={envContent}
            onEnvContentChange={setEnvContent}
          />

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

        <CreateStackFooter
          onCancel={() => {
            resetForm()
            onOpenChange(false)
          }}
          onCreate={handleCreate}
          isCreateDisabled={isCreateDisabled}
          isPending={isPending}
          validationErrors={validationErrors}
        />
      </DialogContent>
    </Dialog>
  )
}
