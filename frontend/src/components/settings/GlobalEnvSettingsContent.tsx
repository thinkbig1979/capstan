import { Button } from '@/components/ui/button'
import { TableSearch } from '@/components/ui/table-search'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { Badge } from '@/components/ui/badge'
import { Plus, Save } from 'lucide-react'
import { useEnvUnlockStore } from '@/stores/envUnlockStore'
import { EnvUnlockDialog } from '@/components/EnvUnlockDialog'
import { EnvUnlockStatus } from '@/components/EnvUnlockStatus'
import { HelpHint } from '@/components/ui/help-hint'
import { useAuth } from '@/hooks/useAuth'
import { GlobalEnvTable } from './global-env/GlobalEnvTable'
import { GlobalEnvCards } from './global-env/GlobalEnvCards'
import { useGlobalEnvReveal } from './global-env/useGlobalEnvReveal'
import { useGlobalEnvVars } from './global-env/useGlobalEnvVars'

export function GlobalEnvSettingsContent() {
  const { authDisabled } = useAuth()
  const isUnlocked = useEnvUnlockStore((s) => s.isUnlocked)
  const unlockedUntil = useEnvUnlockStore((s) => s.unlockedUntil)

  const {
    visible,
    setVisible,
    unlockDialogOpen,
    toggleVisible,
    handleUnlocked,
    handleDialogOpenChange,
  } = useGlobalEnvReveal(authDisabled, isUnlocked, unlockedUntil)

  const {
    isLoading,
    isError,
    locked,
    vars,
    dirty,
    query,
    setQuery,
    filtered,
    handleChange,
    handleAdd,
    handleDelete,
    handleSave,
    isSaving,
  } = useGlobalEnvVars(setVisible)

  if (isLoading) {
    return <div className="py-4"><LoadingSpinner /></div>
  }

  if (isError) {
    return (
      <div className="py-4 text-sm text-destructive">
        Failed to load global environment variables.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-4">
        <p className="text-sm text-muted-foreground">
          Variables here apply to every stack. Docker Compose loads them before each
          stack&apos;s own <code className="text-xs">.env</code>, which can still override them.
          Changes take effect on the next stack restart.
        </p>
        <div className="flex items-center gap-2 shrink-0">
          <HelpHint label="Hidden values" title="Hidden values" align="end">
            <p>
              Keys that look like secrets (passwords, tokens, API keys) stay masked in the table.
            </p>
            <p>
              Revealing one starts a short unlock session protected by your password, and values
              re-hide when it ends.
            </p>
          </HelpHint>
          <EnvUnlockStatus />
          {locked && (
            <Badge variant="outline" className="text-xs whitespace-nowrap">
              Locked
            </Badge>
          )}
          {dirty && (
            <Badge variant="secondary" className="text-xs whitespace-nowrap">
              Unsaved changes
            </Badge>
          )}
        </div>
      </div>
      <EnvUnlockDialog
        open={unlockDialogOpen}
        onOpenChange={handleDialogOpenChange}
        onUnlocked={handleUnlocked}
      />

      {vars.length > 0 && (
        <TableSearch
          value={query}
          onChange={setQuery}
          placeholder="Filter by key or value…"
          className="w-full sm:w-64"
        />
      )}

      <GlobalEnvTable
        hasVars={vars.length > 0}
        filtered={filtered}
        query={query}
        visible={visible}
        onChange={handleChange}
        onToggleVisible={toggleVisible}
        onDelete={handleDelete}
      />

      <GlobalEnvCards
        hasVars={vars.length > 0}
        filtered={filtered}
        query={query}
        visible={visible}
        onChange={handleChange}
        onToggleVisible={toggleVisible}
        onDelete={handleDelete}
      />

      <div className="flex justify-between">
        <Button type="button" variant="outline" onClick={handleAdd}>
          <Plus className="mr-2 h-4 w-4" />
          Add Variable
        </Button>
        <Button
          type="button"
          onClick={handleSave}
          disabled={isSaving || !dirty || locked}
          title={locked ? 'Unlock with your password to edit global environment variables' : undefined}
        >
          {isSaving ? (
            <>
              <span className="mr-2"><LoadingSpinner size="small" /></span>
              Saving...
            </>
          ) : (
            <>
              <Save className="mr-2 h-4 w-4" />
              Save
            </>
          )}
        </Button>
      </div>
    </div>
  )
}
