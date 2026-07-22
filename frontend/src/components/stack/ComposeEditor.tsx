import { useRef, useState } from 'react'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/lib/api'
import { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { LintResultsPanel } from '@/components/stack/LintResultsPanel'
import { ComposeToolbar } from './compose-editor/ComposeToolbar'
import { ExtractToEnvDialog } from './compose-editor/ExtractToEnvDialog'
import { SaveConfirmDialog } from './compose-editor/SaveConfirmDialog'
import { useComposeSaveAndLint } from './compose-editor/useComposeSaveAndLint'
import { useExtractToEnv } from './compose-editor/useExtractToEnv'

interface ComposeEditorProps {
  stackId: string
}

export function ComposeEditor({ stackId }: ComposeEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const handleSaveRef = useRef<(forceSave?: boolean) => void>(() => {})
  const [content, setContent] = useState('')
  const [lastSaved, setLastSaved] = useState('')
  // selectedText/selectedTextRef stay here (not in useExtractToEnv) because
  // they're captured by the useCodeMirrorEditor onSelect option below at
  // mount time, the same way handleSaveRef is — see quirk notes in
  // useComposeSaveAndLint.ts. selectedTextRef itself is write-only (never
  // read) — preserved as-is from the pre-split component.
  const [selectedText, setSelectedText] = useState('')
  const selectedTextRef = useRef('')

  const { isLoading, data } = useQuery({
    queryKey: ['stack', stackId, 'compose'],
    queryFn: async () => {
      const response = await apiClient.get(`/stacks/${stackId}/compose`)
      return (response.data as { content: string }).content
    },
  })

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

  // Hydrate local editable state whenever a new compose query result arrives.
  // Adjusted during render (rather than in an effect) by comparing against
  // the data reference from the previous render — see
  // https://react.dev/learn/you-might-not-need-an-effect. This mirrors
  // EnvEditor.tsx's identical hydrate-on-query-change pattern; ESLint's
  // set-state-in-effect rule only fires here (and not in the pre-split
  // single-file component) because the other setContent/setLastSaved call
  // site now lives in useExtractToEnv.ts, out of this file's static view.
  const [prevData, setPrevData] = useState(data)
  if (data !== prevData) {
    setPrevData(data)
    if (data) {
      setContent(data)
      setLastSaved(data)
    }
  }

  const { lintResults, showSaveConfirm, setShowSaveConfirm, isLintingBeforeSave, saveMutation, lintMutation, handleSave, handleLint } =
    useComposeSaveAndLint({ stackId, viewRef, handleSaveRef, setLastSaved })

  const {
    extractVarName,
    setExtractVarName,
    showExtractDialog,
    setShowExtractDialog,
    isExtracting,
    handleExtractToEnv,
    confirmExtract,
  } = useExtractToEnv({ stackId, viewRef, selectedText, setSelectedText, setContent, setLastSaved })

  const hasUnsavedChanges = content !== lastSaved
  const errorCount = lintResults.filter((r) => r.level === 'error').length

  return (
    <div className="space-y-4">
      <ComposeToolbar
        onSave={() => handleSave()}
        onLint={handleLint}
        onExtract={handleExtractToEnv}
        isLoading={isLoading}
        isSaving={saveMutation.isPending}
        isLintingBeforeSave={isLintingBeforeSave}
        isLinting={lintMutation.isPending}
        selectedText={selectedText}
        hasUnsavedChanges={hasUnsavedChanges}
        errorCount={errorCount}
      />

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

      <ExtractToEnvDialog
        open={showExtractDialog}
        onOpenChange={setShowExtractDialog}
        selectedText={selectedText}
        extractVarName={extractVarName}
        onExtractVarNameChange={setExtractVarName}
        isExtracting={isExtracting}
        onConfirm={confirmExtract}
      />

      <SaveConfirmDialog
        open={showSaveConfirm}
        onOpenChange={setShowSaveConfirm}
        errorCount={errorCount}
        lintResults={lintResults}
        onSaveAnyway={() => handleSave(true)}
      />
    </div>
  )
}
