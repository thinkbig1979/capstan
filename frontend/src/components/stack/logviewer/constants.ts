import type { TimeRangeConfig } from './types'

export const MAX_LOG_BUFFER = 10000

// Per-container identifier colors. Not status — purely a visual diff between
// container names in interleaved log output. Assigned round-robin in order of
// first appearance (see containerColors below), so the first 10 distinct
// services are always different colors. Each entry pairs a darker shade for
// light themes with a lighter shade for dark themes so the bracket stays
// legible on the muted log background in both.
export const CONTAINER_COLORS = [
  'text-red-600 dark:text-red-400',
  'text-orange-600 dark:text-orange-400',
  'text-amber-600 dark:text-amber-400',
  'text-green-600 dark:text-green-400',
  'text-teal-600 dark:text-teal-400',
  'text-sky-600 dark:text-sky-400',
  'text-blue-600 dark:text-blue-400',
  'text-violet-600 dark:text-violet-400',
  'text-fuchsia-600 dark:text-fuchsia-400',
  'text-rose-600 dark:text-rose-400',
]

export const TIME_RANGE_OPTIONS: TimeRangeConfig[] = [
  {
    label: 'All',
    value: 'all',
    getStartTime: () => null,
  },
  {
    label: 'Last 5 min',
    value: '5m',
    getStartTime: () => new Date(Date.now() - 5 * 60 * 1000),
  },
  {
    label: 'Last 15 min',
    value: '15m',
    getStartTime: () => new Date(Date.now() - 15 * 60 * 1000),
  },
  {
    label: 'Last 1 hr',
    value: '1h',
    getStartTime: () => new Date(Date.now() - 60 * 60 * 1000),
  },
  {
    label: 'Custom',
    value: 'custom',
    getStartTime: () => null,
  },
]
