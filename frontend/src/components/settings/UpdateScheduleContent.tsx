import { useState, useEffect } from 'react'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { LoadingSpinner } from '@/components/LoadingSkeleton'
import { useUpdateSettings, useUpdateUpdateSettings } from '@/hooks/useResources'
import { HelpHint } from '@/components/ui/help-hint'
import { formatDateFull } from '@/lib/format'
import { AlertCircle } from 'lucide-react'
import { toast } from 'sonner'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

const PRESETS = ['0', '60', '360', '720', '1440']

export function UpdateScheduleContent() {
  const { data: settings, isLoading } = useUpdateSettings()
  const updateSettingsMutation = useUpdateUpdateSettings()

  const [initialized, setInitialized] = useState(false)
  const [scanPreset, setScanPreset] = useState<string>('0')
  const [customMinutes, setCustomMinutes] = useState<number>(60)
  const [globalAutoUpdate, setGlobalAutoUpdate] = useState(false)

  useEffect(() => {
    if (settings && !initialized) {
      if (PRESETS.includes(String(settings.scanIntervalMinutes))) {
        setScanPreset(String(settings.scanIntervalMinutes))
      } else {
        setScanPreset('custom')
        setCustomMinutes(settings.scanIntervalMinutes)
      }
      setGlobalAutoUpdate(settings.globalAutoUpdate)
      setInitialized(true)
    }
  }, [settings, initialized])

  const scanInterval = settings?.scanIntervalMinutes ?? 0
  const effectivePreset = initialized ? scanPreset : (PRESETS.includes(String(scanInterval)) ? String(scanInterval) : 'custom')
  const effectiveCustom = initialized ? customMinutes : scanInterval
  const effectiveAutoUpdate = initialized ? globalAutoUpdate : (settings?.globalAutoUpdate ?? false)

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoadingSpinner size="small" />
        Loading update settings...
      </div>
    )
  }

  const save = (updates: { scanIntervalMinutes?: number; globalAutoUpdate?: boolean }) => {
    const minutes = updates.scanIntervalMinutes ?? (effectivePreset === 'custom' ? effectiveCustom : parseInt(effectivePreset, 10))
    const autoUpdate = updates.globalAutoUpdate ?? effectiveAutoUpdate
    if (minutes > 0 && minutes < 15) {
      toast.error('Custom interval must be at least 15 minutes')
      return
    }
    updateSettingsMutation.mutate(
      { scanIntervalMinutes: minutes, globalAutoUpdate: autoUpdate },
      {
        onSuccess: () => toast.success('Settings saved'),
        onError: () => toast.error('Failed to save settings'),
      },
    )
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
            <SelectTrigger id="scan-interval" className="w-full max-w-xs">
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
          <div className="flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/10 p-3">
            <AlertCircle className="h-4 w-4 mt-0.5 text-warning shrink-0" />
            <p className="text-sm text-warning">
              Only containers and stacks with auto-update turned on will be updated. Updates happen when new images are detected during scans and may cause brief service interruption.
            </p>
          </div>
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
