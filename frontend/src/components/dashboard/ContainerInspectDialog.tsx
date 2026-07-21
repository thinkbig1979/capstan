import { useState, useMemo, useEffect, useRef } from 'react'
import { resourcesApi } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { AlertCircle, Info, Copy, Check } from 'lucide-react'
import { toast } from 'sonner'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { json } from '@codemirror/lang-json'
import { oneDark } from '@codemirror/theme-one-dark'
import { useUIStore } from '@/stores/uiStore'

// Split into its own chunk (behind React.lazy in ContainersOverviewTab) so the
// codemirror bundle only loads when a user actually opens the Inspect dialog,
// instead of shipping with every visit to the Containers tab.
export function ContainerInspectDialog({
  containerId,
  containerName,
  open,
  onOpenChange,
}: {
  containerId: string
  containerName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [inspectData, setInspectData] = useState<Record<string, unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const { theme } = useUIStore()

  const isDark = useMemo(
    () =>
      theme === 'dark' ||
      (theme === 'system' &&
        typeof window !== 'undefined' &&
        window.matchMedia('(prefers-color-scheme: dark)').matches),
    [theme],
  )

  useEffect(() => {
    if (!open) return
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setCopied(false)

    setLoading(true)

    setError(null)
    resourcesApi
      .inspectContainer(containerId)
      .then((data) => setInspectData(data))
      .catch(() => setError('Failed to inspect container'))
      .finally(() => setLoading(false))
  }, [containerId, open])

  useEffect(() => {
    if (!inspectData || !editorRef.current || loading) return

    viewRef.current?.destroy()
    viewRef.current = null

    const formattedJson = JSON.stringify(inspectData, null, 2)

    const extensions = [
      basicSetup,
      json(),
      EditorState.readOnly.of(true),
      EditorView.theme({
        '&': {
          fontSize: '13px',
          height: '60vh',
        },
        '.cm-scroller': {
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
          overflow: 'auto',
        },
        '.cm-content': {
          caretColor: 'transparent',
        },
        '.cm-cursor': {
          display: 'none',
        },
        '.cm-gutters': {
          backgroundColor: 'transparent',
        },
      }),
    ]

    if (isDark) {
      extensions.push(oneDark)
    }

    const state = EditorState.create({
      doc: formattedJson,
      extensions,
    })

    viewRef.current = new EditorView({
      state,
      parent: editorRef.current,
    })

    return () => {
      viewRef.current?.destroy()
      viewRef.current = null
    }
  }, [inspectData, isDark, loading])

  const handleCopy = async () => {
    if (!inspectData) return
    await navigator.clipboard.writeText(JSON.stringify(inspectData, null, 2))
    setCopied(true)
    toast.success('Copied to clipboard')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl max-h-[85vh] flex flex-col p-0">
        <DialogHeader className="px-6 pt-6 pb-2 shrink-0">
          <DialogTitle className="flex items-center gap-2">
            <Info className="h-5 w-5" />
            Inspect: {containerName}
          </DialogTitle>
          <DialogDescription>
            Container ID: {containerId.slice(0, 12)}
          </DialogDescription>
        </DialogHeader>
        <div className="flex-1 min-h-0 px-6 pb-2 flex items-center justify-end">
          {inspectData && (
            <Button variant="outline" size="sm" onClick={handleCopy} className="h-7 text-xs gap-1.5">
              {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
              {copied ? 'Copied' : 'Copy JSON'}
            </Button>
          )}
        </div>
        <div className="flex-1 min-h-0 px-6 pb-6">
          {loading && (
            <div className="space-y-2">
              {Array.from({ length: 8 }).map((_, i) => (
                <Skeleton key={i} className="h-5 w-full" />
              ))}
            </div>
          )}
          {error && (
            <div className="flex items-center justify-center py-12 text-destructive">
              <AlertCircle className="h-5 w-5 mr-2" />
              {error}
            </div>
          )}
          {inspectData && !loading && (
            <div ref={editorRef} className="rounded-md border overflow-hidden" />
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
