import { QueryClient } from '@tanstack/react-query'
import { isAutoRetryable } from '@/lib/error-handler'

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
