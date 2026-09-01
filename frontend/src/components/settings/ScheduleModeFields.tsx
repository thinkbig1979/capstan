import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

/**
 * Display order is Monday first because that is how people read a week, but the
 * VALUES are Go weekday ints where Sunday is 0. The two do not line up, and that
 * mismatch is deliberate: the wire contract is Go's, the reading order is not.
 */
const WEEKDAYS: { value: number; label: string; short: string }[] = [
  { value: 1, label: 'Monday', short: 'Mon' },
  { value: 2, label: 'Tuesday', short: 'Tue' },
  { value: 3, label: 'Wednesday', short: 'Wed' },
  { value: 4, label: 'Thursday', short: 'Thu' },
  { value: 5, label: 'Friday', short: 'Fri' },
  { value: 6, label: 'Saturday', short: 'Sat' },
  { value: 0, label: 'Sunday', short: 'Sun' },
]

export interface ScheduleModeFieldsProps {
  mode: 'interval' | 'scheduled'
  onModeChange: (mode: 'interval' | 'scheduled') => void
  /** "HH:MM" */
  time: string
  onTimeChange: (time: string) => void
  /** Go weekday ints, 0 = Sunday. */
  days: number[]
  onDaysChange: (days: number[]) => void
  serverTimezone: string
  serverTimeOffset: string
  intervalLabel: string
  /** Give this node the id `${idPrefix}-interval` so intervalLabel labels it. */
  intervalControl: ReactNode
  /** Namespaces every id, so two instances can sit on one page. */
  idPrefix: string
}

export function ScheduleModeFields({
  mode,
  onModeChange,
  time,
  onTimeChange,
  days,
  onDaysChange,
  serverTimezone,
  serverTimeOffset,
  intervalLabel,
  intervalControl,
  idPrefix,
}: ScheduleModeFieldsProps) {
  const toggleDay = (value: number) => {
    const selected = days.includes(value)
    // Refuse to empty the set: a scheduled run with no days never fires.
    if (selected && days.length === 1) return
    const next = selected ? days.filter((d) => d !== value) : [...days, value]
    // Ascending Go ints, so the emitted array is canonical regardless of click order.
    onDaysChange(next.sort((a, b) => a - b))
  }

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label id={`${idPrefix}-mode-label`}>Schedule</Label>
        <div
          role="radiogroup"
          aria-labelledby={`${idPrefix}-mode-label`}
          className="flex flex-wrap gap-2"
        >
          <Button
            type="button"
            id={`${idPrefix}-mode-interval`}
            role="radio"
            aria-checked={mode === 'interval'}
            variant={mode === 'interval' ? 'default' : 'outline'}
            size="sm"
            onClick={() => onModeChange('interval')}
          >
            Every so often
          </Button>
          <Button
            type="button"
            id={`${idPrefix}-mode-scheduled`}
            role="radio"
            aria-checked={mode === 'scheduled'}
            variant={mode === 'scheduled' ? 'default' : 'outline'}
            size="sm"
            onClick={() => onModeChange('scheduled')}
          >
            At a set time
          </Button>
        </div>
      </div>

      {mode === 'interval' && (
        <div className="space-y-2">
          <Label htmlFor={`${idPrefix}-interval`}>{intervalLabel}</Label>
          {intervalControl}
        </div>
      )}

      {mode === 'scheduled' && (
        <>
          <div className="space-y-2">
            <Label htmlFor={`${idPrefix}-time`}>Time of day</Label>
            <Input
              id={`${idPrefix}-time`}
              type="time"
              value={time}
              onChange={(e) => onTimeChange(e.target.value)}
              className="max-w-[10rem]"
            />
          </div>

          <div className="space-y-2">
            <Label id={`${idPrefix}-days-label`}>Days</Label>
            <div
              role="group"
              aria-labelledby={`${idPrefix}-days-label`}
              className="flex flex-wrap gap-2"
            >
              {WEEKDAYS.map((day) => {
                const selected = days.includes(day.value)
                return (
                  <Button
                    key={day.value}
                    type="button"
                    id={`${idPrefix}-day-${day.value}`}
                    aria-label={day.label}
                    aria-pressed={selected}
                    variant={selected ? 'default' : 'outline'}
                    size="sm"
                    className="w-14"
                    onClick={() => toggleDay(day.value)}
                  >
                    {day.short}
                  </Button>
                )
              })}
            </div>
            <p className="text-xs text-muted-foreground">Pick at least one day.</p>
          </div>
        </>
      )}

      <p className="text-xs text-muted-foreground">
        Times are in {serverTimezone} ({serverTimeOffset}), the server&apos;s own clock.
      </p>
    </div>
  )
}
