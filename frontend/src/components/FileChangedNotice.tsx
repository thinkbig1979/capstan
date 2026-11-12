import type { ReactNode } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface FileChangedNoticeProps {
  onReload: () => void
  /** What changed and what saving does, for a source that is not a file. */
  children?: ReactNode
}

/**
 * The file changed on disk while the editor holds unsaved edits
 * (agent-os-umig, see hooks/useExternalFileChange.ts). The edits stay; this
 * says that saving will overwrite the newer file and offers to load it.
 */
export function FileChangedNotice({ onReload, children }: FileChangedNoticeProps) {
  return (
    <div
      // role="alert" for the reason RefreshFailedNotice gives: the element and
      // its text mount in the same DOM mutation.
      role="alert"
      className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-sm"
    >
      <div className="flex items-center gap-2">
        <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden="true" />
        <span>
          {children ?? 'This file changed on disk. Reload to see the new version; saving now overwrites it.'}
        </span>
      </div>
      <Button type="button" variant="outline" size="sm" onClick={onReload}>
        Reload
      </Button>
    </div>
  )
}
