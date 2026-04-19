import { useEffect, useRef } from 'react'
import { useWebSocketJSON } from './useWebSocket'
import { queryClient } from '@/lib/query-client'
import type { Stack } from '@/types'

export interface StackStatusEvent {
  type: 'stack_status'
  stackId: string
  status: 'running' | 'stopped' | 'partial' | 'unknown' | 'error'
  timestamp: string
}

export interface ContainerEvent {
  type: 'container_event'
  stackId: string
  containerId: string
  event: string
  timestamp: string
}

export interface ScanCompleteEvent {
  type: 'scan_complete'
  added: number
  removed: number
  timestamp: string
}

export type StackEvent = StackStatusEvent | ContainerEvent | ScanCompleteEvent

export function useStackEvents() {
  const pendingRef = useRef<Set<string>>(new Set())
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

  const scheduleInvalidations = (keys: string[][]) => {
    keys.forEach((k) => pendingRef.current.add(JSON.stringify(k)))
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      pendingRef.current.forEach((key) => {
        queryClient.invalidateQueries({ queryKey: JSON.parse(key) })
      })
      pendingRef.current.clear()
      timerRef.current = null
    }, 750)
  }

  const handleStackStatusEvent = (event: StackStatusEvent) => {
    queryClient.setQueryData(['stacks'], (old: Stack[] | undefined) => {
      if (!old) return old
      return old.map((stack) =>
        stack.id === event.stackId ? { ...stack, status: event.status } : stack
      )
    })
    scheduleInvalidations([
      ['stack', event.stackId],
      ['dashboard-stats'],
    ])
  }

  const handleContainerEvent = (event: ContainerEvent) => {
    const keys: string[][] = [['dashboard-stats']]
    if (event.stackId) {
      keys.push(['stack', event.stackId], ['stacks'])
    }
    scheduleInvalidations(keys)
  }

  const handleScanCompleteEvent = () => {
    scheduleInvalidations([['stacks'], ['directories']])
  }

  useWebSocketJSON<StackEvent>(
    '/ws/events',
    (data) => {
      switch (data.type) {
        case 'stack_status':
          handleStackStatusEvent(data)
          break
        case 'container_event':
          handleContainerEvent(data)
          break
        case 'scan_complete':
          handleScanCompleteEvent()
          break
      }
    }
  )
}
