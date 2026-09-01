import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within } from '@testing-library/react'
import { ScheduleModeFields } from '../ScheduleModeFields'

/**
 * Presentational component, so there is no API layer to mock. The one thing
 * worth guarding hard is the Monday-first display over Sunday-is-0 values.
 */

const onModeChange = vi.fn()
const onTimeChange = vi.fn()
const onDaysChange = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
})

type Overrides = Partial<Parameters<typeof ScheduleModeFields>[0]>

function renderFields(overrides: Overrides = {}) {
  return render(
    <ScheduleModeFields
      mode="scheduled"
      onModeChange={onModeChange}
      time="03:30"
      onTimeChange={onTimeChange}
      days={[1, 3]}
      onDaysChange={onDaysChange}
      serverTimezone="Europe/Amsterdam"
      serverTimeOffset="+02:00"
      intervalLabel="Interval (minutes)"
      intervalControl={<input id="sched-interval" aria-label="Interval control" />}
      idPrefix="sched"
      {...overrides}
    />,
  )
}

const dayButtons = () =>
  within(screen.getByRole('group', { name: 'Days' })).getAllByRole('button')

describe('ScheduleModeFields — mode choice', () => {
  it('renders the interval control and no time input in interval mode', () => {
    renderFields({ mode: 'interval' })

    expect(screen.getByLabelText('Interval control')).toBeInTheDocument()
    expect(screen.getByText('Interval (minutes)')).toBeInTheDocument()
    expect(screen.queryByLabelText('Time of day')).not.toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Days' })).not.toBeInTheDocument()
  })

  it('renders the time input and seven weekday toggles in scheduled mode', () => {
    renderFields({ mode: 'scheduled' })

    expect(screen.getByLabelText('Time of day')).toHaveValue('03:30')
    expect(dayButtons()).toHaveLength(7)
    expect(screen.queryByLabelText('Interval control')).not.toBeInTheDocument()
  })

  it('marks the active mode with aria-checked and reports a change', () => {
    renderFields({ mode: 'interval' })

    expect(screen.getByRole('radio', { name: 'Every so often' })).toHaveAttribute(
      'aria-checked',
      'true',
    )
    expect(screen.getByRole('radio', { name: 'At a set time' })).toHaveAttribute(
      'aria-checked',
      'false',
    )

    fireEvent.click(screen.getByRole('radio', { name: 'At a set time' }))
    expect(onModeChange).toHaveBeenCalledWith('scheduled')
  })

  it('reports a time change', () => {
    renderFields()

    fireEvent.change(screen.getByLabelText('Time of day'), { target: { value: '07:45' } })
    expect(onTimeChange).toHaveBeenCalledWith('07:45')
  })
})

describe('ScheduleModeFields — Monday-first display over Sunday-is-0 values', () => {
  it('renders the week Monday first and Sunday last', () => {
    renderFields()

    expect(dayButtons().map((b) => b.textContent)).toEqual([
      'Mon',
      'Tue',
      'Wed',
      'Thu',
      'Fri',
      'Sat',
      'Sun',
    ])
  })

  it('adds 1 when the FIRST rendered toggle is clicked, and 0 for the LAST', () => {
    const { unmount } = renderFields({ days: [3] })

    fireEvent.click(dayButtons()[0])
    expect(onDaysChange).toHaveBeenCalledWith([1, 3])
    unmount()

    onDaysChange.mockClear()
    renderFields({ days: [3] })

    fireEvent.click(dayButtons()[6])
    expect(onDaysChange).toHaveBeenCalledWith([0, 3])
  })

  it('removes 1 for the FIRST rendered toggle, and 0 for the LAST', () => {
    const { unmount } = renderFields({ days: [1, 3] })

    fireEvent.click(dayButtons()[0])
    expect(onDaysChange).toHaveBeenCalledWith([3])
    unmount()

    onDaysChange.mockClear()
    renderFields({ days: [0, 3] })

    fireEvent.click(dayButtons()[6])
    expect(onDaysChange).toHaveBeenCalledWith([3])
  })

  it('reflects selection in aria-pressed, Sunday included', () => {
    renderFields({ days: [0, 1] })

    const pressed = dayButtons().map((b) => b.getAttribute('aria-pressed'))
    expect(pressed).toEqual(['true', 'false', 'false', 'false', 'false', 'false', 'true'])
  })
})

describe('ScheduleModeFields — the day set is never emptied', () => {
  /**
   * Both halves on the same instrument: a component that never called
   * onDaysChange at all would pass the refusal half on its own.
   */
  it('refuses to clear the last day, but does clear one of two', () => {
    const { unmount } = renderFields({ days: [1] })

    fireEvent.click(dayButtons()[0])
    expect(onDaysChange).not.toHaveBeenCalled()
    unmount()

    onDaysChange.mockClear()
    renderFields({ days: [1, 4] })

    fireEvent.click(dayButtons()[0])
    expect(onDaysChange).toHaveBeenCalledTimes(1)
    expect(onDaysChange).toHaveBeenCalledWith([4])
  })

  it('refuses to clear a lone Sunday, but still adds days to it', () => {
    const { unmount } = renderFields({ days: [0] })

    fireEvent.click(dayButtons()[6])
    expect(onDaysChange).not.toHaveBeenCalled()
    unmount()

    onDaysChange.mockClear()
    renderFields({ days: [0] })

    fireEvent.click(dayButtons()[0])
    expect(onDaysChange).toHaveBeenCalledWith([0, 1])
  })
})

describe('ScheduleModeFields — timezone note', () => {
  const note = "Times are in Europe/Amsterdam (+02:00), the server's own clock."

  it('renders in interval mode', () => {
    renderFields({ mode: 'interval' })

    expect(screen.getByText(note)).toBeInTheDocument()
  })

  it('renders in scheduled mode', () => {
    renderFields({ mode: 'scheduled' })

    expect(screen.getByText(note)).toBeInTheDocument()
  })

  it('uses the zone and offset it is given', () => {
    renderFields({ serverTimezone: 'UTC', serverTimeOffset: '+00:00' })

    expect(screen.getByText("Times are in UTC (+00:00), the server's own clock.")).toBeInTheDocument()
  })
})

describe('ScheduleModeFields — idPrefix', () => {
  it('namespaces every id it owns', () => {
    renderFields({ idPrefix: 'backup' })

    expect(screen.getByRole('radio', { name: 'Every so often' })).toHaveAttribute(
      'id',
      'backup-mode-interval',
    )
    expect(screen.getByRole('radio', { name: 'At a set time' })).toHaveAttribute(
      'id',
      'backup-mode-scheduled',
    )
    expect(screen.getByLabelText('Time of day')).toHaveAttribute('id', 'backup-time')
    expect(dayButtons().map((b) => b.id)).toEqual([
      'backup-day-1',
      'backup-day-2',
      'backup-day-3',
      'backup-day-4',
      'backup-day-5',
      'backup-day-6',
      'backup-day-0',
    ])
  })

  it('keeps two instances on one page from colliding', () => {
    render(
      <>
        <ScheduleModeFields
          mode="scheduled"
          onModeChange={onModeChange}
          time="01:00"
          onTimeChange={onTimeChange}
          days={[1]}
          onDaysChange={onDaysChange}
          serverTimezone="UTC"
          serverTimeOffset="+00:00"
          intervalLabel="Scan interval"
          intervalControl={<input id="updates-interval" />}
          idPrefix="updates"
        />
        <ScheduleModeFields
          mode="scheduled"
          onModeChange={onModeChange}
          time="02:00"
          onTimeChange={onTimeChange}
          days={[2]}
          onDaysChange={onDaysChange}
          serverTimezone="UTC"
          serverTimeOffset="+00:00"
          intervalLabel="Backup interval"
          intervalControl={<input id="backups-interval" />}
          idPrefix="backups"
        />
      </>,
    )

    const ids = screen.getAllByRole('button').map((b) => b.id)
    expect(new Set(ids).size).toBe(ids.length)
    expect(ids).toContain('updates-day-0')
    expect(ids).toContain('backups-day-0')
  })
})
