import { useRef, useState } from 'react'
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
import { EnvLockedNotice } from '@/components/EnvLockedNotice'
import { useEnvLockedNow } from '@/hooks/useEnvLockedNow'
import { LOCKED_COMPOSE_NOTICE } from '@/lib/locked-copy'

/**
 * One load of the compose file. `masked` is true when the server masked
 * secrets in `text` for a session that has not unlocked env values
 * (agent-os-w6wx): the editor is then read-only, and a save would be refused.
 */
interface ComposeLoad {
  text: string
  masked: boolean
  /**
   * The response carried a body. An empty file is a file to load
   * (agent-os-wn5h); only a body that never arrived is skipped.
   */
  present: boolean
  /** The server answered without an unlock token in play. */
  locked: boolean
}

/** What the view shows in place of a file loaded while unlocked, once locked. */
const HIDDEN_DOC = { text: '' }

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
      // '' rather than undefined, with `present` telling an absent body
      // (skipped by the hydrate below) from an empty file.
      const body = compose as { content?: unknown; maskedValues?: unknown; locked?: unknown } | undefined
      const load: ComposeLoad = {
        text: stringOr(body?.content, ''),
        present: typeof body?.content === 'string',
        masked: typeof body?.maskedValues === 'number' && body.maskedValues > 0,
        locked: body?.locked === true,
      }
      return load
    },
  })
  const lockedNow = useEnvLockedNow()
  // After a lock the last payload is still the one loaded while unlocked, with
  // the secrets in the clear, until the lock's refetch lands, and for good if
  // it fails (agent-os-xz6h). A clean view drops that text and waits for the
  // masked file; unsaved edits stay, as a re-lock keeps them (agent-os-w6wx).
  const staleUnlocked = lockedNow && data !== undefined && !data.locked
  const hideText = staleUnlocked && content === lastSaved
  const masked = (data?.masked ?? false) || staleUnlocked
  const [unlockOpen, setUnlockOpen] = useState(false)
  // The view shows "***" where secrets are. Edits kept across a re-lock
  // stay editable: they hold no "***", and the server refuses their save
  // until unlock.
  const readOnly = (masked && loaded.masked) || hideText
  // Locked, the server refuses save and lint (agent-os-ly6i): their replies
  // pass this text through the secret masking, so a starred guess would name
  // a secret. Either signal locks: fail closed.
  const envLocked = lockedNow || data?.locked === true
  const saveBlocked = readOnly || envLocked

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
    doc: hideText ? HIDDEN_DOC.text : loaded.text,
    // A fresh object per load: the same text loaded again still replaces a
    // view that has moved on (load A, edit and save A', A comes back).
    docKey: hideText ? HIDDEN_DOC : loaded,
    ariaLabel: 'Compose file editor',
    readOnly,
    // Ctrl+S is a no-op while read-only: the view holds masked text.
    onSave: () => {
      if (!saveBlocked) handleSaveRef.current()
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
    // A file held behind the notice was served unlocked: a lock drops it, as
    // the env editors do (agent-os-9wp3).
    lockedNow,
    hydrate: (next) => {
      // An absent body (see the queryFn) is not a file to load.
      if (!next.present) return
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
        onSave={() => {
          if (!saveBlocked) handleSave()
        }}
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
        readOnly={readOnly}
        locked={envLocked}
      />

      {(masked || envLocked) && (
        <EnvLockedNotice onUnlock={() => setUnlockOpen(true)} unlockLabel="Unlock to edit">
          {hasUnsavedChanges
            ? 'Values locked again; unlock to save your edits.'
            : LOCKED_COMPOSE_NOTICE}
        </EnvLockedNotice>
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
