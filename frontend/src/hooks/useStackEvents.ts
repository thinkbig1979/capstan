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
  const invalidateTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    return () => {
      if (invalidateTimeoutRef.current) {
        clearTimeout(invalidateTimeoutRef.current)
      }
    }
  }, [])

  const scheduleInvalidation = () => {
    if (invalidateTimeoutRef.current) {
      clearTimeout(invalidateTimeoutRef.current)
    }
    invalidateTimeoutRef.current = setTimeout(() => {
      queryClient.invalidateQueries({ queryKey: ['stacks'] })
      queryClient.invalidateQueries({ queryKey: ['directories'] })
      invalidateTimeoutRef.current = null
    }, 500)
  }

  const handleStackStatusEvent = (event: StackStatusEvent) => {
    queryClient.setQueryData(['stacks'], (old: Stack[] | undefined) => {
      if (!old) return old
      return old.map((stack) =>
        stack.id === event.stackId ? { ...stack, status: event.status } : stack
      )
    })
    queryClient.invalidateQueries({ queryKey: ['stacks', event.stackId] })
  }

  const handleContainerEvent = (event: ContainerEvent) => {
    queryClient.invalidateQueries({ queryKey: ['stacks', event.stackId] })
  }

  const handleScanCompleteEvent = () => {
    scheduleInvalidation()
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
