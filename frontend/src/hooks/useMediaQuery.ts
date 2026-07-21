import { useCallback, useSyncExternalStore } from 'react'

/**
 * Subscribe to a CSS media query and re-render when it changes.
 *
 * Used to drive JS-only layout switches that CSS classes can't reach, e.g. the
 * `direction` prop on react-resizable-panels. For purely visual show/hide,
 * prefer Tailwind responsive classes instead.
 *
 * Backed by useSyncExternalStore so the matchMedia subscription is tear-free and
 * needs no setState-in-effect.
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    [query],
  )

  const getSnapshot = useCallback(() => window.matchMedia(query).matches, [query])
  // No matchMedia during SSR; default to "not matching".
  const getServerSnapshot = () => false

  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
