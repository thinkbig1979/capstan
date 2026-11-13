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
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { Button } from '@/components/ui/button'
import { Lock } from 'lucide-react'

/**
 * One load of the compose file. `masked` is true when the server masked
 * secrets in `text` for a session that has not unlocked env values
 * (agent-os-w6wx): the editor is then read-only, and a save would be refused.
 */
interface ComposeLoad {
  text: string
  masked: boolean
}

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
  const [loaded, setLoaded] = useState({ text: '', masked: false })
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
      const body = compose as { content?: unknown; maskedValues?: unknown } | undefined
      const load: ComposeLoad = {
        text: stringOr(body?.content, ''),
        masked: typeof body?.maskedValues === 'number' && body.maskedValues > 0,
      }
      return load
    },
  })
  const masked = data?.masked ?? false
  const [unlockOpen, setUnlockOpen] = useState(false)

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
    // The view holds "***" where secrets are. Edits kept across a re-lock
    // stay editable: they hold no "***", and the server refuses their save
    // until unlock.
    readOnly: masked && loaded.masked,
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
      if (!next.text) return
      setContent(next.text)
      setLastSaved(next.text)
      setLoaded({ text: next.text, masked: next.masked })
    },
    // A re-lock while the editor holds unsaved edits: keep them, never load
    // "***" over them, and raise no changed-on-disk notice for a file that
    // did not change (agent-os-w6wx, as the env editor does, vqf5).
    rebaseWhileDirty: (next) => next.masked,
    canHydrateWhileDirty: (next) => next.text === content,
    isOwnSave: (next) => next.text === lastSaved,
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
        masked={masked}
      />

      {masked && (
        <div
          role="status"
          className="flex flex-wrap items-center justify-between gap-3 rounded-md border bg-muted/50 px-3 py-2 text-sm"
        >
          <div className="flex items-center gap-2">
            <Lock className="h-4 w-4 shrink-0" aria-hidden="true" />
            <span>
              {hasUnsavedChanges
                ? 'Values locked again; unlock to save your edits.'
                : 'Secrets in this file are hidden, so it is read-only. Unlock to view and edit it.'}
            </span>
          </div>
          <Button type="button" size="sm" variant="outline" onClick={() => setUnlockOpen(true)}>
            Unlock to edit
          </Button>
        </div>
      )}
      <EnvUnlockDialog open={unlockOpen} onOpenChange={setUnlockOpen} />

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
