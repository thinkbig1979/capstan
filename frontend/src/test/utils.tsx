import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router'
import { render } from '@testing-library/react'

const createTestQueryClient = () =>
  new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        gcTime: 0,
      },
      mutations: {
        retry: false,
      },
    },
  })

export function renderWithProviders(
  ui: ReactNode,
  {
    route = '/',
    queryClient = createTestQueryClient(),
  } = {}
) {
  window.history.pushState({}, 'Test page', route)

  return {
    ...render(
      <BrowserRouter>
        <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
      </BrowserRouter>
    ),
    queryClient,
  }
}
