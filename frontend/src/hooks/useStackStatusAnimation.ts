import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Stack } from '@/types'
import type { QueryCacheNotifyEvent } from '@tanstack/react-query'
import { queryKeys } from '@/lib/query-keys'

interface AnimatedStack {
  id: string
  timestamp: number
}

export function useStackStatusAnimation() {
  const [animatedStacks, setAnimatedStacks] = useState<AnimatedStack[]>([])
  const queryClient = useQueryClient()
  const previousStacksRef = useRef<Map<string, string>>(new Map())

  useEffect(() => {
    const unsubscribe = queryClient.getQueryCache().subscribe((event: QueryCacheNotifyEvent) => {
      if (event.type === 'updated') {
        const query = event.query
        if (query.queryKey[0] === queryKeys.stacks()[0] && Array.isArray(query.state.data)) {
          const stacks = query.state.data as Stack[]
          const newAnimatedStacks: AnimatedStack[] = []

          stacks.forEach((stack) => {
            const previousStatus = previousStacksRef.current.get(stack.id)
            if (previousStatus && previousStatus !== stack.status) {
              newAnimatedStacks.push({
                id: stack.id,
                timestamp: Date.now(),
              })
            }
            previousStacksRef.current.set(stack.id, stack.status)
          })

          if (newAnimatedStacks.length > 0) {
            setAnimatedStacks((prev) => [...prev, ...newAnimatedStacks])
          }
        }
      }
    })

    return () => {
      unsubscribe()
    }
  }, [queryClient])

  useEffect(() => {
    const interval = setInterval(() => {
      const now = Date.now()
      setAnimatedStacks((prev) => prev.filter((s) => now - s.timestamp < 1000))
    }, 100)

    return () => clearInterval(interval)
  }, [])

  const isAnimating = (stackId: string): boolean => {
    return animatedStacks.some((s) => s.id === stackId)
  }

  return { isAnimating }
}
