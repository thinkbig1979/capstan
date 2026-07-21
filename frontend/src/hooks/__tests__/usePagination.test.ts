import { describe, it, expect } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { usePagination } from '../usePagination'

const makeItems = (n: number) => Array.from({ length: n }, (_, i) => i)

describe('usePagination', () => {
  it('slices the first page and reports totals', () => {
    const { result } = renderHook(() => usePagination(makeItems(75), 50))
    expect(result.current.pageItems).toHaveLength(50)
    expect(result.current.pageItems[0]).toBe(0)
    expect(result.current.totalPages).toBe(2)
    expect(result.current.total).toBe(75)
  })

  it('returns the remainder on the last page', () => {
    const { result } = renderHook(() => usePagination(makeItems(75), 50))
    act(() => result.current.setPage(2))
    expect(result.current.pageItems).toHaveLength(25)
    expect(result.current.pageItems[0]).toBe(50)
  })

  it('reports a single page when the list fits', () => {
    const { result } = renderHook(() => usePagination(makeItems(16), 50))
    expect(result.current.totalPages).toBe(1)
    expect(result.current.pageItems).toHaveLength(16)
  })

  it('clamps the page when the list shrinks below the current page', () => {
    let count = 75
    const { result, rerender } = renderHook(() => usePagination(makeItems(count), 50))
    act(() => result.current.setPage(2))
    expect(result.current.page).toBe(2)
    count = 10
    rerender()
    expect(result.current.page).toBe(1)
    expect(result.current.pageItems).toHaveLength(10)
  })
})
