import { StrictMode, type ReactNode } from 'react'
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
    // Opt-in only (agent-os-lqsa): the default render has never double-invoked
    // effects, and flipping that default would double-invoke mount effects in
    // all 118 existing test files. Pass true only when the test specifically
    // targets StrictMode's dev mount -> cleanup -> remount behaviour.
    strictMode = false,
  } = {}
) {
  window.history.pushState({}, 'Test page', route)

  const tree = (
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
    </BrowserRouter>
  )

  return {
    ...render(strictMode ? <StrictMode>{tree}</StrictMode> : tree),
    queryClient,
  }
}
