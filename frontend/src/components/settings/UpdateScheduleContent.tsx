import { useState } from 'react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useUpdateSettings, useUpdateUpdateSettings } from '@/hooks/useResources'
import { HelpHint } from '@/components/ui/help-hint'
import { formatDateFull } from '@/lib/format'
import { ScheduleModeFields } from '@/components/settings/ScheduleModeFields'
import { AlertCircle } from 'lucide-react'
import { toast } from 'sonner'
import { settingsSaveFault } from '@/lib/settings-save-fault'
import { causeOf, presentFault, toastInvalid } from '@/lib/error-handler'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const PRESETS = ['0', '60', '360', '720', '1440']

// Fallbacks for a server that predates the apply-time schedule: it sends no
// applyMode/applyTime/applyDays, and the fields below still have to render.
const DEFAULT_APPLY_TIME = '03:00'
const DEFAULT_APPLY_DAYS = [0, 1, 2, 3, 4, 5, 6]

export function UpdateScheduleContent() {
  const { data: settings, isLoading, isError, error, refetch } = useUpdateSettings()
  const updateSettingsMutation = useUpdateUpdateSettings()

  const [initialized, setInitialized] = useState(false)
  const [scanPreset, setScanPreset] = useState<string>('0')
  const [customMinutes, setCustomMinutes] = useState<number>(60)
  const [globalAutoUpdate, setGlobalAutoUpdate] = useState(false)
  const [applyMode, setApplyMode] = useState<'immediate' | 'scheduled'>('immediate')
  const [applyTime, setApplyTime] = useState(DEFAULT_APPLY_TIME)
  const [applyDays, setApplyDays] = useState<number[]>(DEFAULT_APPLY_DAYS)
  // The three apply fields are optional on the wire. Until the admin touches the
  // schedule we leave them out of every save, so editing the scan interval cannot
  // overwrite a server-side schedule with this screen's defaults.
  const [applyTouched, setApplyTouched] = useState(false)

  // Hydrate local editable state from the query result once it loads.
  // Adjusted during render (rather than in an effect) — `initialized` makes
  // this a one-shot assignment, not an unbounded render loop.
  if (settings && !initialized) {
    setInitialized(true)
    if (PRESETS.includes(String(settings.scanIntervalMinutes))) {
      setScanPreset(String(settings.scanIntervalMinutes))
    } else {
      setScanPreset('custom')
      setCustomMinutes(settings.scanIntervalMinutes)
    }
    setGlobalAutoUpdate(settings.globalAutoUpdate)
    setApplyMode(settings.applyMode ?? 'immediate')
    setApplyTime(settings.applyTime ?? DEFAULT_APPLY_TIME)
    setApplyDays(settings.applyDays?.length ? settings.applyDays : DEFAULT_APPLY_DAYS)
  }

  const scanInterval = settings?.scanIntervalMinutes ?? 0
  const effectivePreset = initialized ? scanPreset : (PRESETS.includes(String(scanInterval)) ? String(scanInterval) : 'custom')
  const effectiveCustom = initialized ? customMinutes : scanInterval
  const effectiveAutoUpdate = initialized ? globalAutoUpdate : (settings?.globalAutoUpdate ?? false)
  const effectiveApplyMode = initialized ? applyMode : (settings?.applyMode ?? 'immediate')
  const effectiveApplyTime = initialized ? applyTime : (settings?.applyTime ?? DEFAULT_APPLY_TIME)
  const effectiveApplyDays = initialized
    ? applyDays
    : (settings?.applyDays?.length ? settings.applyDays : DEFAULT_APPLY_DAYS)
  const effectiveScanMinutes = effectivePreset === 'custom' ? effectiveCustom : parseInt(effectivePreset, 10)

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoadingSpinner size="small" />
        Loading update settings...
      </div>
    )
  }

  // agent-os-fxhl. A FAILED query used to fall straight through to the form,
  // where `settings?.globalAutoUpdate ?? false` turned "not known" into
  // "deliberately off" and the master switch rendered as though an operator had
  // turned it off.
  //
  // WHY AN EARLY RETURN rather than a banner over a live form, or a disabled
  // one: this surface SUBMITS. save() builds its payload from
  // effectiveAutoUpdate, effectiveScanMinutes, effectiveApplyMode,
  // effectiveApplyTime AND effectiveApplyDays -- every one of them a
  // `?? <default>` over `settings` -- so a failed GET does not merely mis-render
  // one switch: any toggle the operator touches writes back FIVE fields that
  // were never read, silently replacing the stored schedule with this screen's
  // defaults. A banner is the bug with a warning sticker (the switch still
  // reads `?? false` and is still clickable); disabling shows the same false
  // values greyed, which says "not editable now" rather than "this was never
  // read". agent-os-bueb's lock-and-explain is a precedent, not a default: its
  // surface is READ-ONLY, so leaving the value visible costs nothing there.
  // Here the value is an input, so leaving it visible IS the defect.
  //
  // The component already answers "state not known yet" by returning early
  // instead of rendering the form against placeholders. "State not known at
  // all" gets the same answer for a stronger reason.
  //
  // `!settings`, NOT bare `isError`. TanStack sets status 'error' when a
  // REFETCH fails on a query that already has data -- which is why it ships
  // isLoadingError (error, no data) beside isRefetchError (error, data
  // present), with isError true for both. That path is routine here, not
  // theoretical: query-client.ts sets staleTime 30_000 and
  // refetchOnWindowFocus true, and gates retries on isAutoRetryable, which is
  // false for a 500. So tabbing away for half a minute and back can 500 on the
  // focus refetch, and a bare isError guard would throw away a fully populated,
  // CORRECT form. The stale values are real values the server did send; keeping
  // them is the right degradation, and it is what this screen did before
  // agent-os-fxhl. Spelled `!settings` rather than `isLoadingError` because it
  // states the invariant the branch actually depends on -- there is nothing to
  // render -- and cannot be transposed with its sibling flag by a reader.
  if (isError && !settings) {
    const cause = causeOf(error)
    return (
      <div className="flex items-start gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-3">
        <AlertCircle className="h-4 w-4 mt-0.5 text-destructive shrink-0" />
        <div className="space-y-2">
          <p className="text-sm font-medium">Could not load update settings.</p>
          {cause && <p className="text-sm text-muted-foreground">{cause}</p>}
          <p className="text-sm text-muted-foreground">
            Nothing is known about the current schedule, so the form is not shown rather than
            shown with defaults it would submit.
          </p>
          <Button variant="outline" size="sm" onClick={() => void refetch()}>
            Retry
          </Button>
        </div>
      </div>
    )
  }

  type ApplyUpdates = {
    applyMode?: 'immediate' | 'scheduled'
    applyTime?: string
    applyDays?: number[]
  }

  const save = (updates: { scanIntervalMinutes?: number; globalAutoUpdate?: boolean } & ApplyUpdates) => {
    const minutes = updates.scanIntervalMinutes ?? (effectivePreset === 'custom' ? effectiveCustom : parseInt(effectivePreset, 10))
    const autoUpdate = updates.globalAutoUpdate ?? effectiveAutoUpdate
    if (minutes > 0 && minutes < 15) {
      toastInvalid('Custom interval must be at least 15 minutes')
      return
    }
    const payload: { scanIntervalMinutes: number; globalAutoUpdate: boolean } & ApplyUpdates = {
      scanIntervalMinutes: minutes,
      globalAutoUpdate: autoUpdate,
    }
    const touchesSchedule =
      updates.applyMode !== undefined ||
      updates.applyTime !== undefined ||
      updates.applyDays !== undefined
    if (applyTouched || touchesSchedule) {
      payload.applyMode = updates.applyMode ?? effectiveApplyMode
      payload.applyTime = updates.applyTime ?? effectiveApplyTime
      payload.applyDays = updates.applyDays ?? effectiveApplyDays
    }
    updateSettingsMutation.mutate(payload, {
      onSuccess: () => toast.success('Settings saved'),
      // Takes the error (agent-os-zlw0). It used to take nothing, so the five
      // distinct 400s UpdateUpdateSettings mints — bad body, interval below 15,
      // invalid apply mode, unparseable apply time, bad weekday list — were
      // unreadable in principle rather than merely unread. The generic sentence
      // stays as the TITLE, so a failure with no usable cause renders exactly
      // what it rendered before.
      //
      // THE ARITY IS DELIBERATE, and this is the one place it is explained —
      // the sibling sites repeat the shape, not the reason. The obvious
      // spelling is
      //     toast.error(TITLE, cause ? { description: cause } : undefined)
      // which makes EVERY call two-argument. Sonner renders that identically,
      // so no operator can tell the two apart; a vitest spy can, and the
      // unconditional form breaks four pre-existing assertions of the form
      //     expect(toast.error).toHaveBeenCalledWith('<generic title>')
      // at UpdateScheduleContent.test.tsx, HistoryRetentionSection.test.tsx,
      // GitSettingsContent.test.tsx and BackupSettingsContent.test.tsx.
      // Each rejects with a bare `new Error('boom')`, which carries no `code`,
      // so settingsSaveFault returns null — meaning those four ALREADY assert
      // the thing this change most needs pinned: a failure with no usable cause
      // still shows the generic sentence and nothing else. They were written
      // before this change by someone with no stake in it, so left untouched
      // and green they are independent witnesses.
      //
      // agent-os-5g8a moved the branch into presentFault rather than deleting
      // it: the helper still emits a ONE-argument call when the cause is null,
      // so all four witnesses stay green and stay untouched.
      onError: (error) => {
        // presentFault, NOT presentError: the cause is read by settingsSaveFault,
        // which is CODE-keyed and deliberately not classifyError. The full
        // argument, measured, is in presentFault's docblock.
        presentFault('Failed to save settings', settingsSaveFault(error))
      },
    })
  }

  const handlePresetChange = (value: string) => {
    setScanPreset(value)
    if (value !== 'custom') {
      save({ scanIntervalMinutes: parseInt(value, 10) })
    }
  }

  const handleCustomBlur = () => {
    if (scanPreset === 'custom') {
      save({ scanIntervalMinutes: effectiveCustom })
    }
  }

  const handleAutoUpdateChange = (checked: boolean) => {
    setGlobalAutoUpdate(checked)
    save({ globalAutoUpdate: checked })
  }

  const handleApplyModeChange = (mode: 'interval' | 'scheduled') => {
    const next = mode === 'interval' ? 'immediate' : 'scheduled'
    setApplyMode(next)
    setApplyTouched(true)
    save({ applyMode: next })
  }

  const handleApplyTimeChange = (time: string) => {
    setApplyTime(time)
    setApplyTouched(true)
    // A time input reports an empty string while the admin is still part-way
    // through typing, so only a complete HH:MM is worth sending.
    if (/^\d{2}:\d{2}$/.test(time)) {
      save({ applyTime: time })
    }
  }

  const handleApplyDaysChange = (days: number[]) => {
    setApplyDays(days)
    setApplyTouched(true)
    save({ applyDays: days })
  }

  const stats = settings?.autoUpdateStats

  return (
    <div className="space-y-6">
      <div className="space-y-4">
        <div className="flex items-center gap-1.5">
          <h3 className="text-lg font-medium">Scan for Image Updates</h3>
          <HelpHint label="Update scans" title="Update scans" side="right">
            <p>
              A scan checks each running container&apos;s image against its registry and lists
              anything newer on the Updates tab.
            </p>
            <p>It only looks. Nothing updates unless you trigger it or turn on auto-update.</p>
          </HelpHint>
        </div>
        <div className="space-y-2">
          <Label htmlFor="scan-interval">Scan Interval</Label>
          <Select value={effectivePreset} onValueChange={handlePresetChange}>
            <SelectTrigger id="scan-interval" className="w-full max-w-xs" aria-label="Scan Interval">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="0">Disabled</SelectItem>
              <SelectItem value="60">Every hour</SelectItem>
              <SelectItem value="360">Every 6 hours</SelectItem>
              <SelectItem value="720">Every 12 hours</SelectItem>
              <SelectItem value="1440">Every 24 hours</SelectItem>
              <SelectItem value="custom">Custom</SelectItem>
            </SelectContent>
          </Select>
        </div>

        {effectivePreset === 'custom' && (
          <div className="space-y-2">
            <Label htmlFor="custom-minutes">Custom interval (minutes)</Label>
            <Input
              id="custom-minutes"
              type="number"
              min={15}
              max={10080}
              value={effectiveCustom}
              onChange={(e) => setCustomMinutes(parseInt(e.target.value, 10) || 0)}
              onBlur={handleCustomBlur}
              className="max-w-xs"
            />
            <p className="text-xs text-muted-foreground">Minimum 15 minutes</p>
          </div>
        )}

        {settings?.lastScanAt && (
          <p className="text-sm text-muted-foreground">
            Last scanned: {formatDateFull(settings.lastScanAt)}
          </p>
        )}
        {!settings?.lastScanAt && (
          <p className="text-sm text-muted-foreground">Last scanned: Never</p>
        )}
        {settings?.lastScanError && (
          <p className="text-sm text-destructive">
            Last scan error: {settings.lastScanError}
          </p>
        )}
      </div>

      <div className="space-y-4 pt-4 border-t">
        <h3 className="text-lg font-medium">Auto-Update</h3>
        <div className="flex items-center gap-3">
          <Switch
            id="global-auto-update"
            checked={effectiveAutoUpdate}
            onCheckedChange={handleAutoUpdateChange}
          />
          <div>
            <Label htmlFor="global-auto-update">Enable Auto-Update</Label>
            <p className="text-xs text-muted-foreground">
              Master switch for automatic container updates. When on, you can opt in individual containers or stacks.
            </p>
          </div>
        </div>
        {effectiveAutoUpdate && (
          <>
            <ScheduleModeFields
              mode={effectiveApplyMode === 'scheduled' ? 'scheduled' : 'interval'}
              onModeChange={handleApplyModeChange}
              time={effectiveApplyTime}
              onTimeChange={handleApplyTimeChange}
              days={effectiveApplyDays}
              onDaysChange={handleApplyDaysChange}
              serverTimezone={settings?.serverTimezone ?? 'UTC'}
              serverTimeOffset={settings?.serverTimeOffset ?? '+00:00'}
              intervalLabel="Apply as soon as an update is found"
              /*
               * ScheduleModeFields renders intervalLabel inside a Label whose htmlFor is
               * hardcoded to `${idPrefix}-interval`, so passing no control would leave that
               * reference dangling. There is no second cadence control to put here — the
               * Scan Interval select above already governs it — so this static line takes
               * the id and says so, instead of duplicating the select.
               */
              intervalControl={
                <p id="update-apply-interval" className="text-xs text-muted-foreground">
                  Scans keep running on the interval above. Anything they find is applied
                  right away.
                </p>
              }
              idPrefix="update-apply"
            />
            {effectiveApplyMode === 'scheduled' && effectiveScanMinutes === 0 && (
              <div className="flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/10 p-3">
                <AlertCircle className="h-4 w-4 mt-0.5 text-warning shrink-0" />
                <p className="text-sm text-warning">
                  Scheduled updates can&apos;t run while scanning is disabled: nothing detects
                  new images to apply. Set a scan interval above.
                </p>
              </div>
            )}
            {settings?.nextApplyAt && (
              <p className="text-sm text-muted-foreground">
                Next scheduled update: {formatDateFull(settings.nextApplyAt)}
              </p>
            )}
            <div className="flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/10 p-3">
              <AlertCircle className="h-4 w-4 mt-0.5 text-warning shrink-0" />
              <p className="text-sm text-warning">
                {effectiveApplyMode === 'scheduled'
                  ? 'Only containers and stacks with auto-update turned on will be updated. Scans keep running on the interval above, and anything they find is applied at the scheduled time, which may cause brief service interruption.'
                  : 'Only containers and stacks with auto-update turned on will be updated. Updates happen when new images are detected during scans and may cause brief service interruption.'}
              </p>
            </div>
          </>
        )}
        {!effectiveAutoUpdate && (
          <div className="flex items-start gap-2 rounded-lg border bg-muted/50 p-3">
            <AlertCircle className="h-4 w-4 mt-0.5 text-muted-foreground shrink-0" />
            <p className="text-sm text-muted-foreground">
              Auto-update is off. Per-container and per-stack auto-update toggles are locked until this is enabled.
            </p>
          </div>
        )}
      </div>

      {stats && (
        <div className="space-y-2 pt-4 border-t">
          <h3 className="text-sm font-medium text-muted-foreground">Statistics</h3>
          <p className="text-sm">
            {stats.enabledContainers} container{stats.enabledContainers !== 1 ? 's' : ''} with auto-update enabled
          </p>
          <p className="text-sm text-muted-foreground">
            {stats.updatesLast7Days} update{stats.updatesLast7Days !== 1 ? 's' : ''} in the last 7 days,{' '}
            {stats.updatesLast30Days} in the last 30 days
          </p>
        </div>
      )}
    </div>
  )
}
