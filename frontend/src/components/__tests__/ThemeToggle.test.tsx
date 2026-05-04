import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ThemeToggle } from '../ThemeToggle'

const mockSetTheme = vi.fn()
const mockStore: { theme: 'system' | 'light' | 'dark'; setTheme: typeof mockSetTheme } = { theme: 'system', setTheme: mockSetTheme }

vi.mock('@/stores/uiStore', () => ({
  useUIStore: () => mockStore,
}))

describe('ThemeToggle', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockStore.theme = 'system'
  })

  it('renders toggle button with accessible label', () => {
    render(<ThemeToggle />)
    expect(screen.getByRole('button', { name: /current theme/i })).toBeInTheDocument()
  })

  it('shows system icon when theme is system', () => {
    mockStore.theme = 'system'
    const { container } = render(<ThemeToggle />)
    const svgs = container.querySelectorAll('svg')
    expect(svgs.length).toBeGreaterThan(0)
  })

  it('shows sun icon when theme is light', () => {
    mockStore.theme = 'light'
    render(<ThemeToggle />)
    expect(screen.getByRole('button', { name: /current theme: light/i })).toBeInTheDocument()
  })

  it('shows moon icon when theme is dark', () => {
    mockStore.theme = 'dark'
    render(<ThemeToggle />)
    expect(screen.getByRole('button', { name: /current theme: dark/i })).toBeInTheDocument()
  })
})
