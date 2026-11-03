import { useEffect, useRef, useState } from 'react'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useQuery } from '@tanstack/react-query'
import { stacksApi } from '@/lib/api'
import { useCodeMirrorEditor } from '@/hooks/useCodeMirrorEditor'
import { LintResultsPanel } from '@/components/stack/LintResultsPanel'
import { ComposeToolbar } from './compose-editor/ComposeToolbar'
import { ExtractToEnvDialog } from './compose-editor/ExtractToEnvDialog'
import { SaveConfirmDialog } from './compose-editor/SaveConfirmDialog'
import { useComposeSaveAndLint } from './compose-editor/useComposeSaveAndLint'
import { useExtractToEnv } from './compose-editor/useExtractToEnv'
import { queryKeys } from '@/lib/query-keys'
import { stringOr } from '@/lib/narrow'
import { CommitPushDialog } from '@/components/git/CommitPushDialog'
import { FileChangedNotice } from '@/components/FileChangedNotice'
import { useExternalFileChange } from '@/hooks/useExternalFileChange'

interface ComposeEditorProps {
  stackId: string
}

export function ComposeEditor({ stackId }: ComposeEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const handleSaveRef = useRef<() => void>(() => {})
  const [content, setContent] = useState('')
  const [lastSaved, setLastSaved] = useState('')
  // The text last loaded into the view. A fresh object per load, so the sync
  // effect below runs even when the same text is loaded twice.
  const [loaded, setLoaded] = useState({ text: '' })
  // selectedText/selectedTextRef stay here (not in useExtractToEnv) because
  // they're captured by the useCodeMirrorEditor onSelect option below at
  // mount time, the same way handleSaveRef is — see quirk notes in
  // useComposeSaveAndLint.ts. selectedTextRef itself is write-only (never
  // read) — preserved as-is from the pre-split component.
  const [selectedText, setSelectedText] = useState('')
  const selectedTextRef = useRef('')

  const { isPending, data } = useQuery({
    queryKey: queryKeys.stack.compose(stackId),
    queryFn: async () => {
      const compose = await stacksApi.getCompose(stackId)
      // agent-os-06c1: narrowed, not asserted. This value becomes a
      // string-typed React state and the editor's initial document.
      // '' rather than undefined: the hydrate below skips an empty body,
      // as it always has for an absent one.
      return stringOr((compose as { content?: unknown } | undefined)?.content, '')
    },
  })

  // The stack row StackPage already fetched, read from the cache: `enabled:
  // false` means this observer never requests it. Decides whether the save
  // toast offers "Commit and push" (agent-os-k81p.3).
  const { data: stack } = useQuery({
    queryKey: queryKeys.stack.detail(stackId),
    queryFn: () => stacksApi.get(stackId),
    enabled: false,
  })
  const [commitOpen, setCommitOpen] = useState(false)

  const { viewRef } = useCodeMirrorEditor(editorRef, {
    // Not `data`: while the editor holds unsaved edits a new query result
    // must not reach the view (agent-os-umig).
    doc: loaded.text,
    ariaLabel: 'Compose file editor',
    onSave: () => {
      handleSaveRef.current()
      return true
    },
    onChange: setContent,
    onSelect: (text) => {
      selectedTextRef.current = text
      setSelectedText(text)
    },
    deps: [stackId],
  })

  const hasUnsavedChanges = content !== lastSaved
  const { changedOnDisk, reload } = useExternalFileChange(data, {
    isDirty: hasUnsavedChanges,
    hydrate: (next) => {
      // '' is an absent body (see the queryFn), not a file to load.
      if (!next) return
      setContent(next)
      setLastSaved(next)
      setLoaded({ text: next })
    },
    canHydrateWhileDirty: (next) => next === content,
    isOwnSave: (next) => next === lastSaved,
  })

  // The doc effect in useCodeMirrorEditor fires only when `doc` changes, so it
  // misses a load whose text equals the previous load while the view has
  // moved on (load A, edit and save A', A comes back from disk).
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (current !== loaded.text) {
      view.dispatch({ changes: { from: 0, to: current.length, insert: loaded.text } })
    }
  }, [loaded, viewRef])

  const { lintResults, showSaveConfirm, setShowSaveConfirm, isLintingBeforeSave, saveMutation, lintMutation, handleSave, handleLint } =
    useComposeSaveAndLint({
      stackId,
      viewRef,
      handleSaveRef,
      setLastSaved,
      onOpenCommit: stack?.isGitRepo ? () => setCommitOpen(true) : undefined,
    })

  const {
    extractVarName,
    setExtractVarName,
    showExtractDialog,
    setShowExtractDialog,
    isExtracting,
    handleExtractToEnv,
    confirmExtract,
  } = useExtractToEnv({ stackId, viewRef, selectedText, setSelectedText, setContent, setLastSaved })

  const errorCount = lintResults.filter((r) => r.level === 'error').length

  return (
    <div className="space-y-4">
      <ComposeToolbar
        onSave={() => handleSave()}
        onLint={handleLint}
        onExtract={handleExtractToEnv}
        isLoading={isPending}
        isSaving={saveMutation.isPending}
        isLintingBeforeSave={isLintingBeforeSave}
        isLinting={lintMutation.isPending}
        selectedText={selectedText}
        hasUnsavedChanges={hasUnsavedChanges}
        errorCount={errorCount}
      />

      {changedOnDisk && <FileChangedNotice onReload={reload} />}

      <div className="relative">
        {isPending && (
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
      />

      {commitOpen && (
        <CommitPushDialog stackId={stackId} composeFile={stack?.composeFile} open onOpenChange={setCommitOpen} />
      )}
    </div>
  )
}
