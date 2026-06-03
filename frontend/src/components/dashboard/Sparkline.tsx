import { useMemo, useSyncExternalStore } from 'react'
import { AreaChart, Area, YAxis, ResponsiveContainer } from 'recharts'

interface SparklineProps {
  /** Numeric time-series values (oldest first). */
  series: number[]
  /** Override chart color. Falls back to resolved CSS --success/--warning/--destructive. */
  color?: string
  /** 0–100 threshold to pick semantic color when no explicit color is given. */
  thresholdPercent?: number
  width?: number | string
  height?: number | string
  className?: string
}

/** Resolve a CSS custom property to a hex/rgb string (needed for SVG attrs). */
function useResolvedColor(thresholdPercent: number | undefined, override: string | undefined): string {
  const isDark = useSyncExternalStore(
    (cb) => {
      if (typeof document === 'undefined') return () => {}
      const obs = new MutationObserver(cb)
      obs.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
      return () => obs.disconnect()
    },
    () => (typeof document !== 'undefined' ? document.documentElement.classList.contains('dark') : false),
    () => false,
  )

  return useMemo(() => {
    if (override) return override
    if (typeof window === 'undefined') return '#22c55e'
    const cs = getComputedStyle(document.documentElement)
    const pct = thresholdPercent ?? 0
    const varName = pct >= 80 ? '--destructive' : pct >= 60 ? '--warning' : '--success'
    return cs.getPropertyValue(varName).trim() || '#22c55e'
    // isDark is intentionally listed to force re-compute on theme flip.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [override, thresholdPercent, isDark])
}

/**
 * Compact sparkline (no axes, grid, or legend).
 * Handles empty and single-point series gracefully — renders a flat line at zero.
 */
export function Sparkline({
  series,
  color,
  thresholdPercent,
  width = '100%',
  height = 32,
  className,
}: SparklineProps) {
  const resolvedColor = useResolvedColor(thresholdPercent, color)

  const data = useMemo(() => {
    if (series.length === 0) return [{ i: 0, v: 0 }, { i: 1, v: 0 }]
    if (series.length === 1) return [{ i: 0, v: series[0] }, { i: 1, v: series[0] }]
    return series.map((v, i) => ({ i, v }))
  }, [series])

  return (
    <div className={className} style={{ width, height }}>
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={data} margin={{ top: 2, right: 0, bottom: 2, left: 0 }}>
          <YAxis domain={[0, 'auto']} hide />
          <Area
            type="monotone"
            dataKey="v"
            stroke={resolvedColor}
            fill={resolvedColor}
            fillOpacity={0.15}
            strokeWidth={1.5}
            dot={false}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  )
}
