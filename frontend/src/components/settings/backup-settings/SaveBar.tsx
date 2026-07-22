import { Button } from '@/components/ui/button'
import { LoadingSpinner } from '@/components/LoadingSkeleton'

interface SaveBarProps {
  isDirty: boolean
  isSaving: boolean
  onDiscard: () => void
  onSave: () => void
}

export function SaveBar({ isDirty, isSaving, onDiscard, onSave }: SaveBarProps) {
  return (
    <div className="sticky bottom-0 -mx-1 mt-2 flex items-center justify-between gap-3 border-t bg-background/95 px-1 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        {isDirty ? (
          <>
            <span className="h-2 w-2 shrink-0 rounded-full bg-amber-500" aria-hidden="true" />
            Unsaved changes
          </>
        ) : (
          'All changes saved. Fields show the last saved value; edits apply only after you Save.'
        )}
      </p>
      <div className="flex items-center gap-2">
        {isDirty && (
          <Button type="button" variant="ghost" onClick={onDiscard} disabled={isSaving}>
            Discard
          </Button>
        )}
        <Button type="button" onClick={onSave} disabled={isSaving || !isDirty}>
          {isSaving ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Saving…
            </>
          ) : (
            'Save Backup Settings'
          )}
        </Button>
      </div>
    </div>
  )
}
