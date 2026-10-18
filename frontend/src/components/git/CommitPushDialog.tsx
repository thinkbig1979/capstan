import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { LoadFailedNotice } from '@/components/LoadFailedNotice'
import { ParsedDiffView } from '@/components/git/DiffViewer'
import { useGitChanges, useGitChangesDiff, useGitCommit, useGitPush } from '@/hooks/useGit'
import { isActionResult } from '@/lib/action-result'
import type { GitCommitResult, GitPushResult } from '@/lib/api'
import { causeOf } from '@/lib/error-handler'
import { stringOr } from '@/lib/narrow'
import type { GitChange, GitChangesResult } from '@/types/generated'

/** D136: an env file's diff is never shown, whatever the server would send. */
export const ENV_DIFF_HIDDEN = 'Diff hidden: env files may contain secrets.'

/**
 * Plain sentences for the refusals of POST /git/commit and /git/push
 * (agent-os-k81p.2). A code not listed shows the server's own message, which
 * for those codes names the specific cause (which upstream, which URL).
 */
const CODE_SENTENCES: Record<string, string> = {
  GIT_REMOTE_AHEAD: 'The remote has newer commits. Pull first.',
  GIT_SSH_UNSUPPORTED: 'Pushing over SSH is not supported yet.',
  OPERATION_IN_PROGRESS: 'Another operation is running on a stack in this repository. Try again in a moment.',
  GIT_PUSH_DISABLED: 'Commit and push are turned off for this folder.',
  GIT_IDENTITY_MISSING: 'Set the commit author name and email in Settings > Git first.',
  GIT_DETACHED_HEAD: 'HEAD is detached. Check out a branch first.',
  GIT_REMOTE_UNREACHABLE: 'Could not reach the git remote, or it refused the credentials.',
}

/**
 * The classified code of a rejection. Two shapes reach here: a commit or push
 * refusal is an ActionResult with the code in `details.code` (renderGitResult),
 * and a handleError answer (unknown stack, bad body, every GET error) carries
 * it top-level.
 */
function codeOf(err: unknown): string | undefined {
  if (!err || typeof err !== 'object') return undefined
  const e = err as { code?: unknown; details?: { code?: unknown } }
  const code = e.details?.code ?? e.code
  return typeof code === 'string' ? code : undefined
}

function sentenceFor(code: string | undefined, serverText: string | null): string {
  return (code && CODE_SENTENCES[code]) || serverText || 'The request failed.'
}

const STATUS_WORDS: Record<string, string> = {
  M: 'modified',
  A: 'added',
  D: 'deleted',
  R: 'renamed',
  C: 'copied',
  T: 'type changed',
}

/** git's two-letter porcelain v2 code as a word: the index side, else the work tree's. */
function statusWord(xy: string): string {
  const c = xy[0] !== '.' ? xy[0] : xy[1]
  return STATUS_WORDS[c] ?? xy
}

/**
 * The change that is the stack's compose file. Paths are relative to the
 * repository top level, which can sit above the stack folder, so the shortest
 * path ending in the compose file name is the folder's own one.
 */
function composePath(changes: GitChange[], composeFile: string | undefined): string | null {
  if (!composeFile) return null
  const hits = changes
    .map((c) => c.path)
    .filter((p) => p === composeFile || p.endsWith(`/${composeFile}`))
    .sort((a, b) => a.length - b.length)
  return hits[0] ?? null
}

type Outcome =
  | { kind: 'success'; text: string }
  | { kind: 'partial'; commit: string; text: string }
  | { kind: 'failure'; text: string }

function outcomeOfResult(r: GitCommitResult | GitPushResult): Outcome {
  const d = (r.details ?? {}) as Record<string, unknown>
  if (r.outcome === 'partial') {
    const reason = sentenceFor(stringOr(d.pushCode, '') || undefined, stringOr(d.pushReason, '') || null)
    return { kind: 'partial', commit: stringOr(d.commit, '').slice(0, 7), text: reason }
  }
  if (r.outcome === 'failed') {
    return { kind: 'failure', text: sentenceFor(stringOr(d.code, '') || undefined, r.reason || null) }
  }
  if (r.outcome === 'no_change') return { kind: 'success', text: r.reason || 'Already up to date.' }
  const commit = stringOr(d.commit, '').slice(0, 7)
  return { kind: 'success', text: commit ? `Commit ${commit}: ${r.reason}` : r.reason }
}

function outcomeOfError(err: unknown): Outcome {
  if (isActionResult(err) && err.outcome === 'partial') return outcomeOfResult(err as GitCommitResult)
  const serverText = isActionResult(err) ? err.reason || null : causeOf(err)
  return { kind: 'failure', text: sentenceFor(codeOf(err), serverText) }
}

interface CommitPushDialogProps {
  stackId: string
  /** The stack's compose file name, pre-ticked when it has changes. */
  composeFile?: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * The user-initiated commit and push (agent-os-k81p.3). Opening it only reads
 * (GET /git/changes, /git/changes/diff); a commit or push runs only on a click
 * of the button that names it. The body mounts with the dialog, so nothing is
 * read while it is closed.
 */
export function CommitPushDialog({ stackId, composeFile, open, onOpenChange }: CommitPushDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Commit and push</DialogTitle>
          <DialogDescription>
            Commits the files you tick in this stack&apos;s folder, and pushes the branch when you choose to.
          </DialogDescription>
        </DialogHeader>
        <CommitPushBody stackId={stackId} composeFile={composeFile} onClose={() => onOpenChange(false)} />
      </DialogContent>
    </Dialog>
  )
}

interface BodyProps {
  stackId: string
  composeFile?: string
  onClose: () => void
}

function CommitPushBody({ stackId, composeFile, onClose }: BodyProps) {
  // isFetchedAfterMount: a cached list from before the latest save is not
  // shown while the mount's re-read is in flight.
  const { data, isPending, isFetchedAfterMount, error, refetch } = useGitChanges(stackId)
  const commit = useGitCommit()
  const push = useGitPush()
  const [outcome, setOutcome] = useState<Outcome | null>(null)

  const busy = commit.isPending || push.isPending
  const runCommit = (req: { message: string; files: string[]; confirmEnv: boolean; push: boolean }, onDone: () => void) => {
    setOutcome(null)
    commit.mutate(
      { stackId, ...req },
      {
        onSuccess: (r) => {
          setOutcome(outcomeOfResult(r))
          onDone()
        },
        onError: (err) => setOutcome(outcomeOfError(err)),
      },
    )
  }
  const runPush = () => {
    setOutcome(null)
    push.mutate(
      { stackId },
      {
        onSuccess: (r) => setOutcome(outcomeOfResult(r)),
        onError: (err) => setOutcome(outcomeOfError(err)),
      },
    )
  }

  let body
  if (isPending || !isFetchedAfterMount) {
    body = <p className="py-4 text-sm text-muted-foreground">Loading the changed files…</p>
  } else if (!data) {
    const cause = sentenceFor(codeOf(error), causeOf(error))
    body = (
      <div className="space-y-2">
        <LoadFailedNotice what="the changed files" consequence="Commit and push are disabled until they load." onRetry={() => void refetch()} />
        <p className="text-sm text-muted-foreground">{cause}</p>
      </div>
    )
  } else {
    body = (
      <ChangesForm
        // A new change list (after a commit) starts a fresh selection.
        key={data.changes.map((c) => c.path).join('\0')}
        stackId={stackId}
        composeFile={composeFile}
        result={data}
        busy={busy}
        onCommit={runCommit}
        onPush={runPush}
      />
    )
  }

  return (
    <div className="space-y-4">
      {outcome && <OutcomeNotice outcome={outcome} />}
      {body}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Close
        </Button>
      </DialogFooter>
    </div>
  )
}

function OutcomeNotice({ outcome }: { outcome: Outcome }) {
  if (outcome.kind === 'success') {
    return (
      <div role="status" className="flex items-start gap-2 rounded-md border border-success/30 bg-success/10 px-3 py-2 text-sm">
        <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-success" aria-hidden="true" />
        <p>{outcome.text}</p>
      </div>
    )
  }
  if (outcome.kind === 'partial') {
    return (
      <div role="alert" className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
        <div className="space-y-1">
          <p className="font-medium">
            Committed{outcome.commit ? ` ${outcome.commit}` : ''}, but the push failed. The commit is kept.
          </p>
          <p>{outcome.text}</p>
        </div>
      </div>
    )
  }
  return (
    <div role="alert" className="flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-destructive" aria-hidden="true" />
      <p>{outcome.text}</p>
    </div>
  )
}

interface ChangesFormProps {
  stackId: string
  composeFile?: string
  result: GitChangesResult
  busy: boolean
  onCommit: (req: { message: string; files: string[]; confirmEnv: boolean; push: boolean }, onDone: () => void) => void
  onPush: () => void
}

function ChangesForm({ stackId, composeFile, result, busy, onCommit, onPush }: ChangesFormProps) {
  const { changes } = result
  const compose = composePath(changes, composeFile)
  // Env files start unticked; only the compose file starts ticked.
  const [ticked, setTicked] = useState<Set<string>>(() => new Set(compose ? [compose] : []))
  const [envConfirmed, setEnvConfirmed] = useState(false)
  const [message, setMessage] = useState('')
  const [messageTouched, setMessageTouched] = useState(false)
  const [selected, setSelected] = useState<string | null>(
    () => compose ?? changes.find((c) => !c.isEnv)?.path ?? null,
  )

  const toggle = (path: string) => {
    const next = new Set(ticked)
    if (next.has(path)) next.delete(path)
    else next.add(path)
    setTicked(next)
    // A confirm given for one selection does not carry over to the next.
    if (!changes.some((c) => c.isEnv && next.has(c.path))) setEnvConfirmed(false)
  }

  // What the server refuses, mirrored so a button never offers what will fail.
  const canCommit = result.pushEnabled && result.identityConfigured && !result.detached
  const canPush = result.pushEnabled && !result.detached && result.hasUpstream
  const envTicked = changes.some((c) => c.isEnv && ticked.has(c.path))
  const messageBlank = message.trim() === ''
  const ready = canCommit && ticked.size > 0 && !messageBlank && (!envTicked || envConfirmed) && !busy

  const submit = (withPush: boolean) => {
    if (!ready || (withPush && !canPush)) return
    onCommit(
      { message, files: changes.filter((c) => ticked.has(c.path)).map((c) => c.path), confirmEnv: envTicked && envConfirmed, push: withPush },
      () => {
        setMessage('')
        setMessageTouched(false)
      },
    )
  }

  const showPush = result.hasUpstream && result.ahead > 0
  const selectedChange = changes.find((c) => c.path === selected) ?? null

  return (
    <div className="space-y-4">
      <BlockedNotes result={result} />

      {changes.length === 0 ? (
        <p className="text-sm text-muted-foreground">No uncommitted changes in this folder&apos;s tracked files.</p>
      ) : (
        <>
          <fieldset className="space-y-1">
            <legend className="mb-1 text-sm font-medium">Files to commit</legend>
            {changes.map((c) => (
              <div key={c.path} className="flex items-center gap-2 text-sm">
                <label className="flex min-w-0 flex-1 items-center gap-2">
                  <input
                    type="checkbox"
                    className="h-4 w-4 accent-[var(--primary)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                    checked={ticked.has(c.path)}
                    onChange={() => toggle(c.path)}
                  />
                  <span className="truncate font-mono">{c.origPath ? `${c.origPath} → ${c.path}` : c.path}</span>
                  <span className="text-xs text-muted-foreground">{statusWord(c.status)}</span>
                  {c.isEnv && <span className="text-xs text-warning">env file, may contain secrets</span>}
                </label>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-7 text-xs"
                  aria-pressed={selected === c.path}
                  aria-label={`Show diff of ${c.path}`}
                  onClick={() => setSelected(c.path)}
                >
                  Diff
                </Button>
              </div>
            ))}
          </fieldset>

          {envTicked && (
            <div className="space-y-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm">
              <p className="flex items-center gap-1.5">
                <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
                An env file is ticked. Env files often hold passwords and tokens, and a commit puts them in the repository history.
              </p>
              <label className="flex items-center gap-2 font-medium">
                <input
                  type="checkbox"
                  className="h-4 w-4 accent-[var(--primary)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  checked={envConfirmed}
                  onChange={(e) => setEnvConfirmed(e.target.checked)}
                />
                Commit the env file anyway
              </label>
            </div>
          )}

          {selectedChange && <ChangeDiff stackId={stackId} change={selectedChange} />}

          <div className="space-y-1">
            <Label htmlFor="commit-message">Commit message</Label>
            <Textarea
              id="commit-message"
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              onBlur={() => setMessageTouched(true)}
              aria-invalid={messageTouched && messageBlank}
              aria-describedby={messageTouched && messageBlank ? 'commit-message-error' : undefined}
              rows={3}
            />
            {messageTouched && messageBlank && (
              <p id="commit-message-error" className="text-xs text-destructive">
                A commit message is required.
              </p>
            )}
          </div>
        </>
      )}

      <div className="flex flex-wrap justify-end gap-2">
        {showPush && (
          <Button type="button" variant="outline" disabled={!canPush || busy} onClick={onPush}>
            {result.ahead === 1 ? 'Push 1 commit' : `Push ${result.ahead} commits`}
          </Button>
        )}
        {changes.length > 0 && (
          <>
            <Button type="button" variant="outline" disabled={!ready} onClick={() => submit(false)}>
              {busy ? 'Working…' : 'Commit'}
            </Button>
            <Button type="button" disabled={!ready || !canPush} onClick={() => submit(true)}>
              {busy ? 'Working…' : 'Commit and push'}
            </Button>
          </>
        )}
      </div>
    </div>
  )
}

/** Each reason a commit or push cannot run, with where to fix it. */
function BlockedNotes({ result }: { result: GitChangesResult }) {
  const notes: ReactNode[] = []
  if (!result.pushEnabled) {
    notes.push(
      <li key="switch">
        Commit and push are off for this folder. Turn on &ldquo;Allow commit and push&rdquo; under the stack&apos;s
        git chip, in Git Credentials.
      </li>,
    )
  }
  if (!result.identityConfigured) {
    notes.push(
      <li key="identity">
        No commit author is set.{' '}
        <Link to="/settings/git" className="underline underline-offset-2">
          Set the author name and email in Settings &gt; Git
        </Link>
        .
      </li>,
    )
  }
  if (result.detached) {
    notes.push(
      <li key="detached">HEAD is detached, so nothing can be committed or pushed. Check out a branch; Capstan does not create one.</li>,
    )
  } else if (!result.hasUpstream) {
    notes.push(
      <li key="upstream">
        Branch {result.branch} has no upstream, so it can be committed but not pushed. Capstan does not set an upstream.
      </li>,
    )
  }
  if (notes.length === 0) return null
  return (
    <ul aria-label="Why some actions are unavailable" className="list-disc space-y-1 rounded-md border bg-muted/50 py-2 pl-8 pr-3 text-sm">
      {notes}
    </ul>
  )
}

/** The selected file's diff; an env file's is never requested (D136). */
function ChangeDiff({ stackId, change }: { stackId: string; change: GitChange }) {
  const { data, isPending, isFetchedAfterMount, error, refetch } = useGitChangesDiff(stackId, change.isEnv ? null : change.path)
  if (change.isEnv || data?.hidden) {
    return <p className="rounded-md border px-3 py-2 text-sm text-muted-foreground">{ENV_DIFF_HIDDEN}</p>
  }
  if (isPending || !isFetchedAfterMount) return <p className="text-sm text-muted-foreground">Loading diff…</p>
  if (!data) {
    return (
      <div className="space-y-1">
        <LoadFailedNotice what="the diff" onRetry={() => void refetch()} />
        <p className="text-sm text-muted-foreground">{sentenceFor(codeOf(error), causeOf(error))}</p>
      </div>
    )
  }
  return <ParsedDiffView diff={data.diff} />
}
