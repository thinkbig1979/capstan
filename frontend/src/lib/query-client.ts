import { QueryClient } from '@tanstack/react-query'
import { isAutoRetryable } from '@/lib/error-handler'

// The stack list's fallback for a silent gap in /ws/events: a Docker event
// stream that was lost and re-subscribed, or a frame dropped in transit. The
// WS stream is the fast path; this only bounds how long a missed event can
// leave a status wrong without a window refocus (agent-os-a1ye.5). Not polled
// from a background tab, where nobody is looking.
export const STACKS_LIST_POLLING = {
  refetchInterval: 60_000,
  refetchIntervalInBackground: false,
} as const

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      // A definitive answer from the server -- a 401, 403, 404, 422, 500 --
      // cannot change on a second identical request (agent-os-8ett). Retrying
      // it only spends a request and delays the error the user is waiting on.
      // A failure that carries no response at all still retries once, because
      // that one genuinely can come out differently.
      retry: (failureCount, error) => failureCount < 1 && isAutoRetryable(error),
      refetchOnWindowFocus: true,
    },
  },
})
